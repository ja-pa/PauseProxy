package mitm

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"github.com/andybalholm/brotli"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/text/encoding/charmap"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"pauseproxy/internal/httpapi"
	"pauseproxy/internal/state"
	"pauseproxy/web"
	"strings"
)

const maxHTML = 4 * 1024 * 1024
const maxReplayBody = 256 * 1024

type Addon struct {
	proxy.BaseAddon
	API *httpapi.API
}

func (a *Addon) ClientConnected(c *proxy.ClientConn) {
	// Issue the local certificate without tying the browser tunnel to a single
	// upstream socket. Upstream verification still happens in the shared client.
	c.UpstreamCert = false
}

type failedBody struct{ err error }

func (b failedBody) Read([]byte) (int, error) { return 0, b.err }

func (a *Addon) StreamRequestModifier(f *proxy.Flow, in io.Reader) io.Reader {
	if !a.API.Target(f.Request.URL.Hostname()) || in == nil {
		return in
	}
	// NewRequest recognizes *bytes.Reader and installs GetBody. This permits
	// Go's normal safe transport retries, without replaying arbitrary POSTs.
	body, err := io.ReadAll(io.LimitReader(in, maxReplayBody+1))
	if err != nil {
		return io.MultiReader(bytes.NewReader(body), failedBody{err})
	}
	if len(body) > maxReplayBody {
		return io.MultiReader(bytes.NewReader(body), in)
	}
	return bytes.NewReader(body)
}

