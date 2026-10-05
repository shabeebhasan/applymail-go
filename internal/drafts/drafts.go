// Package drafts stores application drafts as JSON files and keeps the send log that stops
// duplicates and caps how many emails go out per day.
package drafts

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shabeebhasan/applymail-go/internal/extract"
)

type Draft struct {
	ID        string      `json:"id"`
	State     string      `json:"state"` // draft | sent | cancelled
	Job       extract.Job `json:"job"`
	PostText  string      `json:"post_text,omitempty"`
	To        string      `json:"to"`
	Subject   string      `json:"subject"`
	Body      string      `json:"body"`
	Letter    string      `json:"letter"`
	CVPath    string      `json:"cv_path"`
	CVReason  string      `json:"cv_reason"`
	Source    string      `json:"source"` // discord | n8n | api
	CreatedAt time.Time   `json:"created_at"`
	SentAt    *time.Time  `json:"sent_at,omitempty"`
	Error     string      `json:"error,omitempty"`
}

var (
	ErrNotFound  = errors.New("draft not found")
	ErrDuplicate = errors.New("already applied to this address for this role")
	ErrDailyCap  = errors.New("daily send limit reached")
	ErrState     = errors.New("draft is not in a sendable state")
	idRE         = regexp.MustCompile(`^[a-f0-9]{12}$`)
)

type Store struct {
	Dir      string
	DailyCap int
	mu       sync.Mutex
}

func (s *Store) path(id string) (string, error) {
	if !idRE.MatchString(id) {
		return "", ErrNotFound
	}
	return filepath.Join(s.Dir, "drafts", id+".json"), nil
}

func NewID() string {
	var b [6]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Store) Save(d *Draft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(d)
}

func (s *Store) save(d *Draft) error {
	p, err := s.path(d.ID)
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Dir(p), 0o700)
	b, _ := json.MarshalIndent(d, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (s *Store) Get(id string) (*Draft, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var d Draft
	return &d, json.Unmarshal(b, &d)
}

// AttachmentPath is where a draft's rendered cover letter lives.
func (s *Store) AttachmentPath(id string) string {
	return filepath.Join(s.Dir, "drafts", id+"-cover-letter.pdf")
}

type logEntry struct {
	ID   string    `json:"id"`
	To   string    `json:"to"`
	Role string    `json:"role"`
	At   time.Time `json:"at"`
}

func (s *Store) log() ([]logEntry, error) {
	b, err := os.ReadFile(filepath.Join(s.Dir, "sent.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var out []logEntry
	for _, line := range strings.Split(string(b), "\n") {
		var e logEntry
		if json.Unmarshal([]byte(line), &e) == nil && e.ID != "" {
			out = append(out, e)
		}
	}
	return out, err
}

func norm(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// Send runs fn (the actual SMTP send) only if the draft may go out, and records it.
// The whole check-send-record runs under one lock so a double click cannot send twice.
func (s *Store) Send(id string, force bool, now time.Time, fn func(*Draft) error) (*Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if d.State != "draft" {
		return d, fmt.Errorf("%w (state %s)", ErrState, d.State)
	}
	entries, err := s.log()
	if err != nil {
		return d, err
	}
	today := 0
	for _, e := range entries {
		if now.Sub(e.At) < 24*time.Hour {
			today++
		}
		if !force && norm(e.To) == norm(d.To) && norm(e.Role) == norm(d.Job.Role) {
			return d, fmt.Errorf("%w (draft %s, %s)", ErrDuplicate, e.ID, e.At.Format("2 Jan 15:04"))
		}
	}
	if s.DailyCap > 0 && today >= s.DailyCap {
		return d, fmt.Errorf("%w (%d in 24h)", ErrDailyCap, today)
	}
	if err := fn(d); err != nil {
		d.Error = err.Error()
		s.save(d)
		return d, err
	}
	d.State, d.Error, d.SentAt = "sent", "", &now
	if err := s.save(d); err != nil {
		return d, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "sent.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return d, err
	}
	defer f.Close()
	line, _ := json.Marshal(logEntry{ID: d.ID, To: d.To, Role: d.Job.Role, At: now})
	_, err = f.Write(append(line, '\n'))
	return d, err
}

// Recent lists the latest drafts, newest first.
func (s *Store) Recent(n int) ([]*Draft, error) {
	files, _ := filepath.Glob(filepath.Join(s.Dir, "drafts", "*.json"))
	var out []*Draft
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		if d, err := s.Get(id); err == nil {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}
