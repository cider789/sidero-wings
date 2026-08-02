# Installer Execution

`POST /api/servers/{uuid}/sidero/installers/execute` accepts a Panel-resolved `operation_key`, backup/conflict settings, and bounded `files` containing `source_url`, validated destination, expected size, and SHA-256/SHA-512. It creates `installer_execution`; the operation key is its idempotency key.

Every URL uses the hardened SSRF-resistant downloader. Every destination is normalized before network work, duplicates and non-regular conflicts are rejected, aggregate file/download limits apply, and all downloads finish in staging before commit. Existing files can be copied to server-relative `.sidero/backups/installers/...`; administrator `backup_existing` forces backups even if the request omits them.

Commit is multi-file transactional: existing targets move to rollback staging, staged files move into place, and a later error/cancellation restores earlier targets. Result entries report installed/skipped status, safe relative destinations, bytes, and safe backup references.

Wings never searches providers, trusts downloaded manifests, executes downloaded files, or accepts post-install shell commands. The same engine handles resolved plugins, mods, software JARs, templates, and other files.
