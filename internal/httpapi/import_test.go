package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"pauseproxy/internal/config"
	"pauseproxy/internal/sites"
	"pauseproxy/internal/state"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestImportConfiguration(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "test-import"
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	a := &API{Config: c, State: state.New(c, func() time.Time { return now })}
	path := filepath.Join(t.TempDir(), "sites.json")
	a.State.ConfigureRules(a.Rules(), path)
	call := func(method, body, token, origin, host string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, c.AdminURL+"/api/sites", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", origin)
		if host != "" {
			r.Host = host
		}
		w := httptest.NewRecorder()
		a.Admin(w, r)
		return w
	}
	put := func(body string) *httptest.ResponseRecorder {
		return call("PUT", body, c.AdminPassword, c.AdminURL, "")
	}
	d := a.Rules()
	d.Groups = []sites.Group{{ID: "fun", Name: "Fun", Seconds: 600}}
	d.Sites[0].GroupID, d.Sites[0].BudgetMode = "fun", "capped"
	d.Sites[0].BlurEnabled, d.Sites[0].BlurAfter = true, 50
	d.Sites[0].GrayscaleEnabled, d.Sites[0].GrayscaleAfter = true, 60
	d.Sites[0].ExtraEnabled, d.Sites[0].ExtraMethod = true, "reflection"
	d.Bypass = []string{"twimg.com"}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(d)
	if w := put(string(body)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	exported := call("GET", "", c.AdminPassword, "", "").Body.String()
	if w := put(exported); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	disk, err := sites.Load(path, c)
	if err != nil || !reflect.DeepEqual(disk, d) || !reflect.DeepEqual(a.Rules(), d) {
		t.Fatal("round trip lost rules", err, disk)
	}
	// Settle an active lease under the old rules before publishing the import.
	se, err := a.State.CreateFor("dev", "127.0.0.1", "x.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.State.BeatFrom(se.User, se.IP, state.Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 1, Visible: true}, se.Host); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	d.Sites[0].Seconds = 300
	body, _ = json.Marshal(d)
	if w := put(string(body)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if got := a.State.Status("dev", "x.com").Remaining; got != 295 {
		t.Fatal("usage reset on import", got)
	}

	before := a.Rules()
	pac := a.PAC()
	bytesBefore, _ := os.ReadFile(path)
	for _, invalid := range []string{
		`null`, `[]`, `{}`, `{"version":3}`, `{"version":2,"users":{}}`,
		`{"version":2,"sites":[]} {}`, `{"version":2,"sites":[{"domain":"x.com","unknown":true}]}`,
		strings.Replace(string(body), `"group_id":"fun"`, `"group_id":"missing"`, 1),
		strings.Replace(string(body), `"domains":["x.com","www.x.com"]`, `"domains":["https://x.com"]`, 1),
		strings.Repeat(" ", 1024*1024) + `{"version":2}`,
	} {
		if w := put(invalid); w.Code != 400 {
			t.Fatalf("invalid import accepted: %d %.200s", w.Code, invalid)
		}
		got, _ := os.ReadFile(path)
		if string(got) != string(bytesBefore) || !reflect.DeepEqual(a.Rules(), before) || a.PAC() != pac {
			t.Fatal("invalid import changed rules")
		}
	}
	if w := call("PUT", exported, "wrong", "", ""); w.Code != 401 {
		t.Fatal("missing auth protection")
	}
	if w := call("PUT", exported, c.AdminPassword, "https://evil.test", ""); w.Code != 403 {
		t.Fatal("missing origin protection")
	}
	if w := call("PUT", exported, c.AdminPassword, "", "evil.test"); w.Code != 403 {
		t.Fatal("missing host protection")
	}

	// A failed atomic replacement must leave the existing file and PAC intact.
	a.State.ConfigureRules(before, t.TempDir()) // Replacing a directory with a file fails.
	if w := put(`{"version":2,"sites":[],"groups":[],"bypass_domains":[]}`); w.Code != 400 {
		t.Fatal("write failure accepted")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(bytesBefore) || !reflect.DeepEqual(a.Rules(), before) || a.PAC() != pac {
		t.Fatal("failed commit changed rules")
	}
	a.State.ConfigureRules(before, path)
	if w := put(`{"version":1,"sites":[],"bypass_domains":[]}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if a.Rules().Version != 2 || len(a.Rules().Sites) != 0 || len(a.Rules().Groups) != 0 || len(a.Rules().Bypass) != 0 || a.Target("x.com") {
		t.Fatal("empty import did not replace configuration")
	}
	st, err := a.State.BeatFrom(se.User, se.IP, state.Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 2}, se.Host)
	if err != nil || st.Tracking {
		t.Fatal("removed site's session still tracked", st, err)
	}
	if w := put(exported); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if a.State.Status("dev", "x.com").Remaining != 115 {
		t.Fatal("remove/reimport reset statistics")
	}
}

func TestImportLegacyAndLargeDocument(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "test-import"
	a := &API{Config: c, State: state.New(c, time.Now)}
	legacy := `{"version":1,"sites":[{"domain":"example.com","domains":["example.com"],"enabled":true,"mode":"limit","daily_limit_seconds":900}],"bypass_domains":[]}`
	// Valid JSON over the ordinary list API's 64 KiB, still within 1 MiB.
	r := httptest.NewRequest("PUT", c.AdminURL+"/api/sites", strings.NewReader(strings.Repeat(" ", 65536)+legacy))
	r.Header.Set("Authorization", "Bearer "+c.AdminPassword)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Admin(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	d := a.Rules()
	if d.Version != 2 || len(d.Sites) != 1 || d.Sites[0].Domain != "example.com" || d.Sites[0].BudgetMode != "independent" || d.Sites[0].ExtraMethod != "click" || a.Target("x.com") || !a.Target("example.com") {
		t.Fatal("bad legacy migration", d)
	}
}
