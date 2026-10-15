package state

import (
	"pauseproxy/internal/config"
	"pauseproxy/internal/sites"
	"testing"
	"time"
)

func groupedFixture(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	s, now := fixture(t)
	d := s.Rules()
	d.Groups = []sites.Group{{ID: "fun", Name: "Fun", Seconds: 30}, {ID: "news", Name: "News", Seconds: 60}}
	d.Sites = []sites.Site{
		{Domain: "x.com", Domains: []string{"x.com"}, Enabled: true, Mode: "limit", Seconds: 15, GroupID: "fun", BudgetMode: "shared"},
		{Domain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: true, Mode: "limit", Seconds: 10, GroupID: "fun", BudgetMode: "capped", ExtraEnabled: true, ExtraSeconds: 10, ExtraMaxUses: 2},
		{Domain: "independent.example", Domains: []string{"independent.example"}, Enabled: true, Mode: "limit", Seconds: 50, GroupID: "fun", BudgetMode: "independent"},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	s.ConfigureRules(d, "")
	return s, now
}
func activeGroupSession(t *testing.T, s *Store, host string) Session {
	t.Helper()
	se, err := s.CreateFor("dev", "127.0.0.1", host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeatFrom(se.User, se.IP, Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 1, Visible: true}, se.Host); err != nil {
		t.Fatal(err)
	}
	return se
}

func TestGroupAccountingSumsSitesUnionsTabsAndSeparatesUsers(t *testing.T) {
	s, now := groupedFixture(t)
	activeGroupSession(t, s, "x.com")
	activeGroupSession(t, s, "x.com")
	activeGroupSession(t, s, "youtube.com")
	activeGroupSession(t, s, "independent.example")
	other, err := s.CreateFor("other", "192.0.2.1", "x.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeatFrom(other.User, other.IP, Heartbeat{ID: other.ID, Instance: other.Instance, Sequence: 1, Visible: true}, other.Host); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Second)
	snap := s.Snapshot()
	if snap.Groups[0].Used != 10 || snap.Groups[0].Remaining != 20 {
		t.Fatalf("group did not sum two sites: %+v", snap.Groups)
	}
	if got := s.Status("dev", "x.com"); got.Remaining != 20 {
		t.Fatal(got)
	}
	if got := s.Status("other", "x.com"); got.Remaining != 25 {
		t.Fatal("users share group time", got)
	}
	if got := s.Status("dev", "independent.example"); got.Remaining != 45 {
		t.Fatal(got)
	}
	if used := s.counter("dev", *now, "x.com").Seconds; used != 5 {
		t.Fatal("tabs counted twice", used)
	}
}

func TestGroupCapBonusAndHardGroupCeiling(t *testing.T) {
	s, now := groupedFixture(t)
	se := activeGroupSession(t, s, "youtube.com")
	*now = now.Add(10 * time.Second)
	v := s.Status("dev", "youtube.com")
	if !v.Blocked || v.LimitScope != "site" || !v.ExtraAvailable {
		t.Fatal(v)
	}
	v, err := s.ExtraTime(se.User, se.IP, se.Host, ExtraRequest{ID: se.ID, Instance: se.Instance, RequestID: "bonus"})
	if err != nil || v.Remaining != 10 {
		t.Fatal(v, err)
	}
	if _, err = s.BeatFrom(se.User, se.IP, Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 2, Visible: true}, se.Host); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Second)
	if v = s.Status("dev", "youtube.com"); v.Remaining != 5 || v.GroupRemaining != 15 {
		t.Fatal(v)
	}
	// Spending the rest elsewhere must also block the capped web and its bonus.
	activeGroupSession(t, s, "x.com")
	*now = now.Add(10 * time.Second)
	v = s.Status("dev", "youtube.com")
	if !v.Blocked || v.LimitScope != "group" || v.ExtraAvailable || v.Remaining != 0 || v.Reason != "Group limit reached: Fun" {
		t.Fatal(v)
	}
	if _, err = s.ExtraTime(se.User, se.IP, se.Host, ExtraRequest{ID: se.ID, Instance: se.Instance, RequestID: "no-bypass"}); err != ErrExtra {
		t.Fatal("group bypassed", err)
	}
	// Pure sharing has no bonus even when its dormant site settings enable it.
	s.rules.Sites[0].ExtraEnabled = true
	if v = s.Status("dev", "x.com"); v.ExtraAvailable || v.ExtraUsesRemaining != 0 {
		t.Fatal(v)
	}
}

