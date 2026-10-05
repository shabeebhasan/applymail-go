// Package llm runs a prompt through a local AI CLI (Claude Code or OpenAI Codex), so the service
// works on an existing subscription with no API key. If one backend hits a usage limit the other
// answers. Images (screenshots of a post) are passed as files.
package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Model answers one prompt, optionally about one image file.
type Model interface {
	Ask(ctx context.Context, prompt, imagePath string) (string, error)
}

var limitRE = regexp.MustCompile(`(?i)usage ?limit|session limit|hit your limit|limit reached|rate.?limit|quota exceeded|usageLimitExceeded`)

// ErrLimit means the backend is out of usage for now.
var ErrLimit = errors.New("usage limit reached")

// CLI shells out to "claude" or "codex".
type CLI struct {
	Backend string // "claude" or "codex"
	Bin     string // path to the binary
	Model   string // optional model id
	Timeout time.Duration
}

func (c CLI) Ask(ctx context.Context, prompt, imagePath string) (string, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 4 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cmd *exec.Cmd
	switch c.Backend {
	case "claude":
		p := prompt
		args := []string{"-p"}
		if imagePath != "" {
			p += "\n\nThe post is in this image file; read it first: " + imagePath
			args = append(args, "--allowedTools", "Read")
		} else {
			args = append(args, "--allowedTools", "")
		}
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		}
		args = append(args, "--effort", "low")
		cmd = exec.CommandContext(ctx, c.Bin, args...)
		cmd.Stdin = strings.NewReader(p)
	case "codex":
		out, err := os.CreateTemp("", "applymail-codex-*.txt")
		if err != nil {
			return "", err
		}
		out.Close()
		defer os.Remove(out.Name())
		args := []string{"exec", "--skip-git-repo-check", "--ephemeral", "-s", "read-only",
			"-c", `approval_policy="never"`, "-c", `model_reasoning_effort="low"`, "-o", out.Name()}
		if imagePath != "" {
			args = append(args, "-i", imagePath)
		}
		if c.Model != "" {
			args = append(args, "-m", c.Model)
		}
		args = append(args, "-")
		cmd = exec.CommandContext(ctx, c.Bin, args...)
		cmd.Stdin = strings.NewReader(prompt)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err = cmd.Run()
		text, _ := os.ReadFile(out.Name())
		if err != nil {
			if limitRE.Match(stderr.Bytes()) {
				return "", ErrLimit
			}
			return "", fmt.Errorf("codex: %w: %.300s", err, stderr.String())
		}
		return strings.TrimSpace(string(text)), nil
	default:
		return "", fmt.Errorf("unknown backend %q", c.Backend)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		if limitRE.Match(stdout.Bytes()) || limitRE.Match(stderr.Bytes()) {
			return "", ErrLimit
		}
		return "", fmt.Errorf("claude: %w: %.300s", err, stderr.String()+stdout.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Fallback tries each model in order and moves on only when one reports a usage limit.
type Fallback []Model

func (f Fallback) Ask(ctx context.Context, prompt, imagePath string) (string, error) {
	var last error = errors.New("no model configured")
	for _, m := range f {
		out, err := m.Ask(ctx, prompt, imagePath)
		if err == nil {
			return out, nil
		}
		last = err
		if !errors.Is(err, ErrLimit) {
			return "", err
		}
	}
	return "", last
}

var jsonObj = regexp.MustCompile(`(?s)\{.*\}`)

// JSON returns the outermost {...} in a model reply.
func JSON(reply string) (string, error) {
	m := jsonObj.FindString(reply)
	if m == "" {
		return "", fmt.Errorf("model did not return JSON: %.200s", reply)
	}
	return m, nil
}
