# Sidero Wings Implementation Progress

Last updated: 2026-08-01

## Repository findings

- Base is Pterodactyl Wings `v1.13.1` plus commit `28af6dd` (`update token validation`).
- Module is `github.com/pterodactyl/wings`; `go.mod` requires Go `1.24.0` with toolchain `1.24.1`.
- HTTP uses one Gin engine. Node routes use bearer authentication; server groups additionally use `ServerExists` and expose the existing `server.Server`.
- Server files use `server/filesystem` over descriptor-relative `internal/ufs`; unsafe traversal/symlink resolution is rejected beneath each server root.
- Server WebSockets consume the existing per-server event bus. Sidero publishes to that bus and does not replace its transport.
- Archive support is based on `mholt/archives`; Docker is the current resource/exit implementation; server crash logic is in `server/crash.go`.
- Existing CI builds Linux amd64/arm64 with CGO disabled and runs normal/race tests plus CodeQL.
- No pre-existing Sidero packages/routes were present, and the working tree was clean when implementation began.

## Architecture implemented

`config.Sidero` feeds a typed capability/health layer, bounded operation/upload/query/firewall managers, isolated feature packages under `internal/sidero`, one authenticated router registrar, and existing server WebSocket/activity adapters. Server file mutations stage below the reserved server-relative `.sidero` namespace and commit through secured filesystem calls. Firewall state is the only persistent Sidero desired state and is atomically stored below Wings' controlled system root.

The Panel still owns users, permission decisions, provider discovery/metadata, resolved marketplace logic, product history, billing, notifications, long-term metrics, migrations, active-world settings, UI, and restart orchestration.

## Phase checklist

- [x] Repository inspection and progress ledger
- [x] Validated Sidero configuration and safe defaults
- [x] Versioned capability and bounded health endpoints
- [x] Stable structured errors
- [x] Bounded operation manager, idempotency, cancellation, retention, and WebSocket progress
- [x] Filename search, content search, probe, and conditional writes
- [x] SSRF-resistant staged remote downloads
- [x] Archive inspection and extraction-time hardening with staged commit/rollback
- [x] Resumable uploads, checksums, conflict handling, progress, and expiry
- [x] Atomic regular-file replacement and multi-file transaction helpers
- [x] Generic resolved installer execution
- [x] Merge and explicit staged-clean modpack installation
- [x] Allocation-scoped Minecraft Java and Bedrock queries with cache/stale/backoff/coalescing
- [x] Java/Bedrock world inspect/import/archive/clone/rename/replace/size operations
- [x] Structured process-exit/crash/restart metadata
- [x] Live bytes/rates/packets/drops and bounded restart-aware sample buffers
- [x] Global/per-server per-route rate limiting and safe activity events
- [x] Bounded cleanup for records, sessions, caches, buffers, staging, and firewall rules
- [x] Disabled-by-default structured nftables firewall with dry-run, persistence, rollback, expiry, deletion cleanup, and startup reconciliation
- [x] Documentation, release-check/build targets, full tests, race tests, formatting, targeted vet, and cross-builds

## Configuration and protocol surface

Configuration sections are `operations`, `files`, `remote_download`, `archives`, `uploads`, `installers`, `game_query`, `worlds`, `network_statistics`, `rate_limits`, and `firewall`. Firewall defaults disabled; private download targets and archive links default denied. Full defaults/validation are in `docs/sidero/configuration.md`.

Node routes:

- `GET /api/sidero/v1/capabilities`
- `GET /api/sidero/v1/health`

Server routes:

- operation list/get/cancel
- filename/content search, remote download, archive inspect/extract, probe, conditional write
- upload create/get/chunk/complete/cancel
- installer execute and resolved modpack
- allocation-scoped query
- world inspect/import/archive/clone/rename/replace/size
- current/recent network statistics
- firewall list/dry-run/add/remove

WebSocket events are operation created/progress/completed/failed/cancelled, upload created/progress/completed/cancelled, process exit, and network stats. Events contain only safe summaries/counters/error codes.

Stable error codes are defined in `internal/sidero/errors/errors.go` and documented in `docs/sidero/errors.md`.

## Files created

- `config/sidero.go` and its tests
- `router/router_sidero.go` and its authentication tests
- `internal/sidero/{archives,capabilities,cleanup,download,errors,files,firewall,installers,network,operations,process,query,ratelimit,uploads,worlds}` with package tests
- all 24 requested `docs/sidero/*.md` documents

