package operation_setting

import (
	"strconv"
	"strings"
)

var DemoSiteEnabled = false
var SelfUseModeEnabled = false

// ---- API 访问管控（维护模式）----
// 被管控的用户仍可正常登录后台，但其 API key 的所有请求都会被拦截并返回 ApiRestrictMessage。
// ApiRestrictUserIds 为空时表示全局维护模式：管控所有用户；填写逗号分隔的用户 ID 时只管控这些用户。
var ApiRestrictEnabled = false
var ApiRestrictMessage = "服务正在维护中，请稍后再试。"
var ApiRestrictUserIds = ""

// IsUserApiRestricted 返回该用户的 API 访问是否应被拦截，以及要返回的自定义消息。
func IsUserApiRestricted(userId int) (bool, string) {
	if !ApiRestrictEnabled {
		return false, ""
	}
	ids := strings.TrimSpace(ApiRestrictUserIds)
	if ids == "" {
		// 全局维护模式：管控所有用户
		return true, ApiRestrictMessage
	}
	target := strconv.Itoa(userId)
	for _, part := range strings.Split(ids, ",") {
		if strings.TrimSpace(part) == target {
			return true, ApiRestrictMessage
		}
	}
	return false, ""
}

var AutomaticDisableKeywords = []string{
	"Your credit balance is too low",
	"This organization has been disabled.",
	"You exceeded your current quota",
	"Permission denied",
	"The security token included in the request is invalid",
	"Operation not allowed",
	"Your account is not authorized",
}

func AutomaticDisableKeywordsToString() string {
	return strings.Join(AutomaticDisableKeywords, "\n")
}

func AutomaticDisableKeywordsFromString(s string) {
	AutomaticDisableKeywords = []string{}
	ak := strings.Split(s, "\n")
	for _, k := range ak {
		k = strings.TrimSpace(k)
		k = strings.ToLower(k)
		if k != "" {
			AutomaticDisableKeywords = append(AutomaticDisableKeywords, k)
		}
	}
}
