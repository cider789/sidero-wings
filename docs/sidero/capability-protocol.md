# Capability Protocol

`GET /api/sidero/v1/capabilities` uses normal Wings bearer authentication. The typed response contains `protocol_version`, detected `wings_version`, `sidero_version`, a stable boolean `features` map, `feature_versions`, safe limits, and `compatibility.state`.

Protocol v1 advertises operations, filename/content search, remote download, archive inspection and safe extraction, resumable upload, installers, resolved modpacks, Java/Bedrock queries, file probe, conditional write, world operations, process-exit metadata, network statistics, and firewall. Feature configuration controls each boolean. Firewall is additionally false when the configured nftables backend is unavailable.

Clients must negotiate every connection and must not infer support from the Wings version. During rolling upgrades, use only features reported true by the target node. A false feature produces `feature_disabled` or `unsupported_capability`; it must not be retried as though implemented.

`GET /api/sidero/v1/health` reports `healthy`, `degraded`, `unavailable`, `disabled`, `misconfigured`, or `unsupported` component states. Checks are bounded: configuration, workers, temporary storage writability, registered services/providers, network sampler, and firewall backend/reconciliation. Health never scans every server or queries game servers.
