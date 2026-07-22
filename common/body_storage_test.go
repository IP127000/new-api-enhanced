package common

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type diskSwitchObservingReader struct {
	data             []byte
	offset           int
	observeAfter     int
	cacheDir         string
	observedDiskFile bool
}

func (r *diskSwitchObservingReader) Read(p []byte) (int, error) {
	if r.offset >= r.observeAfter {
		entries, err := os.ReadDir(r.cacheDir)
		if err == nil && len(entries) > 0 {
			r.observedDiskFile = true
		}
	}
	if r.offset == len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

type bodyStorageErrorReader struct {
	err error
}

func (r bodyStorageErrorReader) Read([]byte) (int, error) {
	return 0, r.err
}

func configureBodyStorageDiskCache(t *testing.T) string {
	t.Helper()
	previous := GetDiskCacheConfig()
	root := t.TempDir()
	SetDiskCacheConfig(DiskCacheConfig{
		Enabled:     true,
		ThresholdMB: 1,
		MaxSizeMB:   64,
		Path:        root,
	})
	t.Cleanup(func() {
		SetDiskCacheConfig(previous)
	})
	return filepath.Join(root, diskCacheDir)
}

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

func TestCreateBodyStorageFromUnknownLengthSpoolsAtDiskThreshold(t *testing.T) {
	cacheDir := configureBodyStorageDiskCache(t)
	const threshold = 1 << 20
	payload := bytes.Repeat([]byte("x"), threshold+(64<<10))
	reader := &diskSwitchObservingReader{
		data:         payload,
		observeAfter: threshold,
		cacheDir:     cacheDir,
	}

	storage, err := CreateBodyStorageFromReader(reader, -1, int64(len(payload)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	disk, ok := storage.(*diskStorage)
	require.True(t, ok)
	require.True(t, reader.observedDiskFile,
		"the temp file must exist before the reader supplies bytes beyond the memory threshold")
	require.Equal(t, int64(len(payload)), storage.Size())
	data, err := storage.Bytes()
	require.NoError(t, err)
	require.Equal(t, payload, data)
	require.FileExists(t, disk.filePath)

	require.NoError(t, storage.Close())
	_, err = os.Stat(disk.filePath)
	require.True(t, os.IsNotExist(err))
}

func TestCreateBodyStorageFromUnknownLengthKeepsSmallBodyInMemory(t *testing.T) {
	configureBodyStorageDiskCache(t)
	payload := bytes.Repeat([]byte("small"), 1024)

	storage, err := CreateBodyStorageFromReader(bytes.NewReader(payload), -1, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.False(t, storage.IsDisk())
	require.Equal(t, int64(len(payload)), storage.Size())
	data, err := storage.Bytes()
	require.NoError(t, err)
	require.Equal(t, payload, data)
}

func TestCreateBodyStorageFromUnknownLengthRemovesSpoolOnLimitError(t *testing.T) {
	cacheDir := configureBodyStorageDiskCache(t)
	const maxBytes = (1 << 20) + 512
	payload := bytes.Repeat([]byte("x"), maxBytes+1)
	before := GetDiskCacheStats()

	storage, err := CreateBodyStorageFromReader(bytes.NewReader(payload), -1, maxBytes)
	require.Nil(t, storage)
	require.ErrorIs(t, err, ErrRequestBodyTooLarge)
	entries, readErr := os.ReadDir(cacheDir)
	require.NoError(t, readErr)
	require.Empty(t, entries)
	after := GetDiskCacheStats()
	require.Equal(t, before.ActiveDiskFiles, after.ActiveDiskFiles)
	require.Equal(t, before.CurrentDiskUsageBytes, after.CurrentDiskUsageBytes)
}

func TestCreateBodyStorageFromUnknownLengthRemovesSpoolOnReaderError(t *testing.T) {
	cacheDir := configureBodyStorageDiskCache(t)
	sentinel := errors.New("reader failed")
	reader := io.MultiReader(
		bytes.NewReader(bytes.Repeat([]byte("x"), 1<<20)),
		bodyStorageErrorReader{err: sentinel},
	)

	storage, err := CreateBodyStorageFromReader(reader, -1, 2<<20)
	require.Nil(t, storage)
	require.ErrorIs(t, err, sentinel)
	entries, readErr := os.ReadDir(cacheDir)
	require.NoError(t, readErr)
	require.Empty(t, entries)
}

func TestCreateBodyStorageFromReaderKnownLengthStillStreamsToDisk(t *testing.T) {
	configureBodyStorageDiskCache(t)
	payload := bytes.Repeat([]byte("x"), (1<<20)+1)

	storage, err := CreateBodyStorageFromReader(
		bytes.NewReader(payload),
		int64(len(payload)),
		int64(len(payload)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.True(t, storage.IsDisk())
	require.Equal(t, int64(len(payload)), storage.Size())
}
