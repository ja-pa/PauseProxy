package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"pauseproxy/internal/config"
	"pauseproxy/internal/state"
	"strings"
	"testing"
	"time"
)

func TestClientHeartbeatDiagnostics(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "secret"
	a := &API{Config: c, State: state.New(c, time.Now)}
	call := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://x.com/__pauseproxy/"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.Client(w, r, "dev", "127.0.0.1")
		return w
	}
	w := call("session", "{}")
	var se state.Session
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &se) != nil {
		t.Fatal("session creation failed")
	}
	body, _ := json.Marshal(state.Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 1, Visible: true, Clicks: 2})
	if w := call("heartbeat", string(body)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	call("heartbeat", `{}`)
	if d := a.Diagnostics(); d.Sessions != 1 || d.Heartbeats != 1 {
		t.Fatalf("diagnostics count rejected requests: %+v", d)
	}
	r := httptest.NewRequest("GET", "https://"+c.AdminAddr+"/api/state", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	r.RemoteAddr = "127.0.0.1:1234"
	a.Admin(w, r)
	var got struct {
		Sessions    int         `json:"sessions"`
		Diagnostics Diagnostics `json:"diagnostics"`
	}
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Sessions != 1 || got.Diagnostics.Heartbeats != 1 {
		t.Fatal("admin diagnostics missing", w.Body.String())
	}
}

func TestAdminProtection(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "secret"
	a := &API{Config: c, State: state.New(c, time.Now)}
	for _, v := range []struct {
		host, origin, token string
		want                int
	}{{c.AdminAddr, "", "", 401}, {"evil.example", "", "secret", 403}, {c.AdminAddr, "https://evil.example", "secret", 403}, {c.AdminAddr, "", "secret", 200}} {
		r := httptest.NewRequest("GET", "https://"+v.host+"/api/state", nil)
		r.Header.Set("Origin", v.origin)
		r.Header.Set("Authorization", "Bearer "+v.token)
		w := httptest.NewRecorder()
		r.RemoteAddr = "127.0.0.1:1234"
		a.Admin(w, r)
		if w.Code != v.want {
			t.Fatalf("%+v got %d", v, w.Code)
		}
	}
}
func TestMutationAndPublicRestrictions(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "secret"
	a := &API{Config: c, State: state.New(c, time.Now)}
	r := httptest.NewRequest("PATCH", "https://"+c.AdminAddr+"/api/limits", strings.NewReader(`{"user":"dev","seconds":30}`))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	r.RemoteAddr = "127.0.0.1:1234"
	a.Admin(w, r)
	if w.Code != 410 {
		t.Fatal("mutation allowed non-JSON")
	}
	r = httptest.NewRequest("GET", "http://"+c.PACAddr+"/proxy.pac", nil)
	r.RemoteAddr = "192.168.1.9:1234"
	w = httptest.NewRecorder()
	a.Public(w, r)
	if w.Code != 403 {
		t.Fatal("unknown device allowed")
	}
	if strings.Contains(a.PAC(), "; DIRECT") {
		t.Fatal("target has direct fallback")
	}
}
func TestClientBodiesAndOrigin(t *testing.T) {
	c := config.Default()
	a := &API{Config: c, State: state.New(c, time.Now)}
	for _, body := range []string{`{} {}`, `{"user":"someone"}`, strings.Repeat("x", 5000)} {
		r := httptest.NewRequest("POST", "https://x.com/__pauseproxy/session", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.Client(w, r, "dev", "127.0.0.1")
		if w.Code == 201 {
			t.Fatal("invalid body accepted")
		}
	}
}

func TestHowtoIsPublicButHostAndOriginRemainProtected(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "secret-not-in-howto"
	a := &API{Config: c, State: state.New(c, time.Now)}
	for _, v := range []struct {
		host, origin, method string
		want                 int
	}{
		{c.AdminAddr, "", "GET", 200},
		{c.AdminAddr, "https://" + c.AdminAddr, "GET", 200},
		{"evil.example", "", "GET", 403},
		{c.AdminAddr, "https://evil.example", "GET", 403},
		{c.AdminAddr, "", "POST", 401},
	} {
		r := httptest.NewRequest(v.method, "https://"+v.host+"/howto", nil)
		r.Header.Set("Origin", v.origin)
		w := httptest.NewRecorder()
		r.RemoteAddr = "127.0.0.1:1234"
		a.Admin(w, r)
		if w.Code != v.want {
			t.Fatalf("%+v: got %d", v, w.Code)
		}
		if w.Code == 200 {
			if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("howto headers")
			}
			body := w.Body.String()
			if !strings.Contains(body, "bash ./run_test.sh") || !strings.Contains(body, "certutil -user -addstore Root") || !strings.Contains(body, "fetch('/connection'") {
				t.Fatal("missing setup instructions")
			}
			if strings.Contains(body, a.Config.AdminPassword) || strings.Contains(body, "BEGIN PRIVATE KEY") {
				t.Fatal("secret in howto")
			}
		}
	}
	r := httptest.NewRequest("GET", "https://"+c.AdminAddr+"/", nil)
	w := httptest.NewRecorder()
	r.RemoteAddr = "127.0.0.1:1234"
	a.Admin(w, r)
	if !strings.Contains(w.Body.String(), `href="/howto"`) {
		t.Fatal("admin missing howto link")
	}
}

func TestAdminLANAuthentication(t *testing.T) {
	c := config.Default()
	c.AdminAddr = "0.0.0.0:9083"
	c.AdminURL = "http://192.168.1.50:9083"
	c.AdminPassword = "private-password"
	c.Users["192.168.1.60"] = "android"
	a := &API{Config: c, State: state.New(c, time.Now)}
	for _, tc := range []struct {
		ip, auth, origin string
		want             int
	}{
		{"192.168.1.60", "Bearer private-password", c.AdminURL, 200},
		{"192.168.1.60", "Bearer wrong", c.AdminURL, 401},
		{"192.168.1.60", "", c.AdminURL, 401},
		{"192.168.1.60", "private-password", c.AdminURL, 401},
		{"192.168.1.61", "Bearer private-password", c.AdminURL, 403},
		{"192.168.1.60", "Bearer private-password", "https://192.168.1.50:9083", 403},
	} {
		r := httptest.NewRequest("GET", c.AdminURL+"/api/state", nil)
		r.RemoteAddr = tc.ip + ":1234"
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		a.Admin(w, r)
		if w.Code != tc.want {
			t.Fatalf("expected %d, got %d", tc.want, w.Code)
		}
		if strings.Contains(w.Body.String(), c.AdminPassword) {
			t.Fatal("password leaked")
		}
	}
	r := httptest.NewRequest("GET", c.AdminURL+"/connection", nil)
	r.RemoteAddr = "192.168.1.60:1234"
	w := httptest.NewRecorder()
	a.Admin(w, r)
	if !strings.Contains(w.Body.String(), "http://192.168.1.50:9082/proxy.pac") {
		t.Fatal(w.Body.String())
	}
	r = httptest.NewRequest("POST", "https://x.com/__pauseproxy/session", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.Client(w, r, "android", "192.168.1.60")
	if !strings.Contains(w.Body.String(), c.AdminURL) || strings.Contains(w.Body.String(), c.AdminPassword) {
		t.Fatal("incorrect client metadata")
	}
}

func TestDemoUsesPublicListenerWithWildcardBind(t *testing.T) {
	c := config.Default()
	c.PACAddr = "0.0.0.0:9082"
	a := &API{Config: c, State: state.New(c, time.Now)}
	r := httptest.NewRequest("POST", "http://127.0.0.1:9082/__pauseproxy/session", strings.NewReader("{}"))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Public(w, r)
	var session state.Session
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &session) != nil || session.ID == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	body, _ := json.Marshal(state.Heartbeat{ID: session.ID, Instance: session.Instance, Sequence: 1, Visible: true})
	r = httptest.NewRequest("POST", "http://127.0.0.1:9082/__pauseproxy/heartbeat", strings.NewReader(string(body)))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.Public(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
