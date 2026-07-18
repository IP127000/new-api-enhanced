package common

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJsonRawMessageToString(t *testing.T) {
	tests := []struct {
		name string
		data json.RawMessage
		want string
	}{
		{
			name: "object",
			data: json.RawMessage(`{"city":"Paris","days":0,"strict":false}`),
			want: `{"city":"Paris","days":0,"strict":false}`,
		},
		{
			name: "string",
			data: json.RawMessage(`"{\"city\":\"Paris\",\"days\":0,\"strict\":false}"`),
			want: `{"city":"Paris","days":0,"strict":false}`,
		},
		{
			name: "null",
			data: json.RawMessage(`null`),
			want: "",
		},
		{
			name: "empty",
			data: nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, JsonRawMessageToString(tt.data))
		})
	}
}

func TestExtractTopLevelJSONFieldsDoesNotDecodeNestedValues(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.6-sol","input":[{"text":"brace } and quote \" stay nested"}],"tools":[{"type":"web_search","schema":{"nested":[1,2,3]}}],"stream":true}`)
	fields, err := ExtractTopLevelJSONFields(body, "model", "input", "tools", "stream", "missing")
	require.NoError(t, err)
	require.JSONEq(t, `"gpt-5.6-sol"`, string(fields["model"]))
	require.JSONEq(t, `[{"text":"brace } and quote \" stay nested"}]`, string(fields["input"]))
	require.JSONEq(t, `[{"type":"web_search","schema":{"nested":[1,2,3]}}]`, string(fields["tools"]))
	require.Equal(t, "true", string(fields["stream"]))
	require.NotContains(t, fields, "missing")
}

func TestExtractTopLevelJSONFieldsSupportsEscapedKeys(t *testing.T) {
	t.Parallel()

	fields, err := ExtractTopLevelJSONFields([]byte(`{"mo\u0064el":"gpt-test","input":null}`), "model", "input")
	require.NoError(t, err)
	require.Equal(t, `"gpt-test"`, string(fields["model"]))
	require.Equal(t, "null", string(fields["input"]))
}

func TestForEachJSONArrayValueReturnsOriginalElements(t *testing.T) {
	t.Parallel()

	var got []string
	err := ForEachJSONArrayValue([]byte(`[1,{"nested":[true,false]},"x,y"]`), func(value []byte) bool {
		got = append(got, string(value))
		return true
	})
	require.NoError(t, err)
	require.Equal(t, []string{"1", `{"nested":[true,false]}`, `"x,y"`}, got)
}
