package apicmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type fieldInput struct {
	Raw   string
	Typed bool
}

func parseFields(fields []fieldInput, stdin io.Reader) (map[string]any, error) {
	result := make(map[string]any)
	for _, field := range fields {
		key, raw, hasValue := strings.Cut(field.Raw, "=")
		parts, err := parseFieldKey(key)
		if err != nil {
			return nil, err
		}
		if !hasValue && parts[len(parts)-1] != "" {
			return nil, fmt.Errorf("field %q must use key=value", field.Raw)
		}
		var value any = raw
		if field.Typed && hasValue {
			value, err = magicFieldValue(raw, stdin)
			if err != nil {
				return nil, err
			}
		}
		current, exists := result[parts[0]]
		next, err := insertField(current, exists, parts[1:], value, !hasValue)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", key, err)
		}
		result[parts[0]] = next
	}
	return result, nil
}

func parseFieldKey(key string) ([]string, error) {
	first, rest, hasBracket := strings.Cut(key, "[")
	if first == "" || strings.Contains(first, "]") {
		return nil, fmt.Errorf("invalid field key %q", key)
	}
	parts := []string{first}
	if !hasBracket {
		return parts, nil
	}
	rest = "[" + rest
	for rest != "" {
		if !strings.HasPrefix(rest, "[") {
			return nil, fmt.Errorf("invalid field key %q", key)
		}
		end := strings.IndexByte(rest, ']')
		if end < 0 || strings.Contains(rest[1:end], "[") {
			return nil, fmt.Errorf("invalid field key %q", key)
		}
		parts = append(parts, rest[1:end])
		rest = rest[end+1:]
	}
	return parts, nil
}

func magicFieldValue(raw string, stdin io.Reader) (any, error) {
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	if strings.HasPrefix(raw, "@") {
		var data []byte
		var err error
		if raw == "@-" {
			data, err = io.ReadAll(stdin)
		} else {
			data, err = os.ReadFile(strings.TrimPrefix(raw, "@"))
		}
		if err != nil {
			return nil, err
		}
		return string(data), nil
	}
	if number, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return number, nil
	}
	return raw, nil
}

func insertField(current any, exists bool, parts []string, value any, empty bool) (any, error) {
	if len(parts) == 0 {
		if empty {
			return nil, fmt.Errorf("empty array marker requires brackets")
		}
		if exists {
			return nil, fmt.Errorf("unexpected override existing field")
		}
		return value, nil
	}
	if parts[0] == "" {
		var array []any
		if exists {
			var ok bool
			array, ok = current.([]any)
			if !ok {
				return nil, fmt.Errorf("conflicting array field")
			}
		} else {
			array = []any{}
		}
		if len(parts) == 1 {
			if !empty {
				array = append(array, value)
			}
			return array, nil
		}
		index := len(array) - 1
		useLast := false
		if index >= 0 && parts[1] != "" {
			if last, ok := array[index].(map[string]any); ok {
				child, occupied := last[parts[1]]
				_, isArray := child.([]any)
				useLast = !occupied || isArray
			}
		}
		if !useLast {
			array = append(array, nil)
			index++
		}
		next, err := insertField(array[index], useLast, parts[1:], value, empty)
		if err != nil {
			return nil, err
		}
		array[index] = next
		return array, nil
	}
	var object map[string]any
	if exists {
		var ok bool
		object, ok = current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("conflicting nested field")
		}
	} else {
		object = make(map[string]any)
	}
	child, childExists := object[parts[0]]
	next, err := insertField(child, childExists, parts[1:], value, empty)
	if err != nil {
		return nil, err
	}
	object[parts[0]] = next
	return object, nil
}
