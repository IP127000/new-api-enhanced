package model

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
)

// ChannelSupportsRequestPath applies endpoint-specific channel constraints in
// every selection path (affinity, memory cache and database fallback).
func ChannelSupportsRequestPath(channel *Channel, requestPath string, requestModel string) bool {
	if channel == nil {
		return false
	}
	if IsCodexWebSearchRequestPath(requestPath) || IsCodexModelsRequestPath(requestPath) {
		return channel.Type == constant.ChannelTypeCodex
	}
	if channel.Type != constant.ChannelTypeAdvancedCustom {
		return true
	}
	config := channel.GetOtherSettings().AdvancedCustom
	return config != nil && config.SupportsPathForModel(requestPath, requestModel)
}

func IsCodexModelsRequestPath(requestPath string) bool {
	return requestPath == "/v1/models"
}

func IsCodexWebSearchRequestPath(requestPath string) bool {
	return strings.HasPrefix(requestPath, "/v1/alpha/search")
}
