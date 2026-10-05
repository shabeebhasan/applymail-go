// Command applymail: paste a job post (text or screenshot), get a matched CV and a cover letter,
// review it, then send it from Gmail on request.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/shabeebhasan/applymail-go/internal/api"
	"github.com/shabeebhasan/applymail-go/internal/drafts"
	"github.com/shabeebhasan/applymail-go/internal/letter"
	"github.com/shabeebhasan/applymail-go/internal/llm"
	"github.com/shabeebhasan/applymail-go/internal/mailer"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func must(k string) string {
	v := os.Getenv(k)
	if v == "" {
		slog.Error("missing setting", "env", k)
		os.Exit(1)
	}
	return v
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	facts := ""
	for _, f := range []string{must("PROFILE_FILE"), os.Getenv("CATALOG_FILE")} {
		if f == "" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			slog.Error("read facts", "file", f, "err", err)
			os.Exit(1)
		}
		facts += string(b) + "\n\n"
	}
	claude := llm.CLI{Backend: "claude", Bin: env("CLAUDE_BIN", "claude"), Model: os.Getenv("CLAUDE_MODEL")}
	codex := llm.CLI{Backend: "codex", Bin: env("CODEX_BIN", "codex"), Model: os.Getenv("CODEX_MODEL")}
	model := llm.Fallback{claude, codex}
	if env("LLM_FIRST", "claude") == "codex" {
		model = llm.Fallback{codex, claude}
	}
	capN, _ := strconv.Atoi(env("DAILY_CAP", "15"))
	gmail := must("GMAIL_ADDRESS")
	srv := &api.Server{
		Model: model,
		Store: &drafts.Store{Dir: env("DATA_DIR", "./data"), DailyCap: capN},
		CVDir: must("CV_DIR"),
		Applicant: letter.Applicant{Name: must("APPLICANT_NAME"), Email: gmail,
			Website: os.Getenv("APPLICANT_WEBSITE"), Phone: os.Getenv("APPLICANT_PHONE"), Facts: facts},
		Sender: mailer.SMTP{Addr: env("SMTP_ADDR", "smtp.gmail.com:587"), User: gmail, Password: must("GMAIL_APP_PASSWORD")},
		Token:  must("API_TOKEN"),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// localhost only by default: it holds a mail password and sends email
	hs := &http.Server{Addr: env("LISTEN", "127.0.0.1:8095"), Handler: srv.Routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		slog.Info("listening", "addr", hs.Addr)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http", "err", err)
			stop()
		}
	}()
	<-ctx.Done()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hs.Shutdown(c)
}
