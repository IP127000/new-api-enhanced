package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIsCodexModelsRequestUsesActorAuthorizationMarker(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	require.False(t, IsCodexModelsRequest(c))

	c.Request.Header.Set("x-openai-actor-authorization", "new-api-enhanced")
	require.True(t, IsCodexModelsRequest(c))
}
