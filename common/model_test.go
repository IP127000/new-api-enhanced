package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGPTImage2IsImageGenerationModel(t *testing.T) {
	t.Parallel()
	assert.True(t, IsImageGenerationModel("gpt-image-2"))
}
