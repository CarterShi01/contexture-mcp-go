// Package graph contains the bounded managed canonical/reference candidate spike.
// It is separate from the legacy Catalog profile and is not a public facade.
package graph

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	CanonicalProfile       = "contexture.canonical/jcs-binary64-safeint-v1"
	MaxSafeInteger   int64 = 9007199254740991
	MaxCounter             = "99999999999999999999"
	// MaxJSONDepth is a native processing budget, not a cross-binding contract.
	MaxJSONDepth = 256
)

// GraphError is the minimal diagnostic for this spike, not full error parity.
type GraphError struct{ code, message, location string }

func (e *GraphError) Error() string    { return e.message }
func (e *GraphError) Code() string     { return e.code }
func (e *GraphError) Location() string { return e.location }
func invalid(message string) error     { return diagnostic("schema_invalid", message, "") }
func diagnostic(code, message, location string) error {
	return &GraphError{code: code, message: message, location: location}
}
func atLocation(err error, location string) error {
	if e, ok := err.(*GraphError); ok {
		return diagnostic(e.code, e.message, location)
	}
	return err
}
func pointer(parent, key string) string {
	result := parent + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
	if utf8.RuneCountInString(result) > 4096 {
		return parent
	}
	return result
}
func depthCheck(depth int) error {
	if depth > MaxJSONDepth {
		return diagnostic("limit_exceeded", "JSON nesting exceeds processing budget", "")
	}
	return nil
}

// DigestDomains returns a fresh copy of the exact eleven candidate domains.
func DigestDomains() []string {
	return []string{"contexture.graph/v1", "contexture.facts/v1", "contexture.manifest/v1",
		"contexture.journal-record/v1", "contexture.view/v1", "contexture.page/v1",
		"contexture.command/v1", "contexture.preview/v1", "contexture.journal-payload/v1",
		"contexture.checkpoint/v1", "contexture.file-journal-backup/v1"}
}

// FrozenJSON owns a detached tree. No map, slice or mutable byte view escapes.
// The zero value represents JSON null. Copies can be read concurrently.
type FrozenJSON struct{ value any }

// FreezeJSON accepts only nil, bool, string, finite float64, safe native integer
// types, []any and map[string]any (including Go aliases of those types). Named
// wrapper types, float32, json.Number, structs and custom marshalers are rejected.
// float64 values have already lost their lexemes: imports MUST use DecodeJSON.
// Inputs must not be concurrently mutated while being copied.
func FreezeJSON(value any) (FrozenJSON, error) {
	copy, err := cloneJSON(value)
	return FrozenJSON{copy}, err
}

// JSON returns a recursively detached ordinary JSON tree, never the owned tree.
func (f FrozenJSON) JSON() any                  { copy, _ := cloneJSON(f.value); return copy }
func (f FrozenJSON) Canonical() ([]byte, error) { return CanonicalJSON(f.value) }

func cloneJSON(value any) (any, error) {
	type identity struct {
		kind    reflect.Kind
		pointer uintptr
		length  int
	}
	active := map[identity]bool{}
	var visit func(any, int, string) (any, error)
	visit = func(item any, depth int, location string) (out any, err error) {
		defer func() {
			if e, ok := err.(*GraphError); ok && e.code == "schema_invalid" && e.location == "" {
				err = atLocation(err, location)
			}
		}()
		if err := depthCheck(depth); err != nil {
			return nil, err
		}
		switch v := item.(type) {
		case nil, bool:
			return v, nil
		case string:
			if !utf8.ValidString(v) {
				return nil, invalid("JSON strings must contain Unicode scalars")
			}
			return v, nil
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, invalid("JSON numbers must be finite")
			}
			return v, nil
		case int:
			return visitInteger(int64(v))
		case int8:
			return visitInteger(int64(v))
		case int16:
			return visitInteger(int64(v))
		case int32:
			return visitInteger(int64(v))
		case int64:
			return visitInteger(v)
		case uint:
			return visitUnsigned(uint64(v))
		case uint8:
			return visitUnsigned(uint64(v))
		case uint16:
			return visitUnsigned(uint64(v))
		case uint32:
			return visitUnsigned(uint64(v))
		case uint64:
			return visitUnsigned(v)
		case []any:
			id := identity{reflect.Slice, reflect.ValueOf(v).Pointer(), len(v)}
			if active[id] {
				return nil, invalid("Cyclic values are not JSON")
			}
			active[id] = true
			defer delete(active, id)
			out := make([]any, len(v))
			for i, child := range v {
				x, err := visit(child, depth+1, pointer(location, strconv.Itoa(i)))
				if err != nil {
					return nil, err
				}
				out[i] = x
			}
			return out, nil
		case map[string]any:
			id := identity{reflect.Map, uintptr(reflect.ValueOf(v).UnsafePointer()), 0}
			if active[id] {
				return nil, invalid("Cyclic values are not JSON")
			}
			active[id] = true
			defer delete(active, id)
			out := make(map[string]any, len(v))
			for key, child := range v {
				if !utf8.ValidString(key) {
					return nil, invalid("JSON keys must contain Unicode scalars")
				}
				x, err := visit(child, depth+1, pointer(location, key))
				if err != nil {
					return nil, err
				}
				out[key] = x
			}
			return out, nil
		default:
			return nil, invalid("Value is not JSON")
		}
	}
	return visit(value, 0, "")
}

