package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const (
	jsonReaderAtBufferSize = 8 << 10
	maxJSONNestingDepth    = 1000
	maxJSONFieldNameBytes  = 1 << 20
)

// JSONValueSpan identifies a complete JSON value in an io.ReaderAt source.
// End is exclusive. The span excludes surrounding whitespace.
type JSONValueSpan struct {
	Start int64
	End   int64
}

// JSONFieldSpan identifies one field in a JSON object. Key contains the raw
// quoted key while Value contains the complete raw value.
type JSONFieldSpan struct {
	Name  string
	Key   JSONValueSpan
	Value JSONValueSpan
}

type jsonReaderAtScanner struct {
	reader   io.ReaderAt
	start    int64
	end      int64
	position int64
	buffer   []byte
	bufStart int64
	bufEnd   int64
}

func newJSONReaderAtScanner(reader io.ReaderAt, start, end int64) (*jsonReaderAtScanner, error) {
	if reader == nil {
		return nil, fmt.Errorf("JSON reader is nil")
	}
	if start < 0 || end < start {
		return nil, fmt.Errorf("invalid JSON range [%d,%d)", start, end)
	}
	return &jsonReaderAtScanner{
		reader:   reader,
		start:    start,
		end:      end,
		position: start,
		buffer:   make([]byte, jsonReaderAtBufferSize),
	}, nil
}

func (s *jsonReaderAtScanner) load(position int64) error {
	if position < s.start || position >= s.end {
		return io.EOF
	}
	remaining := s.end - position
	readSize := int64(len(s.buffer))
	if remaining < readSize {
		readSize = remaining
	}
	n, err := s.reader.ReadAt(s.buffer[:readSize], position)
	if n == 0 {
		if err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	}
	s.bufStart = position
	s.bufEnd = position + int64(n)
	return nil
}

func (s *jsonReaderAtScanner) peekByte() (byte, error) {
	if s.position < s.bufStart || s.position >= s.bufEnd {
		if err := s.load(s.position); err != nil {
			return 0, err
		}
	}
	return s.buffer[s.position-s.bufStart], nil
}

func (s *jsonReaderAtScanner) readByte() (byte, error) {
	b, err := s.peekByte()
	if err != nil {
		return 0, err
	}
	s.position++
	return b, nil
}

func (s *jsonReaderAtScanner) skipWhitespace() {
	for s.position < s.end {
		b, err := s.peekByte()
		if err != nil {
			return
		}
		switch b {
		case ' ', '\n', '\r', '\t':
			s.position++
		default:
			return
		}
	}
}

func (s *jsonReaderAtScanner) expectByte(expected byte) error {
	b, err := s.readByte()
	if err != nil {
		return err
	}
	if b != expected {
		return fmt.Errorf("expected %q at byte %d, got %q", expected, s.position-1, b)
	}
	return nil
}

func (s *jsonReaderAtScanner) parseString() error {
	if err := s.expectByte('"'); err != nil {
		return err
	}
	for {
		b, err := s.readByte()
		if err != nil {
			return fmt.Errorf("unterminated JSON string: %w", err)
		}
		switch b {
		case '"':
			return nil
		case '\\':
			escape, err := s.readByte()
			if err != nil {
				return fmt.Errorf("unterminated JSON escape: %w", err)
			}
			switch escape {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				for i := 0; i < 4; i++ {
					hex, err := s.readByte()
					if err != nil {
						return fmt.Errorf("unterminated JSON unicode escape: %w", err)
					}
					if !isJSONHexDigit(hex) {
						return fmt.Errorf("invalid JSON unicode escape at byte %d", s.position-1)
					}
				}
			default:
				return fmt.Errorf("invalid JSON escape at byte %d", s.position-1)
			}
		default:
			if b < 0x20 {
				return fmt.Errorf("invalid control character in JSON string at byte %d", s.position-1)
			}
		}
	}
}

func isJSONHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func (s *jsonReaderAtScanner) parseLiteral(literal string) error {
	for i := 0; i < len(literal); i++ {
		b, err := s.readByte()
		if err != nil {
			return err
		}
		if b != literal[i] {
			return fmt.Errorf("invalid JSON literal at byte %d", s.position-1)
		}
	}
	return nil
}

func (s *jsonReaderAtScanner) parseNumber() error {
	b, err := s.peekByte()
	if err != nil {
		return err
	}
	if b == '-' {
		s.position++
		b, err = s.peekByte()
		if err != nil {
			return fmt.Errorf("invalid JSON number: %w", err)
		}
	}
	if b == '0' {
		s.position++
		if next, nextErr := s.peekByte(); nextErr == nil && next >= '0' && next <= '9' {
			return fmt.Errorf("invalid leading zero in JSON number at byte %d", s.position)
		}
	} else if b >= '1' && b <= '9' {
		for {
			s.position++
			next, nextErr := s.peekByte()
			if nextErr != nil || next < '0' || next > '9' {
				break
			}
		}
	} else {
		return fmt.Errorf("invalid JSON number at byte %d", s.position)
	}

	if next, nextErr := s.peekByte(); nextErr == nil && next == '.' {
		s.position++
		digit, digitErr := s.peekByte()
		if digitErr != nil || digit < '0' || digit > '9' {
			return fmt.Errorf("invalid JSON fraction at byte %d", s.position)
		}
		for {
			s.position++
			next, nextErr := s.peekByte()
			if nextErr != nil || next < '0' || next > '9' {
				break
			}
		}
	}

	if next, nextErr := s.peekByte(); nextErr == nil && (next == 'e' || next == 'E') {
		s.position++
		next, nextErr = s.peekByte()
		if nextErr == nil && (next == '+' || next == '-') {
			s.position++
			next, nextErr = s.peekByte()
		}
		if nextErr != nil || next < '0' || next > '9' {
			return fmt.Errorf("invalid JSON exponent at byte %d", s.position)
		}
		for {
			s.position++
			next, nextErr = s.peekByte()
			if nextErr != nil || next < '0' || next > '9' {
				break
			}
		}
	}
	return nil
}

func (s *jsonReaderAtScanner) parseValue(depth int) (JSONValueSpan, error) {
	if depth > maxJSONNestingDepth {
		return JSONValueSpan{}, fmt.Errorf("JSON nesting exceeds %d levels", maxJSONNestingDepth)
	}
	s.skipWhitespace()
	start := s.position
	b, err := s.peekByte()
	if err != nil {
		return JSONValueSpan{}, fmt.Errorf("missing JSON value: %w", err)
	}
	switch b {
	case '"':
		err = s.parseString()
	case '{':
		_, err = s.parseObject(depth+1, false)
	case '[':
		_, err = s.parseArray(depth+1, false)
	case 't':
		err = s.parseLiteral("true")
	case 'f':
		err = s.parseLiteral("false")
	case 'n':
		err = s.parseLiteral("null")
	default:
		err = s.parseNumber()
	}
	if err != nil {
		return JSONValueSpan{}, err
	}
	return JSONValueSpan{Start: start, End: s.position}, nil
}

