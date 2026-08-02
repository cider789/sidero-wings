# Filename Search

`POST /api/servers/{uuid}/sidero/files/search` creates a `file_search` operation. Request fields are `root`, `query`, `recursive`, `case_sensitive`, `include_files`, `include_directories`, `allowed_extensions`, `blocked_extensions`, `maximum_depth`, `maximum_results`, and `maximum_entries`.

Client limits are clamped to node policy and execution has a two-minute deadline. Empty include flags mean files and directories. Extension matching is case-insensitive; blocked extensions win. Results are deterministic and expose only slash-separated relative path, base name, `file`/`directory`, size, and UTC modification time. `truncated` is true when the result or traversal budget is reached.

Absolute, traversal, backslash, NUL, Windows-drive, and reserved `.sidero` paths are rejected. Directory access and `lstat` use Wings' descriptor-relative sandbox; symlinks and irregular entries are never followed. Memory is bounded by result limits.
