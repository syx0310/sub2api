// Package requestmeta captures bounded, immutable request observations. These
// values are diagnostic only and must not be used for routing or authorization.
package requestmeta

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

type Ingress struct {
	Host   string `json:"host"`
	Source string `json:"source"`
	PeerIP string `json:"peer_ip,omitempty"`
}

type ingressKey struct{}

func WithIngress(ctx context.Context, value Ingress) context.Context {
	return context.WithValue(ctx, ingressKey{}, value)
}

func IngressFromContext(ctx context.Context) *Ingress {
	if ctx == nil {
		return nil
	}
	value, ok := ctx.Value(ingressKey{}).(Ingress)
	if !ok {
		return nil
	}
	return &value
}

// Policy is immutable after construction; it is safe to share between requests.
type Policy struct {
	validate bool
	trusted  []netip.Prefix
}

func NewPolicy(validate bool, entries []string) (*Policy, []string, error) {
	if len(entries) > 256 {
		return nil, nil, fmt.Errorf("at most 256 ingress trusted proxies are allowed")
	}
	p := &Policy{validate: validate}
	normalized := make([]string, 0, len(entries))
	seen := make(map[string]bool)
	for _, raw := range entries {
		if len(raw) > 128 {
			return nil, nil, fmt.Errorf("ingress trusted proxy entry is too long")
		}
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		var prefix netip.Prefix
		if addr, err := netip.ParseAddr(entry); err == nil && addr.Zone() == "" {
			addr = addr.Unmap()
			entry, prefix = addr.String(), netip.PrefixFrom(addr, addr.BitLen())
		} else if parsed, err := netip.ParsePrefix(entry); err == nil {
			prefix = parsed.Masked()
			if prefix.Addr().Is4In6() {
				if prefix.Bits() < 96 {
					return nil, nil, fmt.Errorf("invalid mapped IPv4 ingress CIDR %q", entry)
				}
				prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96).Masked()
			}
			entry = prefix.String()
		} else {
			return nil, nil, fmt.Errorf("invalid ingress trusted IP/CIDR %q", entry)
		}
		if !seen[entry] {
			seen[entry] = true
			normalized = append(normalized, entry)
			p.trusted = append(p.trusted, prefix)
		}
	}
	return p, normalized, nil
}

func peerAddress(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.WithZone("").Unmap()
}

func (p *Policy) Capture(r *http.Request) Ingress {
	if r == nil {
		return Ingress{}
	}
	peer := peerAddress(r.RemoteAddr)
	value := Ingress{Host: normalizeHost(r.Host), Source: "host"}
	if peer.IsValid() {
		value.PeerIP = peer.String()
	}
	// A nil policy (e.g. settings DB unavailable at startup) fails closed.
	trusted := p != nil && !p.validate
	if p != nil && p.validate && peer.IsValid() {
		for _, prefix := range p.trusted {
			if prefix.Contains(peer) {
				trusted = true
				break
			}
		}
	}
	if trusted {
		values := r.Header.Values("X-Forwarded-Host")
		if len(values) == 1 {
			if host := normalizeHost(values[0]); host != "" {
				value.Host, value.Source = host, "forwarded_host"
			}
		}
	}
	return value
}

// Accept a single authority, never a URL, list, userinfo or arbitrary log text.
func normalizeHost(raw string) string {
	if raw == "" || len(raw) > 512 || strings.ContainsAny(raw, " \t\r\n,/@?#\\") {
		return ""
	}
	host := raw
	if strings.HasPrefix(raw, "[") || strings.Count(raw, ":") == 1 {
		if h, port, err := net.SplitHostPort(raw); err == nil {
			n, err := strconv.ParseUint(port, 10, 16)
			if err != nil || n == 0 {
				return ""
			}
			host = h
		} else if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
			host = raw[1 : len(raw)-1]
		} else {
			return ""
		}
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return ""
		}
		if strings.HasPrefix(raw, "[") && !addr.Is6() {
			return ""
		}
		return strings.Clone(strings.ToLower(raw))
	}
	if strings.ContainsAny(raw, "[]") {
		return ""
	}
	if len(host) > 253 || host == "" {
		return ""
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, c := range label {
			validCharacter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
			if !validCharacter {
				return ""
			}
		}
	}
	return strings.Clone(strings.ToLower(raw))
}
