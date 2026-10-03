package mapstructure

import (
	"reflect"
	"sort"
	"testing"
)

func TestMetadataUnusedInputBasic(t *testing.T) {
	type Inner struct {
		A string
	}
	type Outer struct {
		Name  string
		Inner Inner
	}
	input := map[string]any{
		"Name": "bob",
		"Inner": map[string]any{
			"A":   "x",
			"foo": 1,
			"bar": 2,
		},
		"extra": "e",
	}
	var md Metadata
	var out Outer
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"Inner": map[string]any{"bar": 2, "foo": 1},
		"extra": "e",
	}
	if !reflect.DeepEqual(md.UnusedInput, want) {
		t.Fatalf("got %#v want %#v", md.UnusedInput, want)
	}
	if _, ok := md.UnusedInput.(map[string]any)["Inner"].(map[string]any)["A"]; ok {
		t.Fatal("consumed sibling repeated")
	}
	sort.Strings(md.Unused)
	if !reflect.DeepEqual(md.Unused, []string{"Inner.bar", "Inner.foo", "extra"}) {
		t.Fatalf("flat unused: %#v", md.Unused)
	}
}

func TestMetadataUnusedInputRemainConsumed(t *testing.T) {
	type S struct {
		Name  string
		Extra map[string]any `mapstructure:",remain"`
	}
	input := map[string]any{
		"Name":   "bob",
		"a":      1,
		"nested": map[string]any{"x": 1},
	}
	var md Metadata
	var out S
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	if md.UnusedInput != nil {
		t.Fatalf("remain values must count as consumed, got %#v", md.UnusedInput)
	}
	if !reflect.DeepEqual(out.Extra, map[string]any{"a": 1, "nested": map[string]any{"x": 1}}) {
		t.Fatalf("remain: %#v", out.Extra)
	}
}

func TestMetadataUnusedInputDottedKey(t *testing.T) {
	type S struct {
		Name string
	}
	input := map[string]any{
		"Name":     "bob",
		"a.b":      1,
		"a.b.c":    2,
		"deeper.a": map[string]any{"z": 3},
	}
	var md Metadata
	var out S
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"a.b":      1,
		"a.b.c":    2,
		"deeper.a": map[string]any{"z": 3},
	}
	if !reflect.DeepEqual(md.UnusedInput, want) {
		t.Fatalf("got %#v want %#v", md.UnusedInput, want)
	}
}

func TestMetadataUnusedInputNonStringKeys(t *testing.T) {
	type S struct {
		Name string
	}
	input := map[any]any{
		"Name": "bob",
		1:      "one",
		true:   "bool",
	}
	var md Metadata
	var out S
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	m, ok := md.UnusedInput.(map[any]any)
	if !ok {
		t.Fatalf("want map[any]any, got %T", md.UnusedInput)
	}
	if m[1] != "one" || m[true] != "bool" {
		t.Fatalf("got %#v", m)
	}
	if _, ok := m["Name"]; ok {
		t.Fatal("consumed key present")
	}
}

func TestMetadataUnusedInputArrayHierarchy(t *testing.T) {
	type Item struct {
		A string
	}
	type S struct {
		Items []Item
	}
	input := map[string]any{
		"Items": []any{
			map[string]any{"A": "1", "x": "bad"},
			map[string]any{"A": "2", "y": 2},
			map[string]any{"A": "3"},
		},
	}
	var md Metadata
	var out S
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	got := md.UnusedInput.(map[string]any)["Items"].([]any)
	if len(got) != 3 {
		t.Fatalf("len %d", len(got))
	}
	if !reflect.DeepEqual(got[0], map[string]any{"x": "bad"}) {
		t.Fatalf("idx0 %#v", got[0])
	}
	if !reflect.DeepEqual(got[1], map[string]any{"y": 2}) {
		t.Fatalf("idx1 %#v", got[1])
	}
	if got[2] != nil {
		t.Fatalf("idx2 must be nil, got %#v", got[2])
	}
}

func TestMetadataUnusedInputArrayPartialErrors(t *testing.T) {
	type Item struct {
		A int
	}
	type S struct {
		Items []Item
	}
	input := map[string]any{
		"Items": []any{
			map[string]any{"A": 1, "x": "u1"},
			"not-a-map",
			map[string]any{"A": 3, "z": "u3"},
		},
	}
	var md Metadata
	var out S
	err := DecodeMetadata(input, &out, &md)
	if err == nil {
		t.Fatal("expected error")
	}
	got := md.UnusedInput.(map[string]any)["Items"].([]any)
	if len(got) != 3 {
		t.Fatalf("len %d", len(got))
	}
	if !reflect.DeepEqual(got[0], map[string]any{"x": "u1"}) {
		t.Fatalf("idx0 %#v", got[0])
	}
	if got[1] != nil {
		t.Fatalf("idx1 %#v", got[1])
	}
	if !reflect.DeepEqual(got[2], map[string]any{"z": "u3"}) {
		t.Fatalf("idx2 %#v", got[2])
	}
}

