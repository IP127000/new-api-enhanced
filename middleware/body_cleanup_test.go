package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBodyStorageCleanupRunsDuringPanicUnwind(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var storage common.BodyStorage

	router := gin.New()
	router.Use(gin.CustomRecovery(func(c *gin.Context, _ any) {
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	router.Use(BodyStorageCleanup())
	router.POST("/panic", func(c *gin.Context) {
		var err error
		storage, err = common.CreateBodyStorage(bytes.Repeat([]byte("x"), 1<<20))
		require.NoError(t, err)
		c.Set(common.KeyBodyStorage, storage)
		panic("test panic")
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/panic", http.NoBody)
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.NotNil(t, storage)
	_, err := storage.Bytes()
	require.ErrorIs(t, err, common.ErrStorageClosed)
}
