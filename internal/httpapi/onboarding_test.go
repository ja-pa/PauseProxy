package httpapi

import (
	"net/http"
	"net/http/httptest"
	"pauseproxy/internal/config"
	"pauseproxy/internal/state"
	"strings"
	"testing"
	"time"
)

func TestUnknownDeviceCanReachAdminLogin(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "setup-secret"
	a := &API{Config: c, State: state.New(c, time.Now)}
	for _, path := range []string{"/", "/demo", "/howto"} {
		r := httptest.NewRequest("GET", "http://"+c.PACAddr+path, nil)
		r.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		a.Public(w, r)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != c.AdminURL || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %v", path, w.Code, w.Header())
		}
	}
	for _, path := range []string{"/", "/admin.js", "/mvp.css", "/app.css", "/howto", "/connection"} {
		r := httptest.NewRequest("GET", c.AdminURL+path, nil)
		r.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		a.Admin(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("login resource %s: %d %s", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), c.AdminPassword) {
			t.Fatalf("password leaked at %s", path)
		}
	}
	// A redirect must not turn a denied PAC/API request into login HTML.
	for _, path := range []string{"/proxy.pac", "/__pauseproxy/status", "/__pauseproxy/session", "/demo-sites"} {
		for _, method := range []string{"GET", "POST"} {
			r := httptest.NewRequest(method, "http://"+c.PACAddr+path, nil)
			r.RemoteAddr = "192.0.2.1:1234"
			w := httptest.NewRecorder()
			a.Public(w, r)
			if w.Code != http.StatusForbidden || w.Header().Get("Location") != "" {
				t.Fatalf("%s %s: %d", method, path, w.Code)
			}
		}
	}
	for _, path := range []string{"/", "/api/state"} {
		r := httptest.NewRequest("GET", "https://evil.example"+path, nil)
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("Authorization", "Bearer "+c.AdminPassword)
		w := httptest.NewRecorder()
		a.Admin(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("host check bypassed for %s: %d", path, w.Code)
		}
	}
}
