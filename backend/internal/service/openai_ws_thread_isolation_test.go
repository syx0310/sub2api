//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercise the production WS codec against a local upstream, including OAuth
// paths whose production URL is fixed. No OpenAI account/network is used.
type openAIWSThreadLocalDialer struct {
	target string
	inner  openAIWSClientDialer
	dials  atomic.Int32
}

func (d *openAIWSThreadLocalDialer) Dial(ctx context.Context, _ string, headers http.Header, _ string) (openAIWSClientConn, int, http.Header, error) {
	d.dials.Add(1)
	return d.inner.Dial(ctx, d.target, headers, "")
}

func TestOpenAIWSParentSubagentsAndGuardianStayConnected(t *testing.T) {
	for _, model := range []string{"gpt-5.6-sol", "gpt-6-astra"} {
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			t.Cleanup(cancel)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var upstreamConnections atomic.Int32
			var upstreamTurns atomic.Int32
			promptKeys := make(chan string, 8)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id := upstreamConnections.Add(1)
				if state := r.Header.Get(openAIWSTurnStateHeader); state != "" {
					t.Errorf("new thread inherited another thread's turn-state: %q", state)
				}
				w.Header().Set(openAIWSTurnStateHeader, fmt.Sprintf("state-conn-%d", id))
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					t.Errorf("upstream accept: %v", err)
					return
				}
				defer conn.CloseNow()
				for turn := 1; ; turn++ {
					_, payload, err := conn.Read(ctx)
					if err != nil {
						return
					}
					upstreamTurns.Add(1)
					promptKeys <- gjson.GetBytes(payload, "prompt_cache_key").String()
					responseID := fmt.Sprintf("resp_conn_%d_turn_%d", id, turn)
					created := fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"model":%q,"status":"in_progress"}}`, responseID, model)
					if err := conn.Write(ctx, coderws.MessageText, []byte(created)); err != nil {
						return
					}
					if turn == 1 {
						select {
						case <-release:
						case <-ctx.Done():
							return
						}
					}
					completed := fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":%q,"status":"completed","usage":{"input_tokens":2,"output_tokens":1}}}`, responseID, model)
					if err := conn.Write(ctx, coderws.MessageText, []byte(completed)); err != nil {
						return
					}
				}
			}))
			t.Cleanup(upstream.Close)
			cfg := openAIWSThreadTestConfig()
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 8
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 8
			cfg.Gateway.OpenAIWS.PoolTargetUtilization = 1
			cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 5
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 5
			dialer := &openAIWSThreadLocalDialer{target: "ws" + strings.TrimPrefix(upstream.URL, "http"), inner: newDefaultOpenAIWSClientDialer()}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(dialer)
			t.Cleanup(pool.Close)
			svc := &OpenAIGatewayService{cfg: cfg, openaiWSPool: pool, openaiWSPassthroughDialer: dialer, toolCorrector: NewCodexToolCorrector()}
			account := &Account{
				ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8,
				Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"},
				Extra:       prepareCodexFingerprintExtraForCreate(PlatformOpenAI, AccountTypeOAuth, map[string]any{"codex_fingerprint_mode": "device"}),
			}
			serverErrors := make(chan error, 4)
			results := make(chan *OpenAIForwardResult, 8)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					serverErrors <- err
					return
				}
				defer conn.CloseNow()
				_, first, err := conn.Read(ctx)
				if err != nil {
					serverErrors <- err
					return
				}
				c := newOpenAIWSThreadTestContext(7, 11, "root-session", r.Header.Get("thread-id"))
				c.Request = r.Clone(r.Context())
				hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
					if err == nil && result != nil {
						results <- result
					}
				}}
				// Mirror handler ownership followed by the nested service call.
				ownerCtx, cleanup, _ := svc.BeginOpenAIWSIngressSessionPreemption(r.Context(), c, account, first)
				defer cleanup()
				serverErrors <- svc.ProxyResponsesWebSocketFromClient(ownerCtx, c, conn, account, "test-token", first, hooks)
			}))
			t.Cleanup(gateway.Close)
			t.Cleanup(unblock)
			read := func(conn *coderws.Conn, eventType string) []byte {
				t.Helper()
				_, payload, err := conn.Read(ctx)
				require.NoError(t, err)
				require.Equal(t, eventType, gjson.GetBytes(payload, "type").String())
				return payload
			}
			var clients []*coderws.Conn
			threads := []string{"parent", "subagent-a", "subagent-b", "guardian"}
			for _, thread := range threads {
				headers := http.Header{"Session-Id": {"root-session"}, "Thread-Id": {thread}}
				conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(gateway.URL, "http"), &coderws.DialOptions{HTTPHeader: headers})
				require.NoError(t, err)
				t.Cleanup(func() { _ = conn.CloseNow() })
				clients = append(clients, conn)
				first := fmt.Sprintf(`{"type":"response.create","model":%q,"store":false,"prompt_cache_key":"root-session","client_metadata":{"session_id":"root-session","thread_id":%q},"input":[{"role":"user","content":"hello"}]}`, model, thread)
				require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(first)))
				read(conn, "response.created")
			}
			// All four generations are now in flight, not just idle registrations.
			unblock()
			for i, conn := range clients {
				firstDone := read(conn, "response.completed")
				previous := gjson.GetBytes(firstDone, "response.id").String()
				followup := fmt.Sprintf(`{"type":"response.create","model":%q,"store":false,"prompt_cache_key":"root-session","previous_response_id":%q,"client_metadata":{"session_id":"root-session","thread_id":%q},"input":[{"role":"user","content":"continue"}]}`, model, previous, threads[i])
				require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(followup)))
				read(conn, "response.created")
				read(conn, "response.completed")
			}
			for range 8 {
				select {
				case result := <-results:
					require.Equal(t, "response.completed", result.UpstreamTerminalEvent)
				case <-ctx.Done():
					t.Fatal("missing per-turn accounting result")
				}
			}
			for _, conn := range clients {
				_ = conn.Close(coderws.StatusNormalClosure, "done")
			}
			for range clients {
				select {
				case err := <-serverErrors:
					require.NoError(t, err)
				case <-ctx.Done():
					t.Fatal("gateway did not release the closed connection")
				}
			}
			require.Equal(t, int32(4), dialer.dials.Load(), "creating siblings must not cause extra upstream reconnects")
			require.Equal(t, int32(8), upstreamTurns.Load(), "no duplicate request replay")
			firstKey := <-promptKeys
			require.NotEmpty(t, firstKey)
			for range 7 {
				require.Equal(t, firstKey, <-promptKeys, "root prompt-cache identity remains shared across threads and turns")
			}
		})
	}
}
