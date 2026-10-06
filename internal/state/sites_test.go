package state

import (
	"os"
	"path/filepath"
	"pauseproxy/internal/config"
	"pauseproxy/internal/sites"
	"sync"
	"testing"
	"time"
)

func TestIndependentWebsUsersAndLiveRules(t *testing.T) {
	s, n := fixture(t)
	d := s.Rules()
	d.Sites = append(d.Sites, sites.Site{Domain: "reddit.com", Domains: []string{"reddit.com", "www.reddit.com"}, Enabled: true, Mode: "limit", Seconds: 300})
	s.ConfigureRules(d, "")
	x, _ := s.CreateFor("dev", "127.0.0.1", "x.com")
	r, _ := s.CreateFor("dev", "127.0.0.1", "reddit.com")
	other, _ := s.CreateFor("android", "192.168.1.2", "x.com")
	hb := func(se Session, seq uint64) {
		t.Helper()
		if _, e := s.BeatFrom(se.User, se.IP, Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: seq, Visible: true}, se.Host); e != nil {
			t.Fatal(e)
		}
	}
	hb(x, 1)
	hb(r, 1)
	*n = n.Add(5 * time.Second)
	hb(other, 1)
	*n = n.Add(5 * time.Second)
	if s.Status("dev", "x.com").Remaining != 110 || s.Status("dev", "reddit.com").Remaining != 290 || s.Status("android", "x.com").Remaining != 115 {
		t.Fatal("budgets mixed")
	}
	if _, e := s.BeatFrom(x.User, x.IP, Heartbeat{ID: x.ID, Instance: x.Instance, Sequence: 2}, "reddit.com"); e != ErrSession {
		t.Fatal("changed session host")
	}
	if e := s.SetLimit("x.com", 3); e != nil {
		t.Fatal(e)
	}
	if !s.Status("dev", "x.com").Blocked || s.Status("dev", "reddit.com").Blocked {
		t.Fatal("limit leaked across websites")
	}
	if e := s.ChangeRules(func(d *sites.Document) error { d.Sites[0].Enabled = false; return nil }); e != nil {
		t.Fatal(e)
	}
	st, e := s.BeatFrom(x.User, x.IP, Heartbeat{ID: x.ID, Instance: x.Instance, Sequence: 3}, x.Host)
	if e != nil || st.Tracking {
		t.Fatal("removed client not released", st, e)
	}
	used := s.counter("dev", *n, "x.com").Seconds
	*n = n.Add(time.Minute)
	s.Snapshot()
	if s.counter("dev", *n, "x.com").Seconds != used {
		t.Fatal("removed lease counted")
	}
	if e := s.ChangeRules(func(d *sites.Document) error { d.Sites[0].Enabled = true; d.Sites[0].Seconds = 120; return nil }); e != nil {
		t.Fatal(e)
	}
	if s.Status("dev", "x.com").Remaining != 120-used {
		t.Fatal("reenable reset usage")
	}
	if e := s.ChangeRules(func(d *sites.Document) error { d.Sites[1].Mode = "block"; return nil }); e != nil {
		t.Fatal(e)
	}
	if !s.Status("dev", "reddit.com").Blocked {
		t.Fatal("always block ignored")
	}
}
func TestConcurrentPersistentRulesAndFailedCommit(t *testing.T) {
	s := New(config.Default(), time.Now)
	path := filepath.Join(t.TempDir(), "sites.json")
	s.ConfigureRules(s.Rules(), path)
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if e := s.SetLimit("x.com", 100+i); e != nil {
				t.Error(e)
			}
			s.Match("x.com")
			s.Snapshot()
		}(i)
	}
	wg.Wait()
	disk, e := sites.Load(path, config.Default())
	if e != nil || disk.Sites[0].Seconds != s.Rules().Sites[0].Seconds {
		t.Fatal("disk and memory diverged", e)
	}
	blocker := filepath.Join(t.TempDir(), "blocker")
	_ = os.WriteFile(blocker, []byte("x"), 0600)
	before := s.Rules()
	s.ConfigureRules(before, filepath.Join(blocker, "sites.json"))
	if e := s.SetLimit("x.com", 500); e == nil {
		t.Fatal("write succeeded")
	}
	if s.Rules().Sites[0].Seconds != before.Sites[0].Seconds {
		t.Fatal("failed write changed memory")
	}
}
