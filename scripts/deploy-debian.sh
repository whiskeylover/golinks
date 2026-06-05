#!/bin/sh
set -eu

SERVICE_USER="golinks"
SERVICE_GROUP="golinks"
SERVICE_NAME="golinks"
INSTALL_PATH="/usr/local/bin/golinks"
DATA_DIR="/var/lib/golinks"
BACKUP_DIR="/var/backups/golinks"
UNIT_PATH="/etc/systemd/system/golinks.service"
BACKUP_UNIT_PATH="/etc/systemd/system/golinks-backup.service"
BACKUP_TIMER_PATH="/etc/systemd/system/golinks-backup.timer"
MIN_GO_VERSION="1.24.0"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

if [ "$(id -u)" -ne 0 ]; then
	echo "Run this script as root: sudo ./scripts/deploy-debian.sh" >&2
	exit 1
fi

if ! command -v systemctl >/dev/null 2>&1; then
	echo "systemd is required but systemctl was not found" >&2
	exit 1
fi

go_is_compatible() {
	if ! command -v go >/dev/null 2>&1; then
		return 1
	fi

	GO_VERSION=$(go env GOVERSION | sed 's/^go//')
	FIRST_VERSION=$(printf '%s\n%s\n' "$MIN_GO_VERSION" "$GO_VERSION" | sort -V | head -n 1)
	[ "$FIRST_VERSION" = "$MIN_GO_VERSION" ]
}

install_go() {
	case "$(uname -m)" in
		aarch64|arm64)
			GO_ARCH="arm64"
			;;
		armv6l|armv7l)
			GO_ARCH="armv6l"
			;;
		x86_64)
			GO_ARCH="amd64"
			;;
		*)
			echo "Unsupported architecture: $(uname -m)" >&2
			exit 1
			;;
	esac

	echo "Installing a current Go toolchain..."
	apt-get update
	DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl

	GO_RELEASE=$(curl --fail --silent --show-error --location "https://go.dev/VERSION?m=text" | sed -n '1p')
	GO_ARCHIVE="$GO_RELEASE.linux-$GO_ARCH.tar.gz"

	cd "$BUILD_DIR"
	curl --fail --silent --show-error --location --remote-name "https://go.dev/dl/$GO_ARCHIVE"
	curl --fail --silent --show-error --location "https://go.dev/dl/$GO_ARCHIVE.sha256" > "$GO_ARCHIVE.sha256"
	printf '%s  %s\n' "$(cat "$GO_ARCHIVE.sha256")" "$GO_ARCHIVE" | sha256sum --check -

	rm -rf /usr/local/go
	tar -C /usr/local -xzf "$GO_ARCHIVE"
	ln -sf /usr/local/go/bin/go /usr/local/bin/go
}

BUILD_DIR=$(mktemp -d)
trap 'rm -rf "$BUILD_DIR"' EXIT INT TERM

if ! go_is_compatible; then
	install_go
fi

if ! getent group "$SERVICE_GROUP" >/dev/null 2>&1; then
	groupadd --system "$SERVICE_GROUP"
fi

if ! id "$SERVICE_USER" >/dev/null 2>&1; then
	useradd \
		--system \
		--gid "$SERVICE_GROUP" \
		--home-dir "$DATA_DIR" \
		--shell /usr/sbin/nologin \
		"$SERVICE_USER"
fi

install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$DATA_DIR"
install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$BACKUP_DIR"

echo "Building golinks..."
cd "$REPO_DIR"
go build -trimpath -ldflags="-s -w" -o "$BUILD_DIR/golinks" ./cmd/golinks

echo "Installing golinks..."
install -m 0755 "$BUILD_DIR/golinks" "$INSTALL_PATH.new"
mv "$INSTALL_PATH.new" "$INSTALL_PATH"
install -m 0644 "$REPO_DIR/deploy/golinks.service" "$UNIT_PATH"
install -m 0644 "$REPO_DIR/deploy/golinks-backup.service" "$BACKUP_UNIT_PATH"
install -m 0644 "$REPO_DIR/deploy/golinks-backup.timer" "$BACKUP_TIMER_PATH"

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl enable --now golinks-backup.timer
systemctl restart "$SERVICE_NAME"

echo
echo "golinks is installed and running on port 80."
echo "Check it with: systemctl status golinks"
echo "Check backups with: systemctl list-timers golinks-backup.timer"
echo "Open it with:  http://<server-address>/"
echo
echo "To use http://go, configure your DNS server to resolve 'go' to this host."
