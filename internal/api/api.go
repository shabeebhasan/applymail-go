// Package api: create a draft from a post, review and edit it, then send it on an explicit request.
// Nothing is ever sent as a side effect of creating a draft.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shabeebhasan/applymail-go/internal/cvpick"
	"github.com/shabeebhasan/applymail-go/internal/drafts"
	"github.com/shabeebhasan/applymail-go/internal/extract"
	"github.com/shabeebhasan/applymail-go/internal/letter"
	"github.com/shabeebhasan/applymail-go/internal/llm"
	"github.com/shabeebhasan/applymail-go/internal/mailer"
)

type Sender interface {
	Send(from, to string, raw []byte) error
}

type Server struct {
	Model     llm.Model
	Store     *drafts.Store
	CVDir     string
	Applicant letter.Applicant
	Sender    Sender
	Token     string // required in X-Api-Token
	Now       func() time.Time
}

func (s *Server) Routes() http.Handler {
	if s.Now == nil {
		s.Now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /drafts", s.create)
	mux.HandleFunc("GET /drafts", s.list)
	mux.HandleFunc("GET /drafts/{id}", s.get)
	mux.HandleFunc("PATCH /drafts/{id}", s.edit)
	mux.HandleFunc("POST /drafts/{id}/revise", s.revise)
	mux.HandleFunc("GET /drafts/{id}/cover-letter.pdf", s.pdf)
	mux.HandleFunc("POST /drafts/{id}/send", s.send)
	mux.HandleFunc("POST /drafts/{id}/cancel", s.cancel)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	return s.auth(mux)
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Api-Token")), []byte(s.Token)) != 1 {
			fail(w, 401, errors.New("missing or wrong X-Api-Token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// create accepts JSON {"text": "..."} or multipart with "text" and/or an "image" file.
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	var text, imagePath, source string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		text, source = r.FormValue("text"), r.FormValue("source")
		if f, h, err := r.FormFile("image"); err == nil {
			defer f.Close()
			tmp, err := os.CreateTemp("", "applymail-post-*"+filepath.Ext(h.Filename))
			if err != nil {
				fail(w, 500, err)
				return
			}
			io.Copy(tmp, f)
			tmp.Close()
			defer os.Remove(tmp.Name())
			imagePath = tmp.Name()
		}
	} else {
		var in struct{ Text, Source string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(w, 400, errors.New(`body must be JSON {"text": "..."} or multipart with text/image`))
			return
		}
		text, source = in.Text, in.Source
	}
	if strings.TrimSpace(text) == "" && imagePath == "" {
		fail(w, 400, errors.New("send the post text or a screenshot"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Minute)
	defer cancel()
	job, err := extract.FromPost(ctx, s.Model, text, imagePath)
	if err != nil {
		fail(w, 502, fmt.Errorf("reading the post: %w", err))
		return
	}
	d, err := s.build(ctx, job, text, "")
	if err != nil {
		fail(w, 502, err)
		return
	}
	d.Source = source
	if err := s.Store.Save(d); err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 201, d)
}

// build picks the CV, writes the email and letter, renders the PDF. extra is an owner instruction.
func (s *Server) build(ctx context.Context, job extract.Job, text, extra string) (*drafts.Draft, error) {
	cvs, err := cvpick.Load(s.CVDir)
	if err != nil {
		return nil, fmt.Errorf("no CVs in %s", s.CVDir)
	}
	jobText := strings.Join(append([]string{job.Role, job.Summary, text}, append(job.Skills, job.Requirements...)...), " ")
	pick := cvpick.Best(cvs, job.Role, jobText)
	a := s.Applicant
	if extra != "" {
		a.Facts += "\n\nOWNER'S EXTRA INSTRUCTION FOR THIS DRAFT (follow it, still use only the facts above): " + extra
	}
	l, err := letter.Write(ctx, s.Model, a, job, text)
	if err != nil {
		return nil, fmt.Errorf("writing the letter: %w", err)
	}
	d := &drafts.Draft{ID: drafts.NewID(), State: "draft", Job: job, PostText: text, To: job.ApplyEmail,
		Subject: l.Subject, Body: l.Body, Letter: l.Letter, CVPath: pick.CV.Path, CreatedAt: s.Now()}
	if len(pick.Matched) > 0 {
		d.CVReason = "matches " + strings.Join(pick.Matched, ", ")
	} else {
		d.CVReason = "no focus match; general resume"
	}
	return d, s.render(d)
}

func (s *Server) render(d *drafts.Draft) error {
	pdf, err := letter.PDF(s.Applicant, d.Job, d.Letter, s.Now())
	if err != nil {
		return err
	}
	path := s.Store.AttachmentPath(d.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, pdf, 0o600)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	d, err := s.Store.Get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	ds, _ := s.Store.Recent(20)
	writeJSON(w, 200, ds)
}

// edit changes To, Subject, Body or Letter by hand.
func (s *Server) edit(w http.ResponseWriter, r *http.Request) {
	d, err := s.Store.Get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	if d.State != "draft" {
		fail(w, 409, drafts.ErrState)
		return
	}
	var in struct{ To, Subject, Body, Letter *string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, 400, err)
		return
	}
	if in.To != nil {
		j := extract.Check(extract.Job{ApplyEmail: *in.To, Role: d.Job.Role}, *in.To)
		if j.ApplyEmail == "" {
			fail(w, 400, fmt.Errorf("not a valid email: %q", *in.To))
			return
		}
		d.To = j.ApplyEmail
	}
	for _, f := range []struct {
		src *string
		dst *string
	}{{in.Subject, &d.Subject}, {in.Body, &d.Body}, {in.Letter, &d.Letter}} {
		if f.src != nil {
			*f.dst = strings.TrimSpace(*f.src)
		}
	}
	if in.Letter != nil {
		if err := s.render(d); err != nil {
			fail(w, 500, err)
			return
		}
	}
	s.Store.Save(d)
	writeJSON(w, 200, d)
}

// revise rewrites the email and letter with an instruction ("shorter", "mention the Go projects").
func (s *Server) revise(w http.ResponseWriter, r *http.Request) {
	old, err := s.Store.Get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	if old.State != "draft" {
		fail(w, 409, drafts.ErrState)
		return
	}
	var in struct{ Instruction string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Instruction) == "" {
		fail(w, 400, errors.New(`send {"instruction": "..."}`))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()
	d, err := s.build(ctx, old.Job, old.PostText, in.Instruction)
	if err != nil {
		fail(w, 502, err)
		return
	}
	d.ID, d.To, d.Source, d.CreatedAt = old.ID, old.To, old.Source, old.CreatedAt
	if err := s.render(d); err != nil {
		fail(w, 500, err)
		return
	}
	s.Store.Save(d)
	writeJSON(w, 200, d)
}

func (s *Server) pdf(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Store.Get(r.PathValue("id")); err != nil {
		fail(w, 404, err)
		return
	}
	b, err := os.ReadFile(s.Store.AttachmentPath(r.PathValue("id")))
	if err != nil {
		fail(w, 404, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Write(b)
}

// send needs {"confirm": "<draft id>"} so a stray POST cannot send anything.
func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Confirm string
		Force   bool
	}
	json.NewDecoder(r.Body).Decode(&in)
	if in.Confirm != id {
		fail(w, 400, errors.New(`confirm by sending {"confirm": "<draft id>"}`))
		return
	}
	d, err := s.Store.Send(id, in.Force, s.Now(), func(d *drafts.Draft) error {
		if d.To == "" {
			return errors.New("no recipient: set one with PATCH {\"to\": ...}")
		}
		cv, err := os.ReadFile(d.CVPath)
		if err != nil {
			return fmt.Errorf("CV file: %w", err)
		}
		letterPDF, err := os.ReadFile(s.Store.AttachmentPath(d.ID))
		if err != nil {
			return fmt.Errorf("cover letter PDF: %w", err)
		}
		slug := strings.ReplaceAll(s.Applicant.Name, " ", "_")
		raw, err := mailer.Build(mailer.Message{FromName: s.Applicant.Name, From: s.Applicant.Email, To: d.To,
			Subject: d.Subject, Body: d.Body, Attachments: []mailer.Attachment{
				{Name: slug + "_CV.pdf", Type: "application/pdf", Data: cv},
				{Name: slug + "_Cover_Letter.pdf", Type: "application/pdf", Data: letterPDF},
			}}, s.Now())
		if err != nil {
			return err
		}
		return s.Sender.Send(s.Applicant.Email, d.To, raw)
	})
	switch {
	case errors.Is(err, drafts.ErrNotFound):
		fail(w, 404, err)
	case errors.Is(err, drafts.ErrDuplicate), errors.Is(err, drafts.ErrState):
		fail(w, 409, err)
	case errors.Is(err, drafts.ErrDailyCap):
		fail(w, 429, err)
	case err != nil:
		fail(w, 502, err)
	default:
		writeJSON(w, 200, d)
	}
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	d, err := s.Store.Get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	if d.State == "draft" {
		d.State = "cancelled"
		s.Store.Save(d)
	}
	writeJSON(w, 200, d)
}
