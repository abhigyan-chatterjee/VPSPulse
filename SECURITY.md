# Security Policy

## Supported Versions

Only the latest release on `main` is actively supported with security updates.

## Reporting a Vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities.
Instead, report security issues privately via GitHub Security Advisories or by emailing `abhi@nullbit.in` with:
- A description of the issue and affected component (`hub`, `agent`, `install.sh`, or `web`).
- Steps to reproduce or a minimal proof of concept.

## Security Model Notes

- **Read-only agent:** `vpspulse-agent` never accepts remote commands or executes actions on monitored hosts.
- **Docker socket access:** By default, `/install.sh` adds the `vpspulse` system user to the `docker` group if Docker is installed so it can read container stats from `/var/run/docker.sock`. Because `docker` group membership is root-equivalent on Linux, pass `--no-docker` to `/install.sh` on hosts where container monitoring is not needed.
- **Channel secrets:** Webhook URLs, Telegram bot tokens, and SMTP passwords are stored in SQLite (`0600` permissions) and redacted from API responses (`__REDACTED__`).
