//go:build unit

package adminaccess

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyConcurrencyRequiresGlobalOpsRead(t *testing.T) {
	for _, permission := range []string{"", "accounts.read", "users.read", "dashboard.read", "usage.read", "ops.read"} {
		p := Policy{GlobalPermissions: []string{permission}}
		for _, route := range []struct{ method, path string }{
			{"GET", "/api/v1/admin/api-keys/concurrency"},
			{"POST", "/api/v1/admin/api-keys/concurrency/query"},
		} {
			require.Equal(t, permission == "ops.read", RouteAllowed(p, route.method, route.path), permission+" "+route.path)
		}
	}
	p := Policy{GlobalPermissions: []string{"ops.read"}}
	require.NoError(t, ValidatePolicy(p))
	for _, route := range []struct{ method, path string }{
		{"DELETE", "/api/v1/admin/api-keys/concurrency"},
		{"GET", "/api/v1/admin/api-keys/concurrency/query"},
		{"GET", "/api/v1/admin/users/:id/api-keys"},
		{"GET", "/api/v1/admin/groups/:id/api-keys"},
		{"POST", "/api/v1/admin/users/batch-concurrency"},
		{"GET", "/api/v1/admin/settings/admin-api-key"},
	} {
		require.False(t, RouteAllowed(p, route.method, route.path), route.method+" "+route.path)
	}
}
