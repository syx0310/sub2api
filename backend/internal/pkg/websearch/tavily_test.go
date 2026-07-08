package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTavilyProvider_Name(t *testing.T) {
	p := NewTavilyProvider("key", nil)
	require.Equal(t, "tavily", p.Name())

	hikari := NewTavilyHikariProvider("key", "https://hikari.example.com/api/tavily", nil)
	require.Equal(t, "tavily_hikari", hikari.Name())
}

func TestTavilyProvider_Search_RequestConstruction(t *testing.T) {
	// Verify tavilyRequest struct fields map correctly
	req := tavilyRequest{
		APIKey:      "test-key",
		Query:       "golang",
		MaxResults:  3,
		SearchDepth: tavilySearchDepthBasic,
	}
	data, err := json.Marshal(req)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	require.Equal(t, "test-key", parsed["api_key"])
	require.Equal(t, "golang", parsed["query"])
	require.Equal(t, float64(3), parsed["max_results"])
	require.Equal(t, "basic", parsed["search_depth"])
}

func TestTavilyProvider_Search_OfficialModeSendsAPIKeyInBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/search", r.URL.Path)
		require.Empty(t, r.Header.Get("Authorization"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "tvly-official", body["api_key"])
		require.Equal(t, "golang", body["query"])
		require.Equal(t, float64(3), body["max_results"])

		_ = json.NewEncoder(w).Encode(map[string]any{
			"query": "golang",
			"results": []map[string]any{{
				"url":     "https://go.dev",
				"title":   "Go",
				"content": "Go programming language",
				"score":   0.95,
			}},
		})
	}))
	defer srv.Close()

	p := NewTavilyProviderWithBaseURL("tvly-official", srv.URL, srv.Client())
	resp, err := p.Search(context.Background(), SearchRequest{Query: "golang", MaxResults: 3})
	require.NoError(t, err)
	require.Equal(t, "golang", resp.Query)
	require.Len(t, resp.Results, 1)
	require.Equal(t, "Go programming language", resp.Results[0].Snippet)
}

func TestTavilyHikariProvider_Search_UsesBearerAndOmitsBodyAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/tavily/search", r.URL.Path)
		require.Equal(t, "Bearer th-test-token", r.Header.Get("Authorization"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.NotContains(t, body, "api_key")
		require.Equal(t, "hikari query", body["query"])

		_ = json.NewEncoder(w).Encode(map[string]any{
			"query": "hikari query",
			"results": []map[string]any{{
				"url":            "https://example.com",
				"title":          "Example",
				"content":        "Hikari result",
				"published_date": "2026-07-08",
				"score":          "0.98",
			}},
		})
	}))
	defer srv.Close()

	p := NewTavilyHikariProvider("th-test-token", srv.URL+"/api/tavily", srv.Client())
	resp, err := p.Search(context.Background(), SearchRequest{Query: "hikari query"})
	require.NoError(t, err)
	require.Equal(t, "hikari query", resp.Query)
	require.Len(t, resp.Results, 1)
	require.Equal(t, "Hikari result", resp.Results[0].Snippet)
	require.Equal(t, "2026-07-08", resp.Results[0].PageAge)
}

func TestTavilyHikariProvider_Search_RequiresCustomBaseURL(t *testing.T) {
	p := NewTavilyHikariProvider("th-test-token", "", nil)
	_, err := p.Search(context.Background(), SearchRequest{Query: "q"})
	require.ErrorContains(t, err, "Hikari provider requires api_base_url")
}

func TestTavilySearchURLFromBase(t *testing.T) {
	endpoint, err := tavilySearchURLFromBase("https://hikari.example.com/api/tavily")
	require.NoError(t, err)
	require.Equal(t, "https://hikari.example.com/api/tavily/search", endpoint)

	endpoint, err = tavilySearchURLFromBase("https://hikari.example.com/api/tavily/search")
	require.NoError(t, err)
	require.Equal(t, "https://hikari.example.com/api/tavily/search", endpoint)

	endpoint, err = tavilySearchURLFromBase("https://hikari.example.com/prefix/api/tavily/search/")
	require.NoError(t, err)
	require.Equal(t, "https://hikari.example.com/prefix/api/tavily/search", endpoint)

	_, err = tavilySearchURLFromBase("ftp://hikari.example.com/api/tavily")
	require.ErrorContains(t, err, "http or https")
}

func TestTavilyHikariSearchURLFromBase(t *testing.T) {
	endpoint, err := tavilyHikariSearchURLFromBase("https://hikari.example.com/api/tavily")
	require.NoError(t, err)
	require.Equal(t, "https://hikari.example.com/api/tavily/search", endpoint)

	endpoint, err = tavilyHikariSearchURLFromBase("https://hikari.example.com/api/tavily/search")
	require.NoError(t, err)
	require.Equal(t, "https://hikari.example.com/api/tavily/search", endpoint)

	_, err = tavilyHikariSearchURLFromBase("http://192.168.10.23:3012/mcp")
	require.ErrorContains(t, err, "not MCP /mcp")

	_, err = tavilyHikariSearchURLFromBase("https://hikari.example.com/prefix/mcp")
	require.ErrorContains(t, err, "not MCP /mcp")
}

func TestTavilyProvider_Search_ResponseParsing(t *testing.T) {
	rawResp := `{"query":"golang","results":[{"url":"https://go.dev","title":"Go","content":"Go programming language","score":"0.95"}]}`
	var resp tavilyResponse
	require.NoError(t, json.Unmarshal([]byte(rawResp), &resp))
	require.Equal(t, "golang", resp.Query)
	require.Len(t, resp.Results, 1)
	require.Equal(t, "https://go.dev", resp.Results[0].URL)
	require.Equal(t, "Go programming language", resp.Results[0].Content)

	// Verify mapping to SearchResult
	results := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, SearchResult{
			URL: r.URL, Title: r.Title, Snippet: firstNonEmptyString(r.Content, r.RawContent),
		})
	}
	require.Equal(t, "Go programming language", results[0].Snippet)
	require.Equal(t, "", results[0].PageAge)
}

func TestTavilyProvider_Search_EmptyResults(t *testing.T) {
	var resp tavilyResponse
	require.NoError(t, json.Unmarshal([]byte(`{"results":[]}`), &resp))
	require.Empty(t, resp.Results)
}

func TestTavilyProvider_Search_InvalidJSON(t *testing.T) {
	var resp tavilyResponse
	require.Error(t, json.Unmarshal([]byte("not json"), &resp))
}
