package service

import (
	"fmt"
	"strings"

	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

type openAIWSIngressRoute struct {
	protocol OpenAIWSProtocolDecision
	mode     string
}

// This is shared by owner registration and transport execution. In particular,
// Astra's automatic duplex relay must be selected before claiming an owner.
func (s *OpenAIGatewayService) resolveOpenAIWSIngressRoute(account *Account, firstMessage []byte, forwardModel string) (openAIWSIngressRoute, error) {
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
	model := strings.TrimSpace(forwardModel)
	if model == "" {
		model = strings.TrimSpace(gjson.GetBytes(firstMessage, "model").String())
	}
	model = normalizeOpenAIModelForUpstream(account, account.GetMappedModel(model))
	if route.mode != OpenAIWSIngressModePassthrough && route.protocol.Transport == OpenAIUpstreamTransportResponsesWebsocketV2 && isOpenAIGPT6AstraModel(model) {
		route.mode = OpenAIWSIngressModePassthrough
		return route, nil
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
