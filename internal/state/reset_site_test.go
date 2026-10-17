package state

import (
	"testing"
	"time"
)

func TestResetSiteIsScopedToUserSiteAndToday(t *testing.T) {
	s, now := groupedFixture(t)
	s.cfg.Users["192.0.2.1"] = "other"
	se := activeGroupSession(t, s, "x.com")
	other := activeGroupSession(t, s, "youtube.com")
	beforeInstance := s.instance
	oldDate := now.AddDate(0, 0, -1)
	previous := s.counter("dev", oldDate, "x.com")
	previous.Seconds = 19
	c := s.counter("dev", *now, "x.com")
	c.Clicks = 7
	c.ExtraUses = 2
	c.ExtraSeconds = 6
	c.ExtraRequests = map[string]bool{"old": true}
	s.counter("other", *now, "x.com").Seconds = 9
	*now = now.Add(3 * time.Second)
	if err := s.ResetSite("dev", "x.com"); err != nil {
		t.Fatal(err)
	}
	if s.instance != beforeInstance || s.sessions[se.ID] != nil || s.sessions[other.ID] == nil {
		t.Fatal("unrelated sessions changed")
	}
	for _, c := range s.Snapshot().Counters {
		if c.User == "dev" && c.Service == "x.com" && c.Date == now.In(s.loc).Format("2006-01-02") {
			t.Fatal("today not reset", c)
		}
	}
	if previous.Seconds != 19 || s.counter("other", *now, "x.com").Seconds != 9 {
		t.Fatal("other history reset")
	}
	group := s.groupUsage[groupKey{"dev", "fun", now.In(s.loc).Format("2006-01-02")}]
	if group != 6 {
		t.Fatal("settled group time changed", group)
	}
	*now = now.Add(2 * time.Second)
	if used := s.counter("dev", *now, "x.com").Seconds; used != 0 {
		t.Fatal("old lease survived", used)
	}
	if _, err := s.BeatFrom(se.User, se.IP, Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 2, Visible: true}, se.Host); err != ErrSession {
		t.Fatal(err)
	}
	if err := s.ResetSite("unknown", "x.com"); err == nil {
		t.Fatal("unknown user")
	}
	if err := s.ResetSite("dev", "missing.example"); err == nil {
		t.Fatal("unknown site")
	}
}
