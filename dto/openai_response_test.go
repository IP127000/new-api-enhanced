package dto

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesBillingStreamResponseKeepsOnlyRelayFields(t *testing.T) {
	payload := `{
		"type":"response.completed",
		"response":{
			"instructions":"` + strings.Repeat("large-context-", 1024) + `",
			"tools":[{"type":"function","name":"lookup","description":"ignored"}],
			"usage":{
				"input_tokens":200000,
				"output_tokens":12,
				"total_tokens":200012,
				"input_tokens_details":{"cached_tokens":150000,"cache_write_tokens":3000}
			},
			"output":[{"type":"image_generation_call","quality":"high","size":"1024x1024","content":[{"type":"output_text","text":"ignored"}]}]
		}
	}`

	var got ResponsesBillingStreamResponse
	require.NoError(t, json.Unmarshal([]byte(payload), &got))
	require.Equal(t, "response.completed", got.Type)
	require.NotNil(t, got.Response)
	require.NotNil(t, got.Response.Usage)
	require.Equal(t, 200000, got.Response.Usage.InputTokens)
	require.Equal(t, 150000, got.Response.Usage.InputTokensDetails.CachedTokens)
	require.Equal(t, 3000, got.Response.Usage.InputTokensDetails.CacheWriteTokens)
	quality, size, ok := got.Response.ImageGenerationCall()
	require.True(t, ok)
	require.Equal(t, "high", quality)
	require.Equal(t, "1024x1024", size)
}

func TestResponsesBillingStreamResponseAllocatesFarLessThanFullResponse(t *testing.T) {
	var tools strings.Builder
	for i := 0; i < 500; i++ {
		if i > 0 {
			tools.WriteByte(',')
		}
		fmt.Fprintf(&tools, `{"type":"function","name":"tool_%d","description":"%s"}`,
			i, strings.Repeat("schema", 20))
	}
	payload := []byte(`{"type":"response.completed","response":{"tools":[` + tools.String() +
		`],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`)

	minimalAllocs := testing.AllocsPerRun(3, func() {
		var response ResponsesBillingStreamResponse
		if err := json.Unmarshal(payload, &response); err != nil {
			panic(err)
		}
	})
	fullAllocs := testing.AllocsPerRun(3, func() {
		var response ResponsesStreamResponse
		if err := json.Unmarshal(payload, &response); err != nil {
			panic(err)
		}
	})

	t.Logf("minimal allocations/run=%.0f, full allocations/run=%.0f", minimalAllocs, fullAllocs)
	require.Less(t, minimalAllocs*10, fullAllocs,
		"minimal Responses SSE parsing should not materialize the full tools object graph")
}
