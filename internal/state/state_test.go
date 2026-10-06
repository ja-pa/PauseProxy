package state

import (
	"pauseproxy/internal/config"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	loc, _ := time.LoadLocation("Europe/Prague")
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, loc)
	return New(config.Default(), func() time.Time { return now }), &now
}
func beat(t *testing.T, s *Store, se Session, seq uint64, visible bool, clicks int) Status {
	t.Helper()
	st, e := s.Beat(se.User, se.IP, Heartbeat{se.ID, se.Instance, seq, visible, clicks})
	if e != nil {
		t.Fatal(e)
	}
	return st
}
func TestUnionExpiryAndDedupe(t *testing.T) {
	s, n := fixture(t)
	a, _ := s.Create("dev", "127.0.0.1")
	b, _ := s.Create("dev", "127.0.0.1")
	beat(t, s, a, 1, true, 3)
	beat(t, s, b, 1, true, 2)
	*n = n.Add(5 * time.Second)
	beat(t, s, a, 2, true, 4)
	beat(t, s, a, 2, true, 4)
	if got := s.Status("dev").Remaining; got != 115 {
		t.Fatalf("two tabs multiplied time: %v", got)
	}
	*n = n.Add(time.Hour)
	if got := s.Status("dev").Remaining; got != 105 {
		t.Fatalf("expired lease counted sleep: %v", got)
	}
	if got := s.counter("dev", *n).Clicks; got != 9 {
		t.Fatalf("duplicate clicks: %d", got)
	}
}
func TestHiddenAndLimits(t *testing.T) {
	s, n := fixture(t)
	a, _ := s.Create("dev", "127.0.0.1")
	beat(t, s, a, 1, true, 0)
	*n = n.Add(4 * time.Second)
	beat(t, s, a, 2, false, 0)
	*n = n.Add(time.Minute)
	if s.Status("dev").Remaining != 116 {
		t.Fatal("hidden tab counted")
	}
	if e := s.SetLimit("x.com", 3); e != nil {
		t.Fatal(e)
	}
	if !s.Status("dev").Blocked {
		t.Fatal("lower limit did not block")
	}
	_ = s.SetLimit("x.com", 10)
	if s.Status("dev").Blocked {
		t.Fatal("higher limit did not unblock")
	}
}
func TestMidnightAndReset(t *testing.T) {
	s, n := fixture(t)
	*n = time.Date(2026, 10, 3, 23, 59, 55, 0, s.loc)
	s.last = *n
	a, _ := s.Create("dev", "127.0.0.1")
	beat(t, s, a, 1, true, 0)
	*n = n.Add(8 * time.Second)
	st := s.Status("dev")
	if st.Remaining != 117 {
		t.Fatalf("new day: %v", st.Remaining)
	}
	if s.counter("dev", n.Add(-24*time.Hour)).Seconds != 5 {
		t.Fatal("midnight split failed")
	}
	s.Reset()
	if s.Status("dev").Remaining != 120 {
		t.Fatal("reset failed")
	}
	if _, e := s.Beat("dev", "127.0.0.1", Heartbeat{ID: a.ID, Instance: a.Instance, Sequence: 2}); e != ErrSession {
		t.Fatal("old session survived reset")
	}
}
func TestSessionIdentityCapacityAndRate(t *testing.T) {
	s, _ := fixture(t)
	a, _ := s.Create("dev", "127.0.0.1")
	if _, e := s.Beat("dev", "192.168.1.2", Heartbeat{ID: a.ID, Instance: a.Instance, Sequence: 1}); e != ErrSession {
		t.Fatal("session not bound to IP")
	}
	beat(t, s, a, 1, true, 0)
	if _, e := s.Beat("dev", "127.0.0.1", Heartbeat{ID: a.ID, Instance: a.Instance, Sequence: 2, Visible: true}); e != ErrRate {
		t.Fatal("rate not bounded")
	}
	for i := 1; i < 256; i++ {
		_, _ = s.Create("dev", "127.0.0.1")
	}
	if _, e := s.Create("dev", "127.0.0.1"); e != ErrCapacity {
		t.Fatal("unbounded sessions")
	}
}
func TestConcurrentState(t *testing.T) {
	s := New(config.Default(), time.Now)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, e := s.Create("dev", "127.0.0.1")
			if e != nil {
				return
			}
			_, _ = s.Beat("dev", "127.0.0.1", Heartbeat{ID: a.ID, Instance: a.Instance, Sequence: 1, Visible: true})
			s.Snapshot()
			_ = s.SetLimit("x.com", 120)
			s.Status("dev")
		}()
	}
	wg.Wait()
}
func TestScheduleAndDayPruning(t *testing.T) {
	c := config.Default()
	c.BlockedHours = []config.Window{{Start: "22:00", End: "06:00"}}
	now := time.Date(2026, 10, 3, 23, 0, 0, 0, time.UTC)
	s := New(c, func() time.Time { return now })
	if !s.Status("dev").Blocked {
		t.Fatal("overnight window ignored")
	}
	for i := 0; i < 20; i++ {
		now = now.AddDate(0, 0, 1)
		s.Status("dev")
	}
	if len(s.counters) > 2 {
		t.Fatal("daily counters not bounded")
	}
}
