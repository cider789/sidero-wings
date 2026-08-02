# Upstream Rebase

Keep `internal/sidero`, `config/sidero.go`, `router/router_sidero.go`, and `docs/sidero` isolated when rebasing. Resolve that tree first, then reapply only the core hooks listed in `core-patch-surface.md`.

Preserve the existing authentication/server middleware ordering, event bus, SFTP/filesystem behavior, crash handler semantics, Docker resource publication, and daemon cancellation context. Do not copy upstream router, filesystem, lifecycle, or stats files wholesale.

After each upstream rebase run configuration and `internal/sidero/...` tests, affected filesystem/server/router/environment tests, then `go test ./...`, race tests, vet/static analysis configured by upstream, and CGO-disabled builds for supported architectures. Reinspect Docker stats field names and archive-library raw entry behavior.

If an upstream adapter can no longer preserve allocation ownership, descriptor-relative paths, transactional commit, SSRF validation, or dedicated firewall ownership, set that capability false and return a controlled unsupported state until its tests are restored. Never silently weaken the boundary to make a rebase compile.
