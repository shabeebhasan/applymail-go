#!/usr/bin/env bash
# Start applymail with the settings in .env (used by launchd and by hand).
cd "$(dirname "$0")"
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
set -a; . ./.env; set +a
[ -z "${GMAIL_APP_PASSWORD:-}" ] && echo "note: GMAIL_APP_PASSWORD is empty; drafts work, sending is off" >&2
exec ./bin/applymail
