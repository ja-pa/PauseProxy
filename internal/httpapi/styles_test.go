package httpapi

import (
	"net/http"
	"net/http/httptest"
	"pauseproxy/internal/config"
	"pauseproxy/web"
	"testing"
)

func TestLocalStyles(t *testing.T) {
	c := config.Default()
	a := &API{Config: c}
	for name, handler := range map[string]http.HandlerFunc{"admin": a.Admin, "public": a.Public} {
		for path, content := range map[string][]byte{"/mvp.css": web.MVPCSS, "/app.css": web.AppCSS} {
			t.Run(name+path, func(t *testing.T) {
				r := httptest.NewRequest("GET", c.AdminURL+path, nil)
				r.RemoteAddr = "127.0.0.1:1234"
				w := httptest.NewRecorder()
				handler(w, r)
				if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/css; charset=utf-8" || w.Body.String() != string(content) || len(content) == 0 {
					t.Fatalf("stylesheet unavailable: status=%d type=%q", w.Code, w.Header().Get("Content-Type"))
				}
				r.RemoteAddr = "192.0.2.1:1234"
				w = httptest.NewRecorder()
				handler(w, r)
				if w.Code != http.StatusForbidden {
					t.Fatalf("unknown device got %d", w.Code)
				}
			})
		}
	}
}

func TestBlurStyle(t *testing.T) {
	a := &API{Config: config.Default()}
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "https://x.com/__pauseproxy/blur.css", nil)
		w := httptest.NewRecorder()
		a.Client(w, r, "dev", "127.0.0.1")
		if method != "GET" {
			if w.Code != 405 {
				t.Fatal(w.Code)
			}
			continue
		}
		want := "html[data-pauseproxy-blur] img, html[data-pauseproxy-blur] video { filter: blur(8px) !important; }"
		if w.Code != 200 || w.Body.String() != want || w.Header().Get("Content-Type") != "text/css; charset=utf-8" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w)
		}
	}
}
