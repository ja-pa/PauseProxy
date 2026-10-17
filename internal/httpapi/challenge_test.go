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

func TestChallengeAPI(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "test"
	now := time.Now()
	a := &API{Config: c, State: state.New(c, func() time.Time { return now })}
	if err := a.State.ChangeRules(func(d *sites.Document) error {
		r := &d.Sites[0]
		r.Seconds = 1
		r.ExtraEnabled = true
		r.ExtraMethod = "math"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	se, err := a.State.CreateFor("dev", "127.0.0.1", "x.com")
	if err != nil {
		t.Fatal(err)
	}
	a.State.BeatFrom(se.User, se.IP, state.Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 1, Visible: true}, se.Host)
	now = now.Add(2 * time.Second)
	call := func(method, path, origin string, b state.ExtraRequest) *httptest.ResponseRecorder {
		data, _ := json.Marshal(b)
		r := httptest.NewRequest(method, "https://x.com"+path, strings.NewReader(string(data)))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		a.Client(w, r, se.User, se.IP)
		return w
	}
	b := state.ExtraRequest{ID: se.ID, Instance: se.Instance, RequestID: "one"}
	path := "/__pauseproxy/extra-time/challenge"
	if w := call("GET", path, "", b); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := call("POST", path, "https://evil.test", b); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := call("POST", path, "https://x.com", b)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	var ch state.ExtraChallenge
	if err = json.Unmarshal(w.Body.Bytes(), &ch); err != nil {
		t.Fatal(err)
	}
	if ch.ID == "" || ch.Method != "math" || strings.Contains(w.Body.String(), "answer") {
		t.Fatal(w.Body.String())
	}
	if w := call("POST", "/__pauseproxy/extra-time", "", b); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	b.ChallengeID = ch.ID
	wrong := 0
	b.Answer = &wrong
	if w := call("POST", "/__pauseproxy/extra-time", "", b); w.Code != 400 {
		t.Fatal(w.Code)
	}
	correct := ch.A * ch.B
	b.Answer = &correct
	for range 2 {
		if w := call("POST", "/__pauseproxy/extra-time", "", b); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// Unknown methods are rejected by administration; valid PATCHes preserve omitted fields.
	admin := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", c.AdminURL+"/api/sites/x.com", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Authorization", "Bearer test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.Admin(w, r)
		return w
	}
	if w := admin(`{"extra_time_method":"invalid"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := admin(`{"extra_time_method":"reflection"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := admin(`{"extra_time_max_uses":3}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if r, _ := a.Rules().Find("x.com"); r.ExtraMethod != "reflection" {
		t.Fatal(r)
	}
}
