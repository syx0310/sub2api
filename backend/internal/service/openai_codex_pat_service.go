package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const openAICodexPATWhoamiURLDefault = "https://auth.openai.com/api/accounts/v1/user-auth-credential/whoami"

var openAICodexPATWhoamiURL = openAICodexPATWhoamiURLDefault

type openAICodexPATWhoamiResponse struct {
	Email                   string `json:"email"`
	ChatGPTUserID           string `json:"chatgpt_user_id"`
	ChatGPTAccountID        string `json:"chatgpt_account_id"`
	ChatGPTPlanType         string `json:"chatgpt_plan_type"`
	ChatGPTAccountIsFedRAMP *bool  `json:"chatgpt_account_is_fedramp"`
}

// ValidateCodexPersonalAccessToken validates a Codex at-* token using the same
// first-class PAT endpoint used by the Codex client.
func (s *OpenAIOAuthService) ValidateCodexPersonalAccessToken(ctx context.Context, accessToken, proxyURL string) (*OpenAITokenInfo, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_CODEX_PAT_REQUIRED", "access token is required")
	}
	if !strings.HasPrefix(accessToken, "at-") {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_CODEX_PAT_INVALID_PREFIX", "Codex personal access token must start with at-")
	}

	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:              proxyURL,
		Timeout:               20 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	})
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "OPENAI_CODEX_PAT_PROXY_INVALID", "invalid proxy configuration: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAICodexPATWhoamiURL, nil)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "OPENAI_CODEX_PAT_REQUEST_FAILED", "failed to build validation request: %v", err)
	}
	req.Header.Set("authorization", "Bearer "+accessToken)
	req.Header.Set("accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_PAT_VALIDATE_FAILED", "failed to validate Codex personal access token: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_CODEX_PAT_INVALID", "Codex personal access token is invalid or expired")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_PAT_VALIDATE_FAILED", "Codex personal access token validation failed: %s", message)
	}

	var whoami openAICodexPATWhoamiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&whoami); err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_PAT_RESPONSE_INVALID", "invalid Codex personal access token validation response: %v", err)
	}
	if err := validateOpenAICodexPATWhoami(whoami); err != nil {
		return nil, err
	}

	return &OpenAITokenInfo{
		AccessToken:           accessToken,
		AuthMode:              OpenAIAuthModePersonalAccessToken,
		Email:                 strings.TrimSpace(whoami.Email),
		ChatGPTAccountID:      strings.TrimSpace(whoami.ChatGPTAccountID),
		ChatGPTUserID:         strings.TrimSpace(whoami.ChatGPTUserID),
		ChatGPTAccountFedRAMP: *whoami.ChatGPTAccountIsFedRAMP,
		PlanType:              strings.TrimSpace(whoami.ChatGPTPlanType),
	}, nil
}

func validateOpenAICodexPATWhoami(whoami openAICodexPATWhoamiResponse) error {
	required := map[string]string{
		"email":              whoami.Email,
		"chatgpt_user_id":    whoami.ChatGPTUserID,
		"chatgpt_account_id": whoami.ChatGPTAccountID,
		"chatgpt_plan_type":  whoami.ChatGPTPlanType,
	}
	for key, value := range required {
		if strings.TrimSpace(value) == "" {
			return infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_PAT_RESPONSE_INVALID", "Codex personal access token validation response is missing %s", key)
		}
	}
	if whoami.ChatGPTAccountIsFedRAMP == nil {
		return infraerrors.New(http.StatusBadGateway, "OPENAI_CODEX_PAT_RESPONSE_INVALID", "Codex personal access token validation response is missing chatgpt_account_is_fedramp")
	}
	return nil
}
