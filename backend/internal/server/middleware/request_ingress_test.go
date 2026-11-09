package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmeta"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestRequestIngressDoesNotChangeClientIPOrSessionBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, validate := range []bool{false, true} {
		for _, clientIPCompat := range []bool{false, true} {
			cfg := &config.Config{}
			cfg.SetTrustForwardedIPForAPIKeyACL(clientIPCompat)
			policy, _, err := requestmeta.NewPolicy(validate, []string{"10.0.0.0/8"})
			require.NoError(t, err)
			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(nil))
			r.Use(RequestIngress(func() *requestmeta.Policy { return policy }), SessionBindingContext(cfg))
			r.GET("/responses", func(c *gin.Context) {
				wantIP := "192.0.2.1"
				if clientIPCompat {
					wantIP = "203.0.113.9"
				}
				require.Equal(t, wantIP, ip.GetClientIP(c))
				require.Equal(t, wantIP, service.SessionBindingFromContext(c.Request.Context()).IP)
				wantHost := "public.example"
				if validate {
					wantHost = "internal"
				}
				ingress := requestmeta.IngressFromContext(c.Request.Context())
				require.Equal(t, wantHost, ingress.Host)
				require.Equal(t, "192.0.2.1", ingress.PeerIP)
				c.Status(200)
			})
			req := httptest.NewRequest("GET", "http://internal/responses", nil)
			req.RemoteAddr = "192.0.2.1:1234"
			req.Header.Set("X-Forwarded-Host", "public.example")
			req.Header.Set("X-Real-IP", "203.0.113.9")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 200, w.Code)
		}
	}
}
