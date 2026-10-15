package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"pauseproxy/internal/config"
	"pauseproxy/internal/state"
	"testing"
	"time"
)

func TestAdminStateIncludesOverviewAndLegacyCounters(t *testing.T) {
	c := config.Default()
	c.AdminPassword = "test-only"
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	s := state.New(c, func() time.Time { return now })
	se, err := s.Create("dev", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Beat("dev", "127.0.0.1", state.Heartbeat{ID: se.ID, Instance: se.Instance, Sequence: 1, Visible: true, Clicks: 3})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	a := &API{Config: c, State: s}
	r := httptest.NewRequest("GET", c.AdminURL+"/api/state", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Authorization", "Bearer test-only")
	w := httptest.NewRecorder()
	a.Admin(w, r)
	var got struct {
		state.Snapshot
		Diagnostics Diagnostics `json:"diagnostics"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if got.Date != "2026-10-15" || len(got.Overview) != 1 || got.Overview[0].Remaining != 118 || got.Overview[0].State != "available" {
		t.Fatalf("missing or incorrect overview: %+v", got)
	}
	if len(got.Counters) != 1 || got.Counters[0].Clicks != 3 || got.Counters[0].Visits != 1 || got.Limits["x.com"] != 120 || got.Sessions != 1 || got.Instance == "" {
		t.Fatalf("legacy API fields changed: %+v", got)
	}
}
