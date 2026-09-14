package service

import (
	"fmt"

	coderws "github.com/coder/websocket"
)

type openAIWSIngressRoute struct {
	protocol OpenAIWSProtocolDecision
	mode     string
}

// Owner registration and execution must honor the same configured transport.
// Astra uses Codex's serial response.create protocol in ctx_pool; clients that
// need native response.steer can explicitly select passthrough.
func (s *OpenAIGatewayService) resolveOpenAIWSIngressRoute(account *Account, firstMessage []byte, _ string) (openAIWSIngressRoute, error) {
	route := openAIWSIngressRoute{mode: OpenAIWSIngressModeCtxPool}
	if s == nil || account == nil {
		return route, fmt.Errorf("websocket ingress requires an account and service")
	}
	route.protocol = s.getOpenAIWSProtocolResolver().Resolve(account)
	if account.Platform == PlatformGrok || (s.pluginManager != nil && s.pluginManager.ShouldRouteOpenAIOAuth(account)) {
		route.mode = OpenAIWSIngressModeHTTPBridge
		return route, nil
	}
	modeRouterV2 := s.cfg != nil && s.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled
	if modeRouterV2 {
		route.mode = account.ResolveOpenAIResponsesWebSocketV2Mode(s.cfg.Gateway.OpenAIWS.IngressModeDefault)
		if route.mode == OpenAIWSIngressModeOff {
			return route, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "websocket mode is disabled for this account", nil)
		}
	}
	switch route.mode {
	case OpenAIWSIngressModePassthrough:
		if route.protocol.Transport != OpenAIUpstreamTransportResponsesWebsocketV2 {
			return route, fmt.Errorf("websocket ingress requires ws_v2 transport, got=%s", route.protocol.Transport)
		}
		if s.shouldBridgeOpenAIWSPassthroughFirstMessage(account, firstMessage) {
			route.mode = OpenAIWSIngressModeHTTPBridge
		}
	case OpenAIWSIngressModeHTTPBridge:
	case OpenAIWSIngressModeCtxPool, OpenAIWSIngressModeShared, OpenAIWSIngressModeDedicated:
		if route.protocol.Transport != OpenAIUpstreamTransportResponsesWebsocketV2 {
			return route, fmt.Errorf("websocket ingress requires ws_v2 transport, got=%s", route.protocol.Transport)
		}
		if s.shouldBridgeOpenAIWSPassthroughFirstMessage(account, firstMessage) {
			route.mode = OpenAIWSIngressModeHTTPBridge
		}
	default:
		return route, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "websocket mode only supports ctx_pool/passthrough/http_bridge", nil)
	}
	return route, nil
}
