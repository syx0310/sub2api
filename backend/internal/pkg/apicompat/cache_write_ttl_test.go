package apicompat

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheWriteTTLTypedProtocolRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		details     string
		short, long int
	}{
		{`{"ttl_30m_tokens":9,"ttl_12h_tokens":91}`, 9, 91},
		{`{"ttl_30m_tokens":0,"ttl_12h_tokens":100}`, 0, 100},
		{`{"ttl_30m_tokens":100,"ttl_12h_tokens":0}`, 100, 0},
		{`null`, 0, 0}, {`{}`, 0, 0},
		{`{"ttl_12h_tokens":100}`, 0, 0},
		{`{"ttl_30m_tokens":-1,"ttl_12h_tokens":101}`, 0, 0},
		{`{"ttl_30m_tokens":9.5,"ttl_12h_tokens":90.5}`, 0, 0},
		{`{"ttl_30m_tokens":"0","ttl_12h_tokens":"100"}`, 0, 0},
	} {
		for _, path := range []string{"input_tokens_details", "prompt_tokens_details"} {
			t.Run(path+"/"+tc.details, func(t *testing.T) {
				var usage ResponsesUsage
				require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"input_tokens":101,"output_tokens":2,%q:{"cache_write_tokens":100,"cache_write_tokens_details":%s}}`, path, tc.details)), &usage))
				short, long := usage.CacheWriteTTLTokens()
				require.Equal(t, tc.short, short)
				require.Equal(t, tc.long, long)
				chat := chatUsageFromResponsesUsage(&usage)
				roundTrip := ChatUsageToResponsesUsage(chat)
				short, long = roundTrip.CacheWriteTTLTokens()
				require.Equal(t, tc.short, short)
				require.Equal(t, tc.long, long)
				require.Equal(t, 100, roundTrip.CacheCreationInputTokens)
				require.JSONEq(t, tc.details, string(roundTrip.InputTokensDetails.CacheWriteTokensDetails))
			})
		}
	}
}
