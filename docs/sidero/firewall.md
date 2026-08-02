# Firewall

Firewall is disabled by default and requires Linux plus the `nft` binary and sufficient nftables privileges. Routes list, dry-run, add, and remove below `/api/servers/{uuid}/sidero/firewall`. If support is absent, capability negotiation reports false and routes return `unsupported_capability` without mutation.

Requests accept only allocation ID, `tcp`/`udp`, source IP/CIDR, `allow`/`deny`, optional expiry (maximum 30 days), and a 200-byte description. Wings verifies the allocation belongs to the route's server and derives its IP/port; clients cannot supply ports, interfaces, tables, chains, or commands. Administrator source CIDR policy, rule count, deterministic identity, duplicate, and opposing-rule conflict checks run before application.

The backend generates only one `table inet sidero_wings` with an ingress prerouting chain before destination NAT. Rules match the registered allocation destination/port and source. A set containing allow rules becomes an allowlist with a deterministic final drop for that allocation/protocol; deny-only rules block matching sources and leave other traffic accepted.

Every full desired-state change is sent as one nftables transaction. Dry-run uses `nft -c`; apply uses `nft -f` with stdin and no shell. On apply or persistence failure Wings restores the prior desired ruleset. State contains server/allocation/rule intent but not runtime destination/port, is atomically persisted under the Wings system root, revalidated against current allocations/policy, and reconciled at startup. Expiry cleanup and server-deletion cleanup are bounded and audited.

Wings never accepts raw nftables/iptables syntax, flushes global firewall state, modifies unrelated tables, or operates on unsupported systems. `firewall_apply_failed` is a controlled retryable state; node health becomes degraded.
