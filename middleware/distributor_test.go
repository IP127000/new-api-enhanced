package middleware

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type distributorStorageWithoutBytes struct {
	common.BodyStorage
}

func (s *distributorStorageWithoutBytes) Bytes() ([]byte, error) {
	return nil, errors.New("full-body Bytes call is forbidden")
}

func TestGetModelFromJSONBodyDoesNotMaterializeLargeBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-sol","group":"default","input":"` + strings.Repeat("x", 2<<20) + `"}`)
	storage, err := common.CreateBodyStorage(body)
	require.NoError(t, err)
	wrapper := &distributorStorageWithoutBytes{BodyStorage: storage}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", http.NoBody)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Body = io.NopCloser(wrapper)
	c.Set(common.KeyBodyStorage, wrapper)
	t.Cleanup(func() { common.CleanupBodyStorage(c) })

	request, err := getModelFromJSONBody(c)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-sol", request.Model)
	require.Equal(t, "default", request.Group)
}

func TestGetModelRequestReadsFramelessRealtimeModelFromQuery(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet,
		"/v1/live?model=gpt-live-1-codex", http.NoBody)

	request, shouldSelectChannel, err := getModelRequest(c)
	require.NoError(t, err)
	require.True(t, shouldSelectChannel)
	require.Equal(t, "gpt-live-1-codex", request.Model)
}

func TestGetModelRequestReadsWebRtcSidebandModelFromRelayHeader(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet,
		"/v1/realtime?call_id=rtc_test", http.NoBody)
	c.Request.Header.Set("X-Codex-Realtime-Model", "gpt-realtime-1.5")

	request, shouldSelectChannel, err := getModelRequest(c)
	require.NoError(t, err)
	require.True(t, shouldSelectChannel)
	require.Equal(t, "gpt-realtime-1.5", request.Model)
}

func TestGetModelRequestReadsRealtimeCallModelFromMultipartSession(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("sdp", "v=0\r\n"))
	require.NoError(t, writer.WriteField("session", `{"type":"realtime","model":"gpt-realtime-1.5"}`))
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	t.Cleanup(func() { common.CleanupBodyStorage(c) })

	request, shouldSelectChannel, err := getModelRequest(c)
	require.NoError(t, err)
	require.True(t, shouldSelectChannel)
	require.Equal(t, "gpt-realtime-1.5", request.Model)
}
