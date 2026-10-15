package sites

import (
	"encoding/json"
	"fmt"
	"golang.org/x/net/idna"
	"io"
	"net"
	"os"
	"path/filepath"
	"pauseproxy/internal/config"
	"strings"
	"unicode/utf8"
)

type Site struct {
	GroupID          string   `json:"group_id"`
	BudgetMode       string   `json:"budget_mode"`
	GrayscaleEnabled bool     `json:"grayscale_enabled"`
	GrayscaleAfter   int      `json:"grayscale_after_seconds"`
	ExtraEnabled     bool     `json:"extra_time_enabled"`
	ExtraSeconds     int      `json:"extra_time_seconds"`
	ExtraMaxUses     int      `json:"extra_time_max_uses"`
	Domain           string   `json:"domain"`
	Domains          []string `json:"domains"`
	Enabled          bool     `json:"enabled"`
	Mode             string   `json:"mode"`
	Seconds          int      `json:"daily_limit_seconds"`
}
type Document struct {
	Groups  []Group  `json:"groups"`
	Version int      `json:"version"`
	Sites   []Site   `json:"sites"`
	Bypass  []string `json:"bypass_domains"`
}

type Group struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Seconds int    `json:"daily_limit_seconds"`
}

func (d Document) FindGroup(id string) (Group, bool) {
	for _, g := range d.Groups {
		if g.ID == id {
			return g, true
		}
	}
	return Group{}, false
}

func (s Site) SharesGroup() bool { return s.GroupID != "" && s.BudgetMode != "independent" }

func (d *Document) RemoveGroup(id string) error {
	g, ok := d.FindGroup(id)
	if !ok {
		return fmt.Errorf("group does not exist")
	}
	for i := range d.Sites {
		s := &d.Sites[i]
		if s.GroupID != id {
			continue
		}
		if s.BudgetMode == "shared" {
			s.Seconds = g.Seconds
			if s.GrayscaleAfter >= s.Seconds {
				s.GrayscaleEnabled = false
			}
		}
		s.GroupID, s.BudgetMode = "", "independent"
		// Keep the threshold value for later editing if it no longer fits.
	}
	for i, v := range d.Groups {
		if v.ID == id {
			d.Groups = append(d.Groups[:i], d.Groups[i+1:]...)
			break
		}
	}
	return nil
}

