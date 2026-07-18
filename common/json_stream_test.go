package common

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func fieldsByName(fields []JSONFieldSpan) map[string]JSONFieldSpan {
	indexed := make(map[string]JSONFieldSpan, len(fields))
	for _, field := range fields {
		indexed[field.Name] = field
	}
	return indexed
}

func TestIndexTopLevelJSONObjectDoesNotMaterializeNestedValues(t *testing.T) {
	t.Parallel()
	body := []byte(` {"model":"gpt-test","input":{"nested":["` + strings.Repeat("x", 2<<20) + `"]},"tools":[{"type":"web_search","schema":{"description":"large"}}]} `)
	reader := bytes.NewReader(body)

	fields, err := IndexTopLevelJSONObject(reader, int64(len(body)))
	require.NoError(t, err)
	indexed := fieldsByName(fields)
	require.Contains(t, indexed, "model")
	require.Contains(t, indexed, "input")
	require.Contains(t, indexed, "tools")

	model, err := ReadJSONSpan(reader, indexed["model"].Value, 64)
	require.NoError(t, err)
	require.JSONEq(t, `"gpt-test"`, string(model))
	require.Greater(t, indexed["input"].Value.End-indexed["input"].Value.Start, int64(2<<20))
}

func TestIndexJSONObjectAndJSONArrayReturnDirectChildren(t *testing.T) {
	t.Parallel()
	body := []byte(`{"tools":[{"type":"web_search","schema":{"nested":[1,2]}},{"type":"image_generation"}]}`)
	reader := bytes.NewReader(body)
	fields, err := IndexTopLevelJSONObject(reader, int64(len(body)))
	require.NoError(t, err)
	tools := fieldsByName(fields)["tools"]

	values, err := IndexJSONArray(reader, tools.Value)
	require.NoError(t, err)
	require.Len(t, values, 2)
	firstFields, err := IndexJSONObject(reader, values[0])
	require.NoError(t, err)
	typeJSON, err := ReadJSONSpan(reader, fieldsByName(firstFields)["type"].Value, 64)
	require.NoError(t, err)
	require.JSONEq(t, `"web_search"`, string(typeJSON))
}

func TestNewTopLevelJSONObjectReaderRewritesWithoutCopyingLargeInput(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"client","input":"` + strings.Repeat("x", 2<<20) + `","store":true,"temperature":0.7,"future":{"keep":true}}`)
	reader := bytes.NewReader(body)
	fields, err := IndexTopLevelJSONObject(reader, int64(len(body)))
	require.NoError(t, err)

	rewritten, size, err := NewTopLevelJSONObjectReader(reader, fields, map[string]JSONFieldEdit{
		"model":       {Replacement: []byte(`"upstream"`)},
		"store":       {Replacement: []byte("false")},
		"temperature": {Delete: true},
	}, []JSONFieldAddition{{Name: "instructions", Value: []byte(`""`)}})
	require.NoError(t, err)
	got, err := io.ReadAll(rewritten)
	require.NoError(t, err)
	require.EqualValues(t, len(got), size)

	var decoded map[string]any
	require.NoError(t, Unmarshal(got, &decoded))
	require.Equal(t, "upstream", decoded["model"])
	require.Equal(t, false, decoded["store"])
	require.Equal(t, "", decoded["instructions"])
	require.NotContains(t, decoded, "temperature")
	require.Equal(t, map[string]any{"keep": true}, decoded["future"])
	require.Len(t, decoded["input"].(string), 2<<20)
}

func TestIndexTopLevelJSONObjectRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	invalidBodies := []string{
		`{"a":01}`,
		`{"a":"bad\x"}`,
		`{"a":[1,]}`,
		`{"a":true} trailing`,
	}
	for _, body := range invalidBodies {
		_, err := IndexTopLevelJSONObject(strings.NewReader(body), int64(len(body)))
		require.Error(t, err, body)
	}
}

func TestIndexTopLevelJSONObjectAllocationBound(t *testing.T) {
	body := []byte(`{"model":"gpt-test","input":"` + strings.Repeat("x", 2<<20) + `","instructions":"","store":false}`)
	reader := bytes.NewReader(body)
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			fields, err := IndexTopLevelJSONObject(reader, int64(len(body)))
			if err != nil || len(fields) != 4 {
				b.Fatalf("index failed: fields=%d err=%v", len(fields), err)
			}
		}
	})
	t.Logf("streaming JSON index allocated %d bytes/op for a %d-byte object", result.AllocedBytesPerOp(), len(body))
	require.Less(t, result.AllocedBytesPerOp(), int64(64<<10))
}
