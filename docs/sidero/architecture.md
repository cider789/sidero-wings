# Sidero Architecture

Sidero protocol v1 is an isolated node-execution layer under `internal/sidero`. `router/registerSideroRoutes` mounts it into Wings' existing authenticated Gin engine; it does not create a second server, authentication system, WebSocket transport, container abstraction, or filesystem.

The dependency direction is:

`Wings core → Sidero configuration → capabilities/health → operations → feature services → HTTP/WebSocket adapters`

Feature services reuse `server.Server`, the descriptor-relative `internal/ufs` sandbox, `server/filesystem`, the existing server event bus, container resource events, and the daemon lifecycle context. Staging data lives only in generated UUID directories below the server-relative `.sidero` namespace. Client paths into that reserved namespace are rejected.

The operation manager has a fixed worker pool, a bounded queue, per-server semaphores, cancellation, idempotency, retention, and bounded cleanup. Operation records/results are intentionally in-memory and non-recoverable after restart; the Panel reconciles after reconnect. Upload sessions are separately bounded and expiring.

The Panel remains authoritative for users, permissions, provider discovery, marketplace metadata, resolved manifests, product history, billing, notifications, long-term metrics, migrations, and UI. Wings receives authenticated execution intent and independently revalidates paths, allocations, URLs, sizes, checksums, conflicts, and node policy.

Firewall desired state is persisted locally because host rules must be reconciled after restart. It is disabled by default, uses only a generated `inet sidero_wings` nftables table, and becomes `unsupported` when Linux/nftables is unavailable.
