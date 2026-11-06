package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/gin-gonic/gin/binding"
)

type RawMessage = json.RawMessage

// hostJSONCodec is the single place where the host chooses its JSON engine.
// Swap the implementation here (for example to sonic.ConfigStd) and every
// common.* and kitutil.* JSON helper, including relaykit DTO (un)marshalling,
// follows. Injected from init() rather than main() so tests run on the same
// engine as production: common is imported by virtually every root package
// and test binary, while main() never executes under `go test`.
type hostJSONCodec struct{}

func (hostJSONCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (hostJSONCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (hostJSONCodec) Decode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

func (hostJSONCodec) Valid(data []byte) bool {
	return json.Valid(data)
}

func init() {
	kitutil.SetCodec(hostJSONCodec{})
}

func Unmarshal(data []byte, v any) error {
	return kitutil.Unmarshal(data, v)
}

func UnmarshalJsonStr(data string, v any) error {
	return kitutil.UnmarshalJsonStr(data, v)
}

func DecodeJson(reader io.Reader, v any) error {
	return kitutil.DecodeJson(reader, v)
}

// DecodeJsonWithValidation decodes JSON and applies Gin's configured binding-tag
// validator, including binding:"required" and any registered custom validators.
func DecodeJsonWithValidation(reader io.Reader, v any) error {
	if err := DecodeJson(reader, v); err != nil {
		return err
	}
	if binding.Validator == nil {
		return nil
	}
	return binding.Validator.ValidateStruct(v)
}

func Marshal(v any) ([]byte, error) {
	return kitutil.Marshal(v)
}

func IndentJson(data []byte) ([]byte, error) {
	var buffer bytes.Buffer
	if err := json.Indent(&buffer, data, "", "  "); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func GetJsonType(data RawMessage) string {
	return kitutil.GetJsonType(data)
}

// JsonRawMessageToString returns JSON strings as their decoded value and other JSON values as raw text.
func JsonRawMessageToString(data RawMessage) string {
	return kitutil.JsonRawMessageToString(data)
}

// ExtractTopLevelJSONFields returns slices into data for selected fields of a
// top-level JSON object. Unlike unmarshalling into json.RawMessage, it never
// copies large nested values. The returned slices are valid only while data is
// alive and must be treated as read-only.
func ExtractTopLevelJSONFields(data []byte, fieldNames ...string) (map[string][]byte, error) {
	if !json.Valid(data) {
		return nil, fmt.Errorf("invalid JSON")
	}
	wanted := make(map[string]struct{}, len(fieldNames))
	for _, name := range fieldNames {
		wanted[name] = struct{}{}
	}
	result := make(map[string][]byte, len(fieldNames))

	i := skipJSONWhitespace(data, 0)
	if i >= len(data) || data[i] != '{' {
		return nil, fmt.Errorf("JSON value must be an object")
	}
	i++
	for {
		i = skipJSONWhitespace(data, i)
		if data[i] == '}' {
			return result, nil
		}
		keyStart := i
		keyEnd, err := scanJSONStringEnd(data, keyStart)
		if err != nil {
			return nil, err
		}
		var key string
		if bytes.IndexByte(data[keyStart+1:keyEnd-1], '\\') < 0 {
			key = string(data[keyStart+1 : keyEnd-1])
		} else if err := json.Unmarshal(data[keyStart:keyEnd], &key); err != nil {
			return nil, err
		}

		i = skipJSONWhitespace(data, keyEnd)
		if i >= len(data) || data[i] != ':' {
			return nil, fmt.Errorf("missing colon after JSON field %q", key)
		}
		i = skipJSONWhitespace(data, i+1)
		valueStart := i
		valueEnd, err := scanJSONValueEnd(data, valueStart)
		if err != nil {
			return nil, err
		}
		if _, ok := wanted[key]; ok {
			result[key] = bytes.TrimSpace(data[valueStart:valueEnd])
		}

		i = skipJSONWhitespace(data, valueEnd)
		switch data[i] {
		case ',':
			i++
		case '}':
			return result, nil
		default:
			return nil, fmt.Errorf("invalid separator after JSON field %q", key)
		}
	}
}

// ForEachJSONArrayValue visits each top-level element as a read-only slice of
// the original array. It avoids decoding large tool schemas into Go objects.
func ForEachJSONArrayValue(data []byte, visit func(value []byte) bool) error {
	if !json.Valid(data) {
		return fmt.Errorf("invalid JSON")
	}
	i := skipJSONWhitespace(data, 0)
	if i >= len(data) || data[i] != '[' {
		return fmt.Errorf("JSON value must be an array")
	}
	i++
	for {
		i = skipJSONWhitespace(data, i)
		if data[i] == ']' {
			return nil
		}
		end, err := scanJSONValueEnd(data, i)
		if err != nil {
			return err
		}
		if !visit(bytes.TrimSpace(data[i:end])) {
			return nil
		}
		i = skipJSONWhitespace(data, end)
		switch data[i] {
		case ',':
			i++
		case ']':
			return nil
		default:
			return fmt.Errorf("invalid JSON array separator")
		}
	}
}

func skipJSONWhitespace(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\n', '\r', '\t':
			i++
		default:
			return i
		}
	}
	return i
}

func scanJSONStringEnd(data []byte, start int) (int, error) {
	if start >= len(data) || data[start] != '"' {
		return 0, fmt.Errorf("expected JSON string at byte %d", start)
	}
	for i := start + 1; i < len(data); i++ {
		switch data[i] {
		case '\\':
			i++
			if i >= len(data) {
				return 0, fmt.Errorf("unterminated JSON escape")
			}
		case '"':
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated JSON string")
}

func scanJSONValueEnd(data []byte, start int) (int, error) {
	if start >= len(data) {
		return 0, fmt.Errorf("missing JSON value")
	}
	switch data[start] {
	case '"':
		return scanJSONStringEnd(data, start)
	case '{', '[':
		stack := []byte{data[start]}
		for i := start + 1; i < len(data); i++ {
			switch data[i] {
			case '"':
				end, err := scanJSONStringEnd(data, i)
				if err != nil {
					return 0, err
				}
				i = end - 1
			case '{', '[':
				stack = append(stack, data[i])
			case '}', ']':
				open := stack[len(stack)-1]
				if (open == '{' && data[i] != '}') || (open == '[' && data[i] != ']') {
					return 0, fmt.Errorf("mismatched JSON delimiter")
				}
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					return i + 1, nil
				}
			}
		}
		return 0, fmt.Errorf("unterminated JSON value")
	default:
		i := start
		for i < len(data) && !isJSONValueDelimiter(data[i]) {
			i++
		}
		if i == start {
			return 0, fmt.Errorf("missing JSON value")
		}
		// json.Valid above already checked primitive syntax.
		return i, nil
	}
}

func isJSONValueDelimiter(b byte) bool {
	switch b {
	case ',', '}', ']', ' ', '\n', '\r', '\t':
		return true
	default:
		return false
	}
}
