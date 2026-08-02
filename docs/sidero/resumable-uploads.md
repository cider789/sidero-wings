# Resumable Uploads

Routes are:

- `POST /api/servers/{uuid}/sidero/uploads`
- `GET /api/servers/{uuid}/sidero/uploads/{id}`
- `PUT /api/servers/{uuid}/sidero/uploads/{id}/chunks/{index}`
- `POST /api/servers/{uuid}/sidero/uploads/{id}/complete`
- `DELETE /api/servers/{uuid}/sidero/uploads/{id}`

A session is permanently bound to its server, normalized destination/filename, expected size, node-selected chunk size/count, optional SHA-256/SHA-512, expiry, and `fail`/`skip`/`replace` policy. Global/per-server session limits, route rate limits, quota preflight, maximum bytes, exact chunk sizes, and indexed bounds are enforced.

Wings calculates received bytes/chunks from stored data. Re-uploading an identical chunk is idempotent; a mismatched duplicate fails. Completion is single-finalizer, streams chunks in order, verifies every count/byte and final checksum, then atomically places output. Failure removes the partial assembled file while retaining valid chunks for retry; completion, cancellation, and expiry clean the session. UUID staging left by a restart is removed by bounded stale cleanup. Upload progress events contain only trusted counters.
