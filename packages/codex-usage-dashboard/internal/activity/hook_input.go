package activity

import (
	"bufio"
	"encoding/json"
	"io"
)

const (
	maxHookJSONDepth       = 128
	maxProjectedStringJSON = 6*MaxIDLength + 2
)

// projectHookInput validates one JSON object while retaining only the three
// lifecycle fields used by the activity protocol. Unknown values are scanned
// incrementally instead of being materialized, so valid large prompts, tool
// payloads, and assistant messages do not become part of telemetry memory.
func projectHookInput(reader io.Reader, maxPayload int64) (hookInput, error) {
	if reader == nil || maxPayload <= 0 || maxPayload > MaximumHookPayload {
		return hookInput{}, ErrHookReport
	}
	limited := &io.LimitedReader{R: reader, N: maxPayload + 1}
	parser := hookJSONParser{reader: bufio.NewReaderSize(limited, 4096)}
	input, err := parser.projectObject()
	if err == nil {
		err = parser.requireEOF()
	}
	if maxPayload+1-limited.N > maxPayload {
		return hookInput{}, ErrHookReport
	}
	if err != nil {
		return hookInput{}, ErrHookReport
	}
	return input, nil
}

type hookJSONParser struct {
	reader *bufio.Reader
}

func (parser *hookJSONParser) projectObject() (hookInput, error) {
	var input hookInput
	opening, err := parser.readNonSpace()
	if err != nil || opening != '{' {
		return hookInput{}, ErrHookReport
	}
	if closing, err := parser.peekNonSpace(); err != nil {
		return hookInput{}, err
	} else if closing == '}' {
		_, _ = parser.reader.ReadByte()
		return input, nil
	}

	for {
		field, captured, err := parser.readString(maxProjectedStringJSON)
		if err != nil {
			return hookInput{}, err
		}
		separator, err := parser.readNonSpace()
		if err != nil || separator != ':' {
			return hookInput{}, ErrHookReport
		}

		if captured {
			switch field {
			case "hook_event_name":
				input.HookEventName, err = parser.readRequiredString()
			case "session_id":
				input.SessionID, err = parser.readRequiredString()
			case "turn_id":
				input.TurnID, err = parser.readRequiredString()
			default:
				err = parser.skipValue(0)
			}
		} else {
			err = parser.skipValue(0)
		}
		if err != nil {
			return hookInput{}, err
		}

		delimiter, err := parser.readNonSpace()
		if err != nil {
			return hookInput{}, err
		}
		switch delimiter {
		case '}':
			return input, nil
		case ',':
			if next, peekErr := parser.peekNonSpace(); peekErr != nil || next == '}' {
				return hookInput{}, ErrHookReport
			}
		default:
			return hookInput{}, ErrHookReport
		}
	}
}

func (parser *hookJSONParser) readRequiredString() (string, error) {
	value, captured, err := parser.readString(maxProjectedStringJSON)
	if err != nil || !captured {
		return "", ErrHookReport
	}
	return value, nil
}

func (parser *hookJSONParser) skipValue(depth int) error {
	if depth > maxHookJSONDepth {
		return ErrHookReport
	}
	first, err := parser.readNonSpace()
	if err != nil {
		return err
	}
	switch first {
	case '"':
		_, _, err = parser.scanStringAfterQuote(0)
		return err
	case '{':
		return parser.skipObject(depth + 1)
	case '[':
		return parser.skipArray(depth + 1)
	case 't':
		return parser.skipLiteral("rue")
	case 'f':
		return parser.skipLiteral("alse")
	case 'n':
		return parser.skipLiteral("ull")
	default:
		if first == '-' || isDigit(first) {
			return parser.skipNumber(first)
		}
		return ErrHookReport
	}
}

func (parser *hookJSONParser) skipObject(depth int) error {
	if depth > maxHookJSONDepth {
		return ErrHookReport
	}
	if next, err := parser.peekNonSpace(); err != nil {
		return err
	} else if next == '}' {
		_, _ = parser.reader.ReadByte()
		return nil
	}
	for {
		if _, _, err := parser.readString(0); err != nil {
			return err
		}
		separator, err := parser.readNonSpace()
		if err != nil || separator != ':' {
			return ErrHookReport
		}
		if err := parser.skipValue(depth); err != nil {
			return err
		}
		delimiter, err := parser.readNonSpace()
		if err != nil {
			return err
		}
		switch delimiter {
		case '}':
			return nil
		case ',':
			if next, peekErr := parser.peekNonSpace(); peekErr != nil || next == '}' {
				return ErrHookReport
			}
		default:
			return ErrHookReport
		}
	}
}

