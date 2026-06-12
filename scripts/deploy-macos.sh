#!/bin/sh
set -eu

LABEL="local.golinks.server"
APP_DIR="${GOLINKS_APP_DIR:-$HOME/Library/Application Support/golinks}"
BIN_DIR="$APP_DIR/bin"
INSTALL_PATH="$BIN_DIR/golinks"
PLIST_PATH="$HOME/Library/LaunchAgents/$LABEL.plist"
LOG_DIR="$APP_DIR/logs"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
BUILD_DIR=$(mktemp -d)
trap 'rm -rf "$BUILD_DIR"' EXIT INT TERM

if [ "$(uname -s)" != "Darwin" ]; then
	echo "This installer only supports macOS" >&2
	exit 1
fi

if ! command -v go >/dev/null 2>&1; then
	echo "Go 1.24 or newer is required. Install it from https://go.dev/dl/ or Homebrew." >&2
	exit 1
fi

GO_VERSION=$(go env GOVERSION | sed 's/^go//')
GO_MAJOR=$(printf '%s\n' "$GO_VERSION" | awk -F. '{print $1}')
GO_MINOR=$(printf '%s\n' "$GO_VERSION" | awk -F. '{print $2}')
if [ "$GO_MAJOR" -lt 1 ] || { [ "$GO_MAJOR" -eq 1 ] && [ "$GO_MINOR" -lt 24 ]; }; then
	echo "Go 1.24 or newer is required; found go$GO_VERSION" >&2
	exit 1
fi

mkdir -p "$BIN_DIR" "$LOG_DIR" "$HOME/Library/LaunchAgents"

echo "Building golinks..."
cd "$REPO_DIR"
go build -trimpath -ldflags="-s -w" -o "$BUILD_DIR/golinks" ./cmd/golinks

echo "Installing golinks..."
install -m 0755 "$BUILD_DIR/golinks" "$INSTALL_PATH.new"
mv "$INSTALL_PATH.new" "$INSTALL_PATH"

sed \
	-e "s|__GOLINKS_BIN__|$INSTALL_PATH|g" \
	-e "s|__GOLINKS_DB__|$APP_DIR/golinks.db|g" \
	-e "s|__GOLINKS_LOG_DIR__|$LOG_DIR|g" \
	"$REPO_DIR/deploy/local.golinks.server.plist" > "$PLIST_PATH"

launchctl bootout "gui/$(id -u)" "$PLIST_PATH" >/dev/null 2>&1 || true
launchctl bootstrap "gui/$(id -u)" "$PLIST_PATH"
launchctl enable "gui/$(id -u)/$LABEL"
launchctl kickstart -k "gui/$(id -u)/$LABEL"

echo
echo "golinks is installed and running on port 8080."
echo "Open it with: http://localhost:8080/"
echo "Inspect logs in: $LOG_DIR"
