package service

func bodyBytesPtr(v int64) *int64 {
	if v < 0 {
		return nil
	}
	return &v
}

// resolveUsageBodyBytes keeps HTTP handlers authoritative when they pass a
// concrete value, while still allowing service-owned paths (notably WS turns)
// to persist byte counts carried on the ForwardResult.
func resolveUsageBodyBytes(inputValue, resultValue *int64) *int64 {
	if inputValue != nil {
		return inputValue
	}
	return resultValue
}
