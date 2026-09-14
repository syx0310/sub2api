package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type bareErrorSlots struct {
	concurrencyCacheMock
	users, accounts atomic.Int32
	released        chan struct{}
}

func (s *bareErrorSlots) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return s.users.CompareAndSwap(0, 1), nil
}
func (s *bareErrorSlots) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return s.accounts.CompareAndSwap(0, 1), nil
}
func (s *bareErrorSlots) ReleaseUserSlot(context.Context, int64, string) error {
	s.users.Add(-1)
	atomic.AddInt32(&s.releaseUserCalled, 1)
	s.released <- struct{}{}
	return nil
}
func (s *bareErrorSlots) ReleaseAccountSlot(context.Context, int64, string) error {
	s.accounts.Add(-1)
	atomic.AddInt32(&s.releaseAccountCalled, 1)
	return nil
}

// Exercise the real handler, WS codec, pool and async usage path. The first
// upstream NEVER closes on its own; neither does the first downstream client
// until we have proved the concurrency=1 slot is available to a second create.
func TestOpenAIWSBareErrorReleasesSilentExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModePassthrough} {
		for _, scenario := range []string{"no_response_id", "created_then_silent", "auxiliary_traffic", "paired_failed", "contradictory_completed"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				t.Cleanup(cancel)
				var connections, requests atomic.Int32
				upstreamClosed := make(chan struct{}, 2)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					id := connections.Add(1)
					conn, err := coderws.Accept(w, r, nil)
					if err != nil {
						t.Errorf("upstream accept: %v", err)
						return
					}
					defer conn.CloseNow()
					defer func() { upstreamClosed <- struct{}{} }()
					_, _, err = conn.Read(ctx)
					if err != nil {
						t.Errorf("upstream request: %v", err)
						return
					}
					requests.Add(1)
					write := func(payload string) bool { return conn.Write(ctx, coderws.MessageText, []byte(payload)) == nil }
					if id == 1 {
						if scenario != "no_response_id" && !write(`{"type":"response.created","response":{"id":"resp_bare"}}`) {
							return
						}
						if !write(`{"type":"error","status":400,"error":{"code":"invalid_request_error","message":"invalid request"},"usage":{"input_tokens":3,"output_tokens":1}}`) {
							return
						}
						if scenario == "paired_failed" {
							if !write(`{"type":"rate_limits.updated"}`) {
								return
							}
							if !write(`{"type":"response.failed","response":{"id":"resp_bare","error":{"code":"invalid_request_error","message":"invalid request"},"usage":{"input_tokens":11,"output_tokens":4}}}`) {
								return
							}
						}
						if scenario == "contradictory_completed" {
							if !write(`{"type":"response.completed","response":{"id":"resp_bare","usage":{"input_tokens":99,"output_tokens":99}}}`) {
								return
							}
						}
					} else {
						if !write(`{"type":"response.created","response":{"id":"resp_healthy"}}`) {
							return
						}
						if !write(`{"type":"response.completed","response":{"id":"resp_healthy","usage":{"input_tokens":2,"output_tokens":1}}}`) {
							return
						}
					}
					trafficCtx, stopTraffic := context.WithCancel(ctx)
					trafficDone := make(chan struct{})
					if id == 1 && scenario == "auxiliary_traffic" {
						go func() {
							defer close(trafficDone)
							ticker := time.NewTicker(25 * time.Millisecond)
							defer ticker.Stop()
							for {
								select {
								case <-trafficCtx.Done():
									return
								case <-ticker.C:
									if conn.Write(trafficCtx, coderws.MessageText, []byte(`{"type":"rate_limits.updated"}`)) != nil {
										return
									}
								}
							}
						}()
					} else {
						close(trafficDone)
					}
					defer func() { stopTraffic(); <-trafficDone }()
					// Continue reading only to process a peer-initiated close. A bug
					// cannot escape via fixture EOF or a short configured timeout.
					for {
						_, _, err = conn.Read(ctx)
						if err != nil {
							return
						}
						requests.Add(1)
					}
				}))
				t.Cleanup(upstream.Close)
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Default.RateMultiplier = 1
				cfg.Security.URLAllowlist.AllowInsecureHTTP = true
				cfg.Gateway.OpenAIWS.Enabled = true
				cfg.Gateway.OpenAIWS.APIKeyEnabled = true
				cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
				cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
				cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 900
				cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
				cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
				cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
				cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
				account := service.Account{ID: 9909, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
					Credentials: map[string]any{"api_key": "sk-test", "base_url": upstream.URL},
					Extra:       map[string]any{"openai_apikey_responses_websockets_v2_enabled": true, "openai_apikey_responses_websockets_v2_mode": mode}}
				usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 8)}
				billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billingCache.Stop)
				slots := &bareErrorSlots{released: make(chan struct{}, 8)}
				concurrencySvc := service.NewConcurrencyService(slots)
				gatewaySvc := service.NewOpenAIGatewayService(&openAIWSUsageHandlerAccountRepoStub{account: account}, usageRepo, nil, nil, nil, nil, nil, cfg, nil, concurrencySvc, service.NewBillingService(cfg, nil), nil, billingCache, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				h := &OpenAIGatewayHandler{gatewayService: gatewaySvc, billingCacheService: billingCache, apiKeyService: &service.APIKeyService{}, concurrencyHelper: NewConcurrencyHelper(concurrencySvc, SSEPingFormatNone, time.Second)}
				group := int64(4309)
				key := &service.APIKey{ID: 1809, GroupID: &group, User: &service.User{ID: 1709, Status: service.StatusActive}}
				done := make(chan struct{}, 2)
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.User.ID, Concurrency: 1})
					c.Next()
				})
				router.GET("/openai/v1/responses", func(c *gin.Context) { h.ResponsesWebSocket(c); done <- struct{}{} })
				server := httptest.NewServer(router)
				t.Cleanup(server.Close)
				dial := func() *coderws.Conn {
					conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", nil)
					require.NoError(t, err)
					t.Cleanup(func() { _ = conn.CloseNow() })
					return conn
				}
				first := dial()
				require.NoError(t, first.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6-astra","store":false,"input":[{"type":"compaction_trigger"}]}`)))
				for {
					_, frame, err := first.Read(ctx)
					require.NoError(t, err)
					if gjson.GetBytes(frame, "type").String() == "error" {
						break
					}
				}
				select {
				case <-slots.released:
				case <-time.After(2 * time.Second):
					t.Fatal("bare error kept concurrency slot while upstream stayed open")
				}
				require.Zero(t, slots.users.Load())
				require.Zero(t, slots.accounts.Load())
				select {
				case <-upstreamClosed:
				case <-time.After(2 * time.Second):
					t.Fatal("poisoned upstream connection remained open")
				}
				var failed *service.UsageLog
				select {
				case failed = <-usageRepo.created:
				case <-ctx.Done():
					t.Fatal("missing failed usage")
				}
				require.True(t, failed.NativeCompactionV2)
				require.True(t, failed.OpenAIWSMode)
				wantIn, wantOut := 3, 1
				if scenario == "paired_failed" {
					wantIn, wantOut = 11, 4
				}
				require.Equal(t, wantIn, failed.InputTokens)
				require.Equal(t, wantOut, failed.OutputTokens)
				second := dial()
				require.NoError(t, second.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6-astra","store":false,"input":[]}`)))
				for _, want := range []string{"response.created", "response.completed"} {
					_, frame, err := second.Read(ctx)
					require.NoError(t, err)
					require.Equal(t, want, gjson.GetBytes(frame, "type").String())
				}
				require.NoError(t, second.Close(coderws.StatusNormalClosure, "done"))
				// Now consume the first client's close; resource release above did
				// not depend on its cooperation or another request preempting it.
				for {
					_, frame, err := first.Read(ctx)
					if err != nil {
						break
					}
					require.NotEqual(t, "response.completed", gjson.GetBytes(frame, "type").String())
				}
				for range 2 {
					select {
					case <-done:
					case <-ctx.Done():
						t.Fatal("handler did not exit")
					}
				}
				select {
				case log := <-usageRepo.created:
					require.Equal(t, "resp_healthy", log.RequestID)
				case <-ctx.Done():
					t.Fatal("missing healthy usage")
				}
				require.Empty(t, usageRepo.created, "failed turn must settle once")
				require.Equal(t, int32(2), atomic.LoadInt32(&slots.releaseUserCalled))
				require.Equal(t, int32(2), atomic.LoadInt32(&slots.releaseAccountCalled))
				require.Equal(t, int32(2), connections.Load(), "failed socket must not return to pool")
				require.Equal(t, int32(2), requests.Load(), fmt.Sprintf("no automatic replay for %s", scenario))
			})
		}
	}
}
