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
)

type Site struct {
	Domain  string   `json:"domain"`
	Domains []string `json:"domains"`
	Enabled bool     `json:"enabled"`
	Mode    string   `json:"mode"`
	Seconds int      `json:"daily_limit_seconds"`
}
type Document struct {
	Version int      `json:"version"`
	Sites   []Site   `json:"sites"`
	Bypass  []string `json:"bypass_domains"`
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
	if d.Version != 1 || len(d.Sites) > 32 || len(d.Bypass) > 128 {
		return fmt.Errorf("invalid version or too many rules")
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
	d := Document{Version: 1, Sites: []Site{}, Bypass: []string{}}
	for _, h := range c.Hosts {
		s := Site{Domain: h, Domains: []string{h}, Enabled: true, Mode: "limit", Seconds: c.DailyLimit}
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
