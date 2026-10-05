#!/usr/bin/env bash
# Run applymail in the background on login (macOS). Logs: data/applymail.log
set -euo pipefail
SRC="$(cd "$(dirname "$0")" && pwd)"
# macOS blocks background services from ~/Desktop, ~/Documents and ~/Downloads, so run from ~/.applymail
DIR="${APPLYMAIL_HOME:-$HOME/.applymail}"
mkdir -p "$DIR/bin" "$DIR/data"
(cd "$SRC" && go build -o "$DIR/bin/applymail" ./cmd/applymail)
cp "$SRC/run.sh" "$DIR/run.sh"
[ -f "$DIR/.env" ] || cp "$SRC/.env" "$DIR/.env"
chmod 600 "$DIR/.env"
sed -i '' "s#^DATA_DIR=.*#DATA_DIR=$DIR/data#" "$DIR/.env"
PLIST="$HOME/Library/LaunchAgents/com.shabeeb.applymail.plist"
cat > "$PLIST" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.shabeeb.applymail</string>
  <key>ProgramArguments</key><array><string>$DIR/run.sh</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$DIR/data/applymail.log</string>
  <key>StandardErrorPath</key><string>$DIR/data/applymail.log</string>
</dict></plist>
PL
launchctl enable "gui/$(id -u)/com.shabeeb.applymail" 2>/dev/null || true
launchctl bootout "gui/$(id -u)/com.shabeeb.applymail" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"
echo "applymail running on 127.0.0.1:8095 from $DIR (settings: $DIR/.env, logs: $DIR/data/applymail.log)"
