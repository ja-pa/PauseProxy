package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"pauseproxy/internal/config"
	"pauseproxy/internal/sites"
	"pauseproxy/internal/state"
	"pauseproxy/web"
	"strings"
	"sync"
)

type API struct {
	Config        config.Config
	State         *state.Store
	PublicCert    []byte
	diagnosticsMu sync.Mutex
	diagnostics   Diagnostics
}

type Diagnostics struct {
	Injections uint64 `json:"html_injections"`
	Failures   uint64 `json:"injection_failures"`
	Sessions   uint64 `json:"sessions_created"`
	Heartbeats uint64 `json:"heartbeats_received"`
}

func (a *API) RecordInjection(ok bool) {
	a.diagnosticsMu.Lock()
	defer a.diagnosticsMu.Unlock()
	if ok {
		a.diagnostics.Injections++
	} else {
		a.diagnostics.Failures++
	}
}

func (a *API) Diagnostics() Diagnostics {
	a.diagnosticsMu.Lock()
	defer a.diagnosticsMu.Unlock()
	return a.diagnostics
}

func JSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func Error(w http.ResponseWriter, code int, e string)           { JSON(w, code, map[string]string{"error": e}) }
func decode(w http.ResponseWriter, r *http.Request, v any) bool { return decodeLimit(w, r, v, 4096) }
func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		Error(w, 415, "application/json required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		Error(w, 400, "invalid JSON")
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		Error(w, 400, "one JSON object required")
		return false
	}
	return true
}
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o == "" || o == "http://"+r.Host || o == "https://"+r.Host
}
func (a *API) Client(w http.ResponseWriter, r *http.Request, user, ip string) {
	a.client(w, r, user, ip, false)
}
func (a *API) client(w http.ResponseWriter, r *http.Request, user, ip string, public bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !sameOrigin(r) {
		Error(w, 403, "origin denied")
		return
	}
	switch r.URL.Path {
	case "/__pauseproxy/grayscale.css":
		if r.Method != "GET" {
			Error(w, 405, "GET required")
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write([]byte("html[data-pauseproxy-grayscale]{filter:grayscale(100%)!important}"))
	case "/__pauseproxy/client.css":
		if r.Method != "GET" {
			Error(w, 405, "GET required")
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(web.ClientCSS)
	case "/__pauseproxy/client.js":
		if r.Method != "GET" {
			Error(w, 405, "GET required")
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(web.Client)
	case "/__pauseproxy/session":
		if r.Method != "POST" {
			Error(w, 405, "POST required")
			return
		}
		var b struct{}
		if !decode(w, r, &b) {
			return
		}
		if public {
			selected := r.URL.Query().Get("site")
			var status state.Status
			if selected == "" {
				status = a.State.Status(user)
			} else {
				status = a.State.Status(user, selected)
			}
			if !status.Tracking {
				JSON(w, 200, status)
				return
			}
		}
		if !public && !a.Target(r.Host) {
			JSON(w, 200, a.State.Status(user, ""))
			return
		}
		var s state.Session
		var e error
		if public {
			if selected := r.URL.Query().Get("site"); selected != "" {
				s, e = a.State.Create(user, ip, selected)
			} else {
				s, e = a.State.Create(user, ip)
			}
		} else {
			s, e = a.State.CreateFor(user, ip, config.Host(r.Host))
		}
		if e != nil {
			Error(w, 429, e.Error())
			return
		}
		JSON(w, 201, struct {
			state.Session
			AdminURL string `json:"admin_url"`
		}{s, a.Config.AdminURL})
		a.diagnosticsMu.Lock()
		a.diagnostics.Sessions++
		a.diagnosticsMu.Unlock()
	case "/__pauseproxy/heartbeat":
		if r.Method != "POST" {
			Error(w, 405, "POST required")
			return
		}
		var b state.Heartbeat
		if !decode(w, r, &b) {
			return
		}
		host := config.Host(r.Host)
		if public {
			host = ""
		}
		s, e := a.State.BeatFrom(user, ip, b, host)
		if e != nil {
			code := 400
			if e == state.ErrSession {
				code = 401
			}
			if e == state.ErrRate {
				code = 429
			}
			JSON(w, code, map[string]any{"error": e.Error(), "instance_id": s.Instance})
			return
		}
		JSON(w, 200, s)
		a.diagnosticsMu.Lock()
		a.diagnostics.Heartbeats++
		a.diagnosticsMu.Unlock()
	case "/__pauseproxy/extra-time":
		if r.Method != "POST" {
			Error(w, 405, "POST required")
			return
		}
		var b state.ExtraRequest
		if !decode(w, r, &b) {
			return
		}
		host := config.Host(r.Host)
		if public {
			host = ""
		}
		result, err := a.State.ExtraTime(user, ip, host, b)
		if err != nil {
			code := 400
			if err == state.ErrSession {
				code = 401
			}
			if err == state.ErrExtra {
				code = 409
			}
			Error(w, code, err.Error())
			return
		}
		JSON(w, 200, result)
	case "/__pauseproxy/status":
		if r.Method != "GET" {
			Error(w, 405, "GET required")
			return
		}
		selected := r.URL.Query().Get("site")
		if !public {
			selected = ""
			if site, ok := a.State.Match(r.Host); ok {
				selected = site.Domain
			}
		}
		if selected != "" {
			JSON(w, 200, a.State.Status(user, selected))
		} else if public {
			JSON(w, 200, a.State.Status(user))
		} else {
			JSON(w, 200, a.State.Status(user, ""))
		}
	default:
		Error(w, 404, "reserved endpoint not found")
	}
}
func (a *API) PAC() string {
	rules := a.Rules()
	hosts := []string{}
	for _, site := range rules.Sites {
		if site.Enabled {
			hosts = append(hosts, site.Domains...)
		}
	}
	hostJSON, _ := json.Marshal(hosts)
	bypass, _ := json.Marshal(rules.Bypass)
	addr, _ := json.Marshal("PROXY " + a.Config.AdvertisedProxy)
	return fmt.Sprintf(`function FindProxyForURL(url, host) {
 host = host.toLowerCase().replace(/\.$/, "");
 var bypass = %s;
 for(var j=0;j<bypass.length;j++){if(host===bypass[j] || dnsDomainIs(host,"."+bypass[j])) return "DIRECT";}
 var hosts = %s;
 for (var i=0;i<hosts.length;i++) {
   if (host === hosts[i]) return %s;
 }
 return "DIRECT";
}
`, bypass, hostJSON, addr)
}
func (a *API) Public(w http.ResponseWriter, r *http.Request) {
	user, ip := a.Config.User(r.RemoteAddr)
	if user == "" {
		Error(w, 403, "unknown device")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(r.URL.Path, "/__pauseproxy/") {
		a.client(w, r, user, ip, true)
		return
	}
	if r.Method != "GET" {
		Error(w, 405, "GET required")
		return
	}
	switch r.URL.Path {
	case "/mvp.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(web.MVPCSS)
	case "/app.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(web.AppCSS)
	case "/proxy.pac":
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		_, _ = io.WriteString(w, a.PAC())
	case "/healthz":
		JSON(w, 200, map[string]string{"status": "ok"})
	case "/ca-cert.pem":
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", `attachment; filename="pauseproxy-ca.pem"`)
		_, _ = w.Write(a.PublicCert)
	case "/demo-sites":
		JSON(w, 200, a.Rules().Sites)
	case "/demo":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(web.Demo)
	default:
		Error(w, 404, "not found")
	}
}
func (a *API) Admin(w http.ResponseWriter, r *http.Request) {
	// Exact Host check also prevents DNS rebinding; no CORS headers are emitted.
	u, err := url.Parse(a.Config.AdminURL)
	if err != nil || r.Host != u.Host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != a.Config.AdminURL) {
		Error(w, 403, "host/origin denied")
		return
	}
	if user, _ := a.Config.User(r.RemoteAddr); user == "" {
		Error(w, 403, "unknown device")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == "GET" && (r.URL.Path == "/mvp.css" || r.URL.Path == "/app.css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		if r.URL.Path == "/mvp.css" {
			_, _ = w.Write(web.MVPCSS)
		} else {
			_, _ = w.Write(web.AppCSS)
		}
		return
	}
	if r.URL.Path == "/" && r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(web.Admin)
		return
	}
	if r.URL.Path == "/admin.js" && r.Method == "GET" {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(web.AdminJS)
		return
	}
	if r.URL.Path == "/howto" && r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(web.Howto)
		return
	}
	if r.URL.Path == "/connection" && r.Method == "GET" {
		_, port, _ := net.SplitHostPort(a.Config.PACAddr)
		base := "http://" + net.JoinHostPort(u.Hostname(), port)
		JSON(w, 200, map[string]string{"pac": base + "/proxy.pac", "ca": base + "/ca-cert.pem", "demo": base + "/demo"})
		return
	}
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if a.Config.AdminPassword == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(auth), []byte(a.Config.AdminPassword)) != 1 {
		Error(w, 401, "incorrect or missing password")
		return
	}
	switch {
	case r.URL.Path == "/api/state" && r.Method == "GET":
		JSON(w, 200, struct {
			state.Snapshot
			Diagnostics Diagnostics `json:"diagnostics"`
		}{a.State.Snapshot(), a.Diagnostics()})
	case r.URL.Path == "/api/sites" && r.Method == "GET":
		JSON(w, 200, a.Rules())
	case r.URL.Path == "/api/groups" && r.Method == "POST":
		var b struct {
			Name    string `json:"name"`
			Seconds int    `json:"daily_limit_seconds"`
		}
		if !decode(w, r, &b) {
			return
		}
		id := state.Token()
		a.change(w, func(d *sites.Document) error {
			d.Groups = append(d.Groups, sites.Group{ID: id, Name: b.Name, Seconds: b.Seconds})
			return nil
		})
	case strings.HasPrefix(r.URL.Path, "/api/groups/") && (r.Method == "PATCH" || r.Method == "DELETE"):
		id := strings.TrimPrefix(r.URL.Path, "/api/groups/")
		var b struct {
			Name    *string `json:"name"`
			Seconds *int    `json:"daily_limit_seconds"`
		}
		if r.Method == "PATCH" && !decode(w, r, &b) {
			return
		}
		a.change(w, func(d *sites.Document) error {
			if r.Method == "DELETE" {
				return d.RemoveGroup(id)
			}
			for i := range d.Groups {
				if d.Groups[i].ID == id {
					if b.Name != nil {
						d.Groups[i].Name = *b.Name
					}
					if b.Seconds != nil {
						d.Groups[i].Seconds = *b.Seconds
					}
					return nil
				}
			}
			return fmt.Errorf("group does not exist")
		})
	case r.URL.Path == "/api/bypass-domains" && r.Method == "GET":
		JSON(w, 200, map[string]any{"bypass_domains": a.Rules().Bypass})
	case r.URL.Path == "/api/bypass-domains" && r.Method == "PUT":
		var b struct {
			Bypass []string `json:"bypass_domains"`
		}
		if !decodeLimit(w, r, &b, 65536) {
			return
		}
		a.change(w, func(d *sites.Document) error { d.Bypass = b.Bypass; return nil })
	case r.URL.Path == "/api/sites" && r.Method == "POST":
		var b struct {
			sites.Site
			Enabled *bool    `json:"enabled"`
			Bypass  []string `json:"bypass_domains"`
		}
		if !decodeLimit(w, r, &b, 65536) {
			return
		}
		if b.Mode == "" {
			b.Mode = "limit"
		}
		if b.Seconds == 0 {
			b.Seconds = 900
		}
		b.Site.Enabled = b.Enabled == nil || *b.Enabled
		if len(b.Domains) == 0 {
			b.Domains = []string{b.Domain, "www." + b.Domain}
		}
		a.change(w, func(d *sites.Document) error {
			d.Sites = append(d.Sites, b.Site)
			for _, v := range b.Bypass {
				found := false
				for _, old := range d.Bypass {
					if old == v {
						found = true
					}
				}
				if !found {
					d.Bypass = append(d.Bypass, v)
				}
			}
			return nil
		})
	case strings.HasPrefix(r.URL.Path, "/api/sites/") && (r.Method == "PATCH" || r.Method == "DELETE"):
		domain, e := sites.Domain(strings.TrimPrefix(r.URL.Path, "/api/sites/"))
		if e != nil {
			Error(w, 400, e.Error())
			return
		}
		var b struct {
			GroupID          *string   `json:"group_id"`
			BudgetMode       *string   `json:"budget_mode"`
			Domains          *[]string `json:"domains"`
			Enabled          *bool     `json:"enabled"`
			Mode             *string   `json:"mode"`
			Seconds          *int      `json:"daily_limit_seconds"`
			GrayscaleEnabled *bool     `json:"grayscale_enabled"`
			GrayscaleAfter   *int      `json:"grayscale_after_seconds"`
			ExtraEnabled     *bool     `json:"extra_time_enabled"`
			ExtraSeconds     *int      `json:"extra_time_seconds"`
			ExtraMaxUses     *int      `json:"extra_time_max_uses"`
			Bypass           *[]string `json:"bypass_domains"`
		}
		if r.Method == "PATCH" && !decodeLimit(w, r, &b, 65536) {
			return
		}
		a.change(w, func(d *sites.Document) error {
			for i := range d.Sites {
				if d.Sites[i].Domain == domain {
					if r.Method == "DELETE" {
						d.Sites = append(d.Sites[:i], d.Sites[i+1:]...)
					} else {
						v := &d.Sites[i]
						if b.GroupID != nil {
							v.GroupID = *b.GroupID
						}
						if b.BudgetMode != nil {
							v.BudgetMode = *b.BudgetMode
						}
						if b.GrayscaleEnabled != nil {
							v.GrayscaleEnabled = *b.GrayscaleEnabled
						}
						if b.GrayscaleAfter != nil {
							v.GrayscaleAfter = *b.GrayscaleAfter
						}
						if b.ExtraEnabled != nil {
							v.ExtraEnabled = *b.ExtraEnabled
						}
						if b.ExtraSeconds != nil {
							v.ExtraSeconds = *b.ExtraSeconds
						}
						if b.ExtraMaxUses != nil {
							v.ExtraMaxUses = *b.ExtraMaxUses
						}
						if b.Domains != nil {
							v.Domains = *b.Domains
						}
						if b.Enabled != nil {
							v.Enabled = *b.Enabled
						}
						if b.Mode != nil {
							v.Mode = *b.Mode
						}
						if b.Seconds != nil {
							v.Seconds = *b.Seconds
						}
						if b.Bypass != nil {
							d.Bypass = *b.Bypass
						}
					}
					return nil
				}
			}
			return fmt.Errorf("site does not exist")
		})
	case r.URL.Path == "/api/limits" && r.Method == "PATCH":
		Error(w, 410, "Set a site limit via /api/sites; budgets are per site.")
	case r.URL.Path == "/api/reset" && r.Method == "POST":
		var b struct{}
		if !decode(w, r, &b) {
			return
		}
		a.State.Reset()
		JSON(w, 200, map[string]bool{"ok": true})
	default:
		Error(w, 404, "endpoint/method not found")
	}
}

func (a *API) Rules() sites.Document {
	if a.State != nil {
		return a.State.Rules()
	}
	return sites.Seed(a.Config)
}
func (a *API) Target(host string) bool {
	if a.State != nil {
		_, ok := a.State.Match(host)
		return ok
	}
	return a.Config.Target(host)
}
func (a *API) change(w http.ResponseWriter, fn func(*sites.Document) error) {
	if e := a.State.ChangeRules(fn); e != nil {
		Error(w, 400, e.Error())
		return
	}
	JSON(w, 200, a.Rules())
}
