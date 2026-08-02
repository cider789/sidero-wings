# Structured Errors

HTTP errors contain stable `code`, safe `message`, HTTP `status`, `retryable`, and optional safe `details` or `operation_state`. Operation failures use the same code/message/retryability shape. Internal Go causes are never serialized.

Stable protocol-v1 codes are:

`feature_disabled`, `unsupported_capability`, `server_not_found`, `invalid_path`, `path_outside_server_root`, `unsafe_symbolic_link`, `operation_not_found`, `operation_conflict`, `operation_limit_reached`, `operation_cancelled`, `operation_timed_out`, `remote_host_denied`, `remote_redirect_denied`, `remote_size_exceeded`, `remote_checksum_mismatch`, `search_limit_reached`, `archive_malformed`, `archive_traversal_detected`, `archive_entry_limit_exceeded`, `archive_size_limit_exceeded`, `archive_ratio_limit_exceeded`, `upload_session_expired`, `upload_chunk_invalid`, `upload_incomplete`, `upload_checksum_mismatch`, `installer_manifest_invalid`, `installer_destination_invalid`, `game_query_unsupported`, `game_query_timeout`, `world_invalid`, `conditional_write_conflict`, `firewall_rule_invalid`, `firewall_apply_failed`, and `internal_error`.

Rate/concurrency exhaustion returns HTTP 429 and is retryable. Conditional, destination, and desired-state conflicts return HTTP 409. Timeout and transient firewall application failures are retryable. Unexpected failures return `internal_error`; detailed causes remain node-local.
