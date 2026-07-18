package middleware

import (
	"errors"
	"io"
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
