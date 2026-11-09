package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type ingressSettingRepo struct {
	SettingRepository
	mu     sync.Mutex
	values map[string]string
	err    error
	calls  int
}

func (r *ingressSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	copy := make(map[string]string, len(r.values))
	for k, v := range r.values {
		copy[k] = v
	}
	return copy, r.err
}

func TestRequestIngressPolicyCacheAndSettingsRefresh(t *testing.T) {
	repo := &ingressSettingRepo{values: map[string]string{}}
	svc := &SettingService{settingRepo: repo}
	svc.RefreshRequestIngressPolicy(context.Background())
	r := httptest.NewRequest("GET", "http://internal/", nil)
	r.RemoteAddr = "192.0.2.1:1000"
	r.Header.Set("X-Forwarded-Host", "public.example")
	for range 1000 {
		require.Equal(t, "public.example", svc.RequestIngressPolicy().Capture(r).Host)
	}
	require.Equal(t, 1, repo.calls, "cached requests must not query DB")
	svc.publishRequestIngressPolicy(&SystemSettings{RequestIngressValidateProxy: true, RequestIngressTrustedProxies: []string{"10.0.0.0/8"}})
	require.Equal(t, "internal", svc.RequestIngressPolicy().Capture(r).Host)
	repo.mu.Lock()
	repo.err = errors.New("offline")
	repo.mu.Unlock()
	svc.RefreshRequestIngressPolicy(context.Background())
	require.Equal(t, "internal", svc.RequestIngressPolicy().Capture(r).Host, "DB failure retains last valid policy")
	repo.mu.Lock()
	repo.err = nil
	repo.values = map[string]string{SettingKeyRequestIngressValidateProxy: "true", SettingKeyRequestIngressTrustedProxies: `["192.0.2.0/24"]`}
	repo.mu.Unlock()
	cached := svc.requestIngressCache.Load()
	svc.requestIngressCache.Store(&cachedRequestIngressPolicy{policy: cached.policy, expiresAt: time.Now().Add(-time.Minute)})
	_ = svc.RequestIngressPolicy() // stale read starts background refresh
	require.Eventually(t, func() bool { return svc.RequestIngressPolicy().Capture(r).Host == "public.example" }, time.Second, time.Millisecond)
	for _, raw := range []string{`{"not":"array"}`, `["not-ip"]`, "null"} {
		validate, proxies := parseRequestIngressSettings(map[string]string{SettingKeyRequestIngressTrustedProxies: raw})
		require.True(t, validate)
		require.Empty(t, proxies)
	}
}
