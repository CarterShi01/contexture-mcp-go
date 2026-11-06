package graph_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

// Inline independent expected bytes, not a Python subprocess oracle or a
// normative fixture update. Candidate rawcanonical/vectors.json SHA256:
// ebe15fe56dd87714b5e2d4b240047aff7ea131565bda506d69c68f55589b4b9e.
// Candidate protocol.md SHA256:
// 590915a99ae4224cd8ee186e5ff02ce40e556bacb41fc240be05fe505c0fd9c2.
func TestRawCanonicalVectors(t *testing.T) {
	cases := []struct{ raw, want string }{
		{`{"\ue000":1,"\ud83d\ude00":2}`, "{\"😀\":2,\"\ue000\":1}"},
		{`-0.0`, `0`}, {`-0`, `0`}, {`0e99999999999999999999999999999`, `0`},
		{`-0.000e-99999999999999999999999999999`, `0`},
		{`333333333.33333329`, `333333333.3333333`},
		{`9007199254740991`, `9007199254740991`}, {`-9007199254740991.0`, `-9007199254740991`},
		{`900719925474099100e-2`, `9007199254740991`},
		// A mathematical fraction can round to an unsafe integral binary64.
		{`9007199254740991.5`, `9007199254740992`},
		{`9007199254740992.1`, `9007199254740992`},
		{`1e-6`, `0.000001`}, {`1e-7`, `1e-7`}, {`5e-324`, `5e-324`},
		{`{"2":2,"10":10,"01":1}`, `{"01":1,"10":10,"2":2}`},
		{`{"__proto__":null,"a/b~c":[true,false,null]}`, `{"__proto__":null,"a/b~c":[true,false,null]}`},
		{`"<>&/\u2028\u2029\u0000\b\t\n\f\r\u001f\"\\"`, "\"<>&/\u2028\u2029\\u0000\\b\\t\\n\\f\\r\\u001f\\\"\\\\\""},
		{`["é","e\u0301","\ufeff","\ud800\udc00"]`, "[\"é\",\"é\",\"\ufeff\",\"𐀀\"]"},
		{" \t\r\n{\"z\": [1.50, -2.0],\"a\":{}} ", `{"a":{},"z":[1.5,-2]}`},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			value, err := graph.DecodeJSON([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			got, err := graph.CanonicalJSON(value)
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			frozen, err := graph.FreezeJSON(value)
			if err != nil {
				t.Fatal(err)
			}
			again, err := frozen.Canonical()
			if err != nil || string(again) != tc.want {
				t.Fatalf("frozen got %q, %v", again, err)
			}
		})
	}
}

func TestRejectRawBeforeRounding(t *testing.T) {
	cases := []string{
		`9007199254740992`, `9007199254740993`, `-9007199254740992`, `9007199254740992.0`,
		`9007199254740992000000000e-9`, `9007199254740993e0`, `1e20`, `1.000e21`,
		`1e9999`, `1e-9999`, `1e999999999999999999999999`, `1e-999999999999999999999999`,
		`1.7976931348623157e308`, `2e-324`, `NaN`, `Infinity`, `-Infinity`,
		`{"a":1,"\u0061":2}`, `{"x":{"a":0,"a":1}}`, `{"😀":1,"\ud83d\ude00":2}`,
		`"\ud800"`, `"\udfff"`, `"\ud800\u0000"`, `"\ud800\ud800"`, `"\udc00\ud800"`,
		`"\ud800x"`, `"\uD800\n"`, `"\ud800\u"`, `"\u+123"`, `"\u-123"`,
		`"\x00"`, `"\uGGGG"`, `"\q"`, `"unterminated`, `"a` + "\n" + `b"`,
		`+1`, `01`, `-01`, `.1`, `1.`, `1e`, `--1`, `1e+`, `1e--1`,
		`true false`, `{}[]`, `null0`, `1 2`, ``, ` `, `[,]`, `[1,]`, `{"a":1,}`, `{"a" 1}`, `{"a":}`, `]`,
		string([]byte{'"', 0xff, '"'}), string([]byte{0xef, 0xbb, 0xbf, '{', '}'}),
		string([]byte{'"', 0xed, 0xa0, 0x80, '"'}), string([]byte{'"', 0xc0, 0x80, '"'}),
	}
	for i, raw := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			_, err := graph.DecodeJSON([]byte(raw))
			var ge *graph.GraphError
			if !errors.As(err, &ge) || ge.Code() != "schema_invalid" {
				t.Fatalf("accepted %q or wrong error: %v", raw, err)
			}
		})
	}
	// Exponents are scanned with saturation, not exponent-sized allocations.
	for _, raw := range []string{"1e" + strings.Repeat("9", 100000), "1e-" + strings.Repeat("9", 100000)} {
		if _, err := graph.DecodeJSON([]byte(raw)); err == nil {
			t.Fatal("huge exponent accepted")
		}
	}
	for _, raw := range []string{`"\\ud800"`, `"\\\""`, `"\uD800\uDC00"`} {
		if _, err := graph.DecodeJSON([]byte(raw)); err != nil {
			t.Fatalf("valid escape %s: %v", raw, err)
		}
	}
}

