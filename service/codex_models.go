package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/constant"
)

// FetchCodexModels retrieves the ChatGPT Codex model catalog with a
// subscription OAuth credential. The Codex client expects the backend catalog
// shape ({"models":[...]}), not the public OpenAI /v1/models shape.
func FetchCodexModels(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	accessToken string,
	accountID string,
	rawQuery string,
	clientHeaders http.Header,
) (int, http.Header, []byte, error) {
	if client == nil {
		return 0, nil, nil, fmt.Errorf("codex models: http client is required")
	}

	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/") + "/backend-api/codex/models")
	if err != nil {
		return 0, nil, nil, fmt.Errorf("codex models: invalid base URL: %w", err)
	}
	endpoint.RawQuery = rawQuery

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, nil, nil, err
	}
	for _, name := range constant.CodexClientPassThroughHeaders() {
		for _, value := range clientHeaders.Values(name) {
			req.Header.Add(name, value)
		}
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("chatgpt-account-id", strings.TrimSpace(accountID))
	req.Header.Set("Accept", "application/json")
	if req.Header.Get("originator") == "" {
		req.Header.Set("originator", "codex_cli_rs")
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, resp.Header.Clone(), nil, err
	}
	return resp.StatusCode, resp.Header.Clone(), body, nil
}
