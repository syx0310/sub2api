package service

func bodyBytesPtr(v int64) *int64 {
	if v < 0 {
		return nil
	}
	return &v
}

func resolveUsageBodyBytes(inputValue, resultValue *int64) *int64 {
	if inputValue != nil {
		return inputValue
	}
	return resultValue
}
