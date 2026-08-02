# Panel Integration

1. Fetch authenticated `GET /api/sidero/v1/capabilities` for every node and cache it only for that connection/rolling-upgrade window.
2. Send the existing node bearer token. Use the server UUID route for every execution and stable `Idempotency-Key`/`operation_key` for retryable operations.
3. Store returned operation/upload IDs. Consume Sidero WebSocket events for responsiveness, but reconcile authoritative operation/session state through authenticated GET after reconnect.
4. Treat operations present during a Wings disconnect/restart as indeterminate because operation records are not persisted. Re-probe files/checksums before issuing an idempotent replacement.
5. Persist network/query samples in the Panel only if long-term history is desired.

For game query/firewall, include stable allocation records in each server configuration (`id`, `ip`, `port`, optional `query_provider`). The API accepts allocation ID only; Wings derives network details.

For installers/modpacks, the Panel must resolve provider manifests into explicit URL/destination/size/checksum entries. Never send provider search terms, an untrusted downloaded manifest, or post-install commands. Use SHA-512 where providers supply it. For clean modpacks, send `mode: clean`, `clean_install: true`, explicit retained paths, and request backups.

The Panel remains responsible for users, permission decisions, provider/marketplace discovery, add-on settings, installation history, billing, notifications, migrations, product metadata, active-world choice, restarts, customer UI, and long-term metrics. Never send arbitrary query targets, raw firewall syntax, shell fragments, host paths, or client-selected node limits.

Handle 409 as a conflict requiring re-probe/reconciliation, 429 using `Retry-After`, retryable timeout/application errors with bounded backoff, and disabled/unsupported capabilities by hiding or degrading the product feature.
