package errors

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorMarshalsOnlySafeFields(t *testing.T) {
	err := New(CodeArchiveSizeLimitExceeded, "The archive exceeds the permitted extraction size.", 422).
		WithDetails(map[string]any{"limit_bytes": int64(10)}).
		WithCause(assertiveSecretError("token=secret host=/srv/internal"))
	b, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)
	require.JSONEq(t, `{"code":"archive_size_limit_exceeded","message":"The archive exceeds the permitted extraction size.","status":422,"retryable":false,"details":{"limit_bytes":10}}`, string(b))
	require.NotContains(t, string(b), "secret")
	require.Contains(t, err.Error(), "archive_size_limit_exceeded")
	require.EqualError(t, err.Unwrap(), "token=secret host=/srv/internal")
}

type assertiveSecretError string

func (e assertiveSecretError) Error() string { return string(e) }
