package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponseChunkDataByTypePreservesResponsesSSEWireFormat(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	err := ResponseChunkDataByType(c, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"hello"}`)
	require.NoError(t, err)
	require.Equal(t,
		"event: response.output_text.delta\n"+
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n",
		recorder.Body.String())
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
}
