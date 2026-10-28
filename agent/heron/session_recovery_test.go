package heron

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSessionForgetsStaleResumeID covers the recovery path that made the
// A→B verification loop unfalsifiable: the adapter remembers a heron session
// id across restarts, the engine's data directory is cleared, and every turn
// then fails with "JSON-RPC -32002: file not found" — an error about files for
// a problem about a missing conversation.
//
// Dropping the id makes the next turn start a fresh session instead of failing
// the same way forever.
func TestSessionForgetsStaleResumeID(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	session.sessionID.Store("fs-cleared-by-hand")
	require.Equal(t, "fs-cleared-by-hand", session.CurrentSessionID())

	err := session.handleResponse(heronJSONRPCResponse{
		ID: json.RawMessage("1"),
		Error: &heronJSONRPCError{
			Code:    -32002,
			Message: "file not found",
		},
	})
	require.NoError(t, err)

	require.Empty(t, session.CurrentSessionID(),
		"a session the engine no longer has must not be resumed again")
}

// TestSessionKeepsIDOnUnrelatedError makes sure the reset is narrow: a normal
// engine error must not silently drop the conversation.
func TestSessionKeepsIDOnUnrelatedError(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	session.sessionID.Store("fs-still-valid")

	err := session.handleResponse(heronJSONRPCResponse{
		ID: json.RawMessage("1"),
		Error: &heronJSONRPCError{
			Code:    -32603,
			Message: "internal error",
		},
	})
	require.NoError(t, err)

	require.Equal(t, "fs-still-valid", session.CurrentSessionID(),
		"an unrelated error must not throw away the conversation")
}
