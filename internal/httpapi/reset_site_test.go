package httpapi

import (
	"net/http/httptest"
	"pauseproxy/internal/config"
	"pauseproxy/internal/state"
	"strings"
	"testing"
	"time"
)

func TestResetSiteAPI(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "test"
	a := &API{Config: c, State: state.New(c, time.Now)}
	call := func(method, body, token, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, c.AdminURL+"/api/sites/x.com/reset", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		a.Admin(w, r)
		return w
	}
	if w := call("POST", `{"user":"dev"}`, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("POST", `{"user":"dev"}`, "test", "https://evil.test"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{}`, `{"user":"missing"}`, `{"user":"dev","unknown":true}`} {
		if w := call("POST", body, "test", ""); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := call("POST", `{"user":"dev"}`, "test", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", `{}`, "test", ""); w.Code == 200 {
		t.Fatal("GET mutated state")
	}
}
