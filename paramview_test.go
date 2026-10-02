package httphead

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParseViewOptionsKinds(t *testing.T) {
	opts, ok := ParseViewOptions([]byte(`a;bare;empty="";tok=1;q="x;y";b`), ParseConfig{})
	if !ok {
		t.Fatal("unexpected malformed input")
	}
	if len(opts) != 1 {
		t.Fatalf("unexpected options count: %d", len(opts))
	}
	p := opts[0].Params
	exp := []struct {
		name  string
		kind  ParamKind
		value string
		nilV  bool
	}{
		{"bare", ParamBare, "", true},
		{"empty", ParamEmpty, "", false},
		{"tok", ParamToken, "1", false},
		{"q", ParamToken, "x;y", false},
		{"b", ParamBare, "", true},
	}
	if p.Len() != len(exp) {
		t.Fatalf("unexpected params count: %d; want %d", p.Len(), len(exp))
	}
	all := p.All()
	for i, e := range exp {
		if string(all[i].Name) != e.name {
			t.Errorf("%d: name=%q want %q", i, all[i].Name, e.name)
		}
		if all[i].Kind != e.kind {
			t.Errorf("%d: kind=%s want %s", i, all[i].Kind, e.kind)
		}
		if string(all[i].Value) != e.value {
			t.Errorf("%d: value=%q want %q", i, all[i].Value, e.value)
		}
		if e.nilV && all[i].Value != nil {
			t.Errorf("%d: expected nil bare value", i)
		}
		if !e.nilV && all[i].Value == nil {
			t.Errorf("%d: expected non-nil value slice", i)
		}
	}
}

func TestParseViewOptionsOrder(t *testing.T) {
	opts, ok := ParseViewOptions([]byte(`a;x=1;x=2;x=3`), ParseConfig{})
	if !ok {
		t.Fatal("unexpected malformed input")
	}
	got := opts[0].Params.Values([]byte("x"), false)
	if len(got) != 3 {
		t.Fatalf("want 3 occurrences, got %d", len(got))
	}
	for i, want := range []string{"1", "2", "3"} {
		if string(got[i].Value) != want {
			t.Errorf("%d: %q want %q", i, got[i].Value, want)
		}
	}
}

func TestProjectDuplicates(t *testing.T) {
	opts, _ := ParseViewOptions([]byte(`a;X=1;y=2;X=3`), ParseConfig{})
	p := &opts[0].Params

	// Reject (default).
	if _, err := p.Project(ProjectOptions{}); err == nil {
		t.Error("expected reject error")
	} else {
		var dup *DuplicateParameterError
		if !errors.As(err, &dup) {
			t.Errorf("error is not *DuplicateParameterError: %v", err)
		}
		if !errors.Is(err, ErrDuplicateParameter) {
			t.Errorf("error does not wrap ErrDuplicateParameter: %v", err)
		}
		if string(dup.Name) != "X" {
			t.Errorf("duplicate name=%q want X", dup.Name)
		}
	}

	// First.
	first, err := p.Project(ProjectOptions{Duplicate: DuplicateFirst})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := first.Get([]byte("X")); !ok || string(v.Value) != "1" {
		t.Errorf("first X=%q ok=%v want 1", v.Value, ok)
	}
	if names := paramNames(first.All()); strings.Join(names, ",") != "X,y" {
		t.Errorf("first order=%v", names)
	}

	// Last.
	last, err := p.Project(ProjectOptions{Duplicate: DuplicateLast})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := last.Get([]byte("X")); !ok || string(v.Value) != "3" {
		t.Errorf("last X=%q ok=%v want 3", v.Value, ok)
	}
	if names := paramNames(last.All()); strings.Join(names, ",") != "X,y" {
		t.Errorf("last order=%v", names)
	}
}

