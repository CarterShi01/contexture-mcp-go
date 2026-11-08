package introspection

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
)

func invalid(message string) error {
	return fmt.Errorf("%w: %s", foundation.ErrInvalidDeclaration, message)
}

var jsonNumber = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

// FreezeJSON detaches finite, acyclic native JSON without calling serialization
// methods. Native integers, big.Int and integer json.Number remain exact.
func FreezeJSON(value any) (any, error) {
	return freeze(reflect.ValueOf(value), map[visit]bool{})
}

type visit struct {
	kind reflect.Kind
	ptr  uintptr
}

func freeze(v reflect.Value, active map[visit]bool) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, nil
		}
		return freeze(v.Elem(), active)
	}
	if v.CanInterface() {
		switch value := v.Interface().(type) {
		case big.Int:
			return new(big.Int).Set(&value), nil
		case *big.Int:
			if value == nil {
				return nil, invalid("nil big integer is not JSON")
			}
			return new(big.Int).Set(value), nil
		case json.Number:
			raw := string(value)
			if !jsonNumber.MatchString(raw) {
				return nil, invalid("invalid JSON number")
			}
			if !strings.ContainsAny(raw, ".eE") {
				n, ok := new(big.Int).SetString(raw, 10)
				if !ok {
					return nil, invalid("invalid integer")
				}
				return n, nil
			}
			n, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
				return nil, invalid("JSON requires finite numbers")
			}
			return normalizeFloat(n)
		}
		if _, ok := v.Interface().(json.Marshaler); ok {
			return nil, invalid("custom serialization objects are not native JSON")
		}
	}
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return nil, invalid("JSON requires Unicode scalar strings")
		}
		return v.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return big.NewInt(v.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return new(big.Int).SetUint64(v.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return normalizeFloat(v.Float())
	case reflect.Map, reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Map && v.Type().Key().Kind() != reflect.String {
			return nil, invalid("JSON object keys must be strings")
		}
		key := visit{kind: v.Kind()}
		if v.Kind() != reflect.Array {
			key.ptr = v.Pointer()
			if active[key] {
				return nil, invalid("cyclic JSON data")
			}
			active[key] = true
			defer delete(active, key)
		}
		if v.Kind() == reflect.Map {
			result := map[string]any{}
			for _, field := range v.MapKeys() {
				if !utf8.ValidString(field.String()) {
					return nil, invalid("JSON requires Unicode scalar keys")
				}
				item, err := freeze(v.MapIndex(field), active)
				if err != nil {
					return nil, err
				}
				result[field.String()] = item
			}
			return result, nil
		}
		result := make([]any, v.Len())
		for i := range result {
			item, err := freeze(v.Index(i), active)
			if err != nil {
				return nil, err
			}
			result[i] = item
		}
		return result, nil
	default:
		return nil, invalid("Catalog requires native JSON; pointers, structs, functions and custom objects are not JSON")
	}
}

func normalizeFloat(value float64) (any, error) {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return nil, invalid("JSON requires finite numbers")
	}
	if math.Trunc(value) == value {
		integer, _ := new(big.Float).SetFloat64(value).Int(nil)
		return integer, nil
	}
	return value, nil
}

// ParseJSON preserves arbitrary integer tokens before binary64 conversion.
func ParseJSON(text string) (any, error) {
	if !utf8.ValidString(text) || !scalarJSONStrings(text) {
		return nil, invalid("JSON requires Unicode scalar strings")
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, invalid("expected one JSON value")
	}
	return FreezeJSON(value)
}

