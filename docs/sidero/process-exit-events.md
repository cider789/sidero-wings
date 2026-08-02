# Process Exit Events

When a process transitions to offline and Sidero is enabled, Wings emits `sidero process exit` through the existing server event bus after retrieving the environment exit state and running the existing crash handler when appropriate.

The payload contains exit code, expected stop, crash detected, OOM state, restart attempted/count/exhausted, bounded relative latest crash-report/log paths, UTC timestamp, and pre-exit uptime in milliseconds. File discovery checks at most 100 crash-report entries and never follows symlinks or reads report contents.

Expected stops do not invoke crash recovery. Unexpected running/starting exits preserve upstream clean-exit/OOM/crash-detection configuration and restart throttling. The existing lifecycle and WebSocket transport are unchanged. `signal` is omitted because the Docker exit-state interface in this Wings version does not provide a portable signal value.
