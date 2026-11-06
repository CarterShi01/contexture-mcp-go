package graph_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

// Candidate protocol.md SHA256:
// 590915a99ae4224cd8ee186e5ff02ce40e556bacb41fc240be05fe505c0fd9c2.
// These exercise syntax only. No lease, authority, staleness or Host is implied.
const generation = "00000000-0000-0000-0000-000000000000"

func TestManagedReferenceFixedWire(t *testing.T) {
	want := "ctxg:v1:Zw:" + generation + ":bg"
	wire, err := graph.FormatReference("g", generation, "n")
	if err != nil || wire != want {
		t.Fatal(wire, err)
	}
	r, err := graph.ParseReference(want)
	if err != nil {
		t.Fatal(err)
	}
	if r.GraphID() != "g" || r.NodeID() != "n" || r.GenerationUUID() != generation {
		t.Fatal("reference fields")
	}
	if got, err := r.Wire(); err != nil || got != want {
		t.Fatal(got, err)
	}
	for _, uuid := range []string{generation, "ffffffff-ffff-ffff-ffff-ffffffffffff", "01234567-89ab-cdef-0123-456789abcdef"} {
		for _, id := range []string{"/a:b~c", "\x00\n", "\ufeff", "é", "é", strings.Repeat("😀", 64), strings.Repeat("a", 256)} {
			wire, err := graph.FormatReference(id, uuid, id)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := graph.ParseReference(wire)
			if err != nil || parsed.GraphID() != id || parsed.NodeID() != id || parsed.GenerationUUID() != uuid {
				t.Fatal("roundtrip", err)
			}
		}
	}
	a, _ := graph.FormatReference("é", generation, "n")
	b, _ := graph.FormatReference("é", generation, "n")
	if a == b {
		t.Fatal("IDs were normalized")
	}
	var zero graph.ManagedReference
	if _, err := zero.Wire(); err == nil {
		t.Fatal("zero reference admitted")
	}
}

func TestRejectNoncanonicalReferences(t *testing.T) {
	valid := "ctxg:v1:Zw:" + generation + ":bg"
	cases := []string{
		"", "ctxg:v1", valid + ":extra", strings.Replace(valid, "ctxg", "CTXG", 1), strings.Replace(valid, "v1", "v2", 1),
		valid + "\n", " " + valid, strings.Repeat("a", 1025), strings.Replace(valid, "Zw", "Zx", 1),
		strings.Replace(valid, "Zw", "Zw==", 1), strings.Replace(valid, "Zw", "Z", 1), strings.Replace(valid, "Zw", "", 1),
		strings.Replace(valid, "Zw", "/w", 1), strings.Replace(valid, "Zw", "_w", 1), strings.Replace(valid, "Zw", "+w", 1),
		strings.Replace(valid, "Zw", "Z\nw", 1), strings.Replace(valid, "Zw", "Z w", 1), strings.Replace(valid, "Zw", "Zé", 1),
		strings.Replace(valid, "bg", "bh", 1), strings.Replace(valid, "bg", "bg=", 1),
		strings.Replace(valid, generation, "00000000000000000000000000000000", 1),
		strings.Replace(valid, generation, "{00000000-0000-0000-0000-000000000000}", 1),
		strings.Replace(valid, generation, "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF", 1),
		strings.Replace(valid, generation, "00000000_0000-0000-0000-000000000000", 1),
		strings.Replace(valid, generation, "00000000-0000-0000-0000-00000000000g", 1),
	}
	for _, bytes := range [][]byte{{0xff}, {0xed, 0xa0, 0x80}, []byte(strings.Repeat("a", 257))} {
		cases = append(cases, "ctxg:v1:"+base64.RawURLEncoding.EncodeToString(bytes)+":"+generation+":bg")
	}
	for _, wire := range cases {
		if _, err := graph.ParseReference(wire); err == nil {
			t.Fatalf("accepted %q", wire)
		}
	}
	for _, ids := range [][3]string{{"", generation, "n"}, {"g", generation, ""}, {"g", "", "n"}, {strings.Repeat("a", 257), generation, "n"}, {"g", generation, string([]byte{0xff})}} {
		if _, err := graph.NewManagedReference(ids[0], ids[1], ids[2]); err == nil {
			t.Fatal("constructor accepted invalid fields")
		}
		if _, err := graph.FormatReference(ids[0], ids[1], ids[2]); err == nil {
			t.Fatal("formatter accepted invalid fields")
		}
	}
}
