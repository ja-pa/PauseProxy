package state

import (
	"crypto/rand"
	"errors"
	"golang.org/x/text/unicode/norm"
	"math/big"
	"pauseproxy/internal/sites"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrChallenge = errors.New("challenge expired or changed; start a new challenge")

// Only one challenge is retained per session. Reflection answers are never stored.
type ExtraChallenge struct {
	ID        string    `json:"challenge_id"`
	Method    string    `json:"method"`
	A         int       `json:"a,omitempty"`
	B         int       `json:"b,omitempty"`
	Expires   time.Time `json:"expires_at"`
	requestID string
	date      string
	started   time.Time
}

func (c *ExtraChallenge) validAt(t time.Time, loc *time.Location) bool {
	return t.Before(c.Expires) && c.date == t.In(loc).Format("2006-01-02")
}

// Caller holds Store.mu, and has advanced the clock.
func (s *Store) extraSession(user, ip, host string, b ExtraRequest) (*Session, sites.Site, error) {
	se := s.sessions[b.ID]
	if se == nil || se.User != user || se.IP != ip || se.Host != host || b.Instance != s.instance {
		return nil, sites.Site{}, ErrSession
	}
	if len(b.RequestID) < 1 || len(b.RequestID) > 128 {
		return nil, sites.Site{}, errors.New("invalid request_id")
	}
	rule, ok := s.rules.Find(se.Site)
	if host != "" {
		matched, valid := s.rules.Match(host)
		ok = ok && valid && matched.Domain == se.Site
	}
	if !ok || !rule.Enabled {
		return nil, sites.Site{}, ErrExtra
	}
	return se, rule, nil
}
func (s *Store) CreateExtraChallenge(user, ip, host string, b ExtraRequest) (ExtraChallenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.advance(t)
	se, rule, err := s.extraSession(user, ip, host, b)
	if err != nil {
		return ExtraChallenge{}, err
	}
	if !s.status(user, se.Site, t).ExtraAvailable || (rule.ExtraMethod != "math" && rule.ExtraMethod != "reflection") {
		se.Challenge = nil
		return ExtraChallenge{}, ErrExtra
	}
	if s.counter(user, t, se.Site).ExtraRequests[b.RequestID] {
		return ExtraChallenge{}, ErrExtra
	}
	if ch := se.Challenge; ch != nil && ch.requestID == b.RequestID && ch.Method == rule.ExtraMethod && ch.validAt(t, s.loc) {
		return *ch, nil
	}
	ch := &ExtraChallenge{ID: Token(), Method: rule.ExtraMethod, Expires: t.Add(5 * time.Minute), started: t, date: t.In(s.loc).Format("2006-01-02"), requestID: b.RequestID}
	if ch.Method == "math" {
		for {
			a, err := rand.Int(rand.Reader, big.NewInt(121))
			if err != nil {
				return ExtraChallenge{}, err
			}
			pair := int(a.Int64())
			if pair+1 == se.PreviousProblem {
				continue
			}
			se.PreviousProblem = pair + 1
			ch.A, ch.B = 2+pair/11, 2+pair%11
			break
		}
	}
	se.Challenge = ch
	return *ch, nil
}
func ReflectionError(text string) error {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > 500 {
		return errors.New("Use at most 500 characters for your reflection.")
	}
	normalized := strings.ToLower(norm.NFC.String(text))
	words, letters := []string{}, 0
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, r := range normalized {
		if unicode.IsLetter(r) {
			letters++
			word.WriteRune(r)
		} else if unicode.IsMark(r) && word.Len() > 0 {
			word.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	unique, repeated := map[string]bool{}, true
	for _, w := range words {
		unique[w] = true
		chars := []rune(w)
		for _, r := range chars {
			if r != chars[0] {
				repeated = false
			}
		}
	}
	if len(words) > 0 && (len(unique) == 1 || repeated) {
		return errors.New("Describe your intention without just repeating words or letters.")
	}
	if len(words) < 3 || letters < 12 {
		return errors.New("Use at least 3 words and 12 letters to describe your intention.")
	}
	return nil
}
