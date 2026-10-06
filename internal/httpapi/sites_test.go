package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"pauseproxy/internal/config"
	"pauseproxy/internal/sites"
	"pauseproxy/internal/state"
	"strings"
	"testing"
	"time"
)

func TestSiteAPIAndPAC(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "token"
	a := &API{Config: c, State: state.New(c, time.Now)}
	path := filepath.Join(t.TempDir(), "sites.json")
	a.State.ConfigureRules(a.Rules(), path)
	call := func(method, path, body, token, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://"+c.AdminAddr+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		r.RemoteAddr = "127.0.0.1:1234"
		a.Admin(w, r)
		return w
	}
	for _, endpoint := range []string{"/api/sites", "/api/bypass-domains", "/api/sites/x.com"} {
		if w := call("GET", endpoint, "", "", ""); w.Code != 401 {
			t.Fatal("unprotected", endpoint, w.Code)
		}
		if w := call("POST", endpoint, "{}", "token", "https://evil.test"); w.Code != 403 {
			t.Fatal("origin unprotected")
		}
	}
	if w := call("POST", "/api/sites", `{"domain":"youtube.com","domains":["youtube.com","www.youtube.com","m.youtube.com","youtu.be"],"bypass_domains":["googlevideo.com","ytimg.com"]}`, "token", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if !a.Target("youtube.com") || a.Target("unknown.youtube.com") || a.Target("r1.googlevideo.com") {
		t.Fatal("routing wrong")
	}
	disk, e := sites.Load(path, c)
	if e != nil || len(disk.Sites) != 2 || disk.Sites[1].Seconds != 900 || !disk.Sites[1].Enabled {
		t.Fatal(disk, e)
	}
	before := a.PAC()
	for _, body := range []string{`{"domain":"r1.googlevideo.com"}`, `{"domain":"https://bad.com"}`, `{"domain":"other.com","domains":["other.com","youtube.com"]}`} {
		if w := call("POST", "/api/sites", body, "token", ""); w.Code != 400 {
			t.Fatal("invalid accepted", body, w.Code)
		}
		if a.PAC() != before {
			t.Fatal("failed write changed PAC")
		}
	}
	if w := call("PUT", "/api/bypass-domains", `{"bypass_domains":["youtube.com"]}`, "token", ""); w.Code != 400 {
		t.Fatal("conflicting bypass accepted")
	}
	// Execute the actual generated PAC if Node is installed; Go routing checks above always run.
	if node, e := exec.LookPath("node"); e == nil {
		hosts := []string{"x.com", "www.x.com", "api.x.com", "youtube.com", "m.youtube.com", "googlevideo.com", "r1.googlevideo.com", "video.twimg.com", "x.com.evil.test"}
		encoded, _ := json.Marshal(hosts)
		js := `const vm=require('node:vm'),fs=require('node:fs');const c={dnsDomainIs:(h,s)=>h.endsWith(s)};vm.createContext(c);vm.runInContext(fs.readFileSync(0,'utf8'),c);console.log(JSON.stringify(` + string(encoded) + `.map(h=>c.FindProxyForURL('https://'+h,h))));`
		cmd := exec.Command(node, "-e", js)
		cmd.Stdin = strings.NewReader(a.PAC())
		out, e := cmd.Output()
		if e != nil {
			t.Fatal(e)
		}
		var answers []string
		if e = json.Unmarshal(out, &answers); e != nil {
			t.Fatal(e)
		}
		for i, h := range hosts {
			if (answers[i] != "DIRECT") != a.Target(h) {
				t.Fatal("PAC/MITM disagree", h, answers[i])
			}
		}
	}
	if w := call("PATCH", "/api/sites/youtube.com", `{"daily_limit_seconds":180,"mode":"block"}`, "token", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if !a.State.Status("dev", "youtube.com").Blocked {
		t.Fatal("mode not applied")
	}
	if w := call("PATCH", "/api/sites/youtube.com", `{"enabled":false}`, "token", ""); w.Code != 200 || a.Target("youtube.com") {
		t.Fatal("disable failed")
	}
	if w := call("DELETE", "/api/sites/youtube.com", "", "token", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call("PUT", "/api/bypass-domains", strings.Repeat("x", 65537), "token", ""); w.Code != 400 {
		t.Fatal("body limit ignored")
	}
	a.State.Reset()
	disk, e = sites.Load(path, c)
	if e != nil || len(disk.Sites) != 1 {
		t.Fatal("reset changed disk", e)
	}
	bad := filepath.Join(t.TempDir(), "blocker")
	os.WriteFile(bad, []byte("x"), 0600)
	a.State.ConfigureRules(a.Rules(), filepath.Join(bad, "sites.json"))
	before = a.PAC()
	if w := call("PATCH", "/api/sites/x.com", `{"daily_limit_seconds":250}`, "token", ""); w.Code == 200 || a.PAC() != before {
		t.Fatal("failed commit published")
	}
}

func TestRemovedSiteSessionStopsClient(t *testing.T) {
	c := config.Default()
	a := &API{Config: c, State: state.New(c, time.Now)}
	a.State.ConfigureRules(sites.Document{Version: 1, Sites: []sites.Site{}, Bypass: []string{}}, "")
	for _, url := range []string{
		"https://x.com/__pauseproxy/session",
		"http://" + c.PACAddr + "/__pauseproxy/session?site=x.com",
		"http://" + c.PACAddr + "/__pauseproxy/session",
	} {
		r := httptest.NewRequest("POST", url, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.Client(w, r, "dev", "127.0.0.1")
		var status state.Status
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Tracking {
			t.Fatal("removed site must stop the client", url, w.Code, w.Body.String())
		}
	}
	if a.Diagnostics().Sessions != 0 {
		t.Fatal("removed site created a session")
	}
}
