package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func UnmarshalJsonStr(data string, v any) error {
	return json.Unmarshal(StringToByteSlice(data), v)
}

func DecodeJson(reader io.Reader, v any) error {
	return json.NewDecoder(reader).Decode(v)
}

func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func GetJsonType(data json.RawMessage) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return "unknown"
	}
	firstChar := trimmed[0]
	switch firstChar {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}

// JsonRawMessageToString returns JSON strings as their decoded value and other JSON values as raw text.
func JsonRawMessageToString(data json.RawMessage) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] != '"' {
		return string(trimmed)
	}
	var value string
	if err := Unmarshal(trimmed, &value); err != nil {
		return string(trimmed)
	}
	return value
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
