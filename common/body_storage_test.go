package common

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMemoryStorageCloseDropsBackingReferences(t *testing.T) {
	storage := newMemoryStorage(bytes.Repeat([]byte("x"), 1<<20))
	require.NotNil(t, storage.data)
	require.NotNil(t, storage.reader)

	require.NoError(t, storage.Close())
	require.Nil(t, storage.data)
	require.Nil(t, storage.reader)
	_, err := storage.Bytes()
	require.ErrorIs(t, err, ErrStorageClosed)
}

func TestMemoryStorageNewReaderUsesIndependentCursor(t *testing.T) {
	storage := newMemoryStorage([]byte("abcdef"))
	t.Cleanup(func() { _ = storage.Close() })

	first, err := storage.NewReader()
	require.NoError(t, err)
	second, err := storage.NewReader()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = first.Close()
		_ = second.Close()
	})

	firstBytes := make([]byte, 2)
	secondBytes := make([]byte, 2)
	_, err = io.ReadFull(first, firstBytes)
	require.NoError(t, err)
	_, err = io.ReadFull(second, secondBytes)
	require.NoError(t, err)
	require.Equal(t, []byte("ab"), firstBytes)
	require.Equal(t, []byte("ab"), secondBytes)

	_, err = io.ReadFull(first, firstBytes)
	require.NoError(t, err)
	require.Equal(t, []byte("cd"), firstBytes)
	_, err = io.ReadFull(second, secondBytes)
	require.NoError(t, err)
	require.Equal(t, []byte("cd"), secondBytes)
}

func TestCleanupBodyStorageClearsGinRequestReferences(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", http.NoBody)
	storage := newMemoryStorage(bytes.Repeat([]byte("x"), 1<<20))
	c.Set(KeyBodyStorage, storage)
	c.Set(KeyRequestBody, storage.data)
	c.Request.Body = io.NopCloser(storage)

	CleanupBodyStorage(c)

	value, exists := c.Get(KeyBodyStorage)
	require.True(t, exists)
	require.Nil(t, value)
	value, exists = c.Get(KeyRequestBody)
	require.True(t, exists)
	require.Nil(t, value)
	require.Equal(t, http.NoBody, c.Request.Body)
	require.Nil(t, storage.data)
}
