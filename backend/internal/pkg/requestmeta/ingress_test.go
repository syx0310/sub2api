package requestmeta

import (
	"context"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestIngressPolicy(t *testing.T) {
	for _, tt := range []struct {
		name, remote, host, forwarded, want, source string
		validate                                    bool
		proxies                                     []string
	}{
		{"default", "203.0.113.7:9911", "internal:8080", "API.example.com:8443", "api.example.com:8443", "forwarded_host", false, nil},
		{"direct", "203.0.113.7:9911", "192.0.2.5:8080", "", "192.0.2.5:8080", "host", false, nil},
		{"untrusted", "203.0.113.7:9911", "internal:8080", "spoof.example", "internal:8080", "host", true, []string{"10.0.0.0/8"}},
		{"strict_empty", "203.0.113.7:9911", "internal", "spoof.example", "internal", "host", true, nil},
		{"multiple", "192.0.2.5:9911", "internal", "public.example", "public.example", "forwarded_host", true, []string{"10.0.0.1", "192.0.2.0/24"}},
		{"ipv6", "[2001:db8::2]:9911", "internal", "[2001:db8:1::1]:8443", "[2001:db8:1::1]:8443", "forwarded_host", true, []string{"2001:db8::/64"}},
		{"mapped", "[::ffff:192.0.2.5]:9911", "internal", "public.example", "public.example", "forwarded_host", true, []string{"192.0.2.5"}},
		{"ambiguous", "192.0.2.5:9911", "internal", "one.example,two.example", "internal", "host", false, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, _, err := NewPolicy(tt.validate, tt.proxies)
			require.NoError(t, err)
			r := httptest.NewRequest("GET", "http://internal/", nil)
			r.Host, r.RemoteAddr = tt.host, tt.remote
			r.Header.Set("X-Forwarded-Host", tt.forwarded)
			r.Header.Set("X-Forwarded-For", "10.0.0.1") // cannot establish proxy trust
			got := p.Capture(r)
			require.Equal(t, tt.want, got.Host)
			require.Equal(t, tt.source, got.Source)
		})
	}
}

func TestIngressRejectsMalformedHostsAndProxies(t *testing.T) {
	p, normalized, err := NewPolicy(false, []string{"192.0.2.1", "192.0.2.1", "2001:db8::123/64"})
	require.NoError(t, err)
	require.Equal(t, []string{"192.0.2.1", "2001:db8::/64"}, normalized)
	for _, invalid := range []string{"https://example.com", "example.com/path", "a@host", "a:99999", "a:0", "a\r\nb", "-bad.example", "a..b", "a b", "a\\b", "[example.com]", "[192.0.2.1]"} {
		r := httptest.NewRequest("GET", "http://fallback/", nil)
		r.Header.Set("X-Forwarded-Host", invalid)
		require.Equal(t, "fallback", p.Capture(r).Host, invalid)
	}
	r := httptest.NewRequest("GET", "http://fallback/", nil)
	r.Header.Add("X-Forwarded-Host", "one.example")
	r.Header.Add("X-Forwarded-Host", "two.example")
	require.Equal(t, "fallback", p.Capture(r).Host)
	for _, invalid := range []string{"proxy.example", "0.0.0.0/33", "fe80::1%eth0", "10.0.0.1:80", "not-ip"} {
		_, _, err := NewPolicy(true, []string{invalid})
		require.Error(t, err)
	}
	require.Equal(t, "fallback", (*Policy)(nil).Capture(r).Host)
	ctx := WithIngress(context.Background(), p.Capture(r))
	first := IngressFromContext(ctx)
	first.Host = "changed"
	require.Equal(t, "fallback", IngressFromContext(ctx).Host, "immutable snapshot")
}
