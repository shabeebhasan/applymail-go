// Package mailer sends one email with attachments through Gmail SMTP (STARTTLS, app password).
package mailer

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime"
	"net/mail"
	"net/smtp"
	"path/filepath"
	"strings"
	"time"
)

type Attachment struct {
	Name string
	Type string
	Data []byte
}

type Message struct {
	FromName, From, To, ReplyTo, Subject, Body string
	Attachments                                []Attachment
}

// Build returns the RFC 5322 message: multipart/mixed with a text part and base64 attachments.
func Build(m Message, now time.Time) ([]byte, error) {
	if _, err := mail.ParseAddress(m.To); err != nil {
		return nil, fmt.Errorf("bad recipient %q: %w", m.To, err)
	}
	var rnd [12]byte
	rand.Read(rnd[:])
	boundary := fmt.Sprintf("applymail-%x", rnd)
	var b bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	h("From", (&mail.Address{Name: m.FromName, Address: m.From}).String())
	h("To", m.To)
	if m.ReplyTo != "" {
		h("Reply-To", m.ReplyTo)
	}
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", fmt.Sprintf("<%x@%s>", rnd, domain(m.From)))
	h("MIME-Version", "1.0")
	h("Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n", boundary)
	wrap(&b, base64.StdEncoding.EncodeToString([]byte(m.Body)))
	for _, a := range m.Attachments {
		name := mime.QEncoding.Encode("utf-8", filepath.Base(a.Name))
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; name=\"%s\"\r\nContent-Disposition: attachment; filename=\"%s\"\r\nContent-Transfer-Encoding: base64\r\n\r\n",
			boundary, a.Type, name, name)
		wrap(&b, base64.StdEncoding.EncodeToString(a.Data))
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes(), nil
}

func wrap(b *bytes.Buffer, s string) {
	for len(s) > 76 {
		b.WriteString(s[:76] + "\r\n")
		s = s[76:]
	}
	b.WriteString(s + "\r\n")
}

func domain(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return addr[i+1:]
	}
	return "localhost"
}

// SMTP sends through a server such as smtp.gmail.com:587.
type SMTP struct {
	Addr     string // host:port
	User     string
	Password string // Gmail app password
	// for tests: skip auth on a plain local server
	NoAuth bool
}

func (s SMTP) Send(from, to string, raw []byte) error {
	host := s.Addr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	var auth smtp.Auth
	if !s.NoAuth {
		auth = smtp.PlainAuth("", s.User, s.Password, host)
	}
	return smtp.SendMail(s.Addr, auth, from, []string{to}, raw)
}
