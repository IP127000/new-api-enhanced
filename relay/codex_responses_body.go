package relay

import (
	"fmt"
	"io"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type codexParamOverrideApplyError struct {
	err error
}

func (e *codexParamOverrideApplyError) Error() string {
	return e.err.Error()
}

func (e *codexParamOverrideApplyError) Unwrap() error {
	return e.err
}

func commitCodexResponsesStream(c *gin.Context, outbound io.Closer) {
	common.SetContextKey(c, appconstant.ContextKeyRelayRetryCommitted, true)
	if outbound != nil {
		_ = outbound.Close()
	}
	common.ReleaseBodyStorage(c)
}

func prepareCodexOriginalResponsesBody(
	c *gin.Context,
	storage common.BodyStorage,
	info *relaycommon.RelayInfo,
	compact bool,
	mappedModel string,
) (common.BodyStorage, bool, error) {
	if len(info.ParamOverride) > 0 {
		if relaycommon.CanApplyParamOverrideWithoutBody(info.ParamOverride) {
			if _, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{}`), info); err != nil {
				return nil, false, &codexParamOverrideApplyError{err: err}
			}
		} else {
			// Arbitrary parameter overrides can address nested paths and therefore
			// retain the established in-memory fallback. Normal Codex subscription
			// traffic only has header operations and uses the streaming path below.
			body, err := storage.Bytes()
			if err != nil {
				return nil, false, err
			}
			body, err = relaycommon.ApplyParamOverrideWithRelayInfo(body, info)
			if err != nil {
				return nil, false, &codexParamOverrideApplyError{err: err}
			}
			body, _, err = sanitizeCodexOriginalResponsesBody(body, compact, mappedModel)
			if err != nil {
				return nil, false, err
			}
			outboundStorage, err := common.CreateBodyStorage(body)
			if err != nil {
				return nil, false, err
			}
			return outboundStorage, true, nil
		}
	}

	fields, err := common.GetOrIndexJSONBodyFields(c, storage)
	if err != nil {
		return nil, false, err
	}
	fieldsByName := make(map[string]common.JSONFieldSpan, len(fields))
	for _, field := range fields {
		fieldsByName[field.Name] = field
	}

	edits := make(map[string]common.JSONFieldEdit, 4)
	additions := make([]common.JSONFieldAddition, 0, 2)
	changed := false
	if mappedModel != "" {
		currentModel := ""
		if modelField, exists := fieldsByName["model"]; exists {
			modelJSON, err := common.ReadJSONSpan(storage, modelField.Value, 64<<10)
			if err != nil {
				return nil, false, err
			}
			currentModel = common.JsonRawMessageToString(modelJSON)
		}
		if currentModel != mappedModel {
			encodedModel, err := common.Marshal(mappedModel)
			if err != nil {
				return nil, false, err
			}
			edits["model"] = common.JSONFieldEdit{Replacement: encodedModel}
			changed = true
		}
	}
	if _, exists := fieldsByName["instructions"]; !exists {
		additions = append(additions, common.JSONFieldAddition{Name: "instructions", Value: []byte(`""`)})
		changed = true
	}
	if !compact {
		if storeField, exists := fieldsByName["store"]; exists {
			isFalse := false
			if storeField.Value.End-storeField.Value.Start == int64(len("false")) {
				storeJSON, err := common.ReadJSONSpan(storage, storeField.Value, int64(len("false")))
				if err != nil {
					return nil, false, err
				}
				isFalse = string(storeJSON) == "false"
			}
			if !isFalse {
				edits["store"] = common.JSONFieldEdit{Replacement: []byte("false")}
				changed = true
			}
		} else {
			additions = append(additions, common.JSONFieldAddition{Name: "store", Value: []byte("false")})
			changed = true
		}
		for _, fieldName := range []string{"max_output_tokens", "temperature", "frequency_penalty", "presence_penalty"} {
			if _, exists := fieldsByName[fieldName]; exists {
				edits[fieldName] = common.JSONFieldEdit{Delete: true}
				changed = true
			}
		}
	}
	if !changed {
		return storage, false, nil
	}

	reader, size, err := common.NewTopLevelJSONObjectReader(storage, fields, edits, additions)
	if err != nil {
		return nil, false, err
	}
	outboundStorage, err := common.CreateBodyStorageFromReader(reader, size, size)
	if err != nil {
		return nil, false, err
	}
	return outboundStorage, true, nil
}

func shouldUseCodexOriginalResponsesBody(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ApiType != appconstant.APITypeCodex || info.ChannelType != appconstant.ChannelTypeCodex {
		return false
	}
	return info.RelayMode == relayconstant.RelayModeResponses ||
		info.RelayMode == relayconstant.RelayModeResponsesCompact
}

func sanitizeCodexOriginalResponsesBody(body []byte, compact bool, mappedModel string) ([]byte, bool, error) {
	var err error
	changed := false
	if mappedModel != "" && gjson.GetBytes(body, "model").String() != mappedModel {
		body, err = sjson.SetBytes(body, "model", mappedModel)
		if err != nil {
			return nil, false, fmt.Errorf("set mapped Codex model: %w", err)
		}
		changed = true
	}
	if !gjson.GetBytes(body, "instructions").Exists() {
		body, err = sjson.SetBytes(body, "instructions", "")
		if err != nil {
			return nil, false, fmt.Errorf("set Codex instructions: %w", err)
		}
		changed = true
	}
	if compact {
		return body, changed, nil
	}

	store := gjson.GetBytes(body, "store")
	if !store.Exists() || store.Type != gjson.False {
		body, err = sjson.SetBytes(body, "store", false)
		if err != nil {
			return nil, false, fmt.Errorf("set Codex store: %w", err)
		}
		changed = true
	}
	for _, field := range []string{"max_output_tokens", "temperature", "frequency_penalty", "presence_penalty"} {
		if !gjson.GetBytes(body, field).Exists() {
			continue
		}
		body, err = sjson.DeleteBytes(body, field)
		if err != nil {
			return nil, false, fmt.Errorf("remove unsupported Codex field %q: %w", field, err)
		}
		changed = true
	}
	return body, changed, nil
}
