# Network Statistics

`GET /api/servers/{uuid}/sidero/network` returns `available`, the current sample, and a bounded recent sample array. `sidero network stats` publishes each sample over the existing server WebSocket.

Samples contain cumulative receive/transmit bytes, bytes/second rates, receive/transmit packets, drops where Docker reports them, timestamp, and availability. Docker aggregation sums interface counters without inspecting packets. The configured in-memory ring has a hard sample count; nothing is persisted.

Offline servers produce an unavailable sample. A counter rollback resets the buffer so a container restart cannot produce a wrapped or cross-lifecycle rate. The first sample after unavailable/restart has zero rate. Buffers for removed servers are cleaned.
