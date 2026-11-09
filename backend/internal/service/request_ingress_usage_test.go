package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmeta"
	"github.com/stretchr/testify/require"
	"testing"
)

type ingressUsageWriter struct {
	UsageLogRepository
	t        *testing.T
	want     requestmeta.Ingress
	fallback bool
}

func (w *ingressUsageWriter) CreateBestEffort(ctx context.Context, log *UsageLog) error {
	require.NoError(w.t, ctx.Err())
	require.Equal(w.t, &w.want, log.RequestIngress)
	return context.DeadlineExceeded
}

func (w *ingressUsageWriter) Create(ctx context.Context, log *UsageLog) (bool, error) {
	w.fallback = true
	require.NoError(w.t, ctx.Err())
	require.Equal(w.t, &w.want, log.RequestIngress)
	return true, nil
}

func TestRequestIngressSurvivesAsyncBillingAndWriteFallback(t *testing.T) {
	want := requestmeta.Ingress{Host: "ws.example", Source: "forwarded_host", PeerIP: "10.0.0.1"}
	ctx, cancel := context.WithCancel(requestmeta.WithIngress(context.Background(), want))
	cancel()
	w := &ingressUsageWriter{t: t, want: want}
	writeUsageLogBestEffort(ctx, w, &UsageLog{}, "test")
	require.True(t, w.fallback)
}