func (s *jsonReaderAtScanner) parseObject(depth int, collectFields bool) ([]JSONFieldSpan, error) {
	if depth > maxJSONNestingDepth {
		return nil, fmt.Errorf("JSON nesting exceeds %d levels", maxJSONNestingDepth)
	}
	if err := s.expectByte('{'); err != nil {
		return nil, err
	}
	s.skipWhitespace()
	if b, err := s.peekByte(); err == nil && b == '}' {
		s.position++
		return nil, nil
	}
	fields := make([]JSONFieldSpan, 0, 16)
	for {
		s.skipWhitespace()
		keyStart := s.position
		if err := s.parseString(); err != nil {
			return nil, err
		}
		keyEnd := s.position
		s.skipWhitespace()
		if err := s.expectByte(':'); err != nil {
			return nil, err
		}
		value, err := s.parseValue(depth + 1)
		if err != nil {
			return nil, err
		}
		if collectFields {
			nameBytes, err := ReadJSONSpan(s.reader, JSONValueSpan{Start: keyStart, End: keyEnd}, maxJSONFieldNameBytes)
			if err != nil {
				return nil, err
			}
			var name string
			if err := Unmarshal(nameBytes, &name); err != nil {
				return nil, fmt.Errorf("invalid JSON field name: %w", err)
			}
			fields = append(fields, JSONFieldSpan{
				Name:  name,
				Key:   JSONValueSpan{Start: keyStart, End: keyEnd},
				Value: value,
			})
		}

		s.skipWhitespace()
		separator, err := s.readByte()
		if err != nil {
			return nil, fmt.Errorf("unterminated JSON object: %w", err)
		}
		switch separator {
		case ',':
			continue
		case '}':
			return fields, nil
		default:
			return nil, fmt.Errorf("invalid JSON object separator %q at byte %d", separator, s.position-1)
		}
	}
}

func (s *jsonReaderAtScanner) parseArray(depth int, collectValues bool) ([]JSONValueSpan, error) {
	if depth > maxJSONNestingDepth {
		return nil, fmt.Errorf("JSON nesting exceeds %d levels", maxJSONNestingDepth)
	}
	if err := s.expectByte('['); err != nil {
		return nil, err
	}
	s.skipWhitespace()
	if b, err := s.peekByte(); err == nil && b == ']' {
		s.position++
		return nil, nil
	}
	values := make([]JSONValueSpan, 0, 8)
	for {
		value, err := s.parseValue(depth + 1)
		if err != nil {
			return nil, err
		}
		if collectValues {
			values = append(values, value)
		}
		s.skipWhitespace()
		separator, err := s.readByte()
		if err != nil {
			return nil, fmt.Errorf("unterminated JSON array: %w", err)
		}
		switch separator {
		case ',':
			continue
		case ']':
			return values, nil
		default:
			return nil, fmt.Errorf("invalid JSON array separator %q at byte %d", separator, s.position-1)
		}
	}
}

func (s *jsonReaderAtScanner) finishRange() error {
	s.skipWhitespace()
	if s.position != s.end {
		return fmt.Errorf("unexpected JSON data at byte %d", s.position)
	}
	return nil
}

// IndexTopLevelJSONObject validates a complete JSON object and returns byte
// spans for its direct fields without materializing nested values.
func IndexTopLevelJSONObject(reader io.ReaderAt, size int64) ([]JSONFieldSpan, error) {
	return IndexJSONObject(reader, JSONValueSpan{Start: 0, End: size})
}

// IndexJSONObject validates an object in span and returns its direct fields.
func IndexJSONObject(reader io.ReaderAt, span JSONValueSpan) ([]JSONFieldSpan, error) {
	scanner, err := newJSONReaderAtScanner(reader, span.Start, span.End)
	if err != nil {
		return nil, err
	}
	scanner.skipWhitespace()
	fields, err := scanner.parseObject(0, true)
	if err != nil {
		return nil, err
	}
	if err := scanner.finishRange(); err != nil {
		return nil, err
	}
	return fields, nil
}

// IndexJSONArray validates an array in span and returns spans for its direct
// elements without materializing their contents.
func IndexJSONArray(reader io.ReaderAt, span JSONValueSpan) ([]JSONValueSpan, error) {
	scanner, err := newJSONReaderAtScanner(reader, span.Start, span.End)
	if err != nil {
		return nil, err
	}
	scanner.skipWhitespace()
	values, err := scanner.parseArray(0, true)
	if err != nil {
		return nil, err
	}
	if err := scanner.finishRange(); err != nil {
		return nil, err
	}
	return values, nil
}

