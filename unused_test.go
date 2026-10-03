package mapstructure

import (
	"reflect"
	"testing"
)

func TestMetadata_UnusedValues_Nested(t *testing.T) {
	input := map[string]any{
		"name": "bob",
		"sub": map[string]any{
			"known": 1,
			"extra": "x",
			"deep": map[string]any{
				"used":   true,
				"unused": "y",
			},
		},
		"top": "level",
	}

	var result struct {
		Name string
		Sub  struct {
			Known int
			Deep  struct {
				Used bool
			}
		}
	}

	var md Metadata
	if err := DecodeMetadata(input, &result, &md); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := map[string]any{
		"sub": map[string]any{
			"extra": "x",
			"deep":  map[string]any{"unused": "y"},
		},
		"top": "level",
	}

	if !reflect.DeepEqual(md.UnusedValues, expected) {
		t.Fatalf("expected %#v, got %#v", expected, md.UnusedValues)
	}
}

func TestMetadata_UnusedValues_RemainConsumed(t *testing.T) {
	input := map[string]any{
		"name":  "bob",
		"extra": "x",
	}

	var result struct {
		Name  string
		Other map[string]any `mapstructure:",remain"`
	}

	var md Metadata
	if err := DecodeMetadata(input, &result, &md); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if result.Other["extra"] != "x" {
		t.Fatalf("expected remain field to capture extra, got %#v", result.Other)
	}

	if len(md.UnusedValues) != 0 {
		t.Fatalf("expected no unused values, got %#v", md.UnusedValues)
	}
}

func TestMetadata_UnusedValues_DottedKeys(t *testing.T) {
	input := map[string]any{
		"a.b": 1,
		"a": map[string]any{
			"b": 2,
		},
	}

	var result struct {
		A struct{}
	}

	var md Metadata
	if err := DecodeMetadata(input, &result, &md); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := map[string]any{
		"a.b": 1,
		"a":   map[string]any{"b": 2},
	}

	if !reflect.DeepEqual(md.UnusedValues, expected) {
		t.Fatalf("expected %#v, got %#v", expected, md.UnusedValues)
	}
}

func TestMetadata_UnusedValues_NonStringKeys(t *testing.T) {
	input := map[string]any{
		"sub": map[any]any{
			"known": 1,
			2:       "two",
			"2":     "string-two",
		},
	}

	var result struct {
		Sub struct {
			Known int
		}
	}

	var md Metadata
	if err := DecodeMetadata(input, &result, &md); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := map[string]any{
		"sub": map[any]any{
			2:   "two",
			"2": "string-two",
		},
	}

	if !reflect.DeepEqual(md.UnusedValues, expected) {
		t.Fatalf("expected %#v, got %#v", expected, md.UnusedValues)
	}
}

func TestMetadata_UnusedValues_SlicePartialErrors(t *testing.T) {
	input := map[string]any{
		"list": []any{
			map[string]any{"a": "bad", "x": 1},
			map[string]any{"a": 2, "y": 2},
		},
	}

	var result struct {
		List []struct {
			A int
		}
	}

	var md Metadata
	err := DecodeMetadata(input, &result, &md)
	if err == nil {
		t.Fatal("expected an error for the unconvertible element")
	}

	expected := map[string]any{
		"list": []any{
			map[string]any{"a": "bad", "x": 1},
			map[string]any{"y": 2},
		},
	}

	if !reflect.DeepEqual(md.UnusedValues, expected) {
		t.Fatalf("expected %#v, got %#v", expected, md.UnusedValues)
	}
}

func TestMetadata_UnusedValues_MapKeyErrors(t *testing.T) {
	input := map[string]any{
		"sub": map[int]any{
			1: "one",
			2: "two",
		},
	}

	var result struct {
		Sub map[string]int
	}

	var md Metadata
	err := DecodeMetadata(input, &result, &md)
	if err == nil {
		t.Fatal("expected an error for the unconvertible keys")
	}

	sub, ok := md.UnusedValues["sub"].(map[any]any)
	if !ok {
		t.Fatalf("expected map[any]any node, got %#v", md.UnusedValues["sub"])
	}

	expected := map[any]any{1: "one", 2: "two"}
	if !reflect.DeepEqual(sub, expected) {
		t.Fatalf("expected %#v, got %#v", expected, sub)
	}
}

