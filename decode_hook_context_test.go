package mapstructure

import (
	"reflect"
	"strings"
	"testing"
)

type ctxRecord struct {
	ctx  DecodeHookContext
	from any
}

func recordContexts(t *testing.T, records *[]ctxRecord) DecodeHookFunc {
	t.Helper()
	return DecodeHookFuncContext(func(ctx DecodeHookContext, from reflect.Value, to reflect.Value) (any, error) {
		*records = append(*records, ctxRecord{ctx: ctx, from: safeInterface(from)})
		return safeInterface(from), nil
	})
}

func findRecord(t *testing.T, records []ctxRecord, from any) ctxRecord {
	t.Helper()
	for _, r := range records {
		if reflect.DeepEqual(r.from, from) {
			return r
		}
	}
	t.Fatalf("no hook call recorded for input %#v", from)
	return ctxRecord{}
}

func TestDecodeHookFuncContext_paths(t *testing.T) {
	type Credentials struct {
		Password string `mapstructure:"password"`
	}
	type Config struct {
		Credentials Credentials `mapstructure:"credentials"`
		Names       []string    `mapstructure:"names"`
	}

	input := map[string]any{
		"credentials": map[string]any{"password": "secret"},
		"names":       []any{"alice", "bob"},
	}

	var records []ctxRecord
	var result Config
	decoder, err := NewDecoder(&DecoderConfig{
		DecodeHook: recordContexts(t, &records),
		Result:     &result,
	})
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	if err := decoder.Decode(input); err != nil {
		t.Fatalf("bad: %s", err)
	}

	pw := findRecord(t, records, "secret")
	wantIn := Path{MapKeySegment("credentials"), MapKeySegment("password")}
	wantOut := Path{FieldSegment("credentials"), FieldSegment("password")}
	if !reflect.DeepEqual(pw.ctx.InputPath, wantIn) {
		t.Errorf("bad input path: %#v", pw.ctx.InputPath)
	}
	if !reflect.DeepEqual(pw.ctx.OutputPath, wantOut) {
		t.Errorf("bad output path: %#v", pw.ctx.OutputPath)
	}
	if pw.ctx.MapKey {
		t.Errorf("password value must not be flagged as map key")
	}

	name := findRecord(t, records, "bob")
	wantIn = Path{MapKeySegment("names"), IndexSegment(1)}
	wantOut = Path{FieldSegment("names"), IndexSegment(1)}
	if !reflect.DeepEqual(name.ctx.InputPath, wantIn) {
		t.Errorf("bad input path: %#v", name.ctx.InputPath)
	}
	if !reflect.DeepEqual(name.ctx.OutputPath, wantOut) {
		t.Errorf("bad output path: %#v", name.ctx.OutputPath)
	}
}

func TestDecodeHookFuncContext_mapKeyFlag(t *testing.T) {
	var records []ctxRecord
	result := map[string]string{}
	decoder, err := NewDecoder(&DecoderConfig{
		DecodeHook: recordContexts(t, &records),
		Result:     &result,
	})
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	if err := decoder.Decode(map[string]any{"key": "value"}); err != nil {
		t.Fatalf("bad: %s", err)
	}

	key := findRecord(t, records, "key")
	if !key.ctx.MapKey {
		t.Errorf("map key conversion not flagged as such")
	}
	want := Path{MapKeySegment("key")}
	if !reflect.DeepEqual(key.ctx.InputPath, want) {
		t.Errorf("bad key input path: %#v", key.ctx.InputPath)
	}

	val := findRecord(t, records, "value")
	if val.ctx.MapKey {
		t.Errorf("map value conversion flagged as map key")
	}
	if !reflect.DeepEqual(val.ctx.InputPath, want) {
		t.Errorf("bad value input path: %#v", val.ctx.InputPath)
	}
}

func TestDecodeHookFuncContext_arbitraryKeyStrings(t *testing.T) {
	// Keys containing dots or brackets must stay a single structured
	// segment instead of being split or joined by string formatting.
	var records []ctxRecord
	result := map[string]string{}
	decoder, err := NewDecoder(&DecoderConfig{
		DecodeHook: recordContexts(t, &records),
		Result:     &result,
	})
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	if err := decoder.Decode(map[string]any{"a.b[0]": "v"}); err != nil {
		t.Fatalf("bad: %s", err)
	}

	rec := findRecord(t, records, "v")
	want := Path{MapKeySegment("a.b[0]")}
	if !reflect.DeepEqual(rec.ctx.InputPath, want) {
		t.Errorf("bad input path: %#v", rec.ctx.InputPath)
	}
}

