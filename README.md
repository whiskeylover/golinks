# go/links

A small self-hosted URL shortener for memorable internal links.

## Run locally

```bash
./scripts/run-dev.sh
```

The server listens on `:8080` and stores links in `./data/golinks.db` by
default. Open `http://localhost:8080`, create a shortcut, then visit a path
such as `http://localhost:8080/docs/onboarding`.

Configuration is available through flags or environment variables:

```bash
go run ./cmd/golinks serve -addr :8080 -db ./data/golinks.db
GOLINKS_ADDR=:8080 GOLINKS_DB=./data/golinks.db go run ./cmd/golinks serve
```

## Development script

`scripts/run-dev.sh` is a convenience wrapper for running the app from a
repository checkout on Linux or macOS:

```bash
GOLINKS_ADDR=:8081 GOLINKS_DB=./data/test.db ./scripts/run-dev.sh
```

## Use `http://go`

The short hostname needs local network configuration outside the application:

1. Resolve `go` to the server through local DNS or a hosts-file entry.
2. Expose the service on port `80`, either directly with `-addr :80` or
   through a reverse proxy.

## Deploy on Debian Linux

The Debian installer supports ARM64, ARMv6/ARMv7, and x64 systems using
`systemd`, including Raspberry Pi OS, Debian, and Ubuntu. It starts the app on
port `80`, enables it at boot, creates an unprivileged `golinks` user, and
stores the production database at `/var/lib/golinks/golinks.db`.

Copy or clone this repository onto the server and run:

```bash
sudo ./scripts/deploy-debian.sh
```

The command is safe to run again when deploying an updated version. If Go
`1.24` or newer is not already installed, the installer downloads a current
Go release from `go.dev`, verifies its checksum, and installs it before
building the binary.

Manage the installed `golinks.service` with the usual `systemctl` commands:

```bash
sudo systemctl start golinks
sudo systemctl stop golinks
sudo systemctl restart golinks
systemctl status golinks
journalctl -u golinks -f
curl http://localhost/healthz
```

The service runs as the unprivileged `golinks` user. Its `systemd` unit grants
only `CAP_NET_BIND_SERVICE`, which lets it bind to port `80` without running
the application as root.

## Deploy on macOS

The macOS installer builds the app, stores it under
`~/Library/Application Support/golinks`, and registers a user `launchd`
service that starts when you log in:

```bash
./scripts/deploy-macos.sh
```

It listens on `:8080` because an unprivileged macOS user service cannot bind
directly to port `80`. Use a local reverse proxy if the Mac needs to serve
`http://go` without a port number.

Inspect the service with:

```bash
launchctl print gui/$(id -u)/com.golinks.server
```

Rerun `./scripts/deploy-macos.sh` to rebuild and restart the macOS service.

## Back up the database

Create a consistent SQLite backup while the service is running:

```bash
go run ./cmd/golinks backup \
  -db ./data/golinks.db \
  -output ./backups/golinks-$(date +%F).db
```

Run that command from cron or a systemd timer and copy the resulting files to
another machine, NAS, or cloud bucket. A simple retention policy is 7 daily
and 4 weekly backups.

To restore a local development backup, stop the running development server
and replace its configured database file:

```bash
cp ./backups/golinks-2026-05-30.db ./data/golinks.db
```

Periodically verify a backup by opening it as a temporary database:

```bash
go run ./cmd/golinks serve -addr :8081 -db ./backups/golinks-2026-05-30.db
```

### Debian Linux production backup

The Debian installer enables a daily `systemd` timer:

```bash
systemctl list-timers golinks-backup.timer
systemctl status golinks-backup.timer
journalctl -u golinks-backup.service
```

The timer runs once per day around `03:15`, with a small randomized delay. If
the host is powered off at the scheduled time, `Persistent=true` makes systemd
run the missed backup after the host comes back online.

Backups are written to `/var/backups/golinks` as timestamped SQLite files.
After each successful scheduled backup, files older than 30 days are deleted
from that local backup directory. Trigger a backup immediately with:

```bash
sudo systemctl start golinks-backup.service
```

You can also create a consistent online backup manually while the service
remains running:

```bash
sudo /usr/local/bin/golinks backup \
  -db /var/lib/golinks/golinks.db \
  -output /var/backups/golinks/golinks-$(date +%F-%H%M%S).db
```

Copy the resulting files to another machine, NAS, or cloud bucket
periodically.

### Copy production backups to another host

For a NAS, homelab server, or other backup host, create the destination once.
If `/srv` requires elevated permissions, run:

```bash
ssh -t backup-host 'sudo mkdir -p /srv/backups/golinks && sudo chown $USER:$USER /srv/backups/golinks'
```

The local backup directory is protected, so the sync usually runs from root's
crontab on the golinks host. Make sure root can SSH to the backup host without
prompting:

```bash
sudo ssh backup-host 'true'
```

Then add a cron job on the golinks host:

```cron
30 4 * * * rsync -a --ignore-existing /var/backups/golinks/ backup-host:/srv/backups/golinks/
```

Add it to root's crontab without replacing existing cron entries:

```bash
(sudo crontab -l 2>/dev/null; echo '30 4 * * * rsync -a --ignore-existing /var/backups/golinks/ backup-host:/srv/backups/golinks/') | sudo crontab -
```

This copies any new local backup files to `backup-host` every day at `04:30`.
`--ignore-existing` avoids rewriting backups that were already copied.

If you prefer running cron as your login user, grant that user read access to
`/var/backups/golinks` and the backup files first.

### Debian Linux production restore

Restoring replaces the live database, so stop the service first:

```bash
sudo systemctl stop golinks
sudo install -m 0600 -o golinks -g golinks \
  /var/backups/golinks/golinks-2026-05-31-120000.db \
  /var/lib/golinks/golinks.db
sudo systemctl start golinks
systemctl status golinks
curl http://localhost/healthz
curl --head http://localhost/your-shortcut
```

For the macOS user service:

```bash
"$HOME/Library/Application Support/golinks/bin/golinks" backup \
  -db "$HOME/Library/Application Support/golinks/golinks.db" \
  -output /path/to/backups/golinks-$(date +%F).db
```

## Delete a shortcut from the CLI

The web UI intentionally does not advertise shortcut deletion. For
maintenance tasks, including removing a malformed legacy shortcut, delete an
exact stored key with:

```bash
go run ./cmd/golinks list -db ./data/golinks.db

go run ./cmd/golinks delete \
  -db ./data/golinks.db \
  -shortcut 'legacy shortcut'
```

For the Debian Linux service, use `/usr/local/bin/golinks` and the production
database path:

```bash
sudo /usr/local/bin/golinks delete \
  -db /var/lib/golinks/golinks.db \
  -shortcut 'legacy shortcut'
```

## Roadmap

Keep future additions lightweight and focused on reducing friction or
improving reliability.

### Useful additions

- JSON or CSV import and export for migrations and disaster recovery.

### Shared-service features

- Optional descriptions or tags.
- An audit history for edits and deletions.
- Basic create, edit, and delete protection through a shared token or reverse
  proxy authentication.
