package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shabeebhasan/applymail-go/internal/drafts"
	"github.com/shabeebhasan/applymail-go/internal/letter"
	"github.com/shabeebhasan/applymail-go/internal/mailer"
)

// fakeModel answers the extract prompt and the letter prompt with fixed JSON.
type fakeModel struct{ email string }

func (f fakeModel) Ask(_ context.Context, prompt, _ string) (string, error) {
	if strings.Contains(prompt, "Read this job post") {
		return `{"role":"Senior AI Engineer","company":"Acme","apply_email":"` + f.email + `","contact_name":"Sara",
			"skills":["RAG","LangGraph","Python"],"requirements":["agents in production"],"summary":"Build RAG agents."}`, nil
	}
	return `Here you go: {"subject":"Application: Senior AI Engineer — Shabeeb Hasan","email_body":"Hi Sara,\nI am applying.",
		"cover_letter":"Dear Sara,\nI build agents.\n\nSincerely,\nShabeeb Hasan"}`, nil
}

// smtpServer is a tiny SMTP server that records what it receives.
type smtpServer struct {
	addr string
	mu   sync.Mutex
	msgs []string
}

func startSMTP(t *testing.T) *smtpServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpServer{addr: ln.Addr().String()}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	return s
}

func (s *smtpServer) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(l string) { io.WriteString(c, l+"\r\n") }
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			w("250 fake")
		case strings.HasPrefix(cmd, "DATA"):
			w("354 go")
			var b strings.Builder
			for {
				l, _ := r.ReadString('\n')
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.mu.Lock()
			s.msgs = append(s.msgs, b.String())
			s.mu.Unlock()
			w("250 queued")
		case strings.HasPrefix(cmd, "QUIT"):
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func setup(t *testing.T, email string) (*httptest.Server, *smtpServer) {
	dir := t.TempDir()
	cvDir := filepath.Join(dir, "cv")
	os.MkdirAll(cvDir, 0o755)
	for _, n := range []string{"Me_Resume.pdf", "Me_Resume_agentic-rag.pdf", "Me_Resume_mobile-saas.pdf"} {
		os.WriteFile(filepath.Join(cvDir, n), []byte("%PDF-1.4 "+n), 0o644)
	}
	smtpSrv := startSMTP(t)
	s := &Server{Model: fakeModel{email}, Store: &drafts.Store{Dir: dir, DailyCap: 2}, CVDir: cvDir, Token: "tok",
		Applicant: letter.Applicant{Name: "Shabeeb Hasan", Email: "me@example.com", Facts: "builds agents"},
		Sender:    mailer.SMTP{Addr: smtpSrv.addr, NoAuth: true},
		Now:       func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }}
	return httptest.NewServer(s.Routes()), smtpSrv
}

