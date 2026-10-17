package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"pauseproxy/internal/config"
	"pauseproxy/internal/sites"
	"pauseproxy/internal/state"
	"strings"
	"testing"
	"time"
)

func TestExtraAPI(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "test"
	now := time.Now()
	a := &API{Config: c, State: state.New(c, func() time.Time { return now })}
	admin := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, c.AdminURL+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.Admin(w, r)
		return w
	}
	body := `{"domain":"example.com","daily_limit_seconds":10,"extra_time_method":"math","blur_enabled":true,"blur_after_seconds":4,"grayscale_enabled":true,"grayscale_after_seconds":5,"extra_time_enabled":true,"extra_time_seconds":180,"extra_time_max_uses":2}`
	if w := admin("POST", "/api/sites", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := admin("PATCH", "/api/sites/example.com", `{"grayscale_after_seconds":10}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := admin("PATCH", "/api/sites/example.com", `{"extra_time_max_uses":3}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if r, _ := a.Rules().Find("example.com"); !r.BlurEnabled || r.BlurAfter != 4 {
		t.Fatal("unrelated PATCH changed blur", r)
	}
	if w := admin("PATCH", "/api/sites/example.com", `{"blur_after_seconds":10}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := admin("PATCH", "/api/sites/example.com", `{"blur_after_seconds":6}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := admin("PATCH", "/api/sites/example.com", `{"blur_enabled":false}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if r, _ := a.Rules().Find("example.com"); r.BlurEnabled || r.BlurAfter != 6 {
		t.Fatal(r)
	}
	if w := admin("PATCH", "/api/sites/example.com", `{"blur_enabled":true,"blur_after_seconds":4}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	rule, _ := a.Rules().Find("example.com")
	if rule.ExtraMethod != "math" || !rule.BlurEnabled || rule.BlurAfter != 4 || rule.ExtraMaxUses != 3 || rule.GrayscaleAfter != 5 {
		t.Fatal(rule)
	}
	a.State.ChangeRules(func(d *sites.Document) error { d.Sites[0].Seconds = 1; d.Sites[0].ExtraEnabled = true; return nil })
	se, _ := a.State.CreateFor("dev", "127.0.0.1", "x.com")
	a.State.BeatFrom("dev", "127.0.0.1", state.Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 1, Visible: true}, "x.com")
	now = now.Add(2 * time.Second)
	payload, _ := json.Marshal(state.ExtraRequest{ID: se.ID, Instance: se.Instance, RequestID: "retry"})
	call := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://x.com/__pauseproxy/extra-time", strings.NewReader(string(payload)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		a.Client(w, r, "dev", "127.0.0.1")
		return w
	}
	if w := call("https://evil.test"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for i := 0; i < 2; i++ {
		if w := call("https://x.com"); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if v := a.State.Status("dev"); v.Remaining != 180 || v.ExtraUsesRemaining != 0 {
		t.Fatal(v)
	}
}
