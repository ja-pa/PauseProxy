package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"pauseproxy/internal/config"
	"pauseproxy/internal/sites"
	"pauseproxy/internal/state"
	"strings"
	"testing"
	"time"
)

func TestGroupAPIAndSiteModes(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "test-only"
	a := &API{Config: c, State: state.New(c, time.Now)}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, c.AdminURL+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer test-only")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.Admin(w, r)
		return w
	}
	w := call("POST", "/api/groups", `{"name":"Fun","daily_limit_seconds":1800}`)
	var d sites.Document
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil || len(d.Groups) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := d.Groups[0].ID
	if id == "" || d.Version != 2 {
		t.Fatal(d)
	}
	if w = call("POST", "/api/groups", `{"name":"fun","daily_limit_seconds":900}`); w.Code != 400 {
		t.Fatal("duplicate accepted", w.Body.String())
	}
	for _, mode := range []string{"shared", "capped", "independent"} {
		body := `{"group_id":"` + id + `","budget_mode":"` + mode + `"}`
		if w = call("PATCH", "/api/sites/x.com", body); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if got, _ := a.Rules().Find("x.com"); got.BudgetMode != mode || got.GroupID != id {
			t.Fatal(got)
		}
	}
	if w = call("PATCH", "/api/sites/x.com", `{"group_id":"missing"}`); w.Code != 400 {
		t.Fatal("unknown group accepted")
	}
	if w = call("PATCH", "/api/groups/"+id, `{"name":"Entertainment","daily_limit_seconds":600}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if g, _ := a.Rules().FindGroup(id); g.Name != "Entertainment" || g.Seconds != 600 {
		t.Fatal(g)
	}
	if w = call("PATCH", "/api/sites/x.com", `{"budget_mode":"shared"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = call("DELETE", "/api/groups/"+id, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if got, _ := a.Rules().Find("x.com"); got.GroupID != "" || got.BudgetMode != "independent" || got.Seconds != 600 {
		t.Fatal(got)
	}
	if w = call("DELETE", "/api/groups/"+id, ""); w.Code != 400 {
		t.Fatal("missing group deleted")
	}
	for _, tc := range []struct {
		token, host, remote, contentType string
		want                             int
	}{
		{"", c.AdminAddr, "127.0.0.1:1234", "application/json", 401},
		{"test-only", "evil.example", "127.0.0.1:1234", "application/json", 403},
		{"test-only", c.AdminAddr, "192.0.2.5:1234", "application/json", 403},
		{"test-only", c.AdminAddr, "127.0.0.1:1234", "text/plain", 415},
	} {
		r := httptest.NewRequest("POST", "https://"+tc.host+"/api/groups", strings.NewReader(`{"name":"News","daily_limit_seconds":900}`))
		r.RemoteAddr = tc.remote
		r.Header.Set("Content-Type", tc.contentType)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w = httptest.NewRecorder()
		a.Admin(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
}

func TestFailedGroupSavePreservesRules(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "test-only"
	s := state.New(c, time.Now)
	// A directory cannot be replaced by the atomic rules-file writer.
	s.ConfigureRules(s.Rules(), filepath.Join(t.TempDir()))
	a := &API{Config: c, State: s}
	r := httptest.NewRequest("POST", c.AdminURL+"/api/groups", strings.NewReader(`{"name":"Fun","daily_limit_seconds":900}`))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Authorization", "Bearer test-only")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Admin(w, r)
	if w.Code != 400 || len(a.Rules().Groups) != 0 {
		t.Fatal("failed write changed live rules", w.Code, a.Rules())
	}
}
