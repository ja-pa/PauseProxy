package mitm

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/cert"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
	"pauseproxy/internal/config"
	"pauseproxy/internal/httpapi"
	"pauseproxy/internal/state"
)

func testProxyClient(t *testing.T) *http.Client {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ca")
	if err := Bootstrap(dir); err != nil {
		t.Fatal(err)
	}
	ca, err := LoadCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	c := config.Default()
	c.Hosts = []string{"127.0.0.1", "localhost", "x.com"}
	p, err := proxy.NewProxy(&proxy.Options{Addr: addr, NewCaFunc: func() (cert.CA, error) { return ca, nil }})
	if err != nil {
		t.Fatal(err)
	}
	p.SetShouldInterceptRule(func(r *http.Request) bool { return c.Target(r.Host) })
	p.SetUpstreamProxy(func(*http.Request) (*url.URL, error) { return nil, nil })
	p.AddAddon(&Addon{API: &httpapi.API{Config: c, State: state.New(c, time.Now)}})
	done := make(chan error, 1)
	go func() { done <- p.Start() }()
	t.Cleanup(func() { _ = p.Close(); <-done })
	for i := 0; i < 100; i++ {
		conn, e := net.DialTimeout("tcp", addr, 20*time.Millisecond)
		if e == nil {
			_ = conn.Close()
			break
		}
		if i == 99 {
			t.Fatal(e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	pu, _ := url.Parse("http://" + addr)
	roots := x509.NewCertPool()
	roots.AddCert(ca.GetRootCA())
	transport := &http.Transport{Proxy: http.ProxyURL(pu), TLSClientConfig: &tls.Config{RootCAs: roots}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	// TCP readiness alone is insufficient with WSL localhost forwarding. Verify
	// the actual HTTP handler and warm this client's proxy connection first.
	for i := 0; i < 50; i++ {
		r, err := client.Get("http://" + addr + "/__pauseproxy/status")
		if err == nil {
			body, readErr := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if readErr == nil && r.StatusCode == 200 && bytes.Contains(body, []byte("remaining_seconds")) {
				return client
			}
		}
		transport.CloseIdleConnections()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("proxy HTTP handler did not become ready")
	return nil
}

func TestRequestBodyReplayBounds(t *testing.T) {
	a := &Addon{API: &httpapi.API{Config: config.Default()}}
	u, _ := url.Parse("https://x.com/i/api/test")
	f := &proxy.Flow{Request: &proxy.Request{URL: u}}
	for _, size := range []int{0, 1024, maxReplayBody, maxReplayBody + 1, maxReplayBody * 2} {
		payload := bytes.Repeat([]byte("a"), size)
		reader := a.StreamRequestModifier(f, bytes.NewReader(payload))
		r, err := http.NewRequest("POST", u.String(), reader)
		if err != nil {
			t.Fatal(err)
		}
		if (r.GetBody != nil || r.Body == http.NoBody) != (size <= maxReplayBody) {
			t.Fatalf("unexpected replayability for %d bytes", size)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, payload) {
			t.Fatalf("body changed for %d bytes: %v", size, err)
		}
		if r.GetBody != nil {
			again, err := r.GetBody()
			if err != nil {
				t.Fatal(err)
			}
			body, err = io.ReadAll(again)
			_ = again.Close()
			if err != nil || !bytes.Equal(body, payload) {
				t.Fatal("replay changed body")
			}
		}
	}
	reader := a.StreamRequestModifier(f, io.MultiReader(strings.NewReader("partial"), failedBody{context.Canceled}))
	body, err := io.ReadAll(reader)
	if string(body) != "partial" || err != context.Canceled {
		t.Fatal("body read error was swallowed")
	}
}

func TestCanceledRequestDoesNotBreakNextPost(t *testing.T) {
	started := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cancel" {
			close(started)
			<-r.Context().Done()
			return
		}
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(body)
	}))
	defer upstream.Close()
	client := testProxyClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", upstream.URL+"/cancel", nil)
	done := make(chan error, 1)
	go func() {
		r, err := client.Do(req)
		if r != nil {
			_ = r.Body.Close()
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel request did not reach upstream")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("request was not canceled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel hung")
	}
	r, err := client.Post(upstream.URL, "application/json", strings.NewReader(`{"after":"cancel"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil || r.StatusCode != 200 || string(body) != `{"after":"cancel"}` {
		t.Fatalf("POST failed after cancellation: %d %s %v", r.StatusCode, body, err)
	}
}

// Closing an upstream keep-alive socket must not strand the browser's proxy connection.
func TestPostAfterUpstreamConnectionLoss(t *testing.T) {
	var received atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
			return
		}
		received.Add(1)
		if r.Method == "GET" {
			conn, buf, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			// Deliberately no Connection: close header: simulate an idle socket disappearing.
			_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
			_ = buf.Flush()
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer upstream.Close()
	client := testProxyClient(t)
	for i := 0; i < 5; i++ {
		r, e := client.Get(upstream.URL)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		payload := fmt.Sprintf(`{"sequence":%d,"text":"test body"}`, i)
		r, e = client.Post(upstream.URL, "application/json", strings.NewReader(payload))
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if e != nil || r.StatusCode != 200 || string(body) != payload {
			t.Fatalf("POST after connection loss: status=%d body=%q err=%v", r.StatusCode, body, e)
		}
	}
	if received.Load() != 10 {
		t.Fatalf("requests lost or duplicated: %d", received.Load())
	}
}
