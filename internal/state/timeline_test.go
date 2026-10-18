package state

import (
	"testing"
	"time"
)

func TestTimelineSnapshotSettingsAndBonuses(t *testing.T) {
	s, now, se, b := challengeFixture(t, "click")
	r := &s.rules.Sites[0]
	r.GrayscaleEnabled = true
	r.GrayscaleAfter = 3
	r.BlurEnabled = true
	r.BlurAfter = 6
	first := s.Snapshot()
	rule := first.TimelineRules[se.Site]
	if rule.BaseBudget != 10 || !rule.GrayscaleEnabled || rule.GrayscaleAfter != 3 || !rule.BlurEnabled || rule.BlurAfter != 6 || !rule.ExtraEnabled || rule.ExtraSeconds != 180 {
		t.Fatal(rule)
	}
	if first.Overview[0].ExtraGranted != 0 || first.Overview[0].ExtraUsesRemaining != 3 {
		t.Fatal(first.Overview)
	}
	if _, err := s.ExtraTime(se.User, se.IP, se.Host, b); err != nil {
		t.Fatal(err)
	}
	r.ExtraSeconds = 60
	snap := s.Snapshot()
	if snap.Overview[0].ExtraGranted != 180 || snap.Overview[0].ExtraUsesRemaining != 2 || snap.TimelineRules[se.Site].ExtraSeconds != 60 {
		t.Fatal(snap)
	}
	r.Enabled = false
	if row := s.Snapshot().Overview[0]; row.ExtraUsesRemaining != 2 || row.ExtraGranted != 180 || row.State != "paused" {
		t.Fatal(row)
	}
	if err := s.ResetSite(se.User, se.Site); err != nil {
		t.Fatal(err)
	}
	if row := s.Snapshot().Overview[0]; row.ExtraGranted != 0 || row.ExtraUsesRemaining != 3 {
		t.Fatal(row)
	}
	r.Enabled = true
	*now = now.Add(24 * time.Hour)
	if row := s.Snapshot().Overview[0]; row.Used != 0 || row.ExtraGranted != 0 || row.ExtraUsesRemaining != 3 {
		t.Fatal(row)
	}
}
func TestTimelineSnapshotGroupBudgetsAndNoNewCounters(t *testing.T) {
	s, now := groupedFixture(t)
	s.cfg.Users["192.0.2.1"] = "other"
	shared := s.counter("dev", *now, "x.com")
	shared.Seconds = 8
	capped := s.counter("dev", *now, "youtube.com")
	capped.Seconds = 7
	capped.ExtraUses = 1
	capped.ExtraSeconds = 10
	date := now.In(s.loc).Format("2006-01-02")
	s.groupUsage[groupKey{"dev", "fun", date}] = 29
	count := len(s.counters)
	snap := s.Snapshot()
	if len(snap.TimelineRules) != 3 || len(snap.Overview) != 6 || len(s.counters) != count {
		t.Fatal("snapshot size or unwanted counters", snap)
	}
	for _, row := range snap.Overview {
		if row.User != "dev" {
			continue
		}
		switch row.Site {
		case "x.com":
			if row.ExtraUsesRemaining != 0 || row.ExtraGranted != 0 || row.Remaining != 1 {
				t.Fatal(row)
			}
		case "youtube.com":
			if row.Remaining != 1 || row.SiteBudget != 20 || row.ExtraGranted != 10 || row.ExtraUsesRemaining != 1 {
				t.Fatal(row)
			}
		}
	}
}