func visitInteger(v int64) (any, error) {
	if v < -MaxSafeInteger || v > MaxSafeInteger {
		return nil, invalid("Integer exceeds binary64 safe integer range")
	}
	return float64(v), nil
}
func visitUnsigned(v uint64) (any, error) {
	if v > uint64(MaxSafeInteger) {
		return nil, invalid("Integer exceeds binary64 safe integer range")
	}
	return float64(v), nil
}

// DecodeJSON validates UTF-8, surrogate pairs, duplicate decoded keys and exact
// mathematical integer safety BEFORE converting any raw number to binary64.
func DecodeJSON(data []byte) (any, error) {
	if !utf8.Valid(data) || bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return nil, invalid("Invalid UTF-8 JSON input")
	}
	if err := validateEscapes(data); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		if err := depthCheck(depth); err != nil {
			return nil, err
		}
		token, err := d.Token()
		if err != nil {
			return nil, invalid("Invalid JSON input")
		}
		switch v := token.(type) {
		case json.Number:
			return rawNumber(string(v))
		case json.Delim:
			switch v {
			case '{':
				out := map[string]any{}
				for d.More() {
					keyToken, err := d.Token()
					if err != nil {
						return nil, invalid("Invalid JSON object")
					}
					key, ok := keyToken.(string)
					if !ok {
						return nil, invalid("Invalid JSON object key")
					}
					if _, exists := out[key]; exists {
						return nil, invalid("Duplicate JSON object key")
					}
					child, err := read(depth + 1)
					if err != nil {
						return nil, err
					}
					out[key] = child
				}
				end, err := d.Token()
				if err != nil || end != json.Delim('}') {
					return nil, invalid("Invalid JSON object")
				}
				return out, nil
			case '[':
				out := []any{}
				for d.More() {
					child, err := read(depth + 1)
					if err != nil {
						return nil, err
					}
					out = append(out, child)
				}
				end, err := d.Token()
				if err != nil || end != json.Delim(']') {
					return nil, invalid("Invalid JSON array")
				}
				return out, nil
			default:
				return nil, invalid("Invalid JSON delimiter")
			}
		case nil, bool, string:
			return v, nil
		default:
			return nil, invalid("Invalid JSON token")
		}
	}
	value, err := read(0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, invalid("Trailing JSON input")
	}
	return value, nil
}

// encoding/json replaces malformed surrogates, so reject them before decoding.
func validateEscapes(data []byte) error {
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		i++
		for ; i < len(data) && data[i] != '"'; i++ {
			if data[i] != '\\' {
				continue
			}
			i++
			if i >= len(data) {
				return invalid("Invalid JSON escape")
			}
			if data[i] != 'u' {
				continue
			}
			unit, err := hexUnit(data, i+1)
			if err != nil {
				return err
			}
			i += 4
			if unit >= 0xdc00 && unit <= 0xdfff {
				return invalid("Lone JSON surrogate")
			}
			if unit >= 0xd800 && unit <= 0xdbff {
				if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return invalid("Lone JSON surrogate")
				}
				low, err := hexUnit(data, i+3)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return invalid("Lone JSON surrogate")
				}
				i += 6
			}
		}
	}
	return nil
}
func hexUnit(data []byte, start int) (uint64, error) {
	if start+4 > len(data) {
		return 0, invalid("Invalid JSON Unicode escape")
	}
	v, err := strconv.ParseUint(string(data[start:start+4]), 16, 16)
	if err != nil {
		return 0, invalid("Invalid JSON Unicode escape")
	}
	return v, nil
}

