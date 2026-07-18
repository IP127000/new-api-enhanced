package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"gorm.io/gorm"
)

// ApplyChannelDefaultParamOverride enables the official pass_headers operation
// for newly-created Codex subscription channels. Existing non-empty channel
// configuration is authoritative and is never replaced.
func ApplyChannelDefaultParamOverride(channel *Channel) error {
	if channel == nil || channel.Type != constant.ChannelTypeCodex {
		return nil
	}
	if channel.ParamOverride != nil && strings.TrimSpace(*channel.ParamOverride) != "" {
		return nil
	}

	defaultOverride := map[string]interface{}{
		"operations": []map[string]interface{}{
			{
				"mode":        "pass_headers",
				"value":       constant.CodexClientPassThroughHeaders(),
				"keep_origin": true,
			},
		},
	}
	raw, err := common.Marshal(defaultOverride)
	if err != nil {
		return err
	}
	value := string(raw)
	channel.ParamOverride = &value
	return nil
}

func backfillCodexChannelDefaultParamOverrides(db *gorm.DB) error {
	channel := &Channel{Type: constant.ChannelTypeCodex}
	if err := ApplyChannelDefaultParamOverride(channel); err != nil {
		return err
	}
	return db.Model(&Channel{}).
		Where("type = ? AND (param_override IS NULL OR TRIM(param_override) = ?)", constant.ChannelTypeCodex, "").
		Update("param_override", *channel.ParamOverride).Error
}
