package sites

import (
	"os"
	"path/filepath"
	"pauseproxy/internal/config"
	"testing"
)

func TestVersionOneMigrationIsReadOnlyUntilSave(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sites.json")
	original := []byte(`{"version":1,"sites":[{"domain":"x.com","domains":["x.com"],"enabled":true,"mode":"limit","daily_limit_seconds":120}],"bypass_domains":[]}`)
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	d, err := Load(p, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if d.Sites[0].BlurEnabled || d.Sites[0].BlurAfter != 0 || d.Version != 2 || len(d.Groups) != 0 || d.Sites[0].BudgetMode != "independent" {
		t.Fatal(d)
	}
	got, _ := os.ReadFile(p)
	if string(got) != string(original) {
		t.Fatal("load wrote migration")
	}
	d.Groups = append(d.Groups, Group{ID: "fun", Name: "Fun", Seconds: 1800})
	d.Sites[0].GroupID = "fun"
	d.Sites[0].BudgetMode = "shared"
	if err = d.Validate(); err != nil {
		t.Fatal(err)
	}
	if err = Save(p, d); err != nil {
		t.Fatal(err)
	}
	gotDoc, err := Load(p, config.Default())
	if err != nil || gotDoc.Version != 2 || gotDoc.Sites[0].GroupID != "fun" {
		t.Fatal(gotDoc, err)
	}
}

func TestGroupsRejectInvalidReferencesAndNames(t *testing.T) {
	d := Seed(config.Default())
	d.Groups = []Group{{ID: "fun", Name: "Fun", Seconds: 900}}
	for _, tc := range []struct {
		name   string
		change func(*Document)
	}{
		{"unknown group", func(d *Document) { d.Sites[0].GroupID = "missing" }},
		{"unknown mode", func(d *Document) { d.Sites[0].GroupID = "fun"; d.Sites[0].BudgetMode = "unknown" }},
		{"duplicate id", func(d *Document) { d.Groups = append(d.Groups, Group{ID: "fun", Name: "Other", Seconds: 10}) }},
		{"duplicate name", func(d *Document) { d.Groups = append(d.Groups, Group{ID: "other", Name: " fun ", Seconds: 10}) }},
		{"zero budget", func(d *Document) { d.Groups[0].Seconds = 0 }},
		{"empty name", func(d *Document) { d.Groups[0].Name = " " }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := d.Clone()
			tc.change(&bad)
			if bad.Validate() == nil {
				t.Fatal("invalid group accepted")
			}
		})
	}
}

func TestRemoveGroupRetainsSharedThresholdThatNoLongerFits(t *testing.T) {
	d := Seed(config.Default())
	d.Groups = []Group{{ID: "fun", Name: "Fun", Seconds: 60}}
	d.Sites[0].GroupID = "fun"
	d.Sites[0].BudgetMode = "shared"
	d.Sites[0].BlurEnabled = true
	d.Sites[0].BlurAfter = 120
	d.Sites[0].GrayscaleEnabled = true
	d.Sites[0].GrayscaleAfter = 120
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveGroup("fun"); err != nil {
		t.Fatal(err)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Sites[0].BlurEnabled || d.Sites[0].BlurAfter != 120 || d.Sites[0].GrayscaleEnabled || d.Sites[0].GrayscaleAfter != 120 || d.Sites[0].Seconds != 60 {
		t.Fatal(d.Sites[0])
	}
}
