# Content Search

`POST /api/servers/{uuid}/sidero/files/content-search` creates a `content_search` operation. It supports bounded plain text only: root/query, case sensitivity, extension allow/block lists, maximum file size, matches per file, total matches, excerpt bytes, depth, and files scanned.

Node configuration clamps all budgets and execution has a two-minute deadline. Files are streamed line by line; individual reads and scanner buffers cannot exceed the configured file-size bound. NUL-containing files are treated as binary and skipped. Matches contain relative path, one-based line number, and an HTML-escaped, UTF-8-safe bounded excerpt. No regex, raw HTML, host search, or long-term result storage is supported.

The same traversal, Windows-path, reserved namespace, and no-symlink rules as filename search apply. `truncated` indicates match or file-visit exhaustion.
