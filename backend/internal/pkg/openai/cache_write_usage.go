package openai

import "github.com/tidwall/gjson"

// CacheWriteTTLTokens accepts only complete, disjoint usage buckets. Request
// TTLs are intent, not evidence of the lifetime of each billed cache write.
func CacheWriteTTLTokens(details gjson.Result, aggregate int) (int, int) {
	short, long := details.Get("ttl_30m_tokens"), details.Get("ttl_12h_tokens")
	if aggregate <= 0 || short.Type != gjson.Number || long.Type != gjson.Number {
		return 0, 0
	}
	a, b := short.Int(), long.Int()
	if a < 0 || b < 0 || float64(a) != short.Float() || float64(b) != long.Float() ||
		a > int64(aggregate) || b != int64(aggregate)-a {
		return 0, 0
	}
	return int(a), int(b)
}

// CacheWriteTTLTokensFromUsage supports both Responses and Chat usage shapes.
// A present but invalid canonical breakdown must not fall back to another one.
func CacheWriteTTLTokensFromUsage(usage gjson.Result, aggregate int) (int, int) {
	for _, path := range []string{"input_tokens_details", "prompt_tokens_details"} {
		if details := usage.Get(path + ".cache_write_tokens_details"); details.Exists() {
			return CacheWriteTTLTokens(details, aggregate)
		}
	}
	return 0, 0
}
