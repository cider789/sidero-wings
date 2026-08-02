# World Operations

All routes create server-scoped operations:

- `POST .../worlds/inspect` and `POST .../worlds/size`: `{"path":"world"}`
- `POST .../worlds/import`: archive, destination, conflict policy
- `POST .../worlds/archive`: source and destination archive path
- `POST .../worlds/clone` / `rename`: source and destination
- `POST .../worlds/replace`: source, destination, `backup_existing`

Recognition requires a bounded regular `level.dat`. A regular `level.dat` plus directory `db` is reported as Bedrock; otherwise it is Java. Recursive size/copy rejects symlinks and irregular entries and enforces configured bytes/quota.

Import uses hardened archive extraction into a temporary world path, validates the staged markers/size, then applies `fail`, `skip`, `replace`, or deterministic `rename`. Existing output is not touched before validation. Clone, rename, import, and replace require the server offline. Replace moves the old world into rollback staging and can retain it as a safe backup. Archive reads a validated world through Wings' archive streamer and stages output before placement.

Wings never chooses the active world, changes product metadata, deletes arbitrary paths, or restarts the server. The Panel coordinates configuration/restart separately.
