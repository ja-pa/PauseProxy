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
	if r := d.Sites[0]; r.ExtraMethod != "click" || r.BlurEnabled || r.ExtraEnabled || r.GrayscaleEnabled || r.ExtraSeconds != 180 || r.ExtraMaxUses != 1 {
		t.Fatal(r)
	}
	d.Sites[0].BlurEnabled = true
	d.Sites[0].BlurAfter = 30
	d.Sites[0].GrayscaleEnabled = true
	d.Sites[0].GrayscaleAfter = 60
	d.Sites[0].ExtraEnabled = true
	d.Sites[0].ExtraMethod = "reflection"
	path := filepath.Join(t.TempDir(), "sites.json")
	if e := Save(path, d); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(path, c)
	if e != nil || loaded.Sites[0].ExtraMethod != "reflection" || !loaded.Sites[0].BlurEnabled || loaded.Sites[0].BlurAfter != 30 || !loaded.Sites[0].GrayscaleEnabled || !loaded.Sites[0].ExtraEnabled {
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

func TestBlurThresholdValidation(t *testing.T) {
	for _, mode := range []string{"independent", "capped", "shared"} {
		for _, threshold := range []int{-1, 0, 1, 119, 120, 86400, 86401} {
			d := Seed(config.Default())
			d.Groups = []Group{{ID: "fun", Name: "Fun", Seconds: 60}}
			r := &d.Sites[0]
			r.GroupID = "fun"
			r.BudgetMode = mode
			r.BlurEnabled = true
			r.BlurAfter = threshold
			valid := threshold >= 1 && threshold <= 86400 && (mode == "shared" || threshold < r.Seconds)
			if err := d.Validate(); (err == nil) != valid {
				t.Fatalf("mode=%s threshold=%d err=%v", mode, threshold, err)
			}
		}
	}
}

func TestExtraMethods(t *testing.T) {
	for _, method := range []string{"", "click", "math", "reflection", "unknown"} {
		d := Seed(config.Default())
		d.Sites[0].ExtraMethod = method
		if err := d.Validate(); (err == nil) != (method != "unknown") {
			t.Fatal(method, err)
		}
	}
}
