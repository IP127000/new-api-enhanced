package relay

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type codexStorageWithoutBytes struct {
	common.BodyStorage
}

func (s *codexStorageWithoutBytes) Bytes() ([]byte, error) {
	return nil, errors.New("full-body Bytes call is forbidden")
}

func TestShouldUseCodexOriginalResponsesBodyRequiresCodexChannel(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:     appconstant.APITypeCodex,
			ChannelType: appconstant.ChannelTypeCodex,
		},
	}
	require.True(t, shouldUseCodexOriginalResponsesBody(info))

	info.ChannelType = appconstant.ChannelTypeOpenAI
	require.False(t, shouldUseCodexOriginalResponsesBody(info))

	info.ChannelType = appconstant.ChannelTypeCodex
	info.ApiType = appconstant.APITypeOpenAI
	require.False(t, shouldUseCodexOriginalResponsesBody(info))
}

func newCodexBodyTestContext(t *testing.T, body []byte) (*gin.Context, common.BodyStorage) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", http.NoBody)
	c.Request.Header.Set("Content-Type", "application/json")
	storage, err := common.CreateBodyStorage(body)
	require.NoError(t, err)
	c.Set(common.KeyBodyStorage, storage)
	c.Request.Body = io.NopCloser(storage)
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	return c, storage
}

func TestPrepareCodexOriginalResponsesBodyKeepsCompliantStorage(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":"hello","instructions":"","store":false,"stream":true}`)
	c, storage := newCodexBodyTestContext(t, body)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	outbound, owned, err := prepareCodexOriginalResponsesBody(c, storage, info, false, "")
	require.NoError(t, err)
	require.False(t, owned)
	require.Equal(t, storage, outbound)
	reader, err := outbound.NewReader()
	require.NoError(t, err)
	defer reader.Close()
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestPrepareCodexOriginalResponsesBodyKeepsAlreadyMappedStorage(t *testing.T) {
	body := []byte(`{"model":"gpt-upstream","input":"hello","instructions":"","store":false,"stream":true}`)
	c, storage := newCodexBodyTestContext(t, body)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	outbound, owned, err := prepareCodexOriginalResponsesBody(c, storage, info, false, "gpt-upstream")
	require.NoError(t, err)
	require.False(t, owned)
	require.Equal(t, storage, outbound)
}

func TestPrepareCodexOriginalResponsesBodyStreamsTopLevelRewrite(t *testing.T) {
	body := []byte(`{"model":"client","input":"` + strings.Repeat("x", 2<<20) + `","store":true,"temperature":0.7,"max_output_tokens":100,"future":{"keep":true}}`)
	c, storage := newCodexBodyTestContext(t, body)
	wrapped := &codexStorageWithoutBytes{BodyStorage: storage}
	c.Set(common.KeyBodyStorage, wrapped)
	c.Request.Body = io.NopCloser(wrapped)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	outbound, owned, err := prepareCodexOriginalResponsesBody(c, wrapped, info, false, "upstream")
	require.NoError(t, err)
	require.True(t, owned)
	defer outbound.Close()
	got, err := outbound.Bytes()
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, common.Unmarshal(got, &decoded))
	require.Equal(t, "upstream", decoded["model"])
	require.Equal(t, false, decoded["store"])
	require.Equal(t, "", decoded["instructions"])
	require.NotContains(t, decoded, "temperature")
	require.NotContains(t, decoded, "max_output_tokens")
	require.Equal(t, map[string]any{"keep": true}, decoded["future"])
	require.Len(t, decoded["input"].(string), 2<<20)

	storedOriginal, err := storage.Bytes()
	require.NoError(t, err)
	require.Equal(t, body, storedOriginal, "rewriting an attempt must preserve the replay body")
}

func TestPrepareCodexOriginalResponsesBodyHeaderOnlyOverrideDoesNotReadFullBody(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":"` + strings.Repeat("x", 2<<20) + `","instructions":"","store":false,"stream":true}`)
	c, storage := newCodexBodyTestContext(t, body)
	wrapped := &codexStorageWithoutBytes{BodyStorage: storage}
	c.Set(common.KeyBodyStorage, wrapped)
	c.Request.Body = io.NopCloser(wrapped)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]interface{}{
				"operations": []map[string]interface{}{
					{
						"mode":        "pass_headers",
						"value":       []string{"Originator"},
						"keep_origin": true,
					},
				},
			},
		},
		RequestHeaders: map[string]string{"Originator": "codex_cli_rs"},
	}
	outbound, owned, err := prepareCodexOriginalResponsesBody(c, wrapped, info, false, "")
	require.NoError(t, err)
	require.False(t, owned)
	require.Equal(t, wrapped, outbound)
	require.Equal(t, "codex_cli_rs", relaycommon.GetEffectiveHeaderOverride(info)["originator"])
}

