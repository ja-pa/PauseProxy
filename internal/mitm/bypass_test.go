package mitm

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"github.com/lqqyt2423/go-mitmproxy/cert"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"pauseproxy/internal/config"
	"pauseproxy/internal/httpapi"
	"pauseproxy/internal/state"
	"testing"
	"time"
)

func TestMediaTLSRemainsUpstreamAndExactHTMLIsInjected(t *testing.T) {
	load := func(name string) *CA {
		t.Helper()
		dir := filepath.Join(t.TempDir(), name)
		if e := Bootstrap(dir); e != nil {
			t.Fatal(e)
		}
		ca, e := LoadCA(dir)
		if e != nil {
			t.Fatal(e)
		}
		return ca
	}
	// Distinct authorities: a media request can validate only the upstream CA.
	local := load("local")
	upstreamCA := load("upstream")
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><head></head><body>media upstream</body></html>")
	}))
	upstream.TLS = &tls.Config{GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		return upstreamCA.GetCert(hello.ServerName)
	}}
	upstream.StartTLS()
	defer upstream.Close()
	tunnel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html><head></head><body>site upstream</body></html>")
			return
		}
		conn, e := net.Dial("tcp", upstream.Listener.Addr().String())
		if e != nil {
			http.Error(w, e.Error(), 502)
			return
		}
		client, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			conn.Close()
			return
		}
		io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { io.Copy(conn, client); conn.Close() }()
		io.Copy(client, conn)
		client.Close()
	}))
	defer tunnel.Close()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := ln.Addr().String()
	ln.Close()
	c := config.Default()
	a := &httpapi.API{Config: c, State: state.New(c, time.Now)}
	rules := a.Rules()
	rules.Bypass = append(rules.Bypass, "googlevideo.com")
	a.State.ConfigureRules(rules, "")
	p, e := proxy.NewProxy(&proxy.Options{Addr: addr, NewCaFunc: func() (cert.CA, error) { return local, nil }})
	if e != nil {
		t.Fatal(e)
	}
	p.SetShouldInterceptRule(func(r *http.Request) bool { return a.Target(r.Host) })
	tu, _ := url.Parse(tunnel.URL)
	p.SetUpstreamProxy(func(*http.Request) (*url.URL, error) { return tu, nil })
	p.AddAddon(&Addon{API: a})
	done := make(chan error, 1)
	go func() { done <- p.Start() }()
	defer func() { p.Close(); <-done }()
	pu, _ := url.Parse("http://" + addr)
	roots := x509.NewCertPool()
	roots.AddCert(upstreamCA.GetRootCA())
	tr := &http.Transport{Proxy: http.ProxyURL(pu), TLSClientConfig: &tls.Config{RootCAs: roots}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	for _, host := range []string{"googlevideo.com", "r1.googlevideo.com", "video.twimg.com", "unknown.x.com"} {
		var r *http.Response
		for i := 0; i < 50; i++ {
			r, e = client.Get("https://" + host + "/")
			if e == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if e != nil {
			t.Fatal(host, e)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.TLS == nil || !bytes.Equal(r.TLS.VerifiedChains[0][len(r.TLS.VerifiedChains[0])-1].Raw, upstreamCA.GetRootCA().Raw) || bytes.Contains(b, []byte("data-pauseproxy")) {
			t.Fatal("media intercepted", host)
		}
	}
	r, e := client.Get("http://x.com/")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !bytes.Contains(b, []byte("data-pauseproxy")) {
		t.Fatal("exact target not injected", string(b))
	}
}
