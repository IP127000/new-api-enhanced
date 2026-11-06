package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRealtimeCallHelperPreservesSdpAndLocation(t *testing.T) {
	for _, path := range []string{"/v1/realtime/calls", "/v1/live"} {
		t.Run(path, func(t *testing.T) {
			service.InitHttpClient()

			const requestBody = "--voice-boundary\r\nContent-Disposition: form-data; name=\"sdp\"\r\n\r\nv=offer\r\n--voice-boundary--\r\n"
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, path, r.URL.Path)
				require.Equal(t, "Bearer server-token", r.Header.Get("Authorization"))
				require.Equal(t, "server-account", r.Header.Get("chatgpt-account-id"))
				require.Empty(t, r.Header.Get("x-openai-actor-authorization"))
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Equal(t, requestBody, string(body))
				w.Header().Set("Content-Type", "application/sdp")
				w.Header().Set("Location", "/v1/realtime/calls/rtc_proxy_test")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte("v=answer\r\n"))
			}))
			t.Cleanup(upstream.Close)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, path,
				strings.NewReader(requestBody))
			c.Request.Header.Set("Authorization", "Bearer downstream-token")
			c.Request.Header.Set("x-openai-actor-authorization", "client-marker")
			c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=voice-boundary")
			c.Set("original_model", "gpt-realtime-1.5")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeCodex)
			common.SetContextKey(c, constant.ContextKeyChannelId, 965)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey,
				`{"access_token":"server-token","account_id":"server-account"}`)
			t.Cleanup(func() { common.CleanupBodyStorage(c) })

			require.Nil(t, RealtimeCallHelper(c))
			require.Equal(t, http.StatusCreated, recorder.Code)
			require.Equal(t, "application/sdp", recorder.Header().Get("Content-Type"))
			require.Equal(t, "/v1/realtime/calls/rtc_proxy_test", recorder.Header().Get("Location"))
			require.Equal(t, "v=answer\r\n", recorder.Body.String())
			binding, found, err := service.LookupRealtimeCall(c, "rtc_proxy_test")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, 965, binding.ChannelID)
			require.Equal(t, "server-account", binding.AccountID)
		})
	}
}