// RFC 8785 Appendix B: typed IEEE754 values deliberately bypass the raw safe
// integer gate. Constants are fixed expected outputs, not FormatFloat or JSON
// output used as an oracle. Includes round-to-even and all notation boundaries.
func TestRFC8785Binary64Vectors(t *testing.T) {
	cases := []struct {
		bits uint64
		want string
	}{
		{0x0000000000000000, "0"}, {0x8000000000000000, "0"},
		{0x0000000000000001, "5e-324"}, {0x8000000000000001, "-5e-324"},
		{0x7fefffffffffffff, "1.7976931348623157e+308"}, {0xffefffffffffffff, "-1.7976931348623157e+308"},
		{0x4340000000000000, "9007199254740992"}, {0xc340000000000000, "-9007199254740992"},
		{0x4430000000000000, "295147905179352830000"},
		{0x44b52d02c7e14af5, "9.999999999999997e+22"}, {0x44b52d02c7e14af6, "1e+23"}, {0x44b52d02c7e14af7, "1.0000000000000001e+23"},
		{0x444b1ae4d6e2ef4e, "999999999999999700000"}, {0x444b1ae4d6e2ef4f, "999999999999999900000"}, {0x444b1ae4d6e2ef50, "1e+21"},
		{0x3eb0c6f7a0b5ed8c, "9.999999999999997e-7"}, {0x3eb0c6f7a0b5ed8d, "0.000001"},
		{0x41b3de4355555553, "333333333.3333332"}, {0x41b3de4355555554, "333333333.33333325"},
		{0x41b3de4355555555, "333333333.3333333"}, {0x41b3de4355555556, "333333333.3333334"}, {0x41b3de4355555557, "333333333.33333343"},
		{0xbecbf647612f3696, "-0.0000033333333333333333"}, {0x43143ff3c1cb0959, "1424953923781206.2"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%016x", tc.bits), func(t *testing.T) {
			got, err := graph.CanonicalJSON(math.Float64frombits(tc.bits))
			if err != nil || string(got) != tc.want {
				t.Fatalf("%q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestTypedValuePolicyAndCycles(t *testing.T) {
	type namedMap map[string]any
	type namedFloat float64
	for i, value := range []any{
		math.NaN(), math.Inf(1), math.Inf(-1), int64(9007199254740992), int64(-9007199254740992),
		uint64(math.MaxUint64), int64(math.MinInt64), int64(math.MaxInt64), uintptr(1),
		float32(1), json.Number("1"), namedFloat(1), namedMap{}, struct{}{}, new(int), []int{1}, map[int]any{},
		string([]byte{0xff}), map[string]any{string([]byte{0xff}): nil},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, err := graph.FreezeJSON(value); err == nil {
				t.Fatalf("accepted %T", value)
			}
		})
	}
	for _, value := range []any{int(1), int8(-1), int16(2), int32(3), int64(graph.MaxSafeInteger), uint(1), uint8(2), uint16(3), uint32(4), uint64(graph.MaxSafeInteger)} {
		if _, err := graph.CanonicalJSON(value); err != nil {
			t.Fatal(err)
		}
	}
	m := map[string]any{}
	m["self"] = m
	s := make([]any, 1)
	s[0] = s
	mixed := map[string]any{}
	mixed["list"] = []any{mixed}
	for _, value := range []any{m, s, mixed} {
		if _, err := graph.FreezeJSON(value); err == nil {
			t.Fatal("cycle accepted")
		}
	}
	shared := map[string]any{"a": true}
	if _, err := graph.FreezeJSON([]any{shared, shared}); err != nil {
		t.Fatal("acyclic sharing rejected", err)
	}
	// A shortened view of the same backing slice is not itself a cycle.
	a := make([]any, 2)
	a[0] = a[:0]
	a[1] = nil
	if _, err := graph.FreezeJSON(a); err != nil {
		t.Fatal(err)
	}
}

func TestFrozenJSONDetachesEveryAlias(t *testing.T) {
	child := map[string]any{"x": "original"}
	input := map[string]any{"a": []any{child, child}}
	f, err := graph.FreezeJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	child["x"] = "mutated"
	input["b"] = true
	copy := f.JSON().(map[string]any)
	children := copy["a"].([]any)
	children[0].(map[string]any)["x"] = "returned mutation"
	if children[1].(map[string]any)["x"] != "original" {
		t.Fatal("shared returned alias")
	}
	want := `{"a":[{"x":"original"},{"x":"original"}]}`
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				got, err := f.Canonical()
				if err != nil || string(got) != want {
					t.Errorf("%q %v", got, err)
				}
				got[0] = '!'
				x := f.JSON().(map[string]any)
				x["a"] = nil
			}
		}()
	}
	wg.Wait()
	var zero graph.FrozenJSON
	got, err := zero.Canonical()
	if err != nil || string(got) != "null" {
		t.Fatal("zero", string(got), err)
	}
}

