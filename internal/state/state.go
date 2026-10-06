package state

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"pauseproxy/internal/config"
	"pauseproxy/internal/sites"
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
	ID         string    `json:"session_id"`
	Instance   string    `json:"instance_id"`
	Site, Host string    `json:"-"`
	User, IP   string    `json:"-"`
	Last       time.Time `json:"-"`
	Until      time.Time `json:"-"`
	Sequence   uint64    `json:"-"`
}
type Counter struct {
	User    string  `json:"user"`
	Service string  `json:"service"`
	Date    string  `json:"date"`
	Seconds float64 `json:"used_seconds"`
	Clicks  uint64  `json:"clicks"`
	Visits  uint64  `json:"visits"`
}
type Status struct {
	Tracking  bool    `json:"tracking"`
	Service   string  `json:"service"`
	Instance  string  `json:"instance_id"`
	Remaining float64 `json:"remaining_seconds"`
	Blocked   bool    `json:"blocked"`
	Reason    string  `json:"reason"`
	Warning   int     `json:"warning_seconds"`
}
type Heartbeat struct {
	ID       string `json:"session_id"`
	Instance string `json:"instance_id"`
	Sequence uint64 `json:"sequence"`
	Visible  bool   `json:"visible"`
	Clicks   int    `json:"click_delta"`
}
type Store struct {
	mu        sync.Mutex
	cfg       config.Config
	loc       *time.Location
	now       func() time.Time
	last      time.Time
	instance  string
	sessions  map[string]*Session
	counters  map[string]*Counter
	rules     sites.Document
	rulesPath string
}

func New(c config.Config, now func() time.Time) *Store {
	l, _ := time.LoadLocation(c.Timezone)
	return &Store{cfg: c, loc: l, now: now, last: now(), instance: Token(), sessions: map[string]*Session{}, counters: map[string]*Counter{}, rules: sites.Seed(c)}
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
}
func (s *Store) status(u, site string, t time.Time) Status {
	rule, ok := s.rules.Find(site)
	if !ok || !rule.Enabled {
		return Status{Instance: s.instance, Service: site, Tracking: false}
	}
	remaining := math.Max(0, float64(rule.Seconds)-s.counter(u, t, site).Seconds)
	v := Status{Instance: s.instance, Remaining: remaining, Warning: s.cfg.Warning, Tracking: true, Service: site}
	if rule.Mode == "block" {
		v.Blocked = true
		v.Reason = "This site is always blocked"
	} else if s.cfg.Scheduled(t.In(s.loc)) {
		v.Blocked = true
		v.Reason = "Scheduled break"
	} else if remaining <= 0 {
		v.Blocked = true
		v.Reason = "Daily limit reached"
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
	Instance string         `json:"instance_id"`
	Counters []Counter      `json:"counters"`
	Limits   map[string]int `json:"limits"`
	Sessions int            `json:"sessions"`
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
	return Snapshot{s.instance, cs, ls, len(s.sessions)}
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
	s.last = s.now()
}
