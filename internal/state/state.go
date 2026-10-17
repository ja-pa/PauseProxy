package state

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"pauseproxy/internal/config"
	"pauseproxy/internal/sites"
	"sort"
	"sync"
	"time"
)

var ErrSession = errors.New("session expired; create a new session")
var ErrCapacity = errors.New("session capacity reached")
var ErrRate = errors.New("heartbeat too frequent")

func Token() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}

type Session struct {
	ID              string          `json:"session_id"`
	Instance        string          `json:"instance_id"`
	Site, Host      string          `json:"-"`
	User, IP        string          `json:"-"`
	Last            time.Time       `json:"-"`
	Until           time.Time       `json:"-"`
	Challenge       *ExtraChallenge `json:"-"`
	PreviousProblem int             `json:"-"`
	Sequence        uint64          `json:"-"`
}
type Counter struct {
	ExtraUses     int             `json:"extra_time_uses"`
	ExtraSeconds  float64         `json:"extra_time_granted_seconds"`
	ExtraRequests map[string]bool `json:"-"`
	User          string          `json:"user"`
	Service       string          `json:"service"`
	Date          string          `json:"date"`
	Seconds       float64         `json:"used_seconds"`
	Clicks        uint64          `json:"clicks"`
	Visits        uint64          `json:"visits"`
}
type Status struct {
	GroupID            string  `json:"group_id,omitempty"`
	GroupName          string  `json:"group_name,omitempty"`
	GroupRemaining     float64 `json:"group_remaining_seconds,omitempty"`
	LimitScope         string  `json:"limit_scope,omitempty"`
	Blur               bool    `json:"blur"`
	Grayscale          bool    `json:"grayscale"`
	ExtraMethod        string  `json:"extra_time_method"`
	ExtraAvailable     bool    `json:"extra_time_available"`
	ExtraSeconds       int     `json:"extra_time_seconds"`
	ExtraUsesRemaining int     `json:"extra_time_uses_remaining"`
	Tracking           bool    `json:"tracking"`
	Service            string  `json:"service"`
	Instance           string  `json:"instance_id"`
	Remaining          float64 `json:"remaining_seconds"`
	Blocked            bool    `json:"blocked"`
	Reason             string  `json:"reason"`
	Warning            int     `json:"warning_seconds"`
}
type Heartbeat struct {
	ID       string `json:"session_id"`
	Instance string `json:"instance_id"`
	Sequence uint64 `json:"sequence"`
	Visible  bool   `json:"visible"`
	Clicks   int    `json:"click_delta"`
}
type Store struct {
	mu         sync.Mutex
	cfg        config.Config
	loc        *time.Location
	now        func() time.Time
	last       time.Time
	instance   string
	sessions   map[string]*Session
	counters   map[string]*Counter
	groupUsage map[groupKey]float64
	rules      sites.Document
	rulesPath  string
}

type groupKey struct{ User, Group, Date string }