func TestPrepareCodexOriginalResponsesBodyPreservesParamOverrideReturnError(t *testing.T) {
	c, storage := newCodexBodyTestContext(t, []byte(`{"model":"gpt-5.6-sol","input":"hello","instructions":"","store":false}`))
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ParamOverride: map[string]interface{}{
			"operations": []map[string]interface{}{{
				"mode": "return_error",
				"value": map[string]interface{}{
					"message":     "blocked by channel rule",
					"status_code": http.StatusTeapot,
					"code":        "channel_rule_block",
				},
			}},
		},
	}}

	_, _, err := prepareCodexOriginalResponsesBody(c, storage, info, false, "")
	require.Error(t, err)
	var applyErr *codexParamOverrideApplyError
	require.ErrorAs(t, err, &applyErr)
	apiErr := newAPIErrorFromParamOverride(err)
	require.Equal(t, http.StatusTeapot, apiErr.StatusCode)
	require.Equal(t, "channel_rule_block", string(apiErr.GetErrorCode()))
}

func TestCommitCodexResponsesStreamReleasesBodiesWithoutCancelingClient(t *testing.T) {
	c, original := newCodexBodyTestContext(t, []byte(`{"model":"gpt-5.6-sol","input":"hello"}`))
	_, err := common.GetOrIndexJSONBodyFields(c, original)
	require.NoError(t, err)
	outbound, err := common.CreateBodyStorage([]byte(`{"model":"gpt-5.6-sol","input":"hello","instructions":"","store":false}`))
	require.NoError(t, err)

	commitCodexResponsesStream(c, outbound, true)
	require.True(t, common.GetContextKeyBool(c, appconstant.ContextKeyRelayRetryCommitted))
	require.Equal(t, http.NoBody, c.Request.Body)
	_, indexed := common.GetContextKeyType[[]common.JSONFieldSpan](c, appconstant.ContextKeyJSONBodyTopLevelFields)
	require.False(t, indexed)
	_, err = common.GetBodyStorage(c)
	require.Error(t, err)
	_, err = original.Bytes()
	require.ErrorIs(t, err, common.ErrStorageClosed)
	_, err = outbound.Bytes()
	require.ErrorIs(t, err, common.ErrStorageClosed)
	select {
	case <-c.Request.Context().Done():
		t.Fatal("releasing request bodies must not cancel the downstream request context")
	default:
	}
}

func TestSanitizeCodexOriginalResponsesBodyAddsRequiredFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.6-sol","input":"hello","store":true,"temperature":0.7,"max_output_tokens":100,"future_field":{"keep":true}}`)
	got, changed, err := sanitizeCodexOriginalResponsesBody(body, false, "")
	require.NoError(t, err)
	require.True(t, changed)

	var decoded map[string]interface{}
	require.NoError(t, common.Unmarshal(got, &decoded))
	require.Equal(t, false, decoded["store"])
	require.Equal(t, "", decoded["instructions"])
	require.NotContains(t, decoded, "temperature")
	require.NotContains(t, decoded, "max_output_tokens")
	require.Equal(t, map[string]interface{}{"keep": true}, decoded["future_field"])
}

func TestSanitizeCodexOriginalResponsesBodyKeepsCompactParityFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.6-sol","store":true,"temperature":0.7,"max_output_tokens":100,"tools":[{"type":"custom"}]}`)
	got, changed, err := sanitizeCodexOriginalResponsesBody(body, true, "")
	require.NoError(t, err)
	require.True(t, changed)

	var decoded map[string]interface{}
	require.NoError(t, common.Unmarshal(got, &decoded))
	require.Equal(t, true, decoded["store"])
	require.Equal(t, 0.7, decoded["temperature"])
	require.EqualValues(t, 100, decoded["max_output_tokens"])
	require.Equal(t, "", decoded["instructions"])
	require.Contains(t, decoded, "tools")
}

func TestSanitizeCodexOriginalResponsesBodyReturnsOriginalWhenCompliant(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.6-sol","input":"hello","instructions":"","store":false,"future_field":{"keep":true}}`)
	got, changed, err := sanitizeCodexOriginalResponsesBody(body, false, "")
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)
	require.Same(t, &body[0], &got[0], "no-op sanitization must retain the original backing array")
}

func TestSanitizeCodexOriginalResponsesBodyAppliesMappedModel(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-client","input":"hello","instructions":"","store":false}`)
	got, changed, err := sanitizeCodexOriginalResponsesBody(body, false, "gpt-upstream")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "gpt-upstream", gjson.GetBytes(got, "model").String())
}

func TestSanitizeCodexOriginalResponsesBodyNoOpAllocationBound(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.6-sol","input":"` + strings.Repeat("x", 2<<20) + `","instructions":"","store":false}`)
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			got, changed, err := sanitizeCodexOriginalResponsesBody(body, false, "")
			if err != nil || changed || len(got) != len(body) {
				b.Fatalf("unexpected sanitization result: changed=%v err=%v", changed, err)
			}
		}
	})
	t.Logf("no-op sanitizer allocated %d bytes/op for a %d-byte request", result.AllocedBytesPerOp(), len(body))
	require.Less(t, result.AllocedBytesPerOp(), int64(64<<10),
		"no-op sanitization must not allocate in proportion to request size")
}
