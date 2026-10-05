#!/usr/bin/env bash
# Start applymail with the settings in .env (used by launchd and by hand).
cd "$(dirname "$0")"
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
set -a; . ./.env; set +a
if [ -z "${GMAIL_APP_PASSWORD:-}" ]; then echo "GMAIL_APP_PASSWORD is empty in .env" >&2; exit 1; fi
exec ./bin/applymail
