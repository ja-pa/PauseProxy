package mitm

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"github.com/lqqyt2423/go-mitmproxy/cert"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"pauseproxy/internal/config"
	"pauseproxy/internal/httpapi"
	"pauseproxy/internal/state"
	"strings"
	"testing"
	"time"
)

func TestInjectionAndCompression(t *testing.T) {
	c := config.Default()
	a := &Addon{API: &httpapi.API{Config: c}}
	body := []byte(`<html><head><meta http-equiv="Content-Security-Policy" content="script-src 'none'"></head><body>hello</body></html>`)
	var zipped bytes.Buffer
	g := gzip.NewWriter(&zipped)
	_, _ = g.Write(body)
	_ = g.Close()
	u, _ := url.Parse("https://x.com/")
	f := &proxy.Flow{Request: &proxy.Request{Method: "GET", URL: u}, Response: &proxy.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}, "Content-Encoding": []string{"gzip"}, "Content-Security-Policy": []string{"default-src 'none'; script-src 'none'"}}}}
	out, e := io.ReadAll(a.StreamResponseModifier(f, &zipped))
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(out, []byte("data-pauseproxy")) || f.Response.Header.Get("Content-Encoding") != "" || !strings.Contains(f.Response.Header.Get("Content-Security-Policy"), "'nonce-") {
		t.Fatal("injection/compression/CSP failed")
	}
	f.Response.Header.Set("Content-Encoding", "gzip")
	out, _ = io.ReadAll(a.StreamResponseModifier(f, strings.NewReader("bad")))
	if f.Response.StatusCode != 502 || !bytes.Contains(out, []byte("Injection failed")) {
		t.Fatal("failure did not close access")
	}
	if d := a.API.Diagnostics(); d.Injections != 1 || d.Failures != 1 {
		t.Fatalf("injection diagnostics: %+v", d)
	}
}

func TestInlineClientPrecedesUpstreamScriptsAndPreservesTrustedTypes(t *testing.T) {
	input := []byte(`<html><head><script src="/upstream.js"></script></head><body></body></html>`)
	out, err := Inject(input, "test-nonce")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	client := strings.Index(s, `data-pauseproxy="client"`)
	upstream := strings.Index(s, `src="/upstream.js"`)
	if client < 0 || client > upstream || !strings.Contains(s, "const fetchRequest = window.fetch.bind(window)") || strings.Contains(s, "innerHTML") || strings.Contains(s, `src="/__pauseproxy/client.js"`) {
		t.Fatal("client must execute inline before upstream scripts without HTML sinks")
	}
	p := policy("script-src 'none'; require-trusted-types-for 'script'; trusted-types 'none'", "test-nonce")
	if !strings.Contains(p, "'nonce-test-nonce'") || !strings.Contains(p, "require-trusted-types-for 'script'") || !strings.Contains(p, "trusted-types 'none'") {
		t.Fatal("Trusted Types policy was weakened")
	}
}

func TestCSPPreservesFallbackAndInlineStyles(t *testing.T) {
	got := policy("default-src https://cdn.example; style-src 'unsafe-inline'; object-src 'none'", "abc")
	for _, want := range []string{"script-src https://cdn.example 'nonce-abc'", "connect-src https://cdn.example 'self'", "style-src 'unsafe-inline' 'self'", "object-src 'none'"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost effective CSP source: %s", got)
		}
	}
	if strings.Contains(got, "style-src 'unsafe-inline' 'self' 'nonce-") {
		t.Fatal("nonce invalidates upstream inline styles")
	}
}

func TestCSPDoesNotInventRestrictionsOrDisableInlineScripts(t *testing.T) {
	for _, original := range []string{
		"",
		"require-trusted-types-for 'script'",
		"script-src 'unsafe-eval' 'self' 'unsafe-inline' https://www.youtube.com; report-uri /report",
		"script-src-elem 'unsafe-inline' https:; trusted-types youtube",
	} {
		if got := policy(original, "proxy-nonce"); got != original {
			t.Fatalf("unrestricted fetches / allowed inline scripts changed: %q -> %q", original, got)
		}
	}
	for _, original := range []string{
		"script-src 'nonce-upstream' 'unsafe-inline' 'strict-dynamic' https: http: 'unsafe-eval'",
		"script-src 'sha256-abc' 'unsafe-inline' https:",
		"script-src 'self'",
		"script-src-elem 'none'",
	} {
		got := policy(original, "proxy-nonce")
		if !strings.Contains(got, "'nonce-proxy-nonce'") || strings.Contains(got, "connect-src") || strings.Contains(got, "style-src") {
			t.Fatalf("nonce injection changed unrelated fetch rules: %s", got)
		}
	}
	got := policy("default-src 'unsafe-inline' https: data:; object-src 'none'", "proxy-nonce")
	if strings.Contains(got, "script-src") || !strings.Contains(got, "connect-src 'unsafe-inline' https: data: 'self'") {
		t.Fatalf("default-src fallback changed: %s", got)
	}
}

