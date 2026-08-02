# File Probe and Conditional Writes

`POST /api/servers/{uuid}/sidero/files/probe` accepts a bounded list of relative `paths` and optional `include_sha256`. It returns relative path, existence, file/directory/symlink type, size, UTC modification time, and SHA-256 only for regular files below the configured threshold. Symlinks are identified but never followed. UID/GID, inode, device, host permissions, and host paths are omitted.

`PUT /api/servers/{uuid}/sidero/files/conditional-write` accepts `path`, `expected_current_sha256`, bounded string `content`, and `create_backup`. Wings serializes Sidero writes through bounded path-lock stripes, hashes the current regular file, stages/chowns the replacement, optionally creates a server-relative backup, and rechecks the current hash immediately before descriptor-relative atomic replacement.

A changed hash returns HTTP 409 `conditional_write_conflict`; no stale write is intentionally committed. The response contains previous/new SHA-256, safe backup reference, and modification timestamp. The route emits a safe activity record without content. It does not create a missing target in v1.
