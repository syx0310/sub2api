package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexTurnStateLengthsObservationIsPassive(t *testing.T) {
	s := &OpenAIGatewayService{}
	account := &Account{Platform: PlatformOpenAI}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	header := http.Header{"X-Codex-Turn-State": {strings.Repeat("h", 292)}}
	body := []byte(`{"client_metadata":{"x-codex-turn-state":"\u4f60x"}}`)
	before := append([]byte(nil), body...)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header = header.Clone()
	o := s.observeCodexTurnStateHTTPRequest(c, account, req, body)
	o.responseHeaders(http.Header{"X-Codex-Turn-State": {"response-secret"}})
	o.event([]byte(`{"type":"response.metadata","headers":{"X-Codex-Turn-State":["event-secret"]}}`), "response.metadata")
	o.event([]byte(`{"type":"response.metadata","headers":{"x-codex-turn-state":"later"}}`), "response.metadata")
	got := observedCodexTurnStateLengths(c)
	require.Equal(t, 292, *got.RequestHeaderBytes)
	require.Equal(t, 4, *got.RequestMetadataBytes, "decoded UTF-8 bytes, not runes or JSON escape length")
	require.Equal(t, len("response-secret"), *got.ResponseHeaderBytes)
	require.Equal(t, len("event-secret"), *got.ResponseMetadataBytes, "first nonempty declaration wins")
	require.Equal(t, header, req.Header)
	require.Equal(t, before, body)
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret")
	require.NotContains(t, string(encoded), strings.Repeat("h", 20))

	// A new account attempt cannot inherit the previous response or metadata.
	next := s.observeCodexTurnStateHTTPRequest(c, account, httptest.NewRequest(http.MethodPost, "/v1/responses", nil), []byte(`{}`))
	require.Zero(t, *next.snapshot().RequestHeaderBytes)
	require.Zero(t, *next.snapshot().RequestMetadataBytes)
	require.Nil(t, next.snapshot().ResponseHeaderBytes)
	require.Nil(t, next.snapshot().ResponseMetadataBytes)
	require.Equal(t, len("event-secret"), *got.ResponseMetadataBytes, "snapshots stay detached")
}

func TestCodexTurnStateLengthsUseExactHTTPBodySnapshot(t *testing.T) {
	s := &OpenAIGatewayService{}
	account := &Account{Platform: PlatformOpenAI}
	ctx := s.codexTurnStateRequestContext(context.Background(), account, []byte(`{"client_metadata":{"x-codex-turn-state":"actual"}}`))
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	o := s.observeCodexTurnStateHTTPRequest(nil, account, req, []byte(`{}`))
	require.Equal(t, len("actual"), *o.snapshot().RequestMetadataBytes)
}

func TestCodexTurnStateLengthsOptionalAndDisabled(t *testing.T) {
	var absent *codexTurnStateLengthObserver
	absent.request(nil, nil)
	absent.responseHeaders(nil)
	absent.event(nil, "response.metadata")
	require.True(t, absent.snapshot().Empty())
	require.Nil(t, absent.snapshot().Display())
	s := &OpenAIGatewayService{cfg: &config.Config{}}
	require.Nil(t, s.newCodexTurnStateLengthObserver(&Account{Platform: PlatformAnthropic}))
	s.cfg.Gateway.DisableCodexTurnStateLengthObservation = true
	require.Nil(t, s.newCodexTurnStateLengthObserver(&Account{Platform: PlatformOpenAI}))

	s.cfg.Gateway.DisableCodexTurnStateLengthObservation = false
	o := s.newCodexTurnStateLengthObserver(&Account{Platform: PlatformOpenAI})
	o.request(nil, []byte(`{}`)) // Reused WS socket has no new handshake.
	o.event([]byte(`{"headers":{"x-codex-turn-state":"not-metadata"}}`), "response.output_text.delta")
	require.Nil(t, o.snapshot().RequestHeaderBytes)
	require.Nil(t, o.snapshot().ResponseHeaderBytes)
	require.Nil(t, o.snapshot().ResponseMetadataBytes)
	require.Zero(t, *o.snapshot().RequestMetadataBytes)
	o.event([]byte(`{"headers":{}}`), "response.metadata")
	require.Zero(t, *o.snapshot().ResponseMetadataBytes)
	o.event([]byte(`{"headers":{"x-codex-turn-state":42}}`), "response.metadata")
	require.Zero(t, *o.snapshot().ResponseMetadataBytes)
	o.request(nil, []byte(`{"client_metadata":{"x-codex-turn-state":42}}`))
	require.Nil(t, o.snapshot().RequestMetadataBytes)
}

func TestCodexTurnStateLengthsConcurrentSnapshot(t *testing.T) {
	o := (&OpenAIGatewayService{}).newCodexTurnStateLengthObserver(&Account{Platform: PlatformOpenAI})
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			for range 50 {
				o.event([]byte(`{"headers":{"x-codex-turn-state":"opaque"}}`), "response.metadata")
				_ = o.snapshot()
			}
		})
	}
	group.Wait()
	require.Equal(t, 6, *o.snapshot().ResponseMetadataBytes)
}
