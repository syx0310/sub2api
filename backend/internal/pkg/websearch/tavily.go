package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	tavilyOfficialBaseURL      = "https://api.tavily.com"
	tavilySearchEndpoint       = tavilyOfficialBaseURL + "/search"
	tavilyProviderName         = "tavily"
	tavilySearchDepthBasic     = "basic"
	tavilyHikariAuthHeaderName = "Authorization"
)

// TavilyProvider implements web search via the Tavily Search API.
type TavilyProvider struct {
	apiKey     string
	httpClient *http.Client
	name       string
	searchURL  string
	initErr    error
	bearerAuth bool
}

// NewTavilyProvider creates a Tavily Search provider.
// The caller is responsible for configuring the http.Client with proxy/timeouts.
func NewTavilyProvider(apiKey string, httpClient *http.Client) *TavilyProvider {
	return newTavilyProvider(tavilyProviderName, apiKey, tavilySearchEndpoint, false, nil, httpClient)
}

// NewTavilyProviderWithBaseURL creates a Tavily Search provider with an optional
// Tavily-compatible base URL while preserving official Tavily body api_key auth.
func NewTavilyProviderWithBaseURL(apiKey, apiBaseURL string, httpClient *http.Client) *TavilyProvider {
	endpoint, err := tavilySearchURLFromBase(apiBaseURL)
	return newTavilyProvider(tavilyProviderName, apiKey, endpoint, false, err, httpClient)
}

// NewTavilyHikariProvider creates a Tavily Search provider targeting a Hikari
// /api/tavily facade. It uses Bearer token auth and never forwards api_key in
// the JSON body.
func NewTavilyHikariProvider(apiKey, apiBaseURL string, httpClient *http.Client) *TavilyProvider {
	endpoint, err := tavilyHikariSearchURLFromBase(apiBaseURL)
	return newTavilyProvider(ProviderTypeTavilyHikari, apiKey, endpoint, true, err, httpClient)
}

func newTavilyProvider(name, apiKey, searchURL string, bearerAuth bool, initErr error, httpClient *http.Client) *TavilyProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	apiKey = strings.TrimSpace(apiKey)
	return &TavilyProvider{
		apiKey:     apiKey,
		httpClient: httpClient,
		name:       name,
		searchURL:  searchURL,
		initErr:    initErr,
		bearerAuth: bearerAuth,
	}
}

func (t *TavilyProvider) Name() string { return t.name }

func (t *TavilyProvider) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	if t.initErr != nil {
		return nil, t.initErr
	}

	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}

	payload := tavilyRequest{
		Query:       req.Query,
		MaxResults:  maxResults,
		SearchDepth: tavilySearchDepthBasic,
	}
	if !t.bearerAuth {
		payload.APIKey = t.apiKey
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("tavily: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.searchURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("tavily: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if t.bearerAuth {
		httpReq.Header.Set(tavilyHikariAuthHeaderName, "Bearer "+t.apiKey)
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tavily: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("tavily: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily: status %d: %s", resp.StatusCode, truncateBody(body))
	}

	var raw tavilyResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("tavily: decode response: %w", err)
	}

	results := make([]SearchResult, 0, len(raw.Results))
	for _, r := range raw.Results {
		results = append(results, SearchResult{
			URL:     r.URL,
			Title:   r.Title,
			Snippet: firstNonEmptyString(r.Content, r.RawContent),
			PageAge: r.PublishedDate,
		})
	}

	query := firstNonEmptyString(raw.Query, req.Query)
	return &SearchResponse{Results: results, Query: query}, nil
}

func tavilySearchURLFromBase(apiBaseURL string) (string, error) {
	apiBaseURL = strings.TrimSpace(apiBaseURL)
	if apiBaseURL == "" {
		return tavilySearchEndpoint, nil
	}
	parsed, err := parseTavilyAPIBaseURL(apiBaseURL)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, "/search") {
		path += "/search"
	}
	parsed.Path = path
	return parsed.String(), nil
}

func tavilyHikariSearchURLFromBase(apiBaseURL string) (string, error) {
	apiBaseURL = strings.TrimSpace(apiBaseURL)
	if apiBaseURL == "" {
		return "", fmt.Errorf("tavily: Hikari provider requires api_base_url ending with /api/tavily")
	}
	parsed, err := parseTavilyAPIBaseURL(apiBaseURL)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/api/tavily/search"):
	case strings.HasSuffix(path, "/api/tavily"):
		path += "/search"
	default:
		return "", fmt.Errorf("tavily: api_base_url must be Hikari HTTP API base ending with /api/tavily, not MCP /mcp")
	}
	parsed.Path = path
	return parsed.String(), nil
}

func parseTavilyAPIBaseURL(apiBaseURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(apiBaseURL))
	if err != nil {
		return nil, fmt.Errorf("tavily: invalid api_base_url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("tavily: api_base_url must use http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("tavily: api_base_url must include host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("tavily: api_base_url must not include query or fragment")
	}
	return parsed, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type tavilyRequest struct {
	APIKey      string `json:"api_key,omitempty"`
	Query       string `json:"query"`
	MaxResults  int    `json:"max_results"`
	SearchDepth string `json:"search_depth"`
}

type tavilyResponse struct {
	Query   string         `json:"query"`
	Results []tavilyResult `json:"results"`
}

type tavilyResult struct {
	URL           string `json:"url"`
	Title         string `json:"title"`
	Content       string `json:"content"`
	RawContent    string `json:"raw_content"`
	PublishedDate string `json:"published_date"`
}
