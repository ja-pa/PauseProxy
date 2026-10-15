package sites

import (
	"os"
	"path/filepath"
	"pauseproxy/internal/config"
	"testing"
)

func TestDomainsAndRouting(t *testing.T) {
	for _, s := range []string{"https://x.com", "x.com:443", "*.x.com", "127.0.0.1", "localhost", "x..com", "-x.com"} {
		if _, e := Domain(s); e == nil {
			t.Fatal("accepted", s)
		}
	}
	if got, e := Domain(" BÜCHER.de. "); e != nil || got != "xn--bcher-kva.de" {
		t.Fatal(got, e)
	}
	d := Seed(config.Default())
	d.Bypass = append(d.Bypass, "googlevideo.com", "ytimg.com")
	d.Sites = append(d.Sites, Site{Domain: "youtube.com", Domains: []string{"youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be"}, Enabled: true, Mode: "limit", Seconds: 900})
	if e := d.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, host := range []string{"x.com", "www.x.com", "youtube.com", "m.youtube.com", "youtu.be"} {
		if _, ok := d.Match(host); !ok {
			t.Fatal(host)
		}
	}
	for _, host := range []string{"api.x.com", "unknown.youtube.com", "googlevideo.com", "r1.googlevideo.com", "video.twimg.com", "pbs.twimg.com", "x.com.evil.test"} {
		if _, ok := d.Match(host); ok {
			t.Fatal(host)
		}
	}
	d.Sites[0].Domains = append(d.Sites[0].Domains, "video.twimg.com")
	if d.Validate() == nil {
		t.Fatal("bypass conflict accepted")
	}
}
func TestPersistenceMigrationAndFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules", "sites.json")
	d, e := Load(path, config.Default())
	if e != nil || len(d.Sites) != 1 {
		t.Fatal(d, e)
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("load created file")
	}
	if e = Save(path, d); e != nil {
		t.Fatal(e)
	}
	d.Sites[0].Seconds = 300
	if e = Save(path, d); e != nil {
		t.Fatal(e)
	}
	got, e := Load(path, config.Default())
	if e != nil || got.Sites[0].Seconds != 300 {
		t.Fatal(got, e)
	}
	d.Sites = []Site{}
	if e = Save(path, d); e != nil {
		t.Fatal(e)
	}
	got, e = Load(path, config.Default())
	if e != nil || len(got.Sites) != 0 {
		t.Fatal("empty rules resurrected", e)
	}
	for _, bad := range []string{"{}", `{"version":3,"sites":[]}`, `{"version":1,"sites":[],"unexpected":1}`, `{"version":1,"sites":[]} {}`} {
		if e = os.WriteFile(path, []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = Load(path, config.Default()); e == nil {
			t.Fatal("accepted", bad)
		}
		b, _ := os.ReadFile(path)
		if string(b) != bad {
			t.Fatal("invalid file overwritten")
		}
	}
	if e = Save(filepath.Dir(path), d); e == nil {
		t.Fatal("directory overwritten")
	}
}
