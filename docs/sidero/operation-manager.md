# Operation Manager

Server-scoped routes are:

- `GET /api/servers/{uuid}/sidero/operations`
- `GET /api/servers/{uuid}/sidero/operations/{operation}`
- `DELETE /api/servers/{uuid}/sidero/operations/{operation}`

Feature POSTs return HTTP 202 with an operation containing ID, type, state, progress, safe message/result/error, and timestamps. States are `queued`, `validating`, `running`, `finalizing`, `completed`, `failed`, and `cancelled`; terminal state is deterministic.

A fixed global worker pool and bounded queue enforce global concurrency. Per-server semaphores enforce server concurrency. Cancellation propagates through `context.Context`; feature loops and network I/O check it. An `Idempotency-Key` of at most 256 bytes coalesces the same server/type/key until retention cleanup.

Terminal records expire after configured retention, and each cleanup pass removes bounded work. Operations cannot be read or cancelled through another server UUID. Active operations are cancelled when Wings shuts down. Protocol v1 deliberately does not persist operation records across restart; the Panel should mark disconnected operations indeterminate and reconcile product state safely.