func New(c config.Config, now func() time.Time) *Store {
	l, _ := time.LoadLocation(c.Timezone)
	return &Store{cfg: c, loc: l, now: now, last: now(), instance: Token(), sessions: map[string]*Session{}, counters: map[string]*Counter{}, groupUsage: map[groupKey]float64{}, rules: sites.Seed(c)}
}
func (s *Store) counter(user string, t time.Time, site ...string) *Counter {
	date := t.In(s.loc).Format("2006-01-02")
	service := s.defaultSite()
	if len(site) > 0 {
		service = site[0]
	}
	key := user + "/" + service + "/" + date
	c := s.counters[key]
	if c == nil {
		c = &Counter{User: user, Service: service, Date: date}
		s.counters[key] = c
	}
	return c
}
func (s *Store) defaultSite() string {
	if v, ok := s.rules.Find("x.com"); ok && v.Enabled {
		return v.Domain
	}
	for _, v := range s.rules.Sites {
		if v.Enabled {
			return v.Domain
		}
	}
	return ""
}
func (s *Store) Rules() sites.Document { s.mu.Lock(); defer s.mu.Unlock(); return s.rules.Clone() }
func (s *Store) Match(host string) (sites.Site, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.rules.Match(host)
	v.Domains = append([]string{}, v.Domains...)
	return v, ok
}
func (s *Store) ConfigureRules(d sites.Document, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = d.Clone()
	s.rulesPath = path
}
func (s *Store) ChangeRules(change func(*sites.Document) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.rules.Clone()
	if e := change(&d); e != nil {
		return e
	}
	if e := d.Validate(); e != nil {
		return e
	}
	if e := sites.Save(s.rulesPath, d); e != nil {
		return e
	}
	t := s.now()
	s.advance(t)
	s.rules = d
	for _, se := range s.sessions {
		v, ok := d.Find(se.Site)
		valid := ok && v.Enabled
		if se.Host != "" {
			m, matched := d.Match(se.Host)
			valid = valid && matched && m.Domain == se.Site
		}
		if se.Challenge != nil && (!valid || !v.ExtraEnabled || v.ExtraMethod != se.Challenge.Method || !s.status(se.User, se.Site, t).ExtraAvailable) {
			se.Challenge = nil
		}
		if !valid || v.Mode == "block" {
			se.Until = t
		}
	}
	return nil
}

// Each advance accounts the union of still-visible leases, never one clock per tab.
func (s *Store) advance(t time.Time) {
	type account struct{ user, site string }
	ends := map[account]time.Time{}
	for id, se := range s.sessions {
		if se.Challenge != nil && !se.Challenge.validAt(t, s.loc) {
			se.Challenge = nil
		}
		if se.Until.After(ends[account{se.User, se.Site}]) {
			ends[account{se.User, se.Site}] = se.Until
		}
		if t.Sub(se.Last) > 5*time.Minute {
			delete(s.sessions, id)
		}
	}
	for u, end := range ends {
		if end.After(t) {
			end = t
		}
		start := s.last
		for start.Before(end) {
			local := start.In(s.loc)
			midnight := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, s.loc)
			stop := end
			if midnight.Before(stop) {
				stop = midnight
			}
			s.counter(u.user, start, u.site).Seconds += stop.Sub(start).Seconds()
			if rule, ok := s.rules.Find(u.site); ok && rule.Enabled && rule.Mode == "limit" && rule.SharesGroup() {
				key := groupKey{u.user, rule.GroupID, local.Format("2006-01-02")}
				s.groupUsage[key] += stop.Sub(start).Seconds()
			}
			start = stop
		}
	}
	s.last = t
	oldest := t.In(s.loc).AddDate(0, 0, -1).Format("2006-01-02")
	for k, c := range s.counters {
		if c.Date < oldest {
			delete(s.counters, k)
		}
	}
	for key := range s.groupUsage {
		if key.Date < oldest {
			delete(s.groupUsage, key)
		}
	}
}
func (s *Store) status(u, site string, t time.Time) Status {
	rule, ok := s.rules.Find(site)
	if !ok || !rule.Enabled {
		return Status{Instance: s.instance, Service: site, Tracking: false}
	}
	c := s.counter(u, t, site)
	return s.ruleStatus(rule, *c, t)
}

func dailyBudget(rule sites.Site, c Counter) float64 {
	budget := float64(rule.Seconds)
	if rule.ExtraEnabled {
		budget += c.ExtraSeconds
	}
	return budget
}

