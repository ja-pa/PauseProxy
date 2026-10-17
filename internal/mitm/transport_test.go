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
	"net/http/httptrace"
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

func testProxyClient(t *testing.T, transports ...http.RoundTripper) *http.Client {
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
	upstream, err := NewUpstreamTransport()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(upstream.CloseIdleConnections)
	var transport http.RoundTripper = upstream
	if len(transports) > 0 {
		transport = transports[0]
	}
	p, err := proxy.NewProxy(&proxy.Options{Addr: addr, UpstreamTransport: transport, NewCaFunc: func() (cert.CA, error) { return ca, nil }})
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
	clientTransport := &http.Transport{Proxy: http.ProxyURL(pu), TLSClientConfig: &tls.Config{RootCAs: roots}}
	t.Cleanup(clientTransport.CloseIdleConnections)
	client := &http.Client{Transport: clientTransport, Timeout: 5 * time.Second}
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
		clientTransport.CloseIdleConnections()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("proxy HTTP handler did not become ready")
	return nil
}

// The first connection stays open but drops bytes after stall is enabled.
// Subsequent connections work normally, like reconnecting after a network change.
type blackholeListener struct {
	net.Listener
	stall    atomic.Bool
	accepted atomic.Int32
}

func (l *blackholeListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if l.accepted.Add(1) == 1 {
		return &blackholeConn{Conn: c, stall: &l.stall}, nil
	}
	return c, nil
}

type blackholeConn struct {
	net.Conn
	stall *atomic.Bool
}

func (c *blackholeConn) Read(p []byte) (int, error) {
	for {
		n, err := c.Conn.Read(p)
		if err != nil || !c.stall.Load() {
			return n, err
		}
	}
}

func (c *blackholeConn) Write(p []byte) (int, error) {
	if c.stall.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func TestHTTP2BlackholeRecovery(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			var posts atomic.Int32
			var wire *blackholeListener
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 2 {
					t.Errorf("upstream protocol = %s", r.Proto)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if r.Method == http.MethodPost {
					if string(body) != "do-once" {
						t.Errorf("POST body = %q", body)
					}
					if posts.Add(1) == 1 {
						wire.stall.Store(true)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			wire = &blackholeListener{Listener: server.Listener}
			server.Listener = wire
			server.EnableHTTP2 = true
			server.StartTLS()
			t.Cleanup(server.Close)
			// Use production wiring with shorter intervals so the test does not
			// wait 20 seconds. The generous header timeout isolates PING recovery.
			upstream, err := newUpstreamTransport(100*time.Millisecond, 100*time.Millisecond, time.Second, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			upstream.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: "127.0.0.1"}
			t.Cleanup(upstream.CloseIdleConnections)
			client := testProxyClient(t, upstream)
			var browserReused bool
			read := func(method string) (int, string) {
				t.Helper()
				r, err := http.NewRequest(method, strings.Replace(server.URL, "127.0.0.1", "localhost", 1), strings.NewReader("do-once"))
				if err != nil {
					t.Fatal(err)
				}
				r = r.WithContext(httptrace.WithClientTrace(r.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { browserReused = info.Reused }}))
				resp, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				return resp.StatusCode, string(body)
			}
			if code, body := read(http.MethodGet); code != 200 || body != `{"ok":true}` {
				t.Fatalf("warmup: %d %q", code, body)
			}
			if wire.accepted.Load() != 1 {
				t.Fatalf("warmup connections = %d", wire.accepted.Load())
			}
			if method == http.MethodGet {
				wire.stall.Store(true)
			}
			start := time.Now()
			code, _ := read(method)
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Fatalf("dead connection not retired by health check: %s", elapsed)
			}
			if code != 200 && code != 502 {
				t.Fatalf("stalled request status = %d", code)
			}
			if method == http.MethodPost && (code != 502 || posts.Load() != 1) {
				t.Fatalf("ambiguous POST was replayed: status=%d received=%d", code, posts.Load())
			}
			if code, body := read(method); code != 200 || body != `{"ok":true}` {
				t.Fatalf("recovery: %d %q", code, body)
			}
			if wire.accepted.Load() < 2 {
				t.Fatal("no new upstream connection")
			}
			if !browserReused {
				t.Fatal("recovery replaced the browser connection")
			}
			if method == http.MethodPost && posts.Load() != 2 {
				t.Fatalf("POST lost/duplicated: %d", posts.Load())
			}
		})
	}
}

func TestUpstreamHeaderTimeoutRecovery(t *testing.T) {
	for _, tlsEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", tlsEnabled), func(t *testing.T) {
			var stalled atomic.Bool
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !stalled.Swap(true) {
					<-r.Context().Done()
					return
				}
				_, _ = w.Write([]byte("recovered"))
			}))
			if tlsEnabled {
				server.EnableHTTP2 = true
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			upstream, err := newUpstreamTransport(time.Second, time.Second, time.Second, 150*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if tlsEnabled {
				roots := x509.NewCertPool()
				roots.AddCert(server.Certificate())
				upstream.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: "127.0.0.1"}
			}
			t.Cleanup(upstream.CloseIdleConnections)
			client := testProxyClient(t, upstream)
			start := time.Now()
			url := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
			resp, err := client.Get(url)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 502 || time.Since(start) > time.Second {
				t.Fatalf("headers not bounded: status=%d elapsed=%s", resp.StatusCode, time.Since(start))
			}
			resp, err = client.Get(url)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 || string(body) != "recovered" {
				t.Fatalf("recovery: %d %q %v", resp.StatusCode, body, err)
			}
		})
	}
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
