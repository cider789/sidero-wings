# Game Query

`GET /api/servers/{uuid}/sidero/query?provider=minecraft-java|minecraft-bedrock&allocation_id={id}` performs a bounded node-side status query. Hostname and port input are not accepted. Without `allocation_id`, Wings uses the server's configured default mapping. With it, Wings requires a matching `sidero_allocations` record in that server's Panel-supplied configuration and optionally enforces its provider.

Java implements the status handshake with bounded VarInts, packet size (1 MiB), JSON, player sample (12), MOTD/version text, timeout, and connection lifetime. Bedrock sends one 33-byte unconnected ping per attempt, reads at most 4 KiB, validates the magic/length/fields, and never exposes an arbitrary UDP target.

The normalized response contains supported/online/provider, players, version, protocol, sanitized MOTD, latency, query time, and stale flag. Per-server route limiting, a strict timeout, bounded retries, request coalescing, short cache, stale-on-failure, offline backoff, and repeated-failure backoff protect providers and nodes. Cache cleanup is bounded and no query history is persisted.

The Panel must send stable allocation records in server configuration:

```json
{"sidero_allocations":[{"id":123,"ip":"0.0.0.0","port":25565,"query_provider":"minecraft-java"}]}
```

Wildcard allocation addresses are queried through the corresponding loopback family; an unknown ID/provider returns a safe unsupported error.
