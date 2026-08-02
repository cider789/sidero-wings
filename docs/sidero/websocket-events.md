# WebSocket Events

Sidero publishes through the existing authenticated per-server Wings event bus:

- `sidero operation created`
- `sidero operation progress`
- `sidero operation completed`
- `sidero operation failed`
- `sidero operation cancelled`
- `sidero upload created`
- `sidero upload progress`
- `sidero upload completed`
- `sidero upload cancelled`
- `sidero process exit`
- `sidero network stats`

Operation events contain only operation ID/type/state/progress, a bounded safe message, `{available:true}` result summary, and safe error code. Full search results and file lists remain available only through the server-scoped operation GET during retention.

Upload events contain upload ID/state and trusted byte counters calculated by Wings. Process events contain exit metadata and relative crash/log references, never contents. Network events contain counters/rates/timestamp only. Events never include credentials, signed URLs, URL user info/query data, host paths, Docker details, file contents, or packet payloads.
