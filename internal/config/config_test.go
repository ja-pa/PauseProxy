package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSitesFileResolvedRelativeToConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if e := os.WriteFile(path, []byte(`{"sites_file":"shared/sites.json"}`), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := Load(path)
	if e != nil || c.SitesFile != filepath.Join(dir, "shared", "sites.json") {
		t.Fatal(c.SitesFile, e)
	}
}

func TestExactHosts(t *testing.T) {
	c := Default()
	for _, h := range []string{"x.com", "X.COM:443", "x.com."} {
		if !c.Target(h) {
			t.Fatal(h)
		}
	}
	for _, h := range []string{"api.x.com", "notx.com", "x.com.evil.test", "pbs.twimg.com", "video.twimg.com"} {
		if c.Target(h) {
			t.Fatal(h)
		}
	}
	c.AdminURL = "https://0.0.0.0:9083"
	if c.Validate() == nil {
		t.Fatal("admin exposed")
	}
}

func TestAdminConfiguration(t *testing.T) {
	c := Default()
	if c.ValidateRun() == nil {
		t.Fatal("missing password accepted")
	}
	if c.Validate() != nil {
		t.Fatal("CA bootstrap requires password")
	}
	c.AdminPassword = "test-password"
	if e := c.ValidateRun(); e != nil {
		t.Fatal(e)
	}
	c.AdminAddr = "0.0.0.0:9083"
	c.AdminURL = "https://192.168.1.50:9083"
	if e := c.ValidateRun(); e != nil {
		t.Fatal(e)
	}
	c.AdminURL = "http://192.168.1.50:9083"
	if e := c.ValidateRun(); e != nil {
		t.Fatal(e)
	}
	for _, address := range []string{"ftp://192.168.1.50:9083", "http://0.0.0.0:9083", "http://192.168.1.50:9084", "http://user@192.168.1.50:9083", "http://192.168.1.50:9083/path", "http://192.168.1.50:9083?x=1", "https://0.0.0.0:9083", "https://192.168.1.50:9084", "https://user@192.168.1.50:9083", "https://192.168.1.50:9083/path", "https://192.168.1.50:9083?x=1"} {
		c.AdminURL = address
		if c.ValidateRun() == nil {
			t.Fatalf("invalid origin accepted: %s", address)
		}
	}
}
