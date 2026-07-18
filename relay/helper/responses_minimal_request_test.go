package helper

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bodyStorageWithoutBytes struct {
	common.BodyStorage
}

func (s *bodyStorageWithoutBytes) Bytes() ([]byte, error) {
	return nil, errors.New("full-body Bytes call is forbidden")
}

func newResponsesRequestContext(t *testing.T, body []byte, channelType int) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", http.NoBody)
	c.Request.Header.Set("Content-Type", "application/json")
	storage, err := common.CreateBodyStorage(body)
	require.NoError(t, err)
	c.Set(common.KeyBodyStorage, storage)
	c.Request.Body = io.NopCloser(storage)
	common.SetContextKey(c, constant.ContextKeyChannelType, channelType)
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	return c
}

func largeResponsesRequestBody() []byte {
	largeInput := strings.Repeat("x", 1<<20)
	largeSchema := strings.Repeat("y", 1<<20)
	return []byte(`{"model":"gpt-5.6-sol","input":"` + largeInput + `","instructions":"","store":false,"stream":true,"max_output_tokens":4096,"tools":[{"type":"web_search","search_context_size":"high","schema":"` + largeSchema + `"},{"type":"image_generation"}]}`)
}

func enableMinimalCodexResponsesForTest(t *testing.T) {
	t.Helper()
	originalSelfUse := operation_setting.SelfUseModeEnabled
	originalSensitive := setting.CheckSensitiveEnabled
	originalPromptSensitive := setting.CheckSensitiveOnPromptEnabled
	operation_setting.SelfUseModeEnabled = true
	setting.CheckSensitiveEnabled = false
	setting.CheckSensitiveOnPromptEnabled = false
	t.Cleanup(func() {
		operation_setting.SelfUseModeEnabled = originalSelfUse
		setting.CheckSensitiveEnabled = originalSensitive
		setting.CheckSensitiveOnPromptEnabled = originalPromptSensitive
	})
}

func TestMinimalCodexResponsesRequestKeepsOnlyRelayMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	enableMinimalCodexResponsesForTest(t)

	body := largeResponsesRequestBody()
	c := newResponsesRequestContext(t, body, constant.ChannelTypeCodex)
	request, err := GetAndValidateResponsesRequest(c)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-sol", request.Model)
	require.Equal(t, "true", string(request.Input))
	require.True(t, *request.Stream)
	require.EqualValues(t, 4096, *request.MaxOutputTokens)
	require.Less(t, len(request.Tools), 256)
	require.JSONEq(t, `[{"type":"web_search","search_context_size":"high"},{"type":"image_generation"}]`, string(request.Tools))
	require.True(t, common.GetContextKeyBool(c, constant.ContextKeyCodexResponsesMinimalRequest))

	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	stored, err := storage.Bytes()
	require.NoError(t, err)
	require.Equal(t, body, stored)
}

func TestMinimalCodexResponsesRequestDoesNotReadFullBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	enableMinimalCodexResponsesForTest(t)

	body := largeResponsesRequestBody()
	c := newResponsesRequestContext(t, body, constant.ChannelTypeCodex)
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	wrapped := &bodyStorageWithoutBytes{BodyStorage: storage}
	c.Set(common.KeyBodyStorage, wrapped)
	c.Request.Body = io.NopCloser(wrapped)

	request, err := GetAndValidateResponsesRequest(c)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-sol", request.Model)
	require.JSONEq(t, `[{"type":"web_search","search_context_size":"high"},{"type":"image_generation"}]`, string(request.Tools))
}

func TestResponsesRequestUsesFullDecodeOutsideSelfUseCodex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	enableMinimalCodexResponsesForTest(t)

	body := largeResponsesRequestBody()
	c := newResponsesRequestContext(t, body, constant.ChannelTypeOpenAI)
	request, err := GetAndValidateResponsesRequest(c)
	require.NoError(t, err)
	require.Greater(t, len(request.Input), 1<<20)
	require.Greater(t, len(request.Tools), 1<<20)
	require.False(t, common.GetContextKeyBool(c, constant.ContextKeyCodexResponsesMinimalRequest))
}

func TestMinimalCodexResponsesRequestRejectsOversizedMaxTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	enableMinimalCodexResponsesForTest(t)

	body := []byte(`{"model":"gpt-5.6-sol","input":"hello","max_output_tokens":18446744073686646784}`)
	c := newResponsesRequestContext(t, body, constant.ChannelTypeCodex)
	_, err := GetAndValidateResponsesRequest(c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "max_output_tokens is invalid")
}

func TestMinimalCodexResponsesRequestAllocationBound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	enableMinimalCodexResponsesForTest(t)

	body := largeResponsesRequestBody()
	c := newResponsesRequestContext(t, body, constant.ChannelTypeCodex)
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			request, err := GetAndValidateResponsesRequest(c)
			if err != nil || request.Model == "" {
				b.Fatalf("minimal parse failed: %v", err)
			}
		}
	})
	t.Logf("minimal parser allocated %d bytes/op for a %d-byte request", result.AllocedBytesPerOp(), len(body))
	require.Less(t, result.AllocedBytesPerOp(), int64(128<<10),
		"minimal parsing must not allocate in proportion to the 2 MiB input/tool body")

	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	_, err = storage.Seek(0, io.SeekStart)
	require.NoError(t, err)
	stored, err := storage.Bytes()
	require.NoError(t, err)
	require.True(t, bytes.Equal(body, stored))
}

func TestResponsesRequestUsesFullDecodeWhenSensitiveCheckEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalSelfUse := operation_setting.SelfUseModeEnabled
	originalSensitive := setting.CheckSensitiveEnabled
	originalPromptSensitive := setting.CheckSensitiveOnPromptEnabled
	operation_setting.SelfUseModeEnabled = true
	setting.CheckSensitiveEnabled = true
	setting.CheckSensitiveOnPromptEnabled = true
	t.Cleanup(func() {
		operation_setting.SelfUseModeEnabled = originalSelfUse
		setting.CheckSensitiveEnabled = originalSensitive
		setting.CheckSensitiveOnPromptEnabled = originalPromptSensitive
	})

	body := []byte(`{"model":"gpt-5.6-sol","input":"sensitive content"}`)
	c := newResponsesRequestContext(t, body, constant.ChannelTypeCodex)
	request, err := GetAndValidateResponsesRequest(c)
	require.NoError(t, err)
	require.Equal(t, `"sensitive content"`, string(request.Input))
	require.False(t, common.GetContextKeyBool(c, constant.ContextKeyCodexResponsesMinimalRequest))
}