func scalarJSONStrings(text string) bool {
	// encoding/json repairs unpaired surrogate escapes. Reject before that
	// lossy conversion, while retaining valid surrogate pairs.
	inString := false
	for i := 0; i < len(text); i++ {
		if text[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || text[i] != '\\' {
			continue
		}
		i++
		if i >= len(text) {
			return false
		}
		if text[i] != 'u' {
			continue
		}
		if i+4 >= len(text) {
			return false
		}
		point, err := strconv.ParseUint(text[i+1:i+5], 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if point >= 0xdc00 && point <= 0xdfff {
			return false
		}
		if point >= 0xd800 && point <= 0xdbff {
			if i+6 >= len(text) || text[i+1:i+3] != `\u` {
				return false
			}
			low, err := strconv.ParseUint(text[i+3:i+7], 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

// JSONOptions selects legacy Catalog formatting, deliberately not managed JCS.
type JSONOptions struct {
	Indent *int
	Spaced bool
}

// FormatJSON uses Unicode code-point key order and Python finite-float
// normalization. It never uses Go's ordinary JSON float or HTML escaping.
func FormatJSON(value any, options JSONOptions) (string, error) {
	frozen, err := FreezeJSON(value)
	if err != nil {
		return "", err
	}
	gap := ""
	if options.Indent != nil {
		if *options.Indent > 1024 {
			return "", invalid("Catalog indent exceeds 1024")
		}
		gap = strings.Repeat(" ", max(0, *options.Indent))
	}
	var out bytes.Buffer
	var write func(any, int)
	quote := func(value string) {
		out.WriteByte('"')
		for _, r := range value {
			switch r {
			case '"', '\\':
				out.WriteByte('\\')
				out.WriteRune(r)
			case '\b':
				out.WriteString(`\b`)
			case '\f':
				out.WriteString(`\f`)
			case '\n':
				out.WriteString(`\n`)
			case '\r':
				out.WriteString(`\r`)
			case '\t':
				out.WriteString(`\t`)
			default:
				if r < 0x20 {
					fmt.Fprintf(&out, `\u%04x`, r)
				} else {
					out.WriteRune(r)
				}
			}
		}
		out.WriteByte('"')
	}
	write = func(item any, depth int) {
		switch value := item.(type) {
		case nil:
			out.WriteString("null")
		case bool:
			out.WriteString(strconv.FormatBool(value))
		case string:
			quote(value)
		case *big.Int:
			out.WriteString(value.String())
		case float64:
			out.WriteString(floatText(value))
		default:
			object, isObject := item.(map[string]any)
			array, _ := item.([]any)
			keys := []string{}
			size := len(array)
			if isObject {
				for key := range object {
					keys = append(keys, key)
				}
				sort.Strings(keys) // Scalar UTF-8 lexical order equals code-point order.
				size = len(keys)
				out.WriteByte('{')
			} else {
				out.WriteByte('[')
			}
			for i := 0; i < size; i++ {
				if i > 0 {
					out.WriteByte(',')
					if options.Indent == nil && options.Spaced {
						out.WriteByte(' ')
					}
				}
				if options.Indent != nil {
					out.WriteByte('\n')
					out.WriteString(strings.Repeat(gap, depth+1))
				}
				if isObject {
					quote(keys[i])
					out.WriteByte(':')
					if options.Indent != nil || options.Spaced {
						out.WriteByte(' ')
					}
					write(object[keys[i]], depth+1)
				} else {
					write(array[i], depth+1)
				}
			}
			if size > 0 && options.Indent != nil {
				out.WriteByte('\n')
				out.WriteString(strings.Repeat(gap, depth))
			}
			if isObject {
				out.WriteByte('}')
			} else {
				out.WriteByte(']')
			}
		}
	}
	write(frozen, 0)
	return out.String(), nil
}

func floatText(value float64) string {
	// Fractional binary64 values are always below 2^52. Python's only relevant
	// repr transition here is 1e-4; integral values have already become big.Int.
	if math.Abs(value) >= 1e-4 {
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
	raw := strconv.FormatFloat(value, 'e', -1, 64)
	parts := strings.Split(raw, "e")
	exponent, _ := strconv.Atoi(parts[1])
	return parts[0] + fmt.Sprintf("e%+03d", exponent)
}

// CanonicalDigest hashes an object using the legacy Node Catalog profile.
func CanonicalDigest(value any) (string, error) {
	frozen, err := FreezeJSON(value)
	if err != nil {
		return "", err
	}
	if _, ok := frozen.(map[string]any); !ok {
		return "", invalid("digest input must be a JSON object")
	}
	body, err := FormatJSON(frozen, JSONOptions{})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body))), nil
}
