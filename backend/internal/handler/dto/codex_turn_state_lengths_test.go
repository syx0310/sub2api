package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexTurnStateLengthsOnlyExposedToAdmin(t *testing.T) {
	zero, length := 0, 292
	log := &service.UsageLog{CodexTurnState: service.CodexTurnStateLengths{RequestHeaderBytes: &zero, ResponseMetadataBytes: &length}}
	userJSON, err := json.Marshal(UsageLogFromService(log))
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "codex_turn_state")
	admin := UsageLogFromServiceAdmin(log)
	require.NotNil(t, admin.CodexTurnState)
	require.Zero(t, *admin.CodexTurnState.RequestHeaderBytes)
	require.Equal(t, 292, *admin.CodexTurnState.ResponseMetadataBytes)
	require.Nil(t, UsageLogFromServiceAdmin(&service.UsageLog{}).CodexTurnState)
}
