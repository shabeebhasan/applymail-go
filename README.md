# applymail-go

Paste a job post from LinkedIn, as text or a screenshot. applymail reads it, picks the best of your
CVs, writes the email and a cover letter from your real profile, and shows you a preview. When you
press Send it goes out from your Gmail with the CV and the cover letter PDF attached.

Nothing is ever emailed as a side effect: sending is a separate request that must name the draft.

## Flow

```
Discord message / n8n form / API
        |
   POST /drafts  ->  read the post (AI)  ->  check the email in code  ->  pick the CV (code)
        |                                   ->  write email + cover letter (AI, profile facts only)
        |                                   ->  render cover letter PDF
   preview with Send / Cancel
        |
   POST /drafts/{id}/send {"confirm": id}  ->  duplicate check, daily cap  ->  Gmail SMTP
```

## What the code checks (not the model)

- **The email address.** It must be a valid address, and for pasted text it must appear in the post
  word for word; otherwise the draft carries a warning. An address read from a screenshot is
  flagged so you check the spelling. A post with no email cannot be sent.
- **The CV.** Each PDF's focus comes from its name (`..._Resume_agentic-rag.pdf`); words in the job
  map onto those focus tags, and a focus named in the role title counts double. The reason is
  shown ("matches mobile, payments").
- **Duplicates and volume.** The same role to the same address is refused; at most `DAILY_CAP`
  emails go out per 24 hours. Check, send and record happen under one lock, so a double click
  sends once.
- **Applicant instructions.** If the post says "subject: AI-ENG-24 | Your Name", the subject is that.
- **Writing rules.** Claims only from the profile and project catalog you give it; gaps are said
  plainly; em dashes are stripped.

## Tested

Unit tests with a fake model and a fake SMTP server: a draft never sends mail; send needs the
confirm field; both PDFs are attached; a second click is refused; the same role and address is
refused; the daily cap returns 429; a post without an email cannot be sent; an address the model
made up is not marked verified; the API needs its token; draft ids cannot be used for path
traversal. Email extraction and CV choice have their own tests. All pass under `go test -race`.

Also run for real (Claude, 24 real CVs, real profile) on three sample posts, a text post with a
subject code, a screenshot, and a Go backend post: right email, right CV, subject format followed,
honest about missing experience. About 30 seconds per draft. Sending through Gmail was checked up to
authentication with a dummy password (Gmail refused it, nothing was sent).

Not tested: the n8n workflow on a running n8n instance.

## Setup

1. Gmail: turn on 2-Step Verification, then create an App Password (Google Account, Security,
   App passwords). Put it in `.env` as `GMAIL_APP_PASSWORD` yourself.
2. `cp .env.example .env` and fill it in.
3. `./install-launchd.sh` (macOS, runs in the background) or `./run.sh`.

The AI runs through the Claude Code or Codex CLI already logged in on the machine, so there is no
API key. If one hits its usage limit the other answers.

## API (all need `X-Api-Token`)

| Method | Path | |
|---|---|---|
| POST | `/drafts` | JSON `{"text"}` or multipart `text` + `image` |
| GET | `/drafts`, `/drafts/{id}` | |
| PATCH | `/drafts/{id}` | `{"to","subject","body","letter"}` |
| POST | `/drafts/{id}/revise` | `{"instruction": "shorter"}` |
| GET | `/drafts/{id}/cover-letter.pdf` | |
| POST | `/drafts/{id}/send` | `{"confirm": "<id>"}` (and `"force": true` to send a duplicate on purpose) |
| POST | `/drafts/{id}/cancel` | |

## n8n

`n8n/applymail-form.json` is a workflow with an n8n form: paste a post, it calls `POST /drafts` and
shows the summary. Set `APPLYMAIL_URL` and `APPLYMAIL_TOKEN` in n8n's environment. Sending stays a
separate, deliberate step.

## Discord

The companion bot code (`apply_flow.py` in my growth-stack repo) opens a thread per post with the
preview, the cover letter PDF and Send / Cancel buttons; `to: x@y.com` in the thread changes the
recipient, any other message rewrites the draft.

## Layout

```
cmd/applymail     server, config from env
internal/extract  read the post with the model, then verify the email in code
internal/cvpick   choose the CV from file-name focus tags
internal/letter   write email + cover letter, render the PDF
internal/mailer   MIME message with attachments, Gmail SMTP
internal/drafts   JSON draft store, send log, duplicate and daily-cap checks
internal/llm      Claude Code / Codex CLI with fallback on usage limits
internal/api      HTTP API
n8n/              n8n workflow
```
