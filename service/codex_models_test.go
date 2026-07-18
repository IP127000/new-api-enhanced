package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchCodexModelsUsesSubscriptionHeadersAndPreservesQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/backend-api/codex/models", r.URL.Path)
		require.Equal(t, "0.114.0", r.URL.Query().Get("client_version"))
		require.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))
		require.Equal(t, "account-123", r.Header.Get("chatgpt-account-id"))
		require.Equal(t, "Codex CLI", r.Header.Get("originator"))
		require.Equal(t, "session-123", r.Header.Get("Session_id"))
		require.Equal(t, "true", r.Header.Get("X-OpenAI-Internal-Codex-Responses-Lite"))
		require.Empty(t, r.Header.Get("x-openai-actor-authorization"))
		w.Header().Set("ETag", `"models-etag"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()

	status, headers, body, err := FetchCodexModels(
		context.Background(),
		server.Client(),
		server.URL,
		"access-token",
		"account-123",
		"client_version=0.114.0",
		http.Header{
			"Authorization":                          []string{"Bearer client-token"},
			"Originator":                             []string{"Codex CLI"},
			"Session_id":                             []string{"session-123"},
			"X-Openai-Actor-Authorization":           []string{"new-api-enhanced"},
			"X-Openai-Internal-Codex-Responses-Lite": []string{"true"},
		},
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, `"models-etag"`, headers.Get("ETag"))
	require.JSONEq(t, `{"models":[]}`, string(body))
}
