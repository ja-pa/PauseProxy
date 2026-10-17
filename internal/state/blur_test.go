package state

import (
	"testing"
	"time"
)

func TestBlurIndependentThresholds(t *testing.T) {
	s, now := fixture(t)
	r := &s.rules.Sites[0]
	r.BlurEnabled = true
	r.BlurAfter = 3
	r.GrayscaleEnabled = true
	r.GrayscaleAfter = 5
	for _, tc := range []struct {
		used       float64
		blur, gray bool
	}{{2.99, false, false}, {3, true, false}, {5, true, true}} {
		s.counter("dev", *now, "x.com").Seconds = tc.used
		v := s.Status("dev")
		if v.Blur != tc.blur || v.Grayscale != tc.gray {
			t.Fatal(tc, v)
		}
	}
	r.BlurEnabled = false
	if v := s.Status("dev"); v.Blur || !v.Grayscale {
		t.Fatal(v)
	}
	r.BlurEnabled = true
	r.GrayscaleEnabled = false
	if v := s.Status("dev"); !v.Blur || v.Grayscale {
		t.Fatal(v)
	}
	r.Enabled = false
	if v := s.Status("dev"); v.Blur {
		t.Fatal(v)
	}
	r.Enabled = true
	r.Mode = "block"
	if v := s.Status("dev"); v.Blur {
		t.Fatal(v)
	}
	r.Mode = "limit"
	*now = now.Add(24 * time.Hour)
	if v := s.Status("dev"); v.Blur {
		t.Fatal(v)
	}
	s.counter("dev", *now, "x.com").Seconds = 5
	s.Reset()
	if v := s.Status("dev"); v.Blur {
		t.Fatal(v)
	}
}
func TestBlurUsesOwnTimeInGroup(t *testing.T) {
	s, now := groupedFixture(t)
	r := &s.rules.Sites[0]
	r.BlurEnabled = true
	r.BlurAfter = 3
	activeGroupSession(t, s, "youtube.com")
	*now = now.Add(5 * time.Second)
	if v := s.Status("dev", "x.com"); v.Blur {
		t.Fatal(v)
	}
	s.counter("dev", *now, "x.com").Seconds = 3
	if v := s.Status("dev", "x.com"); !v.Blur {
		t.Fatal(v)
	}
}
