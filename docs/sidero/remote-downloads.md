# Remote Downloads

`POST /api/servers/{uuid}/sidero/files/remote-download` creates a `remote_download` operation. The request accepts `url`, server-relative `destination`, optional `filename`/expected size/checksum, and `fail`, `skip`, `replace`, or deterministic `rename` conflict policy. SHA-256 and SHA-512 are supported.

Only HTTP(S) URLs without user info are accepted. Exact hostname allow/block policy is checked before DNS, on every redirect, and again during connection. Every resolved IP must pass policy. Connections resolve, revalidate, and dial a validated IP directly; environment proxies are not used. Loopback, private, link-local, multicast, unspecified, carrier-grade NAT, benchmark, documentation, metadata-relevant, and reserved networks are denied unless private-network access is explicitly enabled. Host allowlisting alone never bypasses address policy.

Redirect count, connect timeout, total timeout, response bytes, expected size, and checksum are enforced. `Accept-Encoding: identity` and disabled transport decompression prevent hidden expansion. Panel authorization, cookies, and proxy credentials are never forwarded. Data streams to a generated server-scoped temporary file, quota is enforced, and verified output is atomically placed. Cancellation/error removes staging data; the final path is never written incrementally.
