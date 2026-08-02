# Core Patch Surface

| File | Reason / extension point | Conflict risk | Reapply / verification |
|---|---|---|---|
| `config/config.go` | Adds typed `Sidero` block and controlled validation before global set. | Low | Re-add field/validation; `go test ./config`. |
| `router/router.go` | Adds context-aware configurator and one authenticated Sidero registrar. | Medium | Preserve route/middleware order; `go test ./router`. |
| `cmd/root.go` | Passes daemon context to Sidero workers. | Low | Keep `ConfigureWithContext(cmd.Context(), ...)`. |
| `internal/ufs/fs_unix.go` | Adds descriptor-relative atomic regular-file replacement. | Medium | Preserve SafePath on both operands/type checks/`renameat`; ufs/filesystem tests. |
| `server/filesystem/filesystem.go` | Exposes quota-aware atomic replacement wrapper. | Low | Subtract replaced size only after success; filesystem/Sidero file tests. |
| `server/configuration.go` | Adds optional stable Sidero allocation records and locked lookup/default helpers. | Low | Preserve JSON compatibility and ownership lookup; server/router tests. |
| `server/crash.go` | Returns crash/restart outcome metadata while retaining existing decision/restart flow. | Medium | Compare upstream crash logic line-by-line; server tests. |
| `server/server.go` | Publishes bounded `sidero process exit` after the existing offline transition/crash handler. | Medium | Preserve status/stats/crash ordering; server/process tests. |
| `environment/stats.go` | Extends safe network counters with packets/drops. | Low | Retain existing JSON fields; environment/network tests. |
| `environment/docker/stats.go` | Aggregates Docker interface packet/drop counters with existing byte counters. | Low | Check Docker API field names; environment build/network tests. |

No SFTP handler, existing WebSocket listener, archive decompressor, container creation/lifecycle abstraction, upstream downloader, or unrelated route behavior was replaced. Sidero archive extraction is a separate extension service built on the same archive dependency and secured filesystem because the upstream decompressor does not expose the transactional limits/progress contract.
