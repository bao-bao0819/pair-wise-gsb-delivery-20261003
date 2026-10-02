package mapstructure

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// hookCall records a single invocation of a context-aware hook.
type hookCall struct {
	inputPath        Path
	targetPath       Path
	convertingMapKey bool
	from             reflect.Type
	to               reflect.Type
	data             any
}

func contextRecordingHook(calls *[]hookCall) DecodeHookFuncTypeWithContext {
	return func(
		context DecodeHookContext,
		from reflect.Type,
		to reflect.Type,
		data any,
	) (any, error) {
		// Record copies to make sure the decoder hands over owned slices.
		*calls = append(*calls, hookCall{
			inputPath:        append(Path(nil), context.InputPath...),
			targetPath:       append(Path(nil), context.TargetPath...),
			convertingMapKey: context.ConvertingMapKey,
			from:             from,
			to:               to,
			data:             data,
		})
		return data, nil
	}
}

func findHookCall(t *testing.T, calls []hookCall, targetPath Path, mapKey bool) hookCall {
	t.Helper()
	for _, call := range calls {
		if call.convertingMapKey != mapKey {
			continue
		}
		if pathsEqual(call.targetPath, targetPath) {
			return call
		}
	}
	t.Fatalf("hook call with target path %q (mapKey=%v) not found in:\n%s",
		targetPath, mapKey, formatHookCalls(calls))
	return hookCall{}
}

func pathsEqual(a, b Path) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind {
			return false
		}
		switch a[i].Kind {
		case PathSegmentField:
			if a[i].Field != b[i].Field {
				return false
			}
		case PathSegmentMapKey:
			if a[i].MapKey != b[i].MapKey {
				return false
			}
		case PathSegmentIndex:
			if a[i].Index != b[i].Index {
				return false
			}
		}
	}
	return true
}

func formatHookCalls(calls []hookCall) string {
	var builder strings.Builder
	for _, call := range calls {
		fmt.Fprintf(&builder, "  input=%s target=%s mapKey=%v from=%v to=%v\n",
			call.inputPath, call.targetPath, call.convertingMapKey, call.from, call.to)
	}
	return builder.String()
}

func TestDecodeHookWithContext_StructuredPaths(t *testing.T) {
	t.Parallel()

	type Credentials struct {
		Token string
	}
	type Address struct {
		Street string
	}
	type Person struct {
		Name        string
		Credentials Credentials
		Addresses   []Address
		Labels      map[string]string
	}

	input := map[string]any{
		"name": "alice",
		"credentials": map[string]any{
			"token": "s3cr3t",
		},
		"addresses": []any{
			map[string]any{"street": "maple"},
		},
		"labels": map[string]any{
			"a.b": "vip",
		},
	}

	var calls []hookCall
	config := &DecoderConfig{
		Result:     &Person{},
		DecodeHook: contextRecordingHook(&calls),
	}
	decoder, err := NewDecoder(config)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := decoder.Decode(input); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	tokenCall := findHookCall(t, calls, Path{
		FieldPathSegment("Credentials"),
		FieldPathSegment("Token"),
	}, false)
	if got := tokenCall.inputPath.String(); got != `["credentials"]["token"]` {
		t.Errorf("unexpected input path: %s", got)
	}
	if tokenCall.from.Kind() != reflect.String || tokenCall.to.Kind() != reflect.String {
		t.Errorf("unexpected types: %v -> %v", tokenCall.from, tokenCall.to)
	}

	streetCall := findHookCall(t, calls, Path{
		FieldPathSegment("Addresses"),
		IndexPathSegment(0),
		FieldPathSegment("Street"),
	}, false)
	if got := streetCall.inputPath.String(); got != `["addresses"][0]["street"]` {
		t.Errorf("unexpected input path: %s", got)
	}

	labelCall := findHookCall(t, calls, Path{
		FieldPathSegment("Labels"),
		MapKeyPathSegment("a.b"),
	}, false)
	if got := labelCall.inputPath.String(); got != `["labels"]["a.b"]` {
		t.Errorf("unexpected input path: %s", got)
	}
}

