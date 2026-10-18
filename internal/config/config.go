package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata"
)

type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
}
type Config struct {
	AdminPassword   string            `json:"admin_password"`
	AdminURL        string            `json:"admin_url"`
	SitesFile       string            `json:"sites_file"`
	ProxyAddr       string            `json:"proxy_addr"`
	PACAddr         string            `json:"pac_addr"`
	AdminAddr       string            `json:"admin_addr"`
	AdvertisedProxy string            `json:"advertised_proxy"`
	CADir           string            `json:"ca_dir"`
	Timezone        string            `json:"timezone"`
	Hosts           []string          `json:"hosts"`
	Users           map[string]string `json:"users"`
	DailyLimit      int               `json:"daily_limit_seconds"`
	Warning         int               `json:"warning_seconds"`
	BlockedHours    []Window          `json:"blocked_hours"`
}

func Default() Config {
	return Config{AdminURL: "http://127.0.0.1:9083", ProxyAddr: "127.0.0.1:9080", PACAddr: "127.0.0.1:9082", AdminAddr: "127.0.0.1:9083", AdvertisedProxy: "127.0.0.1:9080", CADir: ".local/ca", SitesFile: ".local/sites.json", Timezone: "Europe/Prague", Hosts: []string{"x.com"}, Users: map[string]string{"127.0.0.1": "dev", "::1": "dev"}, DailyLimit: 120, Warning: 30}
}
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		f, e := os.Open(path)
		if e != nil {
			return c, e
		}
		defer f.Close()
		d := json.NewDecoder(f)
		d.DisallowUnknownFields()
		if e = d.Decode(&c); e != nil {
			return c, e
		}
	}
	if !filepath.IsAbs(c.SitesFile) && path != "" {
		c.SitesFile = filepath.Join(filepath.Dir(path), c.SitesFile)
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	for _, a := range []string{c.ProxyAddr, c.PACAddr, c.AdminAddr, c.AdvertisedProxy} {
		h, p, e := net.SplitHostPort(a)
		if e != nil || h == "" || p == "" {
			return fmt.Errorf("invalid address: %s", a)
		}
	}
	u, err := url.Parse(c.AdminURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" {
		return fmt.Errorf("admin_url must be an HTTP or HTTPS origin without a path")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || ip.IsUnspecified() {
		return fmt.Errorf("admin_url must use a concrete IP address")
	}
	_, port, _ := net.SplitHostPort(c.AdminAddr)
	if u.Port() != port {
		return fmt.Errorf("admin_url port must match admin_addr")
	}
	bind, _, _ := net.SplitHostPort(c.AdminAddr)
	if b := net.ParseIP(bind); b == nil || (!b.IsUnspecified() && !b.Equal(ip)) {
		return fmt.Errorf("admin_url must match admin_addr or use a wildcard listener")
	}
	if strings.ContainsAny(c.AdvertisedProxy, "\r\n\"\\") {
		return fmt.Errorf("invalid advertised_proxy")
	}
	if c.DailyLimit < 1 || c.DailyLimit > 86400 || c.Warning < 0 || c.Warning > 86400 {
		return fmt.Errorf("limits must be within one day")
	}
	if c.SitesFile == "" || c.CADir == "" || len(c.Hosts) == 0 || len(c.Hosts) > 32 || len(c.Users) == 0 || len(c.Users) > 64 {
		return fmt.Errorf("missing or excessive hosts/users/CA directory")
	}
	for _, h := range c.Hosts {
		if h != strings.ToLower(h) || strings.ContainsAny(h, "/: \"\\\r\n") || h == "" {
			return fmt.Errorf("invalid target host %q", h)
		}
	}
	for ip, u := range c.Users {
		if net.ParseIP(ip) == nil || u == "" || len(u) > 64 {
			return fmt.Errorf("invalid user mapping")
		}
	}
	if _, e := time.LoadLocation(c.Timezone); e != nil {
		return e
	}
	for _, w := range c.BlockedHours {
		if _, e := time.Parse("15:04", w.Start); e != nil {
			return e
		}
		if _, e := time.Parse("15:04", w.End); e != nil {
			return e
		}
		if w.Start == w.End {
			return fmt.Errorf("blocked window must have different start/end")
		}
	}
	return nil
}
func Host(host string) string {
	if h, _, e := net.SplitHostPort(host); e == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
func (c Config) Target(host string) bool {
	host = Host(host)
	for _, h := range c.Hosts {
		if host == h || (h == "x.com" && host == "www.x.com") {
			return true
		}
	}
	return false
}
func (c Config) User(remote string) (string, string) {
	ip, _, e := net.SplitHostPort(remote)
	if e != nil {
		return "", ""
	}
	p := net.ParseIP(ip)
	if p == nil {
		return "", ""
	}
	ip = p.String()
	return c.Users[ip], ip
}
func (c Config) Scheduled(t time.Time) bool {
	s := t.Format("15:04")
	for _, w := range c.BlockedHours {
		if w.Start < w.End {
			if s >= w.Start && s < w.End {
				return true
			}
		} else if s >= w.Start || s < w.End {
			return true
		}
	}
	return false
}

// ValidateRun leaves CA bootstrapping independent of the admin password.
func (c Config) ValidateRun() error {
	if strings.TrimSpace(c.AdminPassword) == "" {
		return fmt.Errorf("set admin_password in the configuration before starting the server")
	}
	return c.Validate()
}
