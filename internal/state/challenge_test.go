package state

import (
	"encoding/json"
	"errors"
	"pauseproxy/internal/sites"
	"strings"
	"sync"
	"testing"
	"time"
)

func challengeFixture(t *testing.T, method string) (*Store, *time.Time, Session, ExtraRequest) {
	t.Helper()
	s, now := fixture(t)
	if err := s.ChangeRules(func(d *sites.Document) error {
		r := &d.Sites[0]
		r.Seconds = 10
		r.ExtraEnabled = true
		r.ExtraMethod = method
		r.ExtraMaxUses = 3
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	se, err := s.CreateFor("dev", "127.0.0.1", "x.com")
	if err != nil {
		t.Fatal(err)
	}
	s.counter("dev", *now, "x.com").Seconds = 10
	return s, now, se, ExtraRequest{ID: se.ID, Instance: se.Instance, RequestID: "one"}
}
func TestMathChallengeLifecycle(t *testing.T) {
	s, now, se, b := challengeFixture(t, "math")
	ch, err := s.CreateExtraChallenge(se.User, se.IP, se.Host, b)
	if err != nil {
		t.Fatal(err)
	}
	if ch.A < 2 || ch.A > 12 || ch.B < 2 || ch.B > 12 {
		t.Fatal(ch)
	}
	repeat, err := s.CreateExtraChallenge(se.User, se.IP, se.Host, b)
	if err != nil || repeat.ID != ch.ID {
		t.Fatal(repeat, err)
	}
	encoded, _ := json.Marshal(ch)
	for _, secret := range []string{"requestID", "started", "date", "answer"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal(string(encoded))
		}
	}
	if _, err = s.ExtraTime(se.User, se.IP, se.Host, b); !errors.Is(err, ErrChallenge) {
		t.Fatal("missing proof", err)
	}
	b.ChallengeID = ch.ID
	wrong := ch.A*ch.B + 1
	b.Answer = &wrong
	if _, err = s.ExtraTime(se.User, se.IP, se.Host, b); err == nil {
		t.Fatal("wrong answer accepted")
	}
	if c := s.counter("dev", *now, "x.com"); c.ExtraUses != 0 || c.ExtraSeconds != 0 {
		t.Fatal(c)
	}
	correct := ch.A * ch.B
	b.Answer = &correct
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.ExtraTime(se.User, se.IP, se.Host, b); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if c := s.counter("dev", *now, "x.com"); c.ExtraUses != 1 {
		t.Fatal(c)
	}
	if s.sessions[se.ID].Challenge != nil {
		t.Fatal("consumed challenge retained")
	}
	s.counter("dev", *now, "x.com").Seconds += 180
	b.RequestID = "two"
	b.ChallengeID = ""
	b.Answer = nil
	if _, err = s.ExtraTime(se.User, se.IP, se.Host, b); err != ErrChallenge {
		t.Fatal("second grant bypassed task", err)
	}
	next, err := s.CreateExtraChallenge(se.User, se.IP, se.Host, b)
	if err != nil || next.ID == ch.ID || (next.A == ch.A && next.B == ch.B) {
		t.Fatal(next, err)
	}
}
func TestChallengeBindingAndInvalidation(t *testing.T) {
	for _, change := range []string{"user", "ip", "host", "instance", "request", "session", "token", "expiry", "midnight", "reset", "method"} {
		t.Run(change, func(t *testing.T) {
			s, now, se, b := challengeFixture(t, "math")
			ch, err := s.CreateExtraChallenge(se.User, se.IP, se.Host, b)
			if err != nil {
				t.Fatal(err)
			}
			answer := ch.A * ch.B
			b.Answer = &answer
			b.ChallengeID = ch.ID
			user, ip, host := se.User, se.IP, se.Host
			switch change {
			case "user":
				user = "other"
			case "ip":
				ip = "192.0.2.1"
			case "host":
				host = "www.x.com"
			case "instance":
				b.Instance = "wrong"
			case "request":
				b.RequestID = "other"
			case "session":
				other, e := s.CreateFor(se.User, se.IP, se.Host)
				if e != nil {
					t.Fatal(e)
				}
				b.ID = other.ID
			case "token":
				b.ChallengeID = "wrong"
			case "expiry":
				*now = now.Add(5 * time.Minute)
				s.sessions[se.ID].Last = *now
			case "midnight":
				*now = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, s.loc)
				s.sessions[se.ID].Last = *now
				s.counter("dev", *now, "x.com").Seconds = 10
			case "reset":
				s.Reset()
			case "method":
				for _, m := range []string{"reflection", "math"} {
					if e := s.ChangeRules(func(d *sites.Document) error { d.Sites[0].ExtraMethod = m; return nil }); e != nil {
						t.Fatal(e)
					}
				}
			}
			if _, err = s.ExtraTime(user, ip, host, b); err == nil {
				t.Fatal("stale/mismatched proof accepted")
			}
		})
	}
}
func TestReflectionValidation(t *testing.T) {
	for _, tc := range []struct {
		text  string
		valid bool
	}{
		{"Chci odpovědět na zprávu", true}, {"I want to reply to a message", true}, {"Chci odpove\u030cde\u030ct na zpra\u0301vu", true},
		{"", false}, {"a b c", false}, {"zpráva zpráva zpráva", false}, {"aaaa bbbb cccc", false}, {"123 456 789", false}, {strings.Repeat("x", 501), false},
	} {
		if err := ReflectionError(tc.text); (err == nil) != tc.valid {
			t.Fatalf("%q: %v", tc.text, err)
		}
	}
}
func TestReflectionRequiresPauseAndNewTask(t *testing.T) {
	s, now, se, b := challengeFixture(t, "reflection")
	ch, err := s.CreateExtraChallenge(se.User, se.IP, se.Host, b)
	if err != nil {
		t.Fatal(err)
	}
	b.ChallengeID = ch.ID
	b.Reflection = "Chci odpovědět na zprávu"
	if _, err = s.ExtraTime(se.User, se.IP, se.Host, b); err == nil {
		t.Fatal("early grant")
	}
	*now = now.Add(10 * time.Second)
	b.Reflection = "aaaa bbbb cccc"
	if _, err = s.ExtraTime(se.User, se.IP, se.Host, b); err == nil {
		t.Fatal("invalid intention")
	}
	b.Reflection = "Chci odpovědět na zprávu"
	if _, err = s.ExtraTime(se.User, se.IP, se.Host, b); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExtraTime(se.User, se.IP, se.Host, b); err != nil {
		t.Fatal("lost-response retry", err)
	}
	s.counter(se.User, *now, se.Site).Seconds += 180
	b.RequestID = "two"
	if _, err = s.ExtraTime(se.User, se.IP, se.Host, b); err != ErrChallenge {
		t.Fatal(err)
	}
	if s.counter(se.User, *now, se.Site).ExtraUses != 1 {
		t.Fatal("extra grant")
	}
}
func TestChallengesUnavailableDuringHardBlocks(t *testing.T) {
	for _, kind := range []string{"click", "disabled", "always", "early"} {
		t.Run(kind, func(t *testing.T) {
			s, now, se, b := challengeFixture(t, "math")
			switch kind {
			case "click":
				s.rules.Sites[0].ExtraMethod = "click"
			case "disabled":
				s.rules.Sites[0].ExtraEnabled = false
			case "always":
				s.rules.Sites[0].Mode = "block"
			case "early":
				s.counter(se.User, *now, se.Site).Seconds = 0
			}
			if _, err := s.CreateExtraChallenge(se.User, se.IP, se.Host, b); err != ErrExtra {
				t.Fatal(err)
			}
		})
	}
}