func TestProjectFoldName(t *testing.T) {
	opts, _ := ParseViewOptions([]byte(`a;Foo=1`), ParseConfig{})
	p := &opts[0].Params

	if _, err := p.Project(ProjectOptions{FoldName: true}); err != nil {
		t.Fatalf("single param must not conflict: %v", err)
	}

	sp, _ := p.Project(ProjectOptions{FoldName: true})
	if _, ok := sp.Get([]byte("foo")); !ok {
		t.Error("case-insensitive lookup failed")
	}
	if _, ok := sp.Get([]byte("FOO")); !ok {
		t.Error("case-insensitive lookup failed")
	}

	strict, _ := p.Project(ProjectOptions{})
	if _, ok := strict.Get([]byte("foo")); ok {
		t.Error("strict lookup unexpectedly matched")
	}

	dup, _ := ParseViewOptions([]byte(`a;foo=1;FOO=2`), ParseConfig{})
	if _, err := dup[0].Params.Project(ProjectOptions{FoldName: true}); err == nil {
		t.Error("expected conflict under fold")
	}
}

func TestWriteViewQuotedSemicolon(t *testing.T) {
	opts, ok := ParseViewOptions([]byte(`a;q="x;y";z`), ParseConfig{})
	if !ok {
		t.Fatal("unexpected malformed input")
	}
	var buf bytes.Buffer
	if _, err := WriteViewOptions(&buf, opts); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if out != `a;q="x;y";z` {
		t.Fatalf("write back=%q", out)
	}

	// Reparse: the semicolon inside quotes must not split parameters.
	re, ok := ParseViewOptions(buf.Bytes(), ParseConfig{})
	if !ok {
		t.Fatal("reparse failed")
	}
	if !re[0].Equal(opts[0]) {
		t.Fatalf("round trip mismatch:\n got %s\nwant %s", re[0], opts[0])
	}
}

func TestWriteViewEmptyAndBare(t *testing.T) {
	opts, _ := ParseViewOptions([]byte(`a;e="";b`), ParseConfig{})
	var buf bytes.Buffer
	if _, err := WriteViewOptions(&buf, opts); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); out != `a;e="";b` {
		t.Fatalf("write back=%q", out)
	}
	re, ok := ParseViewOptions(buf.Bytes(), ParseConfig{})
	if !ok || !re[0].Equal(opts[0]) {
		t.Fatalf("round trip mismatch: %v ok=%v", re, ok)
	}
}

func TestParseViewCopy(t *testing.T) {
	data := []byte(`a;q="x;y";b`)
	opts, ok := ParseViewOptions(data, ParseConfig{Copy: true})
	if !ok {
		t.Fatal("unexpected malformed input")
	}
	copy(data, bytes.Repeat([]byte("z"), len(data)))

	exp, _ := ParseViewOptions([]byte(`a;q="x;y";b`), ParseConfig{})
	if !opts[0].Equal(exp[0]) {
		t.Fatalf("copied option changed after input mutation:\n got %s\nwant %s", opts[0], exp[0])
	}
}

func TestViewCloneIndependence(t *testing.T) {
	data := []byte(`a;x=1;y=2;z=3;w=4;p=5;q=6;r=7;s=8;t=9`)
	opts, _ := ParseViewOptions(data, ParseConfig{})
	clone := opts[0].Clone()

	// Mutate source data and the original option's bytes.
	copy(data, bytes.Repeat([]byte("Z"), len(data)))

	exp := []string{"x", "1", "y", "2", "z", "3", "w", "4", "p", "5", "q", "6", "r", "7", "s", "8", "t", "9"}
	var got []string
	for _, v := range clone.Params.All() {
		got = append(got, string(v.Name), string(v.Value))
	}
	if strings.Join(got, ",") != strings.Join(exp, ",") {
		t.Fatalf("clone depends on input:\n got %v\nwant %v", got, exp)
	}
}

func TestLegacyCompatibility(t *testing.T) {
	opts, ok := ParseOptions([]byte(`a;x=1;x=2,b`), nil)
	if !ok {
		t.Fatal("unexpected malformed input")
	}
	if v, exists := opts[0].Parameters.Get("x"); !exists || string(v) != "1" {
		t.Errorf("legacy Get=%q exists=%v want 1", v, exists)
	}
	if len(opts) != 2 {
		t.Fatalf("options=%d want 2", len(opts))
	}

	view, _ := ParseViewOptions([]byte(`a;x=1;x=2,b`), ParseConfig{})
	if got := view[0].Option(); !got.Equal(opts[0]) {
		t.Errorf("ViewOption.Option mismatch:\n got %s\nwant %s", got, opts[0])
	}
}

func paramNames(ps []Parameter) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p.Name)
	}
	return out
}