func call(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]any) {
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	req.Header.Set("X-Api-Token", "tok")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

const post = `We're hiring a Senior AI Engineer (RAG, LangGraph, Python). Send your CV to jobs@acme.io`

func TestDraftThenSendWithAttachments(t *testing.T) {
	srv, smtp := setup(t, "jobs@acme.io")
	code, d := call(t, srv, "POST", "/drafts", `{"text":"`+post+`"}`)
	if code != 201 {
		t.Fatalf("create %d %v", code, d)
	}
	id := d["id"].(string)
	if d["to"] != "jobs@acme.io" || !strings.Contains(d["cv_path"].(string), "agentic-rag") {
		t.Fatalf("draft %v", d)
	}
	if strings.Contains(d["subject"].(string), "—") {
		t.Fatal("em dash left in subject")
	}
	if len(smtp.msgs) != 0 {
		t.Fatal("creating a draft sent an email")
	}
	if code, _ := call(t, srv, "POST", "/drafts/"+id+"/send", `{}`); code != 400 {
		t.Fatalf("send without confirm: %d", code)
	}
	code, sent := call(t, srv, "POST", "/drafts/"+id+"/send", `{"confirm":"`+id+`"}`)
	if code != 200 || sent["state"] != "sent" {
		t.Fatalf("send %d %v", code, sent)
	}
	if len(smtp.msgs) != 1 {
		t.Fatalf("%d messages", len(smtp.msgs))
	}
	m := smtp.msgs[0]
	for _, want := range []string{"To: jobs@acme.io", "Shabeeb_Hasan_CV.pdf", "Shabeeb_Hasan_Cover_Letter.pdf", "multipart/mixed"} {
		if !strings.Contains(m, want) {
			t.Errorf("message lacks %q", want)
		}
	}
	// a second click does nothing
	if code, _ := call(t, srv, "POST", "/drafts/"+id+"/send", `{"confirm":"`+id+`"}`); code != 409 || len(smtp.msgs) != 1 {
		t.Fatalf("double send: %d, %d messages", code, len(smtp.msgs))
	}
}

func TestDuplicateAndDailyCap(t *testing.T) {
	srv, smtp := setup(t, "jobs@acme.io")
	send := func() int {
		_, d := call(t, srv, "POST", "/drafts", `{"text":"`+post+`"}`)
		id := d["id"].(string)
		code, _ := call(t, srv, "POST", "/drafts/"+id+"/send", `{"confirm":"`+id+`"}`)
		return code
	}
	if send() != 200 {
		t.Fatal("first send failed")
	}
	if c := send(); c != 409 {
		t.Fatalf("same address and role twice: %d", c)
	}
	_, d := call(t, srv, "POST", "/drafts", `{"text":"`+post+`"}`)
	id := d["id"].(string)
	call(t, srv, "PATCH", "/drafts/"+id, `{"to":"other@acme.io"}`)
	if c, _ := call(t, srv, "POST", "/drafts/"+id+"/send", `{"confirm":"`+id+`"}`); c != 200 {
		t.Fatalf("different address: %d", c)
	}
	_, d = call(t, srv, "POST", "/drafts", `{"text":"`+post+`"}`)
	id = d["id"].(string)
	call(t, srv, "PATCH", "/drafts/"+id, `{"to":"third@acme.io"}`)
	if c, _ := call(t, srv, "POST", "/drafts/"+id+"/send", `{"confirm":"`+id+`"}`); c != 429 {
		t.Fatalf("daily cap of 2: %d", c)
	}
	if len(smtp.msgs) != 2 {
		t.Fatalf("%d messages", len(smtp.msgs))
	}
}

func TestNoEmailInPostCannotSend(t *testing.T) {
	srv, smtp := setup(t, "")
	_, d := call(t, srv, "POST", "/drafts", `{"text":"Hiring an AI engineer, DM me"}`)
	id := d["id"].(string)
	warn, _ := json.Marshal(d["job"])
	if !bytes.Contains(warn, []byte("no email address")) {
		t.Fatalf("no warning: %s", warn)
	}
	if c, _ := call(t, srv, "POST", "/drafts/"+id+"/send", `{"confirm":"`+id+`"}`); c != 502 || len(smtp.msgs) != 0 {
		t.Fatalf("sent without a recipient: %d", c)
	}
}

func TestInventedEmailIsFlagged(t *testing.T) {
	srv, _ := setup(t, "hr@made-up.com")
	_, d := call(t, srv, "POST", "/drafts", `{"text":"`+post+`"}`)
	j := d["job"].(map[string]any)
	if j["email_verified"] == true {
		t.Fatal("an address not in the post was marked verified")
	}
}

func TestAuthAndPDF(t *testing.T) {
	srv, _ := setup(t, "jobs@acme.io")
	resp, _ := http.Post(srv.URL+"/drafts", "application/json", strings.NewReader(`{"text":"x"}`))
	if resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	_, d := call(t, srv, "POST", "/drafts", `{"text":"`+post+`"}`)
	req, _ := http.NewRequest("GET", srv.URL+"/drafts/"+d["id"].(string)+"/cover-letter.pdf", nil)
	req.Header.Set("X-Api-Token", "tok")
	r, _ := http.DefaultClient.Do(req)
	b, _ := io.ReadAll(r.Body)
	if !bytes.HasPrefix(b, []byte("%PDF")) {
		t.Fatalf("not a PDF: %.20q", b)
	}
	if c, _ := call(t, srv, "GET", "/drafts/../../etc/passwd", ""); c == 200 {
		t.Fatal("path traversal")
	}
}