// ReadJSONSpan reads a deliberately bounded JSON span. It is intended for
// small routing metadata such as model names and booleans, never large inputs.
func ReadJSONSpan(reader io.ReaderAt, span JSONValueSpan, maxBytes int64) ([]byte, error) {
	length := span.End - span.Start
	if length < 0 {
		return nil, fmt.Errorf("invalid JSON span [%d,%d)", span.Start, span.End)
	}
	if maxBytes >= 0 && length > maxBytes {
		return nil, fmt.Errorf("JSON value is %d bytes, limit is %d", length, maxBytes)
	}
	data := make([]byte, length)
	if length == 0 {
		return data, nil
	}
	if _, err := io.ReadFull(io.NewSectionReader(reader, span.Start, length), data); err != nil {
		return nil, err
	}
	return data, nil
}

// FirstJSONSpanByte returns the first non-whitespace byte in span.
func FirstJSONSpanByte(reader io.ReaderAt, span JSONValueSpan) (byte, error) {
	scanner, err := newJSONReaderAtScanner(reader, span.Start, span.End)
	if err != nil {
		return 0, err
	}
	scanner.skipWhitespace()
	return scanner.peekByte()
}

// JSONFieldEdit describes a top-level field rewrite. Delete takes precedence;
// a nil Replacement means the original field is copied unchanged.
type JSONFieldEdit struct {
	Delete      bool
	Replacement []byte
}

// JSONFieldAddition appends a new top-level field with an already-encoded JSON
// value.
type JSONFieldAddition struct {
	Name  string
	Value []byte
}

// NewTopLevelJSONObjectReader creates an allocation-bounded reader for a
// rewritten object. Large untouched values are copied directly from reader by
// section; only replacement/addition metadata is held in memory.
func NewTopLevelJSONObjectReader(
	reader io.ReaderAt,
	fields []JSONFieldSpan,
	edits map[string]JSONFieldEdit,
	additions []JSONFieldAddition,
) (io.Reader, int64, error) {
	parts := make([]io.Reader, 0, 2+len(fields)*4+len(additions)*4)
	parts = append(parts, bytes.NewReader([]byte{'{'}))
	totalSize := int64(1)
	writtenFields := 0
	appendComma := func() {
		if writtenFields > 0 {
			parts = append(parts, bytes.NewReader([]byte{','}))
			totalSize++
		}
		writtenFields++
	}

	for _, field := range fields {
		edit, hasEdit := edits[field.Name]
		if hasEdit && edit.Delete {
			continue
		}
		appendComma()
		if !hasEdit || edit.Replacement == nil {
			length := field.Value.End - field.Key.Start
			if length < 0 {
				return nil, 0, fmt.Errorf("invalid JSON field span for %q", field.Name)
			}
			parts = append(parts, io.NewSectionReader(reader, field.Key.Start, length))
			totalSize += length
			continue
		}
		if !json.Valid(edit.Replacement) {
			return nil, 0, fmt.Errorf("replacement for JSON field %q is invalid", field.Name)
		}
		keyLength := field.Key.End - field.Key.Start
		parts = append(parts,
			io.NewSectionReader(reader, field.Key.Start, keyLength),
			bytes.NewReader([]byte{':'}),
			bytes.NewReader(edit.Replacement),
		)
		totalSize += keyLength + 1 + int64(len(edit.Replacement))
	}

	for _, addition := range additions {
		if !json.Valid(addition.Value) {
			return nil, 0, fmt.Errorf("addition for JSON field %q is invalid", addition.Name)
		}
		encodedName, err := Marshal(addition.Name)
		if err != nil {
			return nil, 0, err
		}
		appendComma()
		parts = append(parts,
			bytes.NewReader(encodedName),
			bytes.NewReader([]byte{':'}),
			bytes.NewReader(addition.Value),
		)
		totalSize += int64(len(encodedName) + 1 + len(addition.Value))
	}

	parts = append(parts, bytes.NewReader([]byte{'}'}))
	totalSize++
	return io.MultiReader(parts...), totalSize, nil
}