func TestYouTubeMultipleCSPHeadersAndMetaRemainCompatible(t *testing.T) {
	c := config.Default()
	c.Hosts = []string{"www.youtube.com"}
	a := &Addon{API: &httpapi.API{Config: c}}
	u, _ := url.Parse("https://www.youtube.com/")
	policies := []string{
		"script-src 'nonce-upstream' 'strict-dynamic' https: 'unsafe-inline'; object-src 'none'",
		"require-trusted-types-for 'script'",
	}
	f := &proxy.Flow{Request: &proxy.Request{Method: "GET", URL: u}, Response: &proxy.Response{StatusCode: 200, Header: http.Header{
		"Content-Type": {"text/html; charset=utf-8"}, "Content-Security-Policy": policies,
		"Content-Security-Policy-Report-Only": {"require-trusted-types-for 'script'"},
	}}}
	input := `<html><head><meta http-equiv="Content-Security-Policy" content="script-src 'unsafe-inline' https:"><style>.video{display:block}</style><script nonce="upstream">window.videoReady=true</script></head><body style="color:red">Video</body></html>`
	out, e := io.ReadAll(a.StreamResponseModifier(f, strings.NewReader(input)))
	if e != nil || !bytes.Contains(out, []byte("data-pauseproxy")) || !bytes.Contains(out, []byte(`nonce="upstream"`)) {
		t.Fatal("page injection failed", e)
	}
	got := f.Response.Header.Values("Content-Security-Policy")
	if len(got) != 2 || got[1] != policies[1] || strings.Contains(got[0], "connect-src") || strings.Contains(got[0], "style-src") || !strings.Contains(got[0], "'nonce-upstream'") {
		t.Fatal("multiple policies restrict the page", got)
	}
	if f.Response.Header.Get("Content-Security-Policy-Report-Only") != "require-trusted-types-for 'script'" || bytes.Contains(out, []byte("https: &#39;nonce-")) {
		t.Fatal("report-only / meta policy changed effective inline behavior")
	}
}

func TestActualProxyHTMLAndUpstreamTLSVerification(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'")
		g := gzip.NewWriter(w)
		_, _ = g.Write([]byte("<html><head></head><body>upstream</body></html>"))
		_ = g.Close()
	}))
	defer upstream.Close()
	invalidTLS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("should never be trusted")) }))
	defer invalidTLS.Close()
	dir := filepath.Join(t.TempDir(), "ca")
	if e := Bootstrap(dir); e != nil {
		t.Fatal(e)
	}
	ca, e := LoadCA(dir)
	if e != nil {
		t.Fatal(e)
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := l.Addr().String()
	_ = l.Close()
	c := config.Default()
	c.Hosts = []string{"127.0.0.1", "localhost"}
	p, e := proxy.NewProxy(&proxy.Options{Addr: addr, NewCaFunc: func() (cert.CA, error) { return ca, nil }})
	if e != nil {
		t.Fatal(e)
	}
	p.SetShouldInterceptRule(func(r *http.Request) bool { return c.Target(r.Host) })
	p.SetUpstreamProxy(func(*http.Request) (*url.URL, error) { return nil, nil })
	p.AddAddon(&Addon{API: &httpapi.API{Config: c, State: state.New(c, time.Now)}})
	done := make(chan error, 1)
	go func() { done <- p.Start() }()
	defer func() { _ = p.Close(); <-done }()
	pu, _ := url.Parse("http://" + addr)
	roots := x509.NewCertPool()
	roots.AddCert(ca.GetRootCA())
	tr := &http.Transport{Proxy: http.ProxyURL(pu), TLSClientConfig: &tls.Config{RootCAs: roots}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	var r *http.Response
	for i := 0; i < 50; i++ {
		r, e = client.Get(upstream.URL)
		if e == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !bytes.Contains(body, []byte("data-pauseproxy")) || r.Header.Get("Content-Encoding") != "" || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("actual HTML rewrite failed: %d %s", r.StatusCode, body)
	}
	r, e = client.Get(strings.Replace(invalidTLS.URL, "127.0.0.1", "localhost", 1))
	if e == nil {
		defer r.Body.Close()
		if r.StatusCode != 502 {
			t.Fatalf("untrusted upstream was accepted: %d", r.StatusCode)
		}
	}
}
func TestCALoadOnlyAndMITMLocalAPI(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	if _, e := LoadCA(dir); e == nil {
		t.Fatal("missing CA accepted")
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("runtime created CA")
	}
	if e := Bootstrap(dir); e != nil {
		t.Fatal(e)
	}
	if e := Bootstrap(dir); e == nil {
		t.Fatal("CA overwritten")
	}
	ca, e := LoadCA(dir)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "ca-key.pem"))
	for i := 0; i < 150; i++ {
		if _, e := ca.GetCert("x.com"); e != nil {
			t.Fatal(e)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "ca-key.pem"))
	if !bytes.Equal(before, after) {
		t.Fatal("CA changed")
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := l.Addr().String()
	_ = l.Close()
	c := config.Default()
	p, e := proxy.NewProxy(&proxy.Options{Addr: addr, NewCaFunc: func() (cert.CA, error) { return ca, nil }})
	if e != nil {
		t.Fatal(e)
	}
	p.SetShouldInterceptRule(func(r *http.Request) bool { return c.Target(r.Host) })
	p.AddAddon(&Addon{API: &httpapi.API{Config: c, State: state.New(c, time.Now)}})
	done := make(chan error, 1)
	go func() { done <- p.Start() }()
	defer func() { _ = p.Close(); <-done }()
	pu, _ := url.Parse("http://" + addr)
	roots := x509.NewCertPool()
	roots.AddCert(ca.GetRootCA())
	transport := &http.Transport{Proxy: http.ProxyURL(pu), TLSClientConfig: &tls.Config{RootCAs: roots}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	var r *http.Response
	for i := 0; i < 50; i++ {
		r, e = client.Get("https://x.com/__pauseproxy/status")
		if e == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !bytes.Contains(b, []byte("remaining_seconds")) {
		t.Fatalf("MITM API: %d %s", r.StatusCode, b)
	}
	r, e = client.Post("https://x.com/__pauseproxy/session", "application/json", strings.NewReader("{}"))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	if r.StatusCode != 201 {
		t.Fatal("session POST not intercepted", r.StatusCode)
	}
}
