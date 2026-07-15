package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageResponseBodyBytesFromOpenAIImagesExcludesKeepalives(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	stop := service.StartOpenAIImagesJSONKeepalive(c, time.Millisecond)
	require.Eventually(t, func() bool {
		return c.Writer.Size() >= 4
	}, time.Second, time.Millisecond)

	payload := []byte(`{"data":[{"b64_json":"aW1hZ2U="}]}`)
	_, err := c.Writer.Write(payload)
	require.NoError(t, err)
	stop()

	got := usageResponseBodyBytesFromOpenAIImages(c)
	require.NotNil(t, got)
	require.Equal(t, int64(len(payload)), *got)
	require.Greater(t, recorder.Body.Len(), len(payload), "wire body includes keepalive whitespace")
}

func TestUsageResponseBodyBytesFromOpenAIImagesWithoutKeepalive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	payload := []byte(`{"data":[]}`)
	_, err := c.Writer.Write(payload)
	require.NoError(t, err)

	got := usageResponseBodyBytesFromOpenAIImages(c)
	require.NotNil(t, got)
	require.Equal(t, int64(len(payload)), *got)
}