func TestMetadata_UnusedValues_DeepCopy(t *testing.T) {
	nested := map[string]any{"unused": "y"}
	input := map[string]any{
		"sub": nested,
	}

	var result struct{}

	var md Metadata
	if err := DecodeMetadata(input, &result, &md); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	// Mutating the tree must not affect the original input.
	md.UnusedValues["sub"].(map[string]any)["unused"] = "changed"
	if nested["unused"] != "y" {
		t.Fatalf("mutating UnusedValues changed the input: %#v", nested)
	}

	// Mutating the input must not affect the tree.
	nested["unused"] = "mutated"
	if md.UnusedValues["sub"].(map[string]any)["unused"] != "changed" {
		t.Fatalf("mutating the input changed UnusedValues: %#v", md.UnusedValues)
	}
}

func TestMetadata_UnusedValues_Empty(t *testing.T) {
	input := map[string]any{"name": "bob"}

	var result struct {
		Name string
	}

	var md Metadata
	if err := DecodeMetadata(input, &result, &md); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if md.UnusedValues == nil {
		t.Fatal("expected UnusedValues to be initialized")
	}

	if len(md.UnusedValues) != 0 {
		t.Fatalf("expected no unused values, got %#v", md.UnusedValues)
	}
}

func TestMetadata_UnusedValues_MapOfStruct(t *testing.T) {
	input := map[string]any{
		"items": map[string]any{
			"first":  map[string]any{"a": 1, "x": "ex"},
			"second": map[string]any{"a": 2},
		},
	}

	var result struct {
		Items map[string]struct {
			A int
		}
	}

	var md Metadata
	if err := DecodeMetadata(input, &result, &md); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := map[string]any{
		"items": map[string]any{
			"first": map[string]any{"x": "ex"},
		},
	}

	if !reflect.DeepEqual(md.UnusedValues, expected) {
		t.Fatalf("expected %#v, got %#v", expected, md.UnusedValues)
	}
}

func TestMetadata_UnusedValues_Squash(t *testing.T) {
	type Inner struct {
		A int
	}
	type Outer struct {
		Inner `mapstructure:",squash"`
		B     int
	}

	input := map[string]any{"a": 1, "b": 2, "c": 3}

	var result Outer

	var md Metadata
	if err := DecodeMetadata(input, &result, &md); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := map[string]any{"c": 3}
	if !reflect.DeepEqual(md.UnusedValues, expected) {
		t.Fatalf("expected %#v, got %#v", expected, md.UnusedValues)
	}
}

func TestMetadata_UnusedValues_WeakAndPointers(t *testing.T) {
	input := map[string]any{
		"sub": map[string]any{"n": "5", "extra": "e"},
	}

	var result struct {
		Sub *struct {
			N int
		}
	}

	var md Metadata
	config := &DecoderConfig{
		Metadata:         &md,
		Result:           &result,
		WeaklyTypedInput: true,
	}

	decoder, err := NewDecoder(config)
	if err != nil {
		t.Fatal(err)
	}

	if err := decoder.Decode(input); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := map[string]any{
		"sub": map[string]any{"extra": "e"},
	}
	if !reflect.DeepEqual(md.UnusedValues, expected) {
		t.Fatalf("expected %#v, got %#v", expected, md.UnusedValues)
	}
}

func TestMetadata_UnusedValues_NonStringRootKeys(t *testing.T) {
	input := map[any]any{"name": "bob", 1: "one"}

	var result struct {
		Name string
	}

	var md Metadata
	if err := DecodeMetadata(input, &result, &md); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	// Non-string keys cannot be represented at the root of the tree and
	// fall back to their stringified form.
	expected := map[string]any{"1": "one"}
	if !reflect.DeepEqual(md.UnusedValues, expected) {
		t.Fatalf("expected %#v, got %#v", expected, md.UnusedValues)
	}
}
