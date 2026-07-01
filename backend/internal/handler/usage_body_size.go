package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func usageBodyBytesPtr(n int) *int64 {
	if n < 0 {
		return nil
	}
	v := int64(n)
	return &v
}

func usageBodyBytesInt64Ptr(n int64) *int64 {
	if n < 0 {
		return nil
	}
	return &n
}

func usageResponseBodyBytesFromGin(c *gin.Context) *int64 {
	if c == nil || c.Writer == nil {
		return nil
	}
	size := c.Writer.Size()
	if size < 0 {
		return nil
	}
	v := int64(size)
	return &v
}

func attachOpenAIUsageBodyBytes(result *service.OpenAIForwardResult, requestBodyBytes, responseBodyBytes *int64) {
	if result == nil {
		return
	}
	if result.RequestBodyBytes == nil {
		result.RequestBodyBytes = requestBodyBytes
	}
	if result.ResponseBodyBytes == nil {
		result.ResponseBodyBytes = responseBodyBytes
	}
}

func attachGatewayUsageBodyBytes(result *service.ForwardResult, requestBodyBytes, responseBodyBytes *int64) {
	if result == nil {
		return
	}
	if result.RequestBodyBytes == nil {
		result.RequestBodyBytes = requestBodyBytes
	}
	if result.ResponseBodyBytes == nil {
		result.ResponseBodyBytes = responseBodyBytes
	}
}