func rawNumber(lexeme string) (float64, error) {
	unsigned := strings.TrimPrefix(lexeme, "-")
	mantissa, exponent, _ := strings.Cut(strings.ToLower(unsigned), "e")
	fraction := 0
	if dot := strings.IndexByte(mantissa, '.'); dot >= 0 {
		fraction = len(mantissa) - dot - 1
	}
	coefficient := strings.TrimLeft(strings.ReplaceAll(mantissa, ".", ""), "0")
	if coefficient == "" {
		if strings.HasPrefix(lexeme, "-") {
			return math.Copysign(0, -1), nil
		}
		return 0, nil
	}
	// Saturate relative to input length. Never construct 10**untrustedExponent.
	bound := len(lexeme) + 400
	magnitude := 0
	for _, c := range strings.TrimLeft(exponent, "+-") {
		if magnitude > bound/10 || magnitude*10+int(c-'0') > bound {
			magnitude = bound
			break
		}
		magnitude = magnitude*10 + int(c-'0')
	}
	if strings.HasPrefix(exponent, "-") {
		magnitude = -magnitude
	}
	trimmed := strings.TrimRight(coefficient, "0")
	scale := magnitude - fraction + len(coefficient) - len(trimmed)
	coefficient = trimmed
	if scale >= 0 {
		if len(coefficient)+scale > 16 {
			return 0, invalid("Integer lexeme exceeds binary64 safe integer range")
		}
		integer := coefficient + strings.Repeat("0", scale)
		if len(integer) == 16 && integer > "9007199254740991" {
			return 0, invalid("Integer lexeme exceeds binary64 safe integer range")
		}
	}
	number, err := strconv.ParseFloat(lexeme, 64)
	if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
		return 0, invalid("JSON number overflows binary64")
	}
	if number == 0 {
		return 0, invalid("Nonzero JSON number underflows binary64")
	}
	return number, nil
}

// CanonicalJSON emits RFC 8785 strings, UTF-16 key order and ECMAScript binary64
// number notation. Go's shortest-roundtrip digits are retained, but its 'g'
// exponent thresholds and JSON/HTML escaping are deliberately not used.
func CanonicalJSON(value any) ([]byte, error) {
	copy, err := cloneJSON(value)
	if err != nil {
		return nil, err
	}
	var out []byte
	var write func(any)
	write = func(item any) {
		switch v := item.(type) {
		case nil:
			out = append(out, "null"...)
		case bool:
			out = strconv.AppendBool(out, v)
		case string:
			out = appendJSONString(out, v)
		case float64:
			out = append(out, formatNumber(v)...)
		case []any:
			out = append(out, '[')
			for i, child := range v {
				if i > 0 {
					out = append(out, ',')
				}
				write(child)
			}
			out = append(out, ']')
		case map[string]any:
			keys := make([]string, 0, len(v))
			for key := range v {
				keys = append(keys, key)
			}
			slices.SortFunc(keys, func(a, b string) int { return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b))) })
			out = append(out, '{')
			for i, key := range keys {
				if i > 0 {
					out = append(out, ',')
				}
				out = appendJSONString(out, key)
				out = append(out, ':')
				write(v[key])
			}
			out = append(out, '}')
		}
	}
	write(copy)
	return out, nil
}

func appendJSONString(out []byte, value string) []byte {
	const hexDigits = "0123456789abcdef"
	out = append(out, '"')
	for _, c := range value {
		switch c {
		case '"', '\\':
			out = append(out, '\\', byte(c))
		case '\b':
			out = append(out, '\\', 'b')
		case '\t':
			out = append(out, '\\', 't')
		case '\n':
			out = append(out, '\\', 'n')
		case '\f':
			out = append(out, '\\', 'f')
		case '\r':
			out = append(out, '\\', 'r')
		default:
			if c < 0x20 {
				out = append(out, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&15])
			} else {
				out = utf8.AppendRune(out, c)
			}
		}
	}
	return append(out, '"')
}

func formatNumber(value float64) string {
	if value == 0 {
		return "0"
	}
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	text := strconv.FormatFloat(value, 'e', -1, 64)
	mantissa, exponentText, _ := strings.Cut(text, "e")
	exponent, _ := strconv.Atoi(exponentText)
	digits := strings.ReplaceAll(mantissa, ".", "")
	if exponent >= -6 && exponent < 21 {
		point := exponent + 1
		if point <= 0 {
			return sign + "0." + strings.Repeat("0", -point) + digits
		}
		if point >= len(digits) {
			return sign + digits + strings.Repeat("0", point-len(digits))
		}
		return sign + digits[:point] + "." + digits[point:]
	}
	exponentSign := ""
	if exponent >= 0 {
		exponentSign = "+"
	}
	return sign + mantissa + "e" + exponentSign + strconv.Itoa(exponent)
}

func CanonicalDigest(value any, domain string) (string, error) {
	if !slices.Contains(DigestDomains(), domain) {
		return "", invalid("Unsupported managed digest domain")
	}
	data, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(data)
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// ParseCounter parses canonical decimal strings exactly, including values above
// uint64. Every result is caller-owned; there is no mutable shared big.Int.
func ParseCounter(value string) (*big.Int, error) {
	if len(value) < 1 || len(value) > 20 || (len(value) > 1 && value[0] == '0') {
		return nil, invalid("Counter must be a canonical decimal string of at most 20 digits")
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return nil, invalid("Invalid decimal counter")
		}
	}
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return nil, fmt.Errorf("invalid decimal counter")
	}
	return result, nil
}

// ValidateID preserves opaque scalar text, including controls and BOM, exactly.
func ValidateID(value string) (string, error) {
	if !utf8.ValidString(value) || len(value) < 1 || len(value) > 256 {
		return "", invalid("Identity must contain 1 through 256 scalar UTF-8 bytes")
	}
	return value, nil
}
