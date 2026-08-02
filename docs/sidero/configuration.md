# Sidero Configuration

Sidero uses the normal Wings YAML loader and controlled startup validation. `sidero.enabled` defaults to `true`, `protocol_version` to `1`, and firewall application to disabled. Invalid limits return a configuration error; they do not panic or partially replace the active configuration.

The configuration sections and production defaults are:

| Section | Defaults / purpose |
|---|---|
| `operations` | global 16, per-server 3, retention 3600s, cleanup 300s |
| `files` | search/content enabled; 1,000 results/matches; depth 64; 100,000 filename entries; 10,000 content files; 10 MiB/file; 512-byte excerpts; 100 probes; 1 MiB writes |
| `remote_download` | enabled; 1 GiB; 5 redirects; 10s connect; 900s total; private networks denied; exact host allow/block lists |
| `archives` | inspection/extraction enabled; 50,000 entries; 10 GiB total; 2 GiB/entry; ratio 200; symlinks disabled |
| `uploads` | enabled; 8 MiB chunks; 24h expiry; 3/server; 10 GiB/upload |
| `installers` | enabled; 5 GiB download total; 10,000 files; administrator-forced backups enabled |
| `game_query` | enabled; 3s; 15s cache; 60s stale; 1 retry; 60s offline backoff |
| `worlds` | enabled; 10 GiB import/clone |
| `network_statistics` | enabled; 5s interval; 120 samples |
| `rate_limits` | 60s window; 240 global and 30/server per route category; 10,000 tracked server-route buckets |
| `firewall` | disabled; 50 rules/server; 60s expiry cleanup; optional administrator source CIDR allow/block lists |

`remote_download.allowed_hosts` is an exact hostname allowlist; it does not override private-address denial. Set `allow_private_networks: true` explicitly when a trusted private origin is required. `blocked_hosts` always wins.

`firewall.allowed_sources` requires a requested source prefix to be wholly contained by an allowed CIDR. Any overlap with `blocked_sources` is rejected. Firewall expiry is limited to 30 days by protocol v1.

Validation also enforces per-server concurrency ≤ global concurrency, chunk size ≤ upload size, archive single-entry size ≤ archive total, stale query time ≥ cache time, redirects 0–20, retries 0–3, and valid firewall CIDRs. Logs announce controlled component state without tokens, paths, or URLs.
