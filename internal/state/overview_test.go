package state

import (
	"pauseproxy/internal/config"
	"pauseproxy/internal/sites"
	"testing"
	"time"
)

func TestOverviewIncludesUnusedSitesAndUniqueSortedUsers(t *testing.T) {
	c := config.Default()
	c.Users = map[string]string{"127.0.0.1": "zoe", "::1": "amy", "192.0.2.1": "amy"}
	c.Hosts = []string{"youtube.com", "x.com"}
	s := New(c, time.Now)
	for range 3 {
		snapshot := s.Snapshot()
		if len(snapshot.Overview) != 4 || len(snapshot.Counters) != 0 || len(s.counters) != 0 {
			t.Fatalf("overview must not create counters: %+v", snapshot)
		}
		for i, want := range []struct{ user, site string }{{"amy", "youtube.com"}, {"amy", "x.com"}, {"zoe", "youtube.com"}, {"zoe", "x.com"}} {
			row := snapshot.Overview[i]
			if row.User != want.user || row.Site != want.site || row.Used != 0 || row.Remaining != row.Budget || row.State != "available" {
				t.Fatalf("unexpected row %d: %+v", i, row)
			}
		}
	}
}

func TestOverviewBudgetsAndStatesMatchClientStatus(t *testing.T) {
	for _, tc := range []struct {
		name, wantState          string
		used, bonus              float64
		wantRemaining            float64
		wantBudget               float64
		mode                     string
		paused, extra, scheduled bool
	}{
		{name: "unused", wantState: "available", wantRemaining: 120, wantBudget: 120},
		{name: "partial", wantState: "available", used: 45, wantRemaining: 75, wantBudget: 120},
		{name: "exhausted", wantState: "limit_reached", used: 120, wantBudget: 120},
		{name: "reduced below usage", wantState: "limit_reached", used: 200, wantBudget: 120},
		{name: "extra time", wantState: "available", used: 130, bonus: 180, extra: true, wantRemaining: 170, wantBudget: 300},
		{name: "bonus disabled", wantState: "limit_reached", used: 130, bonus: 180, wantBudget: 120},
		{name: "always blocked", wantState: "always_blocked", mode: "block", wantRemaining: 120, wantBudget: 120},
		{name: "paused", wantState: "paused", paused: true, used: 30, wantRemaining: 90, wantBudget: 120},
		{name: "scheduled break", wantState: "scheduled_break", scheduled: true, used: 30, wantRemaining: 90, wantBudget: 120},
		{name: "paused overrides schedule", wantState: "paused", paused: true, scheduled: true, wantRemaining: 120, wantBudget: 120},
		{name: "always blocked overrides schedule", wantState: "always_blocked", mode: "block", scheduled: true, wantRemaining: 120, wantBudget: 120},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, n := fixture(t)
			rule := &s.rules.Sites[0]
			rule.Enabled = !tc.paused
			rule.ExtraEnabled = tc.extra
			if tc.mode != "" {
				rule.Mode = tc.mode
			}
			if tc.scheduled {
				s.cfg.BlockedHours = []config.Window{{Start: "00:00", End: "23:59"}}
			}
			c := s.counter("dev", *n, rule.Domain)
			c.Seconds, c.ExtraSeconds, c.Clicks, c.Visits = tc.used, tc.bonus, 7, 3
			snapshot := s.Snapshot()
			row := snapshot.Overview[0]
			if row.State != tc.wantState || row.Used != tc.used || row.Budget != tc.wantBudget || row.Remaining != tc.wantRemaining {
				t.Fatalf("unexpected budget: %+v", row)
			}
			if !tc.paused && row.Remaining != s.Status("dev", rule.Domain).Remaining {
				t.Fatal("overview disagrees with client status")
			}
			if snapshot.Counters[0].Clicks != 7 || snapshot.Counters[0].Visits != 3 || snapshot.Limits[rule.Domain] != 120 {
				t.Fatal("legacy snapshot fields changed")
			}
		})
	}
}

func TestOverviewExtraGrantMidnightAndReset(t *testing.T) {
	s, n := fixture(t)
	s.rules.Sites[0].ExtraEnabled = true
	s.rules.Sites[0].ExtraSeconds = 180
	s.rules.Sites[0].ExtraMaxUses = 1
	se, err := s.CreateFor("dev", "127.0.0.1", "x.com")
	if err != nil {
		t.Fatal(err)
	}
	s.counter("dev", *n, "x.com").Seconds = 120
	v, err := s.ExtraTime("dev", se.IP, se.Host, ExtraRequest{ID: se.ID, Instance: se.Instance, RequestID: "overview-grant"})
	if err != nil {
		t.Fatal(err)
	}
	row := s.Snapshot().Overview[0]
	if row.Budget != 300 || row.Remaining != v.Remaining || row.Remaining != 180 {
		t.Fatalf("bonus omitted: %+v", row)
	}
	// At 22:30 UTC it is already the next day in Prague (summer time).
	*n = time.Date(2026, 10, 3, 22, 30, 0, 0, time.UTC)
	snapshot := s.Snapshot()
	if snapshot.Date != "2026-10-04" || snapshot.Overview[0].Used != 0 || snapshot.Overview[0].Budget != 120 || len(s.counters) != 1 {
		t.Fatalf("new local day reused yesterday's usage/bonus or created a counter: %+v", snapshot)
	}
	s.counter("dev", *n, "x.com").Seconds = 60
	s.Reset()
	snapshot = s.Snapshot()
	if snapshot.Overview[0].Remaining != 120 || len(snapshot.Counters) != 0 {
		t.Fatal("reset did not restore full budget")
	}
}

func TestOverviewLiveUsageAndCurrentRules(t *testing.T) {
	s, n := fixture(t)
	se, err := s.Create("dev", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	beat(t, s, se, 1, true, 0)
	*n = n.Add(5 * time.Second)
	row := s.Snapshot().Overview[0]
	if row.Used != 5 || row.Remaining != 115 {
		t.Fatalf("live lease not advanced: %+v", row)
	}
	// Removed rules disappear even though their legacy counters remain available.
	d := s.Rules()
	d.Sites = []sites.Site{{Domain: "new.example", Domains: []string{"new.example"}, Enabled: true, Mode: "limit", Seconds: 900}}
	s.ConfigureRules(d, "")
	snapshot := s.Snapshot()
	if len(snapshot.Overview) != 1 || snapshot.Overview[0].Site != "new.example" || snapshot.Overview[0].Remaining != 900 || len(snapshot.Counters) != 1 {
		t.Fatalf("stale rules shown: %+v", snapshot)
	}
	d.Sites = nil
	s.ConfigureRules(d, "")
	if rows := s.Snapshot().Overview; rows == nil || len(rows) != 0 {
		t.Fatalf("empty rules must produce an empty array: %+v", rows)
	}
}
