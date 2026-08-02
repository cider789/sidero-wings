# Archive Security

`POST /api/servers/{uuid}/sidero/files/archive/inspect` creates `archive_inspection`. It identifies content through Wings' archive dependency rather than trusting extensions and reports format, entries/files/directories/symlinks, estimated bytes, largest entry, maximum depth, bounded suspicious entries, rejection reasons, unsafe flag, and disk estimate.

`POST /api/servers/{uuid}/sidero/files/archive/extract` creates `archive_extraction` with `archive`, `destination`, and `fail`/`skip`/`replace`/`rename`. It first performs inspection and quota preflight, then independently revalidates every raw entry while writing to a UUID staging directory. Preflight is never trusted as authorization.

Both passes detect absolute/traversal/Windows/backslash/NUL paths, hard/symbolic links, devices, FIFOs, sockets, irregular entries, duplicates, malformed input, entry/single-entry/total-size limits, and compression-ratio limits. Extraction rejects all links and unsafe types regardless of inspection display policy. Exact reads detect truncated entries.

Only a completely validated staging tree is committed. Replace moves the prior destination into rollback staging before the final rename; failed commit restores it. Cancellation and failures remove partial output. Nested archives remain ordinary files and are never recursively extracted.