func Domain(input string) (string, error) {
	s := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(input), "."))
	if strings.ContainsAny(s, "/:*\\@?# \t\r\n") || s == "" {
		return "", fmt.Errorf("enter a domain without a URL, port or wildcard")
	}
	s, e := idna.Lookup.ToASCII(s)
	if e != nil || len(s) > 253 || net.ParseIP(s) != nil || !strings.Contains(s, ".") {
		return "", fmt.Errorf("invalid domain %q", input)
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid domain %q", input)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", fmt.Errorf("invalid domain %q", input)
			}
		}
	}
	return s, nil
}
func (d Document) Clone() Document {
	b, _ := json.Marshal(d)
	var out Document
	_ = json.Unmarshal(b, &out)
	return out
}
func (d Document) Bypasses(host string) bool {
	host = config.Host(host)
	for _, b := range d.Bypass {
		if host == b || strings.HasSuffix(host, "."+b) {
			return true
		}
	}
	return false
}
func (d Document) Match(host string) (Site, bool) {
	host = config.Host(host)
	if d.Bypasses(host) {
		return Site{}, false
	}
	for _, s := range d.Sites {
		if s.Enabled {
			for _, h := range s.Domains {
				if h == host {
					return s, true
				}
			}
		}
	}
	return Site{}, false
}
func (d Document) Find(domain string) (Site, bool) {
	for _, s := range d.Sites {
		if s.Domain == domain {
			return s, true
		}
	}
	return Site{}, false
}
func (d *Document) Validate() error {
	if (d.Version != 1 && d.Version != 2) || len(d.Sites) > 32 || len(d.Bypass) > 128 || len(d.Groups) > 32 {
		return fmt.Errorf("invalid version or too many rules")
	}
	if d.Version == 1 {
		if len(d.Groups) != 0 {
			return fmt.Errorf("groups require rules version 2")
		}
		for _, s := range d.Sites {
			if s.GroupID != "" || (s.BudgetMode != "" && s.BudgetMode != "independent") {
				return fmt.Errorf("groups require rules version 2")
			}
		}
		d.Version = 2
	}
	if d.Groups == nil {
		d.Groups = []Group{}
	}
	groupIDs, groupNames := map[string]bool{}, map[string]bool{}
	for i := range d.Groups {
		g := &d.Groups[i]
		g.Name = strings.TrimSpace(g.Name)
		if g.ID == "" || len(g.ID) > 64 || strings.ContainsAny(g.ID, "/\\ \t\r\n") || g.Name == "" || utf8.RuneCountInString(g.Name) > 100 || strings.ContainsAny(g.Name, "\r\n\t") || g.Seconds < 1 || g.Seconds > 86400 {
			return fmt.Errorf("invalid group settings")
		}
		if groupIDs[g.ID] || groupNames[strings.ToLower(g.Name)] {
			return fmt.Errorf("duplicate group ID or name")
		}
		groupIDs[g.ID], groupNames[strings.ToLower(g.Name)] = true, true
	}
	if d.Sites == nil {
		d.Sites = []Site{}
	}
	if d.Bypass == nil {
		d.Bypass = []string{}
	}
	seenBypass := map[string]bool{}
	for i, b := range d.Bypass {
		n, e := Domain(b)
		if e != nil {
			return e
		}
		if seenBypass[n] {
			return fmt.Errorf("duplicate exception %s", n)
		}
		seenBypass[n] = true
		d.Bypass[i] = n
	}
	ids := map[string]bool{}
	hosts := map[string]bool{}
	for i := range d.Sites {
		s := &d.Sites[i]
		if s.BudgetMode != "" && s.BudgetMode != "shared" && s.BudgetMode != "capped" && s.BudgetMode != "independent" {
			return fmt.Errorf("invalid budget mode")
		}
		if s.GroupID == "" {
			s.BudgetMode = "independent"
		} else {
			if !groupIDs[s.GroupID] {
				return fmt.Errorf("unknown group for %s", s.Domain)
			}
			if s.BudgetMode == "" {
				s.BudgetMode = "shared"
			}
		}
		if s.BudgetMode != "shared" && s.BudgetMode != "capped" && s.BudgetMode != "independent" {
			return fmt.Errorf("invalid budget mode")
		}
		if s.ExtraSeconds == 0 {
			s.ExtraSeconds = 180
		}
		if s.ExtraMaxUses == 0 {
			s.ExtraMaxUses = 1
		}
		if s.ExtraSeconds < 1 || s.ExtraSeconds > 86400 || s.ExtraMaxUses < 1 || s.ExtraMaxUses > 100 {
			return fmt.Errorf("invalid extra time settings")
		}
		if s.GrayscaleEnabled && (s.GrayscaleAfter < 1 || s.GrayscaleAfter > 86400 || (s.BudgetMode != "shared" && s.GrayscaleAfter >= s.Seconds)) {
			return fmt.Errorf("grayscale threshold must be between 1 second and 24 hours")
		}
		n, e := Domain(s.Domain)
		if e != nil {
			return e
		}
		s.Domain = n
		if ids[n] {
			return fmt.Errorf("duplicate site %s", n)
		}
		ids[n] = true
		if s.Mode != "limit" && s.Mode != "block" || s.Seconds < 1 || s.Seconds > 86400 || len(s.Domains) == 0 || len(s.Domains) > 32 {
			return fmt.Errorf("invalid site settings %s", n)
		}
		hasMain := false
		for j, h := range s.Domains {
			v, e := Domain(h)
			if e != nil {
				return e
			}
			if hosts[v] {
				return fmt.Errorf("domain %s already belongs to a rule", v)
			}
			hosts[v] = true
			s.Domains[j] = v
			hasMain = hasMain || v == n
			if d.Bypasses(v) {
				return fmt.Errorf("domain %s is in Media exceptions", v)
			}
		}
		if !hasMain {
			return fmt.Errorf("target domains must include the main domain %s", n)
		}
	}
	return nil
}
func Seed(c config.Config) Document {
	d := Document{Version: 2, Groups: []Group{}, Sites: []Site{}, Bypass: []string{}}
	for _, h := range c.Hosts {
		s := Site{Domain: h, Domains: []string{h}, Enabled: true, Mode: "limit", Seconds: c.DailyLimit, BudgetMode: "independent"}
		if h == "x.com" {
			s.Domains = append(s.Domains, "www.x.com")
			d.Bypass = append(d.Bypass, "twimg.com")
		}
		d.Sites = append(d.Sites, s)
	}
	return d
}
func Load(path string, c config.Config) (Document, error) {
	d := Seed(c)
	f, e := os.Open(path)
	if os.IsNotExist(e) {
		e = d.Validate()
		return d, e
	}
	if e != nil {
		return d, e
	}
	defer f.Close()
	d = Document{}
	if info, err := f.Stat(); err != nil || info.Size() > 1024*1024 {
		return d, fmt.Errorf("rules file is too large or unreadable")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 1024*1024+1))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&d); e != nil {
		return d, e
	}
	if decoder.Decode(new(any)) != io.EOF {
		return d, fmt.Errorf("invalid rules file")
	}
	e = d.Validate()
	return d, e
}
func Save(path string, d Document) error {
	if path == "" {
		return nil
	} // In-memory stores used by tests.
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".sites-*.json")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	b, e := json.MarshalIndent(d, "", "  ")
	if e == nil {
		_, e = f.Write(append(b, '\n'))
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return replace(name, path)
}