func TestMembershipAndModeChangesApplyOnlyFromSave(t *testing.T) {
	s, now := groupedFixture(t)
	activeGroupSession(t, s, "x.com")
	*now = now.Add(4 * time.Second)
	if err := s.ChangeRules(func(d *sites.Document) error { d.Sites[0].GroupID = "news"; return nil }); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(3 * time.Second)
	snap := s.Snapshot()
	if snap.Groups[0].Used != 4 || snap.Groups[1].Used != 3 {
		t.Fatal("usage reassigned retroactively", snap.Groups)
	}
	if err := s.ChangeRules(func(d *sites.Document) error { d.Sites[0].BudgetMode = "independent"; return nil }); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Second)
	snap = s.Snapshot()
	if snap.Groups[1].Used != 3 || s.Status("dev", "x.com").Remaining != 6 {
		t.Fatal("independent mode lost site usage", snap)
	}
	if err := s.ChangeRules(func(d *sites.Document) error {
		d.Sites[0].GroupID = "fun"
		d.Sites[0].BudgetMode = "capped"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	snap = s.Snapshot()
	if snap.Groups[0].Used != 5 || snap.Groups[1].Used != 3 || s.Status("dev", "x.com").Remaining != 5 {
		t.Fatal(snap)
	}
}

func TestGroupRenameLimitDeleteResetAndLocalMidnight(t *testing.T) {
	s, now := groupedFixture(t)
	activeGroupSession(t, s, "x.com")
	*now = now.Add(5 * time.Second)
	if err := s.ChangeRules(func(d *sites.Document) error { d.Groups[0].Name = "Entertainment"; d.Groups[0].Seconds = 4; return nil }); err != nil {
		t.Fatal(err)
	}
	if row := s.Snapshot().Groups[0]; row.Used != 5 || row.Remaining != 0 || row.Name != "Entertainment" {
		t.Fatal(row)
	}
	if v := s.Status("dev", "x.com"); v.Reason != "Group limit reached: Entertainment" {
		t.Fatal(v)
	}
	// No active leases cross midnight in this portion of the test.
	s.sessions = map[string]*Session{}
	*now = time.Date(2026, 10, 3, 22, 30, 0, 0, time.UTC)
	if snap := s.Snapshot(); snap.Date != "2026-10-04" || snap.Groups[0].Used != 0 || snap.Groups[0].Remaining != 4 {
		t.Fatal(snap)
	}
	s.groupUsage[groupKey{"dev", "fun", "2026-10-04"}] = 2
	s.Reset()
	if snap := s.Snapshot(); snap.Groups[0].Remaining != 4 || len(s.groupUsage) != 0 {
		t.Fatal(snap)
	}
	if err := s.ChangeRules(func(d *sites.Document) error { return d.RemoveGroup("fun") }); err != nil {
		t.Fatal(err)
	}
	d := s.Rules()
	if d.Sites[0].GroupID != "" || d.Sites[0].BudgetMode != "independent" || d.Sites[0].Seconds != 4 || d.Sites[1].Seconds != 10 || d.Sites[2].Seconds != 50 {
		t.Fatal(d)
	}
}

func TestGroupGrayscaleBlocksAndOverviewDoNotCreateCounters(t *testing.T) {
	s, now := groupedFixture(t)
	s.rules.Sites[0].GrayscaleEnabled = true
	s.rules.Sites[0].GrayscaleAfter = 3
	activeGroupSession(t, s, "youtube.com")
	*now = now.Add(5 * time.Second)
	if v := s.Status("dev", "x.com"); v.Grayscale {
		t.Fatal("grayscale follows group usage", v)
	}
	s.counter("dev", *now, "x.com").Seconds = 3
	if v := s.Status("dev", "x.com"); !v.Grayscale {
		t.Fatal(v)
	}
	s.cfg.BlockedHours = []config.Window{{Start: "00:00", End: "23:59"}}
	if v := s.Status("dev", "x.com"); v.Reason != "Scheduled break" {
		t.Fatal(v)
	}
	s.rules.Sites[0].Mode = "block"
	if v := s.Status("dev", "x.com"); v.Reason != "This site is always blocked" {
		t.Fatal(v)
	}
	s.rules.Sites[0].Enabled = false
	if v := s.Status("dev", "x.com"); v.Tracking {
		t.Fatal(v)
	}
	s.Reset()
	for range 3 {
		s.Snapshot()
	}
	if len(s.counters) != 0 || len(s.groupUsage) != 0 {
		t.Fatal("snapshot created accounting records")
	}
}

func TestGroupActiveLeaseSplitsAtLocalMidnight(t *testing.T) {
	s, now := groupedFixture(t)
	*now = time.Date(2026, 10, 3, 23, 59, 57, 0, s.loc)
	activeGroupSession(t, s, "x.com")
	*now = now.Add(6 * time.Second)
	snap := s.Snapshot()
	if snap.Date != "2026-10-04" || snap.Groups[0].Used != 3 || s.groupUsage[groupKey{"dev", "fun", "2026-10-03"}] != 3 {
		t.Fatal("active group time was not split at local midnight", snap.Groups, s.groupUsage)
	}
	// A fresh process keeps rules but starts with empty statistics.
	fresh := New(s.cfg, func() time.Time { return *now })
	fresh.ConfigureRules(s.Rules(), "")
	if row := fresh.Snapshot().Groups[0]; row.Used != 0 || row.Remaining != 30 {
		t.Fatal(row)
	}
}