func TestDecodeHookWithContext_MapKeyConversion(t *testing.T) {
	t.Parallel()

	var calls []hookCall
	config := &DecoderConfig{
		// Source keys are strings, target keys are ints: the decoder
		// converts keys through the hook.
		Result:           &map[int]string{},
		DecodeHook:       contextRecordingHook(&calls),
		WeaklyTypedInput: true,
	}
	decoder, err := NewDecoder(config)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := decoder.Decode(map[string]any{"7": "seven"}); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	keyCall := findHookCall(t, calls, Path{MapKeyPathSegment("7")}, true)
	if keyCall.from.Kind() != reflect.String || keyCall.to.Kind() != reflect.Int {
		t.Errorf("unexpected key conversion types: %v -> %v", keyCall.from, keyCall.to)
	}
	if got := keyCall.inputPath.String(); got != `["7"]` {
		t.Errorf("unexpected key input path: %s", got)
	}

	valueCall := findHookCall(t, calls, Path{MapKeyPathSegment(7)}, false)
	if valueCall.data != "seven" {
		t.Errorf("unexpected value: %#v", valueCall.data)
	}
}

func TestDecodeHookWithContext_OnlyCredentials(t *testing.T) {
	t.Parallel()

	type Credentials struct {
		Token string
	}
	type Config struct {
		Name        string
		Credentials Credentials
	}

	// The hook uppercases strings only below the Credentials field.
	hook := func(context DecodeHookContext, from, to reflect.Type, data any) (any, error) {
		if from.Kind() != reflect.String || to.Kind() != reflect.String {
			return data, nil
		}
		if len(context.TargetPath) >= 1 &&
			context.TargetPath[0].Kind == PathSegmentField &&
			context.TargetPath[0].Field == "Credentials" {
			return strings.ToUpper(data.(string)), nil
		}
		return data, nil
	}

	var result Config
	decoder, err := NewDecoder(&DecoderConfig{
		Result:     &result,
		DecodeHook: DecodeHookFuncTypeWithContext(hook),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	err = decoder.Decode(map[string]any{
		"name": "alice",
		"credentials": map[string]any{
			"token": "s3cr3t",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if result.Name != "alice" {
		t.Errorf("expected Name unchanged, got %q", result.Name)
	}
	if result.Credentials.Token != "S3CR3T" {
		t.Errorf("expected Token uppercased, got %q", result.Credentials.Token)
	}
}

func TestDecodeHookWithContext_PathIsolation(t *testing.T) {
	t.Parallel()

	type Inner struct {
		V string
	}

	var outerCalls []hookCall
	innerHook := func(context DecodeHookContext, from, to reflect.Type, data any) (any, error) {
		if from.Kind() == reflect.String && to.Kind() == reflect.String {
			// The path seen inside a fresh decode must always start empty.
			if len(context.InputPath) != 0 || len(context.TargetPath) != 0 {
				return nil, fmt.Errorf("nested decode leaked path: input=%s target=%s",
					context.InputPath, context.TargetPath)
			}
		}
		return data, nil
	}

	innerDecoder, err := NewDecoder(&DecoderConfig{
		Result:     &Inner{},
		DecodeHook: DecodeHookFuncTypeWithContext(innerHook),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	type Outer struct {
		A Inner
		B Inner
	}

	outerHook := func(context DecodeHookContext, from, to reflect.Type, data any) (any, error) {
		outerCalls = append(outerCalls, hookCall{
			inputPath:  append(Path(nil), context.InputPath...),
			targetPath: append(Path(nil), context.TargetPath...),
			from:       from,
			to:         to,
			data:       data,
		})
		// Retain and mutate the handed-out path slice. The decoder copies
		// it, so this must never affect later invocations.
		if saved := context.TargetPath; len(saved) > 0 {
			for i := range saved {
				saved[i] = FieldPathSegment("HACKED")
			}
		}
		// Trigger an independent nested decode while descending.
		if len(context.TargetPath) == 1 && context.TargetPath[0].Field == "A" {
			var nested Inner
			if err := innerDecoder.Decode(map[string]any{"v": "nested"}); err != nil {
				return nil, err
			}
			_ = nested
		}
		return data, nil
	}

	var result Outer
	outerDecoder, err := NewDecoder(&DecoderConfig{
		Result:     &result,
		DecodeHook: DecodeHookFuncTypeWithContext(outerHook),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	err = outerDecoder.Decode(map[string]any{
		"a": map[string]any{"v": "one"},
		"b": map[string]any{"v": "two"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	// After the nested decode and user mutation, later outer calls must
	// still report correct paths.
	findHookCall(t, outerCalls, Path{
		FieldPathSegment("B"),
		FieldPathSegment("V"),
	}, false)
}

func TestDecodeHookWithContext_ReentrantSameDecoderIsolation(t *testing.T) {
	t.Parallel()

	type Inner struct {
		V string
	}
	type Outer struct {
		A Inner
		B Inner
	}

	var calls []hookCall

	var decoder *Decoder
	hook := func(context DecodeHookContext, from, to reflect.Type, data any) (any, error) {
		calls = append(calls, hookCall{
			inputPath:  append(Path(nil), context.InputPath...),
			targetPath: append(Path(nil), context.TargetPath...),
			from:       from,
			to:         to,
			data:       data,
		})
		// Re-enter the very same Decoder while deep in the path. The
		// reentrant decode uses the configured Result (Outer), so trigger it
		// once from the deepest point of A.
		if pathsEqual(context.TargetPath, Path{
			FieldPathSegment("A"),
			FieldPathSegment("V"),
		}) && data == "one" {
			if err := decoder.Decode(map[string]any{}); err != nil {
				return nil, err
			}
		}
		return data, nil
	}

	var result Outer
	decoder, err := NewDecoder(&DecoderConfig{
		Result:     &result,
		DecodeHook: DecodeHookFuncTypeWithContext(hook),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := decoder.Decode(map[string]any{
		"a": map[string]any{"v": "one"},
		"b": map[string]any{"v": "two"},
	}); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	// The nested decode must not leave an A prefix behind.
	findHookCall(t, calls, Path{
		FieldPathSegment("B"),
		FieldPathSegment("V"),
	}, false)
}

func TestDecodeHookWithContext_PointerAndRoot(t *testing.T) {
	t.Parallel()

	type Inner struct {
		N int
	}
	type Root struct {
		P *Inner
	}

	var calls []hookCall
	decoder, err := NewDecoder(&DecoderConfig{
		Result:     &Root{},
		DecodeHook: contextRecordingHook(&calls),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := decoder.Decode(map[string]any{"p": map[string]any{"n": 5}}); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	// Root call has empty paths.
	if len(calls) == 0 || len(calls[0].inputPath) != 0 || len(calls[0].targetPath) != 0 {
		t.Fatalf("root hook call must have empty paths:\n%s", formatHookCalls(calls))
	}

	// Pointers must not add an extra path segment.
	findHookCall(t, calls, Path{
		FieldPathSegment("P"),
		FieldPathSegment("N"),
	}, false)
}

func TestDecodeHookWithContext_PathStringForm(t *testing.T) {
	t.Parallel()

	path := Path{
		FieldPathSegment("Config"),
		MapKeyPathSegment("a.b"),
		IndexPathSegment(2),
		FieldPathSegment("Name"),
	}
	expected := `.Config["a.b"][2].Name`
	if got := path.String(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
	if Path(nil).String() != "" {
		t.Errorf("empty path must render as empty string")
	}

	segments := []PathSegment{
		FieldPathSegment("F"),
		MapKeyPathSegment("k"),
		IndexPathSegment(1),
	}
	kinds := []PathSegmentKind{PathSegmentField, PathSegmentMapKey, PathSegmentIndex}
	for i, segment := range segments {
		if segment.Kind != kinds[i] {
			t.Errorf("segment %d: expected kind %v, got %v", i, kinds[i], segment.Kind)
		}
	}
}

func TestDecodeHookWithContext_ComposePropagatesContext(t *testing.T) {
	t.Parallel()

	type Target struct {
		Field string
	}

	var seenByOld, seenByNew []Path

	// A classic context-free hook mixed with a context-aware one: both must
	// run, in composition order, and the new hook must see the decoder path.
	oldHook := func(from, to reflect.Type, data any) (any, error) {
		if s, ok := data.(string); ok {
			return s + ">old", nil
		}
		return data, nil
	}
	newHook := func(context DecodeHookContext, from, to reflect.Type, data any) (any, error) {
		if from.Kind() == reflect.String {
			seenByNew = append(seenByNew, append(Path(nil), context.TargetPath...))
			if s, ok := data.(string); ok {
				return s + ">new", nil
			}
		}
		return data, nil
	}
	observingHook := func(context DecodeHookContext, from, to reflect.Kind, data any) (any, error) {
		if from == reflect.String {
			seenByOld = append(seenByOld, append(Path(nil), context.TargetPath...))
		}
		return data, nil
	}

	hook := ComposeDecodeHookFunc(
		DecodeHookFuncType(oldHook),
		DecodeHookFuncTypeWithContext(newHook),
		DecodeHookFuncKindWithContext(observingHook),
	)

	var result Target
	decoder, err := NewDecoder(&DecoderConfig{
		Result:     &result,
		DecodeHook: hook,
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := decoder.Decode(map[string]any{"field": "x"}); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := Path{FieldPathSegment("Field")}
	if result.Field != "x>old>new" {
		t.Errorf("expected composition order old then new, got %q", result.Field)
	}
	if len(seenByNew) == 0 || !pathsEqual(seenByNew[0], expected) {
		t.Errorf("new hook did not see expected path: %v", seenByNew)
	}
	if len(seenByOld) == 0 || !pathsEqual(seenByOld[0], expected) {
		t.Errorf("kind-with-context hook did not see expected path: %v", seenByOld)
	}
}

func TestDecodeHookWithContext_OrComposePropagatesContext(t *testing.T) {
	t.Parallel()

	var seenPath Path
	failingHook := func(
		context DecodeHookContext, from, to reflect.Kind, data any,
	) (any, error) {
		return nil, errors.New("always fails")
	}
	succeedingHook := func(
		context DecodeHookContext, from, to reflect.Kind, data any,
	) (any, error) {
		seenPath = append(Path(nil), context.TargetPath...)
		return data, nil
	}

	var result struct{ Field string }
	decoder, err := NewDecoder(&DecoderConfig{
		Result: &result,
		DecodeHook: OrComposeDecodeHookFunc(
			DecodeHookFuncKindWithContext(failingHook),
			DecodeHookFuncKindWithContext(succeedingHook),
		),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := decoder.Decode(map[string]any{"field": "v"}); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !pathsEqual(seenPath, Path{FieldPathSegment("Field")}) {
		t.Errorf("unexpected path: %v", seenPath)
	}
}

type contextUnmarshaler struct {
	received string
}

func (u *contextUnmarshaler) UnmarshalMapstructure(data any) error {
	s, ok := data.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", data)
	}
	u.received = s
	return nil
}

func TestDecodeHookWithContext_HookRunsBeforeUnmarshaler(t *testing.T) {
	t.Parallel()

	var hookPath Path
	hook := func(context DecodeHookContext, from, to reflect.Type, data any) (any, error) {
		if from.Kind() == reflect.String {
			hookPath = append(Path(nil), context.TargetPath...)
			return "hooked:" + data.(string), nil
		}
		return data, nil
	}

	var result struct {
		U contextUnmarshaler
	}
	decoder, err := NewDecoder(&DecoderConfig{
		Result:     &result,
		DecodeHook: DecodeHookFuncTypeWithContext(hook),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := decoder.Decode(map[string]any{"u": "raw"}); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if result.U.received != "hooked:raw" {
		t.Errorf("Unmarshaler must receive hook-transformed input, got %q",
			result.U.received)
	}
	if !pathsEqual(hookPath, Path{FieldPathSegment("U")}) {
		t.Errorf("hook ran with unexpected path: %v", hookPath)
	}
}

func TestDecodeHookWithContext_ValueForm(t *testing.T) {
	t.Parallel()

	var calls []hookCall
	hook := func(
		context DecodeHookContext,
		from reflect.Value,
		to reflect.Value,
	) (any, error) {
		calls = append(calls, hookCall{
			inputPath:  append(Path(nil), context.InputPath...),
			targetPath: append(Path(nil), context.TargetPath...),
			from:       from.Type(),
			to:         to.Type(),
			data:       safeInterface(from),
		})
		return safeInterface(from), nil
	}

	var result struct{ N int }
	decoder, err := NewDecoder(&DecoderConfig{
		Result:     &result,
		DecodeHook: DecodeHookFuncValueWithContext(hook),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := decoder.Decode(map[string]any{"n": 3}); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if result.N != 3 {
		t.Errorf("expected 3, got %d", result.N)
	}
	findHookCall(t, calls, Path{FieldPathSegment("N")}, false)
}

func TestDecodeHookExecWithContext(t *testing.T) {
	t.Parallel()

	context := DecodeHookContext{
		InputPath:        Path{MapKeyPathSegment("k")},
		TargetPath:       Path{MapKeyPathSegment(42)},
		ConvertingMapKey: true,
	}

	var seen DecodeHookContext
	hook := DecodeHookFuncTypeWithContext(func(
		got DecodeHookContext, from, to reflect.Type, data any,
	) (any, error) {
		seen = got
		return data, nil
	})

	_, err := DecodeHookExecWithContext(
		context, hook, reflect.ValueOf("x"), reflect.ValueOf(42))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !seen.ConvertingMapKey ||
		!pathsEqual(seen.InputPath, context.InputPath) ||
		!pathsEqual(seen.TargetPath, context.TargetPath) {
		t.Errorf("context not propagated: %+v", seen)
	}

	// Context-free hooks keep working through the context-aware entry point.
	oldHook := DecodeHookFuncKind(func(from, to reflect.Kind, data any) (any, error) {
		if from == reflect.String && to == reflect.Int {
			return strconv.Atoi(data.(string))
		}
		return data, nil
	})
	out, err := DecodeHookExecWithContext(
		DecodeHookContext{}, oldHook,
		reflect.ValueOf("7"), reflect.ValueOf(0))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if out.(int) != 7 {
		t.Errorf("expected 7, got %#v", out)
	}

	// The legacy entry point keeps working with the new hook forms, with an
	// empty context.
	var empty bool
	kindHook := DecodeHookFuncKindWithContext(func(
		got DecodeHookContext, from, to reflect.Kind, data any,
	) (any, error) {
		empty = len(got.TargetPath) == 0 && !got.ConvertingMapKey
		return data, nil
	})
	if _, err := DecodeHookExec(
		kindHook, reflect.ValueOf(""), reflect.ValueOf("")); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !empty {
		t.Errorf("legacy entry point must provide an empty context")
	}
}

func TestDecodeHookWithContext_InvalidSignature(t *testing.T) {
	t.Parallel()

	var result struct{ Field string }
	decoder, err := NewDecoder(&DecoderConfig{
		Result:     &result,
		DecodeHook: func() {},
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	err = decoder.Decode(map[string]any{"field": "v"})
	if err == nil || !strings.Contains(err.Error(), "invalid decode hook signature") {
		t.Fatalf("expected invalid signature error, got %v", err)
	}
}