// Evaluate a rule without creating a counter, including for unused overview rows.
func (s *Store) ruleStatus(rule sites.Site, c Counter, t time.Time) Status {
	remaining := math.Max(0, dailyBudget(rule, c)-c.Seconds)
	groupRemaining := math.Inf(1)
	g, grouped := s.rules.FindGroup(rule.GroupID)
	if grouped && rule.SharesGroup() {
		groupRemaining = math.Max(0, float64(g.Seconds)-s.groupUsage[groupKey{c.User, g.ID, t.In(s.loc).Format("2006-01-02")}])
		if rule.BudgetMode == "shared" {
			remaining = groupRemaining
		} else {
			remaining = math.Min(remaining, groupRemaining)
		}
	}
	v := Status{Instance: s.instance, Remaining: remaining, Warning: s.cfg.Warning, Tracking: rule.Enabled, Service: rule.Domain}
	if grouped {
		v.GroupID, v.GroupName = g.ID, g.Name
		if rule.SharesGroup() {
			v.GroupRemaining = groupRemaining
		}
	}
	v.ExtraMethod = rule.ExtraMethod
	if v.ExtraMethod == "" {
		v.ExtraMethod = "click"
	}
	if !rule.Enabled {
		return v
	}
	if rule.Mode == "block" {
		v.Blocked = true
		v.Reason = "This site is always blocked"
	} else if s.cfg.Scheduled(t.In(s.loc)) {
		v.Blocked = true
		v.Reason = "Scheduled break"
	} else if remaining <= 0 {
		v.Blocked = true
		v.Reason, v.LimitScope = "Daily limit reached", "site"
		if groupRemaining <= 0 {
			v.Reason, v.LimitScope = "Group limit reached: "+g.Name, "group"
		}
	}
	v.Blur = rule.Mode == "limit" && rule.BlurEnabled && c.Seconds >= float64(rule.BlurAfter)
	v.Grayscale = rule.Mode == "limit" && rule.GrayscaleEnabled && c.Seconds >= float64(rule.GrayscaleAfter)
	v.ExtraSeconds = rule.ExtraSeconds
	if rule.ExtraEnabled && rule.Mode == "limit" && rule.BudgetMode != "shared" {
		v.ExtraUsesRemaining = max(0, rule.ExtraMaxUses-c.ExtraUses)
		v.ExtraAvailable = v.Blocked && !s.cfg.Scheduled(t.In(s.loc)) && v.ExtraUsesRemaining > 0 && groupRemaining > 0
	}
	return v
}
func (s *Store) Create(user, ip string, selected ...string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.advance(t)
	if len(s.sessions) >= 256 {
		return Session{}, ErrCapacity
	}
	site := s.defaultSite()
	if len(selected) > 0 {
		site = selected[0]
	}
	rule, ok := s.rules.Find(site)
	if !ok || !rule.Enabled {
		return Session{}, errors.New("site is not active")
	}
	v := Session{Site: site, ID: Token(), Instance: s.instance, User: user, IP: ip, Last: t}
	s.sessions[v.ID] = &v
	s.counter(user, t, site).Visits++
	return v, nil
}
func (s *Store) Beat(user, ip string, b Heartbeat) (Status, error) {
	return s.BeatFrom(user, ip, b, "")
}
func (s *Store) BeatFrom(user, ip string, b Heartbeat, host string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.advance(t)
	se := s.sessions[b.ID]
	if se == nil || se.User != user || se.IP != ip || b.Instance != s.instance {
		return Status{Instance: s.instance}, ErrSession
	}
	if host != se.Host {
		return Status{}, ErrSession
	}
	rule, enabled := s.rules.Find(se.Site)
	valid := enabled && rule.Enabled
	if se.Host != "" {
		m, ok := s.rules.Match(se.Host)
		valid = valid && ok && m.Domain == se.Site
	}
	if !valid {
		se.Until = t
		return Status{Instance: s.instance, Service: se.Site, Tracking: false}, nil
	}
	if b.Sequence == 0 || b.Clicks < 0 || b.Clicks > 1000 {
		return Status{}, errors.New("invalid heartbeat")
	}
	if b.Sequence <= se.Sequence {
		return s.status(user, se.Site, t), nil
	}
	if b.Visible && se.Sequence > 0 && t.Sub(se.Last) < time.Second {
		return Status{}, ErrRate
	}
	se.Sequence = b.Sequence
	se.Last = t
	s.counter(user, t, se.Site).Clicks += uint64(b.Clicks)
	se.Until = t
	status := s.status(user, se.Site, t)
	if b.Visible && !status.Blocked {
		se.Until = t.Add(10 * time.Second)
	}
	return status, nil
}
func (s *Store) Status(user string, selected ...string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.advance(t)
	site := s.defaultSite()
	if len(selected) > 0 {
		site = selected[0]
	}
	return s.status(user, site, t)
}

