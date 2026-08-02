# Security Model

The Panel authenticates and authorizes product intent; Wings revalidates node execution. Every custom route uses existing bearer authentication. Every server route also uses existing server lookup/scope, operation/upload/firewall records carry server identity, and allocation actions resolve only Panel-supplied records owned by that server.

Filesystem security uses descriptor-relative Wings APIs, slash-relative normalization, Windows/NUL/traversal rejection, no-follow `lstat`, reserved `.sidero` client denial, bounded reads/walks, disk/quota preflight, UUID staging, atomic rename/replace, explicit conflicts, and rollback. Archive entry validation occurs during both inspection and output. Cleanup visits only known Sidero categories and UUID-named, old, non-symlink directories; backups are excluded.

Network egress permits HTTP(S) only, revalidates every DNS/redirect/connect target, pins a validated IP, denies special/private networks by default, does not use environment proxies, strips sensitive headers, and bounds time/bytes. Game queries never accept host/port and send only bounded protocol probes to server allocations.

Concurrency is bounded by workers, queues, per-server semaphores, upload session limits, traversal limits, cache/sample caps, fixed-window global/per-server route limits, deadlines, and bounded cleanup. Cancellation flows through contexts.

Client responses/events exclude credentials, raw internal errors, host paths, UID/GID/inode/device data, Docker internals, file/archive contents, full crash reports, arbitrary query targets, and packet contents. URL user info is rejected and signed/query-string URLs are not logged. Safe node activity contains operation/rule IDs, types, state, safe path references, and error codes only.

Firewall is a separate disabled-by-default boundary. Only a generated Sidero table can be changed, full rulesets apply transactionally, prior state rolls back on failure, and unsupported privilege/platform state fails closed.

The only direct host filesystem calls in Sidero production code are the controlled firewall state file beneath `system.root_directory` and a create/close/remove writability probe beneath `system.tmp_directory`. The firewall store rejects symlink/non-regular targets, uses a 1 MiB decode cap, mode 0600 temporary file, fsync, and atomic rename. Host command execution is limited to the discovered `nft` binary with fixed argument arrays and generated stdin; no shell is invoked.
