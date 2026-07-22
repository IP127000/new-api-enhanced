package relay

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// PrepareRelayAccounting performs the request-side checks and billing setup
// shared by HTTP relays and transports that carry multiple logical Responses
// requests over one connection. The caller remains responsible for refunding
// info.Billing if the logical request fails after this function succeeds.
func PrepareRelayAccounting(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request, useUpstreamUsage bool) *types.NewAPIError {
	needSensitiveCheck := setting.ShouldCheckPromptSensitive()
	needCountToken := constant.CountToken && !useUpstreamUsage

	var meta *types.TokenCountMeta
	if needSensitiveCheck || needCountToken {
		meta = request.GetTokenCountMeta()
	} else {
		meta = FastTokenCountMetaForPricing(request)
	}
	if meta == nil {
		meta = &types.TokenCountMeta{}
	}

	if needSensitiveCheck {
		contains, words := service.CheckSensitiveText(meta.CombineText)
		if contains {
			logger.LogWarn(c, fmt.Sprintf("user sensitive words detected: %s", strings.Join(words, ", ")))
			return types.NewError(errors.New("sensitive words detected"), types.ErrorCodeSensitiveWordsDetected)
		}
	}

	tokens := 0
	if !useUpstreamUsage {
		var err error
		tokens, err = service.EstimateRequestToken(c, meta, info)
		if err != nil {
			return types.NewError(err, types.ErrorCodeCountTokenFailed)
		}
	}
	info.SetEstimatePromptTokens(tokens)

	priceData, err := helper.ModelPriceHelper(c, info, tokens, meta)
	if err != nil {
		return types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest))
	}

	switch {
	case priceData.FreeModel:
		logger.LogInfo(c, fmt.Sprintf("模型 %s 免费，跳过预扣费", info.OriginModelName))
	case useUpstreamUsage:
		logger.LogInfo(c, "Responses 使用上游 usage 结算，跳过本地 token 统计和预扣费")
	default:
		if billingErr := service.PreConsumeBilling(c, priceData.QuotaToPreConsume, info); billingErr != nil {
			return billingErr
		}
	}

	return nil
}

// FastTokenCountMetaForPricing extracts only fields that affect pricing and
// avoids building a large joined prompt when neither counting nor sensitive
// checks are enabled.
func FastTokenCountMetaForPricing(request dto.Request) *types.TokenCountMeta {
	if request == nil {
		return &types.TokenCountMeta{}
	}
	meta := &types.TokenCountMeta{TokenType: types.TokenTypeTokenizer}
	switch typed := request.(type) {
	case *dto.GeneralOpenAIRequest:
		maxCompletionTokens := lo.FromPtrOr(typed.MaxCompletionTokens, uint(0))
		maxTokens := lo.FromPtrOr(typed.MaxTokens, uint(0))
		if maxCompletionTokens > maxTokens {
			meta.MaxTokens = int(maxCompletionTokens)
		} else {
			meta.MaxTokens = int(maxTokens)
		}
	case *dto.OpenAIResponsesRequest:
		meta.MaxTokens = int(lo.FromPtrOr(typed.MaxOutputTokens, uint(0)))
	case *dto.ClaudeRequest:
		meta.MaxTokens = int(lo.FromPtr(typed.MaxTokens))
	case *dto.ImageRequest:
		return typed.GetTokenCountMeta()
	}
	return meta
}