func TestDecodeHookFuncContext_scopedTransformation(t *testing.T) {
	// The motivating use case: a transformation that only applies below
	// the "credentials" field.
	type Config struct {
		Credentials map[string]string `mapstructure:"credentials"`
		Public      map[string]string `mapstructure:"public"`
	}

	hook := DecodeHookFuncContext(func(ctx DecodeHookContext, from reflect.Value, to reflect.Value) (any, error) {
		data := safeInterface(from)
		str, ok := data.(string)
		if !ok || ctx.MapKey || len(ctx.OutputPath) == 0 {
			return data, nil
		}
		if ctx.OutputPath[0] != FieldSegment("credentials") {
			return data, nil
		}
		return "decrypted:" + str, nil
	})

	var result Config
	decoder, err := NewDecoder(&DecoderConfig{
		DecodeHook: hook,
		Result:     &result,
	})
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	err = decoder.Decode(map[string]any{
		"credentials": map[string]any{"password": "secret"},
		"public":      map[string]any{"password": "secret"},
	})
	if err != nil {
		t.Fatalf("bad: %s", err)
	}

	if result.Credentials["password"] != "decrypted:secret" {
		t.Errorf("credentials not transformed: %#v", result.Credentials)
	}
	if result.Public["password"] != "secret" {
		t.Errorf("public must not be transformed: %#v", result.Public)
	}
}

func TestDecodeHookFuncContext_composedWithOldForms(t *testing.T) {
	var order []string

	ctxHook := DecodeHookFuncContext(func(ctx DecodeHookContext, from reflect.Value, to reflect.Value) (any, error) {
		if str, ok := safeInterface(from).(string); ok {
			order = append(order, "ctx:"+ctx.OutputPath.String())
			return str + "-ctx", nil
		}
		return safeInterface(from), nil
	})
	oldHook := func(f reflect.Kind, t reflect.Kind, data any) (any, error) {
		if str, ok := data.(string); ok {
			order = append(order, "old")
			return str + "-old", nil
		}
		return data, nil
	}

	var result struct {
		Name string `mapstructure:"name"`
	}
	decoder, err := NewDecoder(&DecoderConfig{
		DecodeHook: ComposeDecodeHookFunc(ctxHook, oldHook),
		Result:     &result,
	})
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	if err := decoder.Decode(map[string]any{"name": "x"}); err != nil {
		t.Fatalf("bad: %s", err)
	}

	if result.Name != "x-ctx-old" {
		t.Errorf("bad composition result: %#v", result.Name)
	}
	// The context hook must see the field path even when composed.
	found := false
	for _, entry := range order {
		if entry == "ctx:name" {
			found = true
		}
	}
	if !found {
		t.Errorf("context not propagated through composition: %#v", order)
	}
}

type orderTrackingString struct {
	calls *[]string
	value string
}

func (s *orderTrackingString) UnmarshalMapstructure(input any) error {
	*s.calls = append(*s.calls, "unmarshal:"+input.(string))
	s.value = input.(string)
	return nil
}

func TestDecodeHookFuncContext_hookRunsBeforeUnmarshaler(t *testing.T) {
	var calls []string

	hook := DecodeHookFuncContext(func(ctx DecodeHookContext, from reflect.Value, to reflect.Value) (any, error) {
		if str, ok := safeInterface(from).(string); ok {
			calls = append(calls, "hook:"+str)
			return str + "-hooked", nil
		}
		return safeInterface(from), nil
	})

	var result struct {
		V orderTrackingString `mapstructure:"v"`
	}
	result.V.calls = &calls

	decoder, err := NewDecoder(&DecoderConfig{
		DecodeHook: hook,
		Result:     &result,
	})
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	if err := decoder.Decode(map[string]any{"v": "x"}); err != nil {
		t.Fatalf("bad: %s", err)
	}

	want := []string{"hook:x", "unmarshal:x-hooked"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("bad call order: %#v", calls)
	}
	if result.V.value != "x-hooked" {
		t.Errorf("unmarshaler did not receive hooked value: %#v", result.V.value)
	}
}