## Existing files modified / upstream patch surface

- `config/config.go`, `router/router.go`, and `cmd/root.go` add configuration, route registration, and daemon context hooks.
- `internal/ufs/fs_unix.go` and `server/filesystem/filesystem.go` add secured atomic regular-file replacement.
- `server/configuration.go` adds stable Sidero allocation records/locked lookup.
- `server/crash.go` and `server/server.go` expose safe crash/restart outcome metadata without replacing lifecycle behavior.
- `environment/stats.go` and `environment/docker/stats.go` add packet/drop counters.
- `Makefile` adds `sidero-release-check` and `sidero-build`.

Rebase details and conflict risk are recorded in `docs/sidero/core-patch-surface.md`.

## Verification actually run

- Targeted red/green tests were run throughout for configuration, operations, paths/search, SSRF/redirect/size/checksum download behavior, malicious archives, uploads, installer/modpack rollback, Java/Bedrock fixtures, worlds, process paths, network restart handling, rate limits, firewall rendering/persistence/rollback/policy, cleanup, router authentication, and affected upstream packages.
- `go test ./internal/sidero/... ./server ./router ./environment/... ./config -count=1`: passed.
- `go test ./... -count=1`: passed.
- `CGO_ENABLED=1 go test -race ./... -count=1`: passed with no race reports.
- `go vet ./internal/sidero/... ./config`: passed.
- `make sidero-release-check`: passed (targeted tests, targeted vet, CGO-disabled build).
- `make sidero-build`: passed; produced statically linked Linux amd64 and arm64 binaries in ignored `build/` output.
- `gofmt` was applied to every changed Go file; `git diff --check`: passed.
- `go vet ./...`: did not pass because of pre-existing upstream diagnostics: unreachable code in `server/filesystem/filesystem_test.go` and `router/router.go`; lock copies in `server/resources.go`, the existing configuration assignment in `server/server.go`, and `router/downloader/downloader.go`. No new Sidero package vet finding was reported.
- No separate repository static analyzer beyond vet and CI CodeQL is configured.

Go was absent from the initial environment. Go 1.24.11 was bootstrapped in temporary local storage; the system installation was not changed.

## Security protections

Authenticated server scope, stable allocation ownership, descriptor-relative path resolution, reserved staging namespace, no-follow symlink behavior, bounded traversal/I/O/memory/time, quota checks, atomic commit/rollback, checksum/size verification, SSRF rebinding/redirect defense, private/special IP denial, exact host policy, header stripping, operation/session/rate limits, safe event/error/log shapes, dedicated firewall ownership, nftables transaction/rollback, symlink-safe state storage, and bounded cleanup are implemented and tested where locally testable.

## Known limitations and controlled states

- Operation/search results and upload sessions are in memory and are not recovered after Wings restart. Abandoned UUID staging is cleaned after seven days; the Panel must reconcile product state.
- Docker's exit interface does not expose a portable signal, so `signal` is omitted. Exit code/OOM/crash/restart metadata is present.
- Firewall application requires Linux, `nft`, and host privileges. This environment verified generation, validation, dry-run/apply behavior through fake backends, persistence, rollback, expiry, and unsupported state, but did not mutate the host firewall. A real node must validate its nftables privilege through health/dry-run before enabling.
- Game protocols are tested against local fixtures, not a live customer server. Remote-download tests use local HTTP servers and require no public Internet.
- Conditional writes serialize/recheck Sidero requests and atomically replace the target. Uncoordinated non-Sidero/external writers do not participate in that endpoint lock; the immediate pre-commit checksum recheck detects normal concurrent changes but the host filesystem offers no universal compare-and-swap primitive.
- Supported archive formats follow the compiled `mholt/archives` extractor set. Nested archives are not recursively extracted.
- Long-term operation results, query/network history, notifications, and installation history intentionally remain Panel responsibilities.

## Exact next Panel integration step

Update the Panel node handshake to call `GET /api/sidero/v1/capabilities`, then include `sidero_allocations` (`id`, `ip`, `port`, optional `query_provider`) in each server configuration. Implement one operation client that submits authenticated server-scoped requests with stable idempotency keys, consumes Sidero WebSocket progress, and reconciles through operation GET; use that client first for filename search and remote download before enabling destructive/staged installer or world workflows.