func (parser *hookJSONParser) skipArray(depth int) error {
	if depth > maxHookJSONDepth {
		return ErrHookReport
	}
	if next, err := parser.peekNonSpace(); err != nil {
		return err
	} else if next == ']' {
		_, _ = parser.reader.ReadByte()
		return nil
	}
	for {
		if err := parser.skipValue(depth); err != nil {
			return err
		}
		delimiter, err := parser.readNonSpace()
		if err != nil {
			return err
		}
		switch delimiter {
		case ']':
			return nil
		case ',':
			if next, peekErr := parser.peekNonSpace(); peekErr != nil || next == ']' {
				return ErrHookReport
			}
		default:
			return ErrHookReport
		}
	}
}

func (parser *hookJSONParser) readString(captureLimit int) (string, bool, error) {
	opening, err := parser.readNonSpace()
	if err != nil || opening != '"' {
		return "", false, ErrHookReport
	}
	return parser.scanStringAfterQuote(captureLimit)
}

func (parser *hookJSONParser) scanStringAfterQuote(captureLimit int) (string, bool, error) {
	captured := captureLimit >= 2
	raw := make([]byte, 0, min(captureLimit, 64))
	if captured {
		raw = append(raw, '"')
	}
	appendRaw := func(value byte) {
		if !captured {
			return
		}
		if len(raw) == captureLimit {
			captured = false
			raw = nil
			return
		}
		raw = append(raw, value)
	}

	for {
		current, err := parser.reader.ReadByte()
		if err != nil {
			return "", false, ErrHookReport
		}
		appendRaw(current)
		switch current {
		case '"':
			if !captured {
				return "", false, nil
			}
			var decoded string
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return "", false, ErrHookReport
			}
			return decoded, true, nil
		case '\\':
			escaped, err := parser.reader.ReadByte()
			if err != nil {
				return "", false, ErrHookReport
			}
			appendRaw(escaped)
			switch escaped {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				for index := 0; index < 4; index++ {
					hex, hexErr := parser.reader.ReadByte()
					if hexErr != nil || !isHex(hex) {
						return "", false, ErrHookReport
					}
					appendRaw(hex)
				}
			default:
				return "", false, ErrHookReport
			}
		default:
			if current < 0x20 {
				return "", false, ErrHookReport
			}
		}
	}
}

func (parser *hookJSONParser) skipLiteral(remainder string) error {
	for index := range remainder {
		current, err := parser.reader.ReadByte()
		if err != nil || current != remainder[index] {
			return ErrHookReport
		}
	}
	return parser.requireValueBoundary()
}

func (parser *hookJSONParser) skipNumber(first byte) error {
	current := first
	if current == '-' {
		var err error
		current, err = parser.reader.ReadByte()
		if err != nil {
			return ErrHookReport
		}
	}
	switch {
	case current == '0':
		if next, err := parser.peekByte(); err == nil && isDigit(next) {
			return ErrHookReport
		}
	case current >= '1' && current <= '9':
		parser.skipDigits()
	default:
		return ErrHookReport
	}

	if next, err := parser.peekByte(); err == nil && next == '.' {
		_, _ = parser.reader.ReadByte()
		if !parser.skipDigits() {
			return ErrHookReport
		}
	}
	if next, err := parser.peekByte(); err == nil && (next == 'e' || next == 'E') {
		_, _ = parser.reader.ReadByte()
		if sign, signErr := parser.peekByte(); signErr == nil && (sign == '+' || sign == '-') {
			_, _ = parser.reader.ReadByte()
		}
		if !parser.skipDigits() {
			return ErrHookReport
		}
	}
	return parser.requireValueBoundary()
}

func (parser *hookJSONParser) skipDigits() bool {
	found := false
	for {
		next, err := parser.peekByte()
		if err != nil || !isDigit(next) {
			return found
		}
		_, _ = parser.reader.ReadByte()
		found = true
	}
}

func (parser *hookJSONParser) requireValueBoundary() error {
	next, err := parser.peekByte()
	if err == io.EOF {
		return nil
	}
	if err != nil || !isValueBoundary(next) {
		return ErrHookReport
	}
	return nil
}

func (parser *hookJSONParser) requireEOF() error {
	for {
		current, err := parser.reader.ReadByte()
		if err == io.EOF {
			return nil
		}
		if err != nil || !isJSONSpace(current) {
			return ErrHookReport
		}
	}
}

func (parser *hookJSONParser) peekNonSpace() (byte, error) {
	for {
		next, err := parser.peekByte()
		if err != nil {
			return 0, ErrHookReport
		}
		if !isJSONSpace(next) {
			return next, nil
		}
		_, _ = parser.reader.ReadByte()
	}
}

func (parser *hookJSONParser) readNonSpace() (byte, error) {
	if _, err := parser.peekNonSpace(); err != nil {
		return 0, err
	}
	return parser.reader.ReadByte()
}

func (parser *hookJSONParser) peekByte() (byte, error) {
	next, err := parser.reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return next[0], nil
}

func isJSONSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

func isValueBoundary(value byte) bool {
	return isJSONSpace(value) || value == ',' || value == ']' || value == '}'
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func isHex(value byte) bool {
	return isDigit(value) || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}
