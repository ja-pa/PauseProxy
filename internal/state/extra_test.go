package state

import (
	"pauseproxy/internal/config"
	"pauseproxy/internal/sites"
	"sync"
	"testing"
	"time"
)

func TestExtraLifecycle(t *testing.T) {
	s, n := fixture(t)
	err := s.ChangeRules(func(d *sites.Document) error {
		r := &d.Sites[0]
		r.Seconds = 10
		r.BlurEnabled = true
		r.BlurAfter = 5
		r.GrayscaleEnabled = true
		r.GrayscaleAfter = 5
		r.ExtraEnabled = true
		r.ExtraSeconds = 180
		r.ExtraMaxUses = 2
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	se, _ := s.CreateFor("dev", "127.0.0.1", "x.com")
	req := ExtraRequest{ID: se.ID, Instance: se.Instance, RequestID: "one"}
	grant := func() (Status, error) { return s.ExtraTime(se.User, se.IP, se.Host, req) }
	if _, e := grant(); e != ErrExtra {
		t.Fatal("early grant", e)
	}
	s.BeatFrom(se.User, se.IP, Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 1, Visible: true}, se.Host)
	*n = n.Add(5 * time.Second)
	if v := s.Status("dev"); !v.Blur || !v.Grayscale || v.Blocked {
		t.Fatal(v)
	}
	*n = n.Add(4 * time.Second)
	s.BeatFrom(se.User, se.IP, Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 2, Visible: true}, se.Host)
	*n = n.Add(5 * time.Second)
	if v := s.Status("dev"); !v.Blocked || !v.ExtraAvailable {
		t.Fatal(v)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := grant(); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if v := s.Status("dev"); v.Remaining != 180 || v.ExtraUsesRemaining != 1 || !v.Blur || !v.Grayscale {
		t.Fatal(v)
	}
	// Stop outstanding pre-block leases before checking idle time.
	s.BeatFrom(se.User, se.IP, Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 3}, se.Host)
	*n = n.Add(time.Minute)
	if v := s.Status("dev"); v.Remaining != 180 {
		t.Fatal("idle bonus charged", v)
	}
	if _, e := s.ExtraTime("other", se.IP, se.Host, req); e != ErrSession {
		t.Fatal(e)
	}
	if _, e := s.ExtraTime(se.User, se.IP, "www.x.com", req); e != ErrSession {
		t.Fatal(e)
	}
	s.ChangeRules(func(d *sites.Document) error { d.Sites[0].ExtraSeconds = 60; return nil })
	if s.Status("dev").Remaining != 180 {
		t.Fatal("retroactive duration")
	}
	s.counter("dev", *n, "x.com").Seconds += 180
	req.RequestID = "two"
	if v, e := grant(); e != nil || v.Remaining != 60 || v.ExtraUsesRemaining != 0 {
		t.Fatal(v, e)
	}
	s.counter("dev", *n, "x.com").Seconds += 60
	req.RequestID = "three"
	if _, e := grant(); e != ErrExtra {
		t.Fatal(e)
	}
	*n = n.Add(24 * time.Hour)
	if v := s.Status("dev"); v.ExtraUsesRemaining != 2 || v.Blur || v.Grayscale || v.Blocked {
		t.Fatal(v)
	}
	s.Reset()
	if _, e := grant(); e != ErrSession {
		t.Fatal(e)
	}
}
func TestExtraHardBlocksAndDisable(t *testing.T) {
	s, n := fixture(t)
	s.ChangeRules(func(d *sites.Document) error { d.Sites[0].ExtraEnabled = true; return nil })
	se, _ := s.CreateFor("dev", "127.0.0.1", "x.com")
	r := ExtraRequest{ID: se.ID, Instance: se.Instance, RequestID: "one"}
	s.counter("dev", *n, "x.com").Seconds = 130
	s.cfg.BlockedHours = []config.Window{{Start: "00:00", End: "23:59"}}
	if _, e := s.ExtraTime(se.User, se.IP, se.Host, r); e != ErrExtra {
		t.Fatal(e)
	}
	s.cfg.BlockedHours = nil
	s.ChangeRules(func(d *sites.Document) error { d.Sites[0].Mode = "block"; return nil })
	if _, e := s.ExtraTime(se.User, se.IP, se.Host, r); e != ErrExtra {
		t.Fatal(e)
	}
	s.ChangeRules(func(d *sites.Document) error { d.Sites[0].Mode = "limit"; return nil })
	if _, e := s.ExtraTime(se.User, se.IP, se.Host, r); e != nil {
		t.Fatal(e)
	}
	s.ChangeRules(func(d *sites.Document) error { d.Sites[0].ExtraEnabled = false; return nil })
	if !s.Status("dev").Blocked {
		t.Fatal("disabled bonus still effective")
	}
	saved := s.Rules()
	s.ChangeRules(func(d *sites.Document) error { d.Sites = nil; return nil })
	s.ChangeRules(func(d *sites.Document) error { *d = saved; d.Sites[0].ExtraEnabled = true; return nil })
	if s.Status("dev").ExtraUsesRemaining != 0 {
		t.Fatal("re-add reset uses")
	}
}