type Snapshot struct {
	Groups   []GroupOverview `json:"group_overview"`
	Instance string          `json:"instance_id"`
	Counters []Counter       `json:"counters"`
	Limits   map[string]int  `json:"limits"`
	Sessions int             `json:"sessions"`
	Date     string          `json:"date"`
	Overview []OverviewRow   `json:"overview"`
}

type OverviewRow struct {
	SiteBudget float64 `json:"site_budget_seconds"`
	GroupID    string  `json:"group_id"`
	BudgetMode string  `json:"budget_mode"`
	User       string  `json:"user"`
	Site       string  `json:"site"`
	Used       float64 `json:"used_seconds"`
	Budget     float64 `json:"budget_seconds"`
	Remaining  float64 `json:"remaining_seconds"`
	State      string  `json:"state"`
}

type GroupOverview struct {
	User      string  `json:"user"`
	GroupID   string  `json:"group_id"`
	Name      string  `json:"name"`
	Used      float64 `json:"used_seconds"`
	Budget    float64 `json:"budget_seconds"`
	Remaining float64 `json:"remaining_seconds"`
	State     string  `json:"state"`
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.advance(t)
	cs := []Counter{}
	for _, c := range s.counters {
		cs = append(cs, *c)
	}
	ls := map[string]int{}
	for _, v := range s.rules.Sites {
		ls[v.Domain] = v.Seconds
	}
	date := t.In(s.loc).Format("2006-01-02")
	uniqueUsers := map[string]bool{}
	for _, user := range s.cfg.Users {
		uniqueUsers[user] = true
	}
	users := make([]string, 0, len(uniqueUsers))
	for user := range uniqueUsers {
		users = append(users, user)
	}
	sort.Strings(users)
	rows := make([]OverviewRow, 0, len(users)*len(s.rules.Sites))
	groups := []GroupOverview{}
	for _, user := range users {
		for _, g := range s.rules.Groups {
			used := s.groupUsage[groupKey{user, g.ID, date}]
			remaining := math.Max(0, float64(g.Seconds)-used)
			state := "available"
			if s.cfg.Scheduled(t.In(s.loc)) {
				state = "scheduled_break"
			} else if remaining <= 0 {
				state = "limit_reached"
			}
			groups = append(groups, GroupOverview{user, g.ID, g.Name, used, float64(g.Seconds), remaining, state})
		}
		for _, rule := range s.rules.Sites {
			c := Counter{User: user}
			if existing := s.counters[user+"/"+rule.Domain+"/"+date]; existing != nil {
				c = *existing
			}
			status := s.ruleStatus(rule, c, t)
			state := "available"
			switch {
			case !rule.Enabled:
				state = "paused"
			case status.Reason == "This site is always blocked":
				state = "always_blocked"
			case status.Reason == "Scheduled break":
				state = "scheduled_break"
			case status.LimitScope == "group":
				state = "group_limit_reached"
			case status.Reason == "Daily limit reached":
				state = "limit_reached"
			}
			budget := dailyBudget(rule, c)
			if rule.SharesGroup() {
				if g, ok := s.rules.FindGroup(rule.GroupID); ok {
					if rule.BudgetMode == "shared" {
						budget = float64(g.Seconds)
					} else {
						budget = math.Min(budget, float64(g.Seconds))
					}
				}
			}
			rows = append(rows, OverviewRow{User: user, Site: rule.Domain, Used: c.Seconds, GroupID: rule.GroupID, BudgetMode: rule.BudgetMode,
				Budget: budget, SiteBudget: dailyBudget(rule, c), Remaining: status.Remaining, State: state})
		}
	}
	return Snapshot{Instance: s.instance, Counters: cs, Limits: ls, Sessions: len(s.sessions), Date: date, Overview: rows, Groups: groups}
}
func (s *Store) SetLimit(domain string, n int) error {
	return s.ChangeRules(func(d *sites.Document) error {
		for i := range d.Sites {
			if d.Sites[i].Domain == domain {
				d.Sites[i].Seconds = n
				return nil
			}
		}
		return errors.New("unknown site")
	})
}
func (s *Store) CreateFor(user, ip, host string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.advance(t)
	rule, ok := s.rules.Match(host)
	if !ok {
		return Session{}, errors.New("site is not active")
	}
	if len(s.sessions) >= 256 {
		return Session{}, ErrCapacity
	}
	se := Session{ID: Token(), Instance: s.instance, Site: rule.Domain, Host: config.Host(host), User: user, IP: ip, Last: t}
	s.sessions[se.ID] = &se
	s.counter(user, t, se.Site).Visits++
	return se, nil
}
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instance = Token()
	s.sessions = map[string]*Session{}
	s.counters = map[string]*Counter{}
	s.groupUsage = map[groupKey]float64{}
	s.last = s.now()
}