func response(code int, body string) *proxy.Response {
	return &proxy.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}, "Cache-Control": []string{"no-store"}}, Body: []byte(body)}
}
func blockPage(reason string, resumable bool) string {
	marker := ""
	if resumable {
		marker = " data-pauseproxy-blocked"
	}
	return `<!doctype html><html lang="en"` + marker + `><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>PauseProxy</title><style>body{background:#f5f6f8;color:#182230;font:18px system-ui;text-align:center;margin:15vh auto;padding:24px;max-width:600px}h1{font-size:36px}p{line-height:1.6}</style></head><body><h1>Time for a break</h1><p>` + html.EscapeString(reason) + `</p><p>Reload after changing the limit or at the start of a new day.</p><script defer src="/__pauseproxy/client.js" data-pauseproxy="client"></script></body></html>`
}
func (a *Addon) Requestheaders(f *proxy.Flow) {
	if f.Request.Method == "CONNECT" || (!a.API.Target(f.Request.URL.Hostname()) && !strings.HasPrefix(f.Request.URL.Path, "/__pauseproxy/")) {
		return
	}
	remote := f.ConnContext.ClientConn.Conn.RemoteAddr().String()
	user, ip := a.API.Config.User(remote)
	if user == "" {
		f.Response = response(403, "Unknown device")
		return
	}
	if strings.HasPrefix(f.Request.URL.Path, "/__pauseproxy/") {
		raw := &http.Request{Method: f.Request.Method, URL: f.Request.URL, Host: f.Request.URL.Host, Header: f.Request.Header, Body: f.Request.Raw().Body, RemoteAddr: remote}
		rec := httptest.NewRecorder()
		a.API.Client(rec, raw, user, ip)
		f.Response = &proxy.Response{StatusCode: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
		return
	}
	site, _ := a.API.State.Match(f.Request.URL.Hostname())
	if a.API.State.Status(user, site.Domain).Blocked {
		if strings.Contains(f.Request.Header.Get("Accept"), "text/html") {
			f.Response = response(403, blockPage(a.API.State.Status(user, site.Domain).Reason, true))
		} else {
			f.Response = &proxy.Response{StatusCode: 403, Header: http.Header{"Content-Type": []string{"application/json"}, "Cache-Control": []string{"no-store"}}, Body: []byte(`{"error":"PauseProxy blocked"}`)}
		}
		return
	}
	if strings.EqualFold(f.Request.Header.Get("Upgrade"), "websocket") {
		f.Response = response(501, "WebSocket is disabled in the RAM MVP")
		return
	}
	f.Request.Header.Set("Accept-Encoding", "identity")
	f.Request.Header.Del("If-None-Match")
	f.Request.Header.Del("If-Modified-Since")
	f.Stream = true // Do not buffer requests, API traffic or media in the library.
	// The library's connection-bound transport returns the same socket even
	// when Go asks to redial. Use its shared, independently dialing transport.
	f.UseSeparateClient = true
}
func (a *Addon) Responseheaders(f *proxy.Flow) {
	if f.Request.Method == "CONNECT" || !a.API.Target(f.Request.URL.Hostname()) || strings.HasPrefix(f.Request.URL.Path, "/__pauseproxy/") {
		return
	}
	h := f.Response.Header
	h.Del("Alt-Svc")
	if strings.Contains(h.Get("Content-Type"), "text/event-stream") {
		f.Response = response(501, "SSE disabled in RAM MVP to avoid unbounded upstream event history")
		return
	}
	// Prevent installation/use of an upstream service worker in this test profile.
	if f.Request.Header.Get("Sec-Fetch-Dest") == "serviceworker" || f.Request.Header.Get("Service-Worker") == "script" {
		f.Response = response(403, "Service workers disabled by PauseProxy")
		return
	}
	mt, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	if mt != "text/html" || f.Request.Method == "HEAD" {
		return
	}
	h.Del("Content-Length")
	h.Del("ETag")
	h.Del("Last-Modified")
	h.Del("Content-Md5")
	h.Set("Cache-Control", "no-store")
	f.Stream = true
}
func policy(value, nonce string) string {
	parts := strings.Split(value, ";")
	seen := map[string]bool{}
	// An absent fetch directive is unrestricted unless default-src exists.
	// In particular, a separate Trusted Types policy must stay fetch-neutral.
	fallback := ""
	hasDefault := false
	for _, part := range parts {
		fields := strings.Fields(part)
		if len(fields) > 0 && strings.EqualFold(fields[0], "default-src") && !hasDefault {
			fallback = strings.Join(fields[1:], " ")
			hasDefault = true
		}
	}
	for i, p := range parts {
		fields := strings.Fields(p)
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		seen[name] = true
		switch name {
		case "script-src", "script-src-elem":
			// A nonce disables otherwise effective unsafe-inline. Keep those
			// policies unchanged so the site's inline scripts still execute.
			if allowsInlineScripts(fields[1:]) {
				continue
			}
			clean := []string{fields[0]}
			for _, v := range fields[1:] {
				if v != "'none'" {
					clean = append(clean, v)
				}
			}
			parts[i] = strings.Join(clean, " ") + " 'nonce-" + nonce + "'"
		case "connect-src":
			parts[i] = strings.ReplaceAll(p, "'none'", "") + " 'self'"
		case "style-src", "style-src-elem":
			parts[i] = strings.ReplaceAll(p, "'none'", "") + " 'self'"
			if !allowsInlineScripts(fields[1:]) {
				parts[i] += " 'nonce-" + nonce + "'"
			}
		}
	}
	if !seen["script-src"] && hasDefault && !allowsInlineScripts(strings.Fields(fallback)) {
		parts = append(parts, "script-src "+strings.ReplaceAll(fallback, "'none'", "")+" 'nonce-"+nonce+"'")
	}
	if !seen["connect-src"] && hasDefault {
		parts = append(parts, "connect-src "+strings.ReplaceAll(fallback, "'none'", "")+" 'self'")
	}
	if !seen["style-src"] && hasDefault {
		parts = append(parts, "style-src "+strings.ReplaceAll(fallback, "'none'", "")+" 'self'")
		if !allowsInlineScripts(strings.Fields(fallback)) {
			parts[len(parts)-1] += " 'nonce-" + nonce + "'"
		}
	}
	return strings.Join(parts, ";")
}

func allowsInlineScripts(sources []string) bool {
	inline := false
	for _, source := range sources {
		if source == "'unsafe-inline'" {
			inline = true
		}
		if strings.HasPrefix(source, "'nonce-") || strings.HasPrefix(source, "'sha256-") || strings.HasPrefix(source, "'sha384-") || strings.HasPrefix(source, "'sha512-") || source == "'strict-dynamic'" {
			return false
		}
	}
	return inline
}
func Inject(body []byte, nonce string) ([]byte, error) {
	if !bytes.Contains(bytes.ToLower(body), []byte("<html")) {
		return nil, fmt.Errorf("missing HTML document")
	}
	doc, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	var head *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "head" {
				head = n
			}
			if n.Data == "meta" {
				// Inject renders UTF-8, including pages decoded from Windows-1250.
				for i, v := range n.Attr {
					if v.Key == "charset" {
						n.Attr[i].Val = "utf-8"
					}
					if v.Key == "http-equiv" && strings.EqualFold(v.Val, "Content-Type") {
						for j, attr := range n.Attr {
							if attr.Key == "content" {
								n.Attr[j].Val = "text/html; charset=utf-8"
							}
						}
					}
				}
				isCSP := false
				for _, v := range n.Attr {
					if v.Key == "http-equiv" && strings.EqualFold(v.Val, "Content-Security-Policy") {
						isCSP = true
					}
				}
				if isCSP {
					for i, v := range n.Attr {
						if v.Key == "content" {
							n.Attr[i].Val = policy(v.Val, nonce)
						}
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if head == nil {
		return nil, fmt.Errorf("missing head")
	}
	// Execute before upstream deferred/module scripts or hydration can replace
	// the head. No external script request can be intercepted by a stale worker.
	script := &html.Node{Type: html.ElementNode, Data: "script", DataAtom: atom.Script, Attr: []html.Attribute{{Key: "nonce", Val: nonce}, {Key: "data-pauseproxy", Val: "client"}}}
	script.AppendChild(&html.Node{Type: html.TextNode, Data: string(web.Client)})
	head.InsertBefore(script, head.FirstChild)
	var b bytes.Buffer
	e = html.Render(&b, doc)
	return b.Bytes(), e
}
func (a *Addon) StreamResponseModifier(f *proxy.Flow, in io.Reader) io.Reader {
	if !a.API.Target(f.Request.URL.Hostname()) || strings.HasPrefix(f.Request.URL.Path, "/__pauseproxy/") || f.Request.Method == "HEAD" {
		return in
	}
	mt, params, _ := mime.ParseMediaType(f.Response.Header.Get("Content-Type"))
	if mt != "text/html" {
		return in
	}
	body, e := io.ReadAll(io.LimitReader(in, maxHTML+1))
	if len(body) > maxHTML {
		e = fmt.Errorf("HTML too large")
	}
	if e == nil {
		var r io.Reader = bytes.NewReader(body)
		var closer io.Closer
		switch strings.ToLower(f.Response.Header.Get("Content-Encoding")) {
		case "", "identity":
		case "gzip":
			g, err := gzip.NewReader(r)
			e = err
			if err == nil {
				r = g
				closer = g
			}
		case "deflate":
			g, err := zlib.NewReader(r)
			e = err
			if err == nil {
				r = g
				closer = g
			}
		case "br":
			r = brotli.NewReader(r)
		default:
			e = fmt.Errorf("unsupported encoding")
		}
		if e == nil {
			body, e = io.ReadAll(io.LimitReader(r, maxHTML+1))
			if len(body) > maxHTML {
				e = fmt.Errorf("decoded HTML too large")
			}
		}
		if closer != nil {
			_ = closer.Close()
		}
	}
	charset := strings.ToLower(params["charset"])
	if e == nil {
		switch charset {
		case "", "utf-8", "utf8":
		case "windows-1250", "cp1250", "x-cp1250":
			body, e = io.ReadAll(io.LimitReader(charmap.Windows1250.NewDecoder().Reader(bytes.NewReader(body)), maxHTML+1))
			if len(body) > maxHTML {
				e = fmt.Errorf("UTF-8 HTML too large")
			}
		default:
			e = fmt.Errorf("unsupported charset")
		}
	}
	nonce := state.Token()
	if e == nil {
		body, e = Inject(body, nonce)
	}
	h := f.Response.Header
	h.Del("Content-Encoding")
	h.Del("Content-Length")
	h.Set("Content-Type", "text/html; charset=utf-8")
	if e != nil {
		a.API.RecordInjection(false)
		f.Response.StatusCode = 502
		body = []byte(blockPage("Injection failed; page protection could not start.", false))
		h.Del("Content-Security-Policy")
		h.Del("Content-Security-Policy-Report-Only")
	} else {
		a.API.RecordInjection(true)
		for _, key := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
			values := h.Values(key)
			h.Del(key)
			for _, v := range values {
				h.Add(key, policy(v, nonce))
			}
		}
	}
	h.Set("Cache-Control", "no-store")
	return bytes.NewReader(body)
}
