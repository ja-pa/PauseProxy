package sites

import (
	"path/filepath"
	"pauseproxy/internal/config"
	"testing"
)

func TestOptionalSettingsPersistence(t *testing.T) {
	c := config.Default()
	d := Seed(c)
	if e := d.Validate(); e != nil {
		t.Fatal(e)
	}
	if r := d.Sites[0]; r.ExtraEnabled || r.GrayscaleEnabled || r.ExtraSeconds != 180 || r.ExtraMaxUses != 1 {
		t.Fatal(r)
	}
	d.Sites[0].GrayscaleEnabled = true
	d.Sites[0].GrayscaleAfter = 60
	d.Sites[0].ExtraEnabled = true
	path := filepath.Join(t.TempDir(), "sites.json")
	if e := Save(path, d); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(path, c)
	if e != nil || !loaded.Sites[0].GrayscaleEnabled || !loaded.Sites[0].ExtraEnabled {
		t.Fatal(loaded, e)
	}
	for _, v := range []int{0, 120, 121} {
		bad := d.Clone()
		bad.Sites[0].GrayscaleAfter = v
		if bad.Validate() == nil {
			t.Fatal("invalid grayscale accepted", v)
		}
	}
	for _, v := range []int{-1, 86401} {
		bad := d.Clone()
		bad.Sites[0].ExtraSeconds = v
		if bad.Validate() == nil {
			t.Fatal("invalid extra accepted", v)
		}
	}
	bad := d.Clone()
	bad.Sites[0].ExtraMaxUses = 101
	if bad.Validate() == nil {
		t.Fatal("invalid count")
	}
}