// ExtraTime grants a daily shared budget, atomically and idempotently.
type ExtraRequest struct {
	ID          string `json:"session_id"`
	Instance    string `json:"instance_id"`
	RequestID   string `json:"request_id"`
	ChallengeID string `json:"challenge_id,omitempty"`
	Answer      *int   `json:"answer,omitempty"`
	Reflection  string `json:"reflection,omitempty"`
}

var ErrExtra = errors.New("extra time is not available")

func (s *Store) ExtraTime(user, ip, host string, b ExtraRequest) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.advance(t)
	se, rule, err := s.extraSession(user, ip, host, b)
	if err != nil {
		return Status{}, err
	}
	c := s.counter(user, t, se.Site)
	v := s.status(user, se.Site, t)
	if c.ExtraRequests[b.RequestID] {
		return v, nil
	}
	if !v.ExtraAvailable {
		se.Challenge = nil
		return v, ErrExtra
	}
	if rule.ExtraMethod != "" && rule.ExtraMethod != "click" {
		ch := se.Challenge
		if ch == nil || ch.ID != b.ChallengeID || ch.requestID != b.RequestID || ch.Method != rule.ExtraMethod || !ch.validAt(t, s.loc) {
			return v, ErrChallenge
		}
		if ch.Method == "math" && (b.Answer == nil || *b.Answer != ch.A*ch.B) {
			return v, errors.New("That is not the correct answer. Try the same problem again.")
		}
		if ch.Method == "reflection" {
			if t.Sub(ch.started) < 10*time.Second {
				return v, errors.New("Complete the 10-second pause before continuing.")
			}
			if err := ReflectionError(b.Reflection); err != nil {
				return v, err
			}
		}
	}
	se.Challenge = nil
	c.ExtraSeconds = math.Max(c.ExtraSeconds, c.Seconds-float64(rule.Seconds)) + float64(rule.ExtraSeconds)
	c.ExtraUses++
	// A blocked page must send a fresh active heartbeat before spending the grant.
	for _, active := range s.sessions {
		if active.User == user && active.Site == se.Site {
			active.Until = t
		}
	}
	if c.ExtraRequests == nil {
		c.ExtraRequests = map[string]bool{}
	}
	c.ExtraRequests[b.RequestID] = true
	return s.status(user, se.Site, t), nil
}

// ResetSite clears only today's counters and sessions for one configured user/site.
// Settled group usage remains historical and belongs to the whole group.
func (s *Store) ResetSite(user, domain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	known := false
	for _, name := range s.cfg.Users {
		if name == user {
			known = true
			break
		}
	}
	if !known {
		return errors.New("unknown user")
	}
	if _, ok := s.rules.Find(domain); !ok {
		return errors.New("unknown site")
	}
	t := s.now()
	s.advance(t)
	date := t.In(s.loc).Format("2006-01-02")
	delete(s.counters, user+"/"+domain+"/"+date)
	for id, se := range s.sessions {
		if se.User == user && se.Site == domain {
			delete(s.sessions, id)
		}
	}
	return nil
}
