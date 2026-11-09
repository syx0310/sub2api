package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmeta"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUsageRecordContextCopiesIngressAfterRequestEnds(t *testing.T) {
	value := requestmeta.Ingress{Host: "ws.example.com", Source: "forwarded_host", PeerIP: "10.0.0.1"}
	ctx, cancel := context.WithCancel(requestmeta.WithIngress(context.Background(), value))
	var observed *requestmeta.Ingress
	task, _ := wrapUsageRecordTaskContext(ctx, func(worker context.Context) {
		require.NoError(t, worker.Err())
		observed = requestmeta.IngressFromContext(worker)
	})
	cancel()
	task(context.Background())
	require.Equal(t, &value, observed)
	require.Nil(t, requestmeta.IngressFromContext(usageRecordContext(context.Background(), context.Background())))
}

func TestRequestIngressPreservedByDedicatedCyberErrorEntries(t *testing.T) {
	ingress := &requestmeta.Ingress{Host: "cyber.example", Source: "host", PeerIP: "10.0.0.1"}
	meta := cyberPolicyOpsErrorMeta{RequestIngress: ingress}
	require.Equal(t, ingress, buildCyberSessionBlockedOpsEntry(meta).RequestIngress)
	require.Equal(t, ingress, buildCyberPolicyOpsErrorEntry(meta, &service.CyberPolicyMark{}).RequestIngress)
}
