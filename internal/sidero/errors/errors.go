// Package errors defines the stable, client-safe Sidero error contract.
package errors

import "fmt"

type Code string

const (
	CodeFeatureDisabled             Code = "feature_disabled"
	CodeUnsupportedCapability       Code = "unsupported_capability"
	CodeServerNotFound              Code = "server_not_found"
	CodeInvalidPath                 Code = "invalid_path"
	CodePathOutsideServerRoot       Code = "path_outside_server_root"
	CodeUnsafeSymbolicLink          Code = "unsafe_symbolic_link"
	CodeOperationNotFound           Code = "operation_not_found"
	CodeOperationConflict           Code = "operation_conflict"
	CodeOperationLimitReached       Code = "operation_limit_reached"
	CodeOperationCancelled          Code = "operation_cancelled"
	CodeOperationTimedOut           Code = "operation_timed_out"
	CodeRemoteHostDenied            Code = "remote_host_denied"
	CodeRemoteRedirectDenied        Code = "remote_redirect_denied"
	CodeRemoteSizeExceeded          Code = "remote_size_exceeded"
	CodeRemoteChecksumMismatch      Code = "remote_checksum_mismatch"
	CodeSearchLimitReached          Code = "search_limit_reached"
	CodeArchiveMalformed            Code = "archive_malformed"
	CodeArchiveTraversalDetected    Code = "archive_traversal_detected"
	CodeArchiveEntryLimitExceeded   Code = "archive_entry_limit_exceeded"
	CodeArchiveSizeLimitExceeded    Code = "archive_size_limit_exceeded"
	CodeArchiveRatioLimitExceeded   Code = "archive_ratio_limit_exceeded"
	CodeUploadSessionExpired        Code = "upload_session_expired"
	CodeUploadChunkInvalid          Code = "upload_chunk_invalid"
	CodeUploadIncomplete            Code = "upload_incomplete"
	CodeUploadChecksumMismatch      Code = "upload_checksum_mismatch"
	CodeInstallerManifestInvalid    Code = "installer_manifest_invalid"
	CodeInstallerDestinationInvalid Code = "installer_destination_invalid"
	CodeGameQueryUnsupported        Code = "game_query_unsupported"
	CodeGameQueryTimeout            Code = "game_query_timeout"
	CodeWorldInvalid                Code = "world_invalid"
	CodeConditionalWriteConflict    Code = "conditional_write_conflict"
	CodeFirewallRuleInvalid         Code = "firewall_rule_invalid"
	CodeFirewallApplyFailed         Code = "firewall_apply_failed"
	CodeInternal                    Code = "internal_error"
)

// Error contains only fields safe for API and WebSocket consumers. Cause is
// deliberately excluded from serialization and is only available to node logs.
type Error struct {
	Code           Code           `json:"code"`
	Message        string         `json:"message"`
	HTTPStatus     int            `json:"status"`
	Retryable      bool           `json:"retryable"`
	Details        map[string]any `json:"details,omitempty"`
	OperationState string         `json:"operation_state,omitempty"`
	cause          error
}

func New(code Code, message string, status int) *Error {
	return &Error{Code: code, Message: message, HTTPStatus: status}
}

func (e *Error) Error() string { return fmt.Sprintf("sidero: %s", e.Code) }
func (e *Error) Unwrap() error { return e.cause }

func (e *Error) WithCause(err error) *Error                { e.cause = err; return e }
func (e *Error) WithDetails(details map[string]any) *Error { e.Details = details; return e }
func (e *Error) WithRetryable(retryable bool) *Error       { e.Retryable = retryable; return e }
func (e *Error) WithOperationState(state string) *Error    { e.OperationState = state; return e }
