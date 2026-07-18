package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestApplyChannelDefaultParamOverrideForCodex(t *testing.T) {
	t.Parallel()

	channel := &Channel{Type: constant.ChannelTypeCodex}
	require.NoError(t, ApplyChannelDefaultParamOverride(channel))
	require.NotNil(t, channel.ParamOverride)

	var decoded struct {
		Operations []struct {
			Mode       string   `json:"mode"`
			Value      []string `json:"value"`
			KeepOrigin bool     `json:"keep_origin"`
		} `json:"operations"`
	}
	require.NoError(t, common.Unmarshal([]byte(*channel.ParamOverride), &decoded))
	require.Len(t, decoded.Operations, 1)
	require.Equal(t, "pass_headers", decoded.Operations[0].Mode)
	require.True(t, decoded.Operations[0].KeepOrigin)
	require.Equal(t, constant.CodexClientPassThroughHeaders(), decoded.Operations[0].Value)
	require.NotContains(t, decoded.Operations[0].Value, "Authorization")
}

func TestApplyChannelDefaultParamOverridePreservesCustomConfig(t *testing.T) {
	t.Parallel()

	custom := `{"temperature":0.2}`
	channel := &Channel{Type: constant.ChannelTypeCodex, ParamOverride: &custom}
	require.NoError(t, ApplyChannelDefaultParamOverride(channel))
	require.Same(t, &custom, channel.ParamOverride)
	require.Equal(t, custom, *channel.ParamOverride)
}

func TestApplyChannelDefaultParamOverrideSkipsOtherChannelTypes(t *testing.T) {
	t.Parallel()

	channel := &Channel{Type: constant.ChannelTypeOpenAI}
	require.NoError(t, ApplyChannelDefaultParamOverride(channel))
	require.Nil(t, channel.ParamOverride)
}

func TestBackfillCodexChannelDefaultParamOverridesOnlyFillsEmptyValues(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open("file:codex-channel-defaults?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}))

	custom := `{"operations":[{"mode":"set","path":"temperature","value":0.2}]}`
	channels := []Channel{
		{Id: 1, Type: constant.ChannelTypeCodex, Key: "codex-empty", Name: "codex-empty"},
		{Id: 2, Type: constant.ChannelTypeCodex, Key: "codex-custom", Name: "codex-custom", ParamOverride: &custom},
		{Id: 3, Type: constant.ChannelTypeOpenAI, Key: "openai-empty", Name: "openai-empty"},
	}
	require.NoError(t, db.Create(&channels).Error)
	require.NoError(t, backfillCodexChannelDefaultParamOverrides(db))

	var got []Channel
	require.NoError(t, db.Order("id").Find(&got).Error)
	require.Len(t, got, 3)
	require.NotNil(t, got[0].ParamOverride)
	require.Contains(t, *got[0].ParamOverride, "pass_headers")
	require.NotNil(t, got[1].ParamOverride)
	require.Equal(t, custom, *got[1].ParamOverride)
	require.Nil(t, got[2].ParamOverride)
}
