# Contributing to VPSPulse

Contributions, bug reports, and feature ideas are welcome!

## Prerequisites

- **Go:** 1.26+
- **Node.js:** 22+ and **pnpm** 10+

## Local Development

1. Run Go tests and vet:
   ```bash
   make vet
   make test
   ```
2. Run the frontend dev server (proxies `/api`, `/install.sh`, and `/downloads` to `:8080`):
   ```bash
   cd web && pnpm install && pnpm dev
   ```
3. In another terminal, run the hub in `--demo` mode:
   ```bash
   go run ./cmd/hub --demo --listen :8080
   ```
4. Build the full production bundle (React SPA + cross-compiled `amd64`/`arm64` agents + hub):
   ```bash
   make build-all
   ```

## Pull Request Guidelines

- Keep PRs focused and include unit tests for bug fixes or new behavior.
- Ensure `make vet`, `make test`, and `make build-all` pass before submitting.
- Avoid adding unnecessary dependencies; prefer the Go standard library where practical.