func TestCanonicalCountersIDsAndDomains(t *testing.T) {
	for _, wire := range []string{"0", "1", "18446744073709551615", "18446744073709551616", graph.MaxCounter} {
		got, err := graph.ParseCounter(wire)
		if err != nil || got.String() != wire {
			t.Fatal(wire, got, err)
		}
		got.SetInt64(0)
		again, _ := graph.ParseCounter(wire)
		if again.String() != wire {
			t.Fatal("counter alias")
		}
	}
	max, _ := graph.ParseCounter(graph.MaxCounter)
	if max.Cmp(new(big.Int).SetUint64(math.MaxUint64)) <= 0 {
		t.Fatal("counter narrowed to uint64")
	}
	for _, wire := range []string{"", "00", "01", "-1", "+1", "1.0", "1e2", " 1", "1\n", "１", "100000000000000000000"} {
		if _, err := graph.ParseCounter(wire); err == nil {
			t.Fatalf("counter accepted %q", wire)
		}
	}
	for _, id := range []string{"a", "\x00", "\ufeff", "é", "é", strings.Repeat("😀", 64), strings.Repeat("a", 256)} {
		if got, err := graph.ValidateID(id); err != nil || got != id {
			t.Fatal("ID", err)
		}
	}
	for _, id := range []string{"", strings.Repeat("😀", 65), strings.Repeat("a", 257), string([]byte{0xed, 0xa0, 0x80})} {
		if _, err := graph.ValidateID(id); err == nil {
			t.Fatal("invalid ID accepted")
		}
	}
	domains := []string{"contexture.graph/v1", "contexture.facts/v1", "contexture.manifest/v1", "contexture.journal-record/v1", "contexture.view/v1", "contexture.page/v1", "contexture.command/v1", "contexture.preview/v1", "contexture.journal-payload/v1", "contexture.checkpoint/v1", "contexture.file-journal-backup/v1"}
	if !reflect.DeepEqual(graph.DigestDomains(), domains) {
		t.Fatal("domain inventory drift")
	}
	graph.DigestDomains()[0] = "forged"
	seen := map[string]bool{}
	for _, domain := range domains {
		got, err := graph.CanonicalDigest(map[string]any{"a": 1}, domain)
		// Independently fixed payload and explicit NUL framing, not canonical output.
		h := sha256.Sum256([]byte(domain + "\x00" + `{"a":1}`))
		want := "sha256:" + hex.EncodeToString(h[:])
		if err != nil || got != want || seen[got] {
			t.Fatal(got, want, err)
		}
		seen[got] = true
	}
	for _, domain := range []string{"", "contexture.graph/v2", "contexture.canonical/jcs-binary64-safeint-v1", "contexture.graph/v1\x00", "contexture.snapshot/v1"} {
		if _, err := graph.CanonicalDigest(nil, domain); err == nil {
			t.Fatal("unknown domain accepted")
		}
	}
}

func TestDepthBudget(t *testing.T) {
	for _, depth := range []int{graph.MaxJSONDepth, graph.MaxJSONDepth + 1} {
		raw := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
		_, err := graph.DecodeJSON([]byte(raw))
		var value any = float64(0)
		for range depth {
			value = []any{value}
		}
		_, typedErr := graph.FreezeJSON(value)
		if depth == graph.MaxJSONDepth {
			if err != nil || typedErr != nil {
				t.Fatal(err, typedErr)
			}
		} else {
			for _, e := range []error{err, typedErr} {
				var ge *graph.GraphError
				if !errors.As(e, &ge) || ge.Code() != "limit_exceeded" {
					t.Fatal(e)
				}
			}
		}
	}
}
