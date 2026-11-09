package dto

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmeta"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRequestIngressOnlyExposedToAdmin(t *testing.T) {
	log := &service.UsageLog{RequestIngress: &requestmeta.Ingress{Host: "api.example.com", Source: "forwarded_host", PeerIP: "10.0.0.1"}}
	userJSON, err := json.Marshal(UsageLogFromService(log))
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "request_ingress")
	admin := UsageLogFromServiceAdmin(log)
	require.Equal(t, log.RequestIngress, admin.RequestIngress)
	require.Nil(t, UsageLogFromServiceAdmin(&service.UsageLog{}).RequestIngress)
	adminJSON, err := json.Marshal(admin)
	require.NoError(t, err)
	require.NotContains(t, string(adminJSON), "codex_turn_state")
}
