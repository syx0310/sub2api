package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmeta"
)

const (
	SettingKeyRequestIngressValidateProxy  = "request_ingress_validate_proxy"
	SettingKeyRequestIngressTrustedProxies = "request_ingress_trusted_proxies"
)

type cachedRequestIngressPolicy struct {
	policy    *requestmeta.Policy
	expiresAt time.Time
}

func parseRequestIngressSettings(values map[string]string) (bool, []string) {
	validate := values[SettingKeyRequestIngressValidateProxy] == "true"
	if raw := values[SettingKeyRequestIngressValidateProxy]; raw != "" && raw != "true" && raw != "false" {
		return true, []string{}
	}
	entries := []string{}
	if raw := values[SettingKeyRequestIngressTrustedProxies]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &entries); err != nil || entries == nil {
			return true, []string{} // corrupt persisted policy must not broaden trust
		}
	}
	_, normalized, err := requestmeta.NewPolicy(validate, entries)
	if err != nil {
		return true, []string{}
	}
	return validate, normalized
}

// RefreshRequestIngressPolicy is called at startup and off the request hot path.
// Serializing refresh and local settings publication prevents a stale DB read
// from overwriting a newly saved local policy.
func (s *SettingService) RefreshRequestIngressPolicy(ctx context.Context) {
	if s == nil {
		return
	}
	s.requestIngressMu.Lock()
	defer s.requestIngressMu.Unlock()
	if s.settingRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	values, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyRequestIngressValidateProxy, SettingKeyRequestIngressTrustedProxies})
	if err != nil {
		var previous *requestmeta.Policy
		if cached := s.requestIngressCache.Load(); cached != nil {
			previous = cached.policy
		}
		s.requestIngressCache.Store(&cachedRequestIngressPolicy{policy: previous, expiresAt: time.Now().Add(5 * time.Second)})
		slog.Warn("request ingress settings refresh failed; retaining policy", "error", err)
		return
	}
	validate, entries := parseRequestIngressSettings(values)
	policy, _, _ := requestmeta.NewPolicy(validate, entries)
	s.requestIngressCache.Store(&cachedRequestIngressPolicy{policy: policy, expiresAt: time.Now().Add(time.Minute)})
}

func (s *SettingService) RequestIngressPolicy() *requestmeta.Policy {
	if s == nil {
		return nil
	}
	cached := s.requestIngressCache.Load()
	if (cached == nil || time.Now().After(cached.expiresAt)) && s.requestIngressRefreshing.CompareAndSwap(false, true) {
		go func() {
			defer s.requestIngressRefreshing.Store(false)
			s.RefreshRequestIngressPolicy(context.Background())
		}()
	}
	if cached == nil {
		return nil
	}
	return cached.policy
}

func (s *SettingService) publishRequestIngressPolicy(settings *SystemSettings) {
	policy, _, err := requestmeta.NewPolicy(settings.RequestIngressValidateProxy, settings.RequestIngressTrustedProxies)
	if err != nil {
		policy = nil
	}
	s.requestIngressMu.Lock()
	s.requestIngressCache.Store(&cachedRequestIngressPolicy{policy: policy, expiresAt: time.Now().Add(time.Minute)})
	s.requestIngressMu.Unlock()
}
