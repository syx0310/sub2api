package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSClientDrainPreservesOperationDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	drain := newOpenAIWSClientDrain(ctx, time.Minute)
	defer drain.close()
	select {
	case <-drain.readCtx.Done():
		require.ErrorIs(t, context.Cause(drain.readCtx), context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("operation deadline was extended by the drain")
	}
}

func TestOpenAIWSClientDrainBudgetDoesNotReset(t *testing.T) {
	drain := newOpenAIWSClientDrain(context.Background(), 30*time.Millisecond)
	defer drain.close()
	drain.begin()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-drain.readCtx.Done():
			return
		case <-ticker.C:
			drain.begin()
		case <-deadline.C:
			t.Fatal("repeated disconnect observations reset the drain budget")
		}
	}
}

type observedDrainReadConn struct {
	openAIWSClientConn
	reads   atomic.Int32
	waitAt  int32
	waiting chan struct{}
}

func (c *observedDrainReadConn) ReadMessage(ctx context.Context) ([]byte, error) {
	if c.reads.Add(1) == c.waitAt {
		close(c.waiting)
	}
	return c.openAIWSClientConn.ReadMessage(ctx)
}

type observedDrainReadDialer struct {
	delegate openAIWSClientDialer
	waitAt   int32
	waiting  chan struct{}
}

func (d *observedDrainReadDialer) Dial(ctx context.Context, wsURL string, headers http.Header, proxyURL string) (openAIWSClientConn, int, http.Header, error) {
	conn, status, responseHeaders, err := d.delegate.Dial(ctx, wsURL, headers, proxyURL)
	if err != nil {
		return conn, status, responseHeaders, err
	}
	return &observedDrainReadConn{openAIWSClientConn: conn, waitAt: d.waitAt, waiting: d.waiting}, status, responseHeaders, nil
}

func TestForwardOpenAIWSV2_RealSocketClientCancellation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stream   bool
		partial  bool
		terminal bool
	}{
		{name: "before first output", stream: true, terminal: true},
		{name: "after partial output", stream: true, partial: true, terminal: true},
		{name: "non-streaming response", terminal: true},
		{name: "silent upstream exhausts drain", stream: true, partial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			serverCtx, cancelServer := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancelServer()
			release := make(chan struct{})
			serverErrors := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					serverErrors <- err
					return
				}
				defer func() { _ = conn.CloseNow() }()
				if _, _, err = conn.Read(serverCtx); err != nil {
					serverErrors <- err
					return
				}
				if err = conn.Write(serverCtx, coderws.MessageText, []byte(`{"type":"response.created","response":{"id":"resp_drain","model":"gpt-5.5"}}`)); err != nil {
					serverErrors <- err
					return
				}
				if tc.partial {
					if err = conn.Write(serverCtx, coderws.MessageText, []byte(`{"type":"response.output_text.delta","response_id":"resp_drain","delta":"partial"}`)); err != nil {
						serverErrors <- err
						return
					}
				}
				select {
				case <-release:
				case <-serverCtx.Done():
					serverErrors <- serverCtx.Err()
					return
				}
				if tc.terminal {
					err = conn.Write(serverCtx, coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_drain","model":"gpt-5.5","output":[{"type":"image_generation_call","id":"ig_1","result":"aW1hZ2U=","size":"1024x1024","status":"completed"}],"usage":{"input_tokens":3,"output_tokens":5}}}`))
					serverErrors <- err
					return
				}
				// A real coder/websocket read must be interrupted when the fixed
				// drain budget expires, even if the upstream never sends another event.
				_, _, err = conn.Read(serverCtx)
				if err == nil || errors.Is(serverCtx.Err(), context.DeadlineExceeded) {
					serverErrors <- errors.New("gateway did not close the drained connection")
					return
				}
				serverErrors <- nil
			}))
			defer server.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 1
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
			waiting := make(chan struct{})
			waitAt := int32(2)
			if tc.partial {
				waitAt = 3
			}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&observedDrainReadDialer{delegate: newDefaultOpenAIWSClientDialer(), waitAt: waitAt, waiting: waiting})
			svc := &OpenAIGatewayService{cfg: cfg, cache: &stubGatewayCache{}, httpUpstream: &httpUpstreamRecorder{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
			account := &Account{ID: 9102, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test", "base_url": server.URL},
				Extra:       map[string]any{"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool}}
			payload := `{"model":"gpt-5.5","stream":false,"reasoning":{"effort":"high"},"input":"hello"}`
			if tc.stream {
				payload = strings.Replace(payload, `"stream":false`, `"stream":true`, 1)
			}
			type outcome struct {
				result *OpenAIForwardResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := svc.Forward(ctx, c, account, []byte(payload))
				done <- outcome{result: result, err: err}
			}()
			select {
			case <-waiting:
			case got := <-done:
				t.Fatalf("forward exited before waiting for upstream: %v", got.err)
			case <-time.After(4 * time.Second):
				t.Fatal("forward did not start its upstream read")
			}
			cancel()
			close(release)
			select {
			case got := <-done:
				require.NotNil(t, got.result)
				require.True(t, got.result.ClientDisconnect)
				require.NotNil(t, got.result.RequestedReasoningEffort)
				require.NotNil(t, got.result.RequestBodyBytes)
				require.NotNil(t, got.result.ResponseBodyBytes)
				require.Equal(t, "high", *got.result.RequestedReasoningEffort)
				require.Positive(t, *got.result.RequestBodyBytes)
				require.Equal(t, int64(writer.Body.Len()), *got.result.ResponseBodyBytes)
				if tc.terminal {
					require.NoError(t, got.err)
					require.Equal(t, 3, got.result.Usage.InputTokens)
					require.Equal(t, 5, got.result.Usage.OutputTokens)
					require.Equal(t, 1, got.result.ImageCount)
				} else {
					require.ErrorIs(t, got.err, context.Canceled)
				}
				require.NotContains(t, writer.Body.String(), "response.failed")
				require.NotContains(t, writer.Body.String(), "response.completed")
				_, bound := svc.getOpenAIWSStateStore().GetResponseConn("resp_drain")
				require.False(t, bound, "a discarded socket cannot become a resume binding")
			case <-time.After(4 * time.Second):
				t.Fatal("client cancellation did not finish within the drain budget")
			}
			require.NoError(t, <-serverErrors)
			ap, ok := pool.getAccountPool(account.ID)
			require.True(t, ok)
			ap.mu.Lock()
			remaining := len(ap.conns)
			ap.mu.Unlock()
			require.Zero(t, remaining, "the canceled request's connection must not return to the pool")
		})
	}
}
