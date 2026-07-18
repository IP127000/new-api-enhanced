package dto

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// CodexWebSearchRequest is the standalone search payload used by Codex
// Responses Lite. Unknown fields stay in the reusable raw request body and are
// forwarded unchanged; this DTO only exposes fields needed for routing,
// validation and accounting.
type CodexWebSearchRequest struct {
	ID              string                 `json:"id,omitempty"`
	Model           string                 `json:"model"`
	Input           json.RawMessage        `json:"input,omitempty"`
	Commands        json.RawMessage        `json:"commands,omitempty"`
	Settings        CodexWebSearchSettings `json:"settings,omitempty"`
	MaxOutputTokens *uint                  `json:"max_output_tokens,omitempty"`
}

type CodexWebSearchSettings struct {
	SearchContextSize string `json:"search_context_size,omitempty"`
}

func (r *CodexWebSearchRequest) GetTokenCountMeta() *types.TokenCountMeta {
	parts := make([]string, 0, 3)
	for _, raw := range []json.RawMessage{r.Input, r.Commands} {
		if len(raw) > 0 {
			parts = append(parts, string(raw))
		}
	}
	return &types.TokenCountMeta{
		CombineText: strings.Join(parts, "\n"),
		MaxTokens:   intValue(r.MaxOutputTokens),
	}
}

func intValue(value *uint) int {
	if value == nil {
		return 0
	}
	return int(*value)
}

func (r *CodexWebSearchRequest) IsStream(*gin.Context) bool { return false }

func (r *CodexWebSearchRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}