func TestDecodeHookFuncContext_nestedDecodeIsolation(t *testing.T) {
	type Inner struct {
		Value string `mapstructure:"value"`
	}
	type Outer struct {
		Name string `mapstructure:"name"`
	}

	var innerPaths []Path
	innerHook := DecodeHookFuncContext(func(ctx DecodeHookContext, from reflect.Value, to reflect.Value) (any, error) {
		innerPaths = append(innerPaths, ctx.OutputPath)
		return safeInterface(from), nil
	})

	var outerPaths []Path
	outerHook := DecodeHookFuncContext(func(ctx DecodeHookContext, from reflect.Value, to reflect.Value) (any, error) {
		outerPaths = append(outerPaths, ctx.OutputPath)
		if len(ctx.OutputPath) == 0 {
			// Trigger a nested decode from within the hook.
			var inner Inner
			decoder, err := NewDecoder(&DecoderConfig{
				DecodeHook: innerHook,
				Result:     &inner,
			})
			if err != nil {
				return nil, err
			}
			if err := decoder.Decode(map[string]any{"value": "inner"}); err != nil {
				return nil, err
			}
		}
		return safeInterface(from), nil
	})

	var outer Outer
	decoder, err := NewDecoder(&DecoderConfig{
		DecodeHook: outerHook,
		Result:     &outer,
	})
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	if err := decoder.Decode(map[string]any{"name": "outer"}); err != nil {
		t.Fatalf("bad: %s", err)
	}

	// The nested decode starts with a fresh, root-level context.
	for _, p := range innerPaths {
		if len(p) > 1 || (len(p) == 1 && p[0] != FieldSegment("value")) {
			t.Errorf("inner decode observed outer path: %#v", innerPaths)
		}
	}

	// The outer decode continues with its own paths unaffected.
	wantOuter := []Path{nil, {FieldSegment("name")}}
	if !reflect.DeepEqual(outerPaths, wantOuter) {
		t.Errorf("outer paths corrupted by nested decode: %#v", outerPaths)
	}
}

func TestDecodeHookFuncContext_retainedPathsAreSnapshots(t *testing.T) {
	var saved []Path
	hook := DecodeHookFuncContext(func(ctx DecodeHookContext, from reflect.Value, to reflect.Value) (any, error) {
		if _, ok := safeInterface(from).(string); ok {
			saved = append(saved, ctx.InputPath)
		}
		return safeInterface(from), nil
	})

	var result []string
	decoder, err := NewDecoder(&DecoderConfig{
		DecodeHook: hook,
		Result:     &result,
	})
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	if err := decoder.Decode([]any{"a", "b", "c"}); err != nil {
		t.Fatalf("bad: %s", err)
	}

	// Paths retained by the hook must not grow or change as decoding
	// continues past their position.
	want := []Path{{IndexSegment(0)}, {IndexSegment(1)}, {IndexSegment(2)}}
	if !reflect.DeepEqual(saved, want) {
		t.Errorf("retained paths were rewritten: %#v", saved)
	}
}

func TestDecodeHookExecContext(t *testing.T) {
	hook := DecodeHookFuncContext(func(ctx DecodeHookContext, from reflect.Value, to reflect.Value) (any, error) {
		if ctx.OutputPath.String() != "creds" || !ctx.MapKey {
			t.Errorf("unexpected context: %#v", ctx)
		}
		return safeInterface(from), nil
	})

	ctx := DecodeHookContext{
		OutputPath: Path{FieldSegment("creds")},
		MapKey:     true,
	}
	if _, err := DecodeHookExecContext(hook, ctx, reflect.ValueOf("x"), reflect.ValueOf("")); err != nil {
		t.Fatalf("bad: %s", err)
	}

	// DecodeHookExec must still accept context hooks, with a zero context.
	called := false
	zeroHook := DecodeHookFuncContext(func(ctx DecodeHookContext, from reflect.Value, to reflect.Value) (any, error) {
		called = true
		if ctx.InputPath != nil || ctx.OutputPath != nil || ctx.MapKey {
			t.Errorf("expected zero context, got %#v", ctx)
		}
		return safeInterface(from), nil
	})
	if _, err := DecodeHookExec(zeroHook, reflect.ValueOf("x"), reflect.ValueOf("")); err != nil {
		t.Fatalf("bad: %s", err)
	}
	if !called {
		t.Fatal("context hook not invoked by DecodeHookExec")
	}
}

func TestPathString(t *testing.T) {
	p := Path{
		FieldSegment("credentials"),
		MapKeySegment("a.b"),
		IndexSegment(2),
	}
	got := p.String()
	if !strings.Contains(got, "credentials") || !strings.Contains(got, `"a.b"`) || !strings.Contains(got, "[2]") {
		t.Errorf("bad path string: %q", got)
	}

	if Path(nil).String() != "" {
		t.Errorf("root path should render empty")
	}
}