func TestMetadataUnusedInputOwnership(t *testing.T) {
	type S struct{ Name string }
	input := map[string]any{
		"Name":   "bob",
		"nested": map[string]any{"x": 1},
		"slice":  []any{map[string]any{"y": 2}},
	}
	var md Metadata
	var out S
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	md.UnusedInput.(map[string]any)["nested"].(map[string]any)["x"] = 999
	md.UnusedInput.(map[string]any)["slice"].([]any)[0] = "mutated"
	if input["nested"].(map[string]any)["x"] != 1 {
		t.Fatal("original input mutated")
	}
	if input["slice"].([]any)[0].(map[string]any)["y"] != 2 {
		t.Fatal("original slice input mutated")
	}
}

func TestMetadataUnusedInputAllConsumed(t *testing.T) {
	type S struct{ Name string }
	var md Metadata
	var out S
	if err := DecodeMetadata(map[string]any{"Name": "bob"}, &out, &md); err != nil {
		t.Fatal(err)
	}
	if md.UnusedInput != nil {
		t.Fatalf("expected nil, got %#v", md.UnusedInput)
	}
}

func TestMetadataUnusedInputTargetMap(t *testing.T) {
	type S struct {
		Known string
		M     map[string]map[string]string
	}
	input := map[string]any{
		"Known": "k",
		"M": map[string]any{
			"k1": map[string]any{"a": "1", "b": "2"},
			"k2": map[string]any{"c": "3"},
		},
	}
	var md Metadata
	var out S
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	if md.UnusedInput != nil {
		t.Fatalf("maps consume everything: %#v", md.UnusedInput)
	}
	if out.M["k1"]["b"] != "2" {
		t.Fatalf("%#v", out.M)
	}
}

func TestMetadataUnusedInputPointerNested(t *testing.T) {
	type Inner struct {
		A int
	}
	type S struct {
		I *Inner
	}
	input := map[string]any{
		"I": map[string]any{"A": 1, "extra": "e"},
	}
	var md Metadata
	var out S
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"I": map[string]any{"extra": "e"}}
	if !reflect.DeepEqual(md.UnusedInput, want) {
		t.Fatalf("got %#v want %#v", md.UnusedInput, want)
	}
}

func TestMetadataUnusedInputStructInput(t *testing.T) {
	type In struct {
		Name string
		Foo  int
	}
	type Out struct {
		Name string
	}
	input := In{Name: "bob", Foo: 7}
	var md Metadata
	var out Out
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"Foo": 7}
	if !reflect.DeepEqual(md.UnusedInput, want) {
		t.Fatalf("got %#v want %#v", md.UnusedInput, want)
	}
}

func TestMetadataUnusedInputDecoderReuse(t *testing.T) {
	type S struct{ Name string }

	config := &DecoderConfig{Result: &S{}, Metadata: &Metadata{}}
	decoder, err := NewDecoder(config)
	if err != nil {
		t.Fatal(err)
	}

	if err := decoder.Decode(map[string]any{"Name": "a", "x": 1}); err != nil {
		t.Fatal(err)
	}
	if config.Metadata.UnusedInput == nil {
		t.Fatal("first decode should track unused")
	}

	if err := decoder.Decode(map[string]any{"Name": "b"}); err != nil {
		t.Fatal(err)
	}
	if config.Metadata.UnusedInput != nil {
		t.Fatalf("second decode should reset tree, got %#v", config.Metadata.UnusedInput)
	}
}

func TestMetadataUnusedInputRemainInsideSlice(t *testing.T) {
	type Item struct {
		A     int
		Extra map[string]any `mapstructure:",remain"`
	}
	type S struct {
		Items []Item
	}
	input := map[string]any{
		"Items": []any{
			map[string]any{"A": 1, "q": "u"},
			map[string]any{"A": 2},
		},
	}
	var md Metadata
	var out S
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	if md.UnusedInput != nil {
		t.Fatalf("remain inside slices must consume input: %#v", md.UnusedInput)
	}
	if out.Items[0].Extra["q"] != "u" {
		t.Fatalf("%#v", out.Items[0].Extra)
	}
}

func TestMetadataUnusedInputFixedArray(t *testing.T) {
	type Item struct {
		A int
	}
	type S struct {
		Items [2]Item
	}
	input := map[string]any{
		"Items": []any{
			map[string]any{"A": 1, "x": "u1"},
			map[string]any{"A": 2},
		},
	}
	var md Metadata
	var out S
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	got := md.UnusedInput.(map[string]any)["Items"].([]any)
	if !reflect.DeepEqual(got, []any{map[string]any{"x": "u1"}, nil}) {
		t.Fatalf("got %#v", got)
	}
}

func TestMetadataUnusedInputRemainNonStringKeys(t *testing.T) {
	type S struct {
		Name  string
		Extra map[any]any `mapstructure:",remain"`
	}
	input := map[any]any{
		"Name": "bob",
		7:      "seven",
		"ext":  1,
	}
	var md Metadata
	var out S
	if err := DecodeMetadata(input, &out, &md); err != nil {
		t.Fatal(err)
	}
	if md.UnusedInput != nil {
		t.Fatalf("remain should consume non-string keys too: %#v", md.UnusedInput)
	}
	if out.Extra[7] != "seven" || out.Extra["ext"] != 1 {
		t.Fatalf("remain map: %#v", out.Extra)
	}
}
