# Modpack Installation

`POST /api/servers/{uuid}/sidero/installers/modpack` creates `modpack_installation`. The Panel must supply a fully resolved `operation_key`, `mode`, resolved file URLs/destinations/checksums, optional resolved overrides archive, retained paths, conflict policy, and backup intent. Provider discovery and manifest resolution never occur in Wings.

`merge` downloads/extracts into staging and transactionally commits regular files under `fail`, `skip`, `replace`, or `rename`. All targets are preflighted; any commit failure rolls back earlier files. Overrides use hardened archive extraction.

`clean` additionally requires `clean_install: true`. Wings builds the complete replacement tree first, copies only validated retained paths, moves all current top-level entries except the reserved `.sidero` namespace into rollback staging, and then installs staged top-level entries. Commit failure restores the original tree. Administrator backup policy moves the old tree into a persistent server-relative backup; otherwise rollback staging is deleted only after success.

Arbitrary manifest paths, `.sidero` destinations, symlinks, shell commands, and automatic server restarts are rejected/not performed.
