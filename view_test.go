package httphead

import (
	"bufio"
	"bytes"
	"testing"
)

func TestParseOptionViewsKinds(t *testing.T) {
	data := []byte(`c;none;empty="";tok=1;quoted="a;b";again`)

	opts, ok := ParseOptionViews(data, nil)
	if !ok {
		t.Fatalf("ParseOptions returned ok=false")
	}
	if len(opts) != 1 {
		t.Fatalf("unexpected number of options: %d; want 1", len(opts))
	}

	p := opts[0].Parameters
	exp := []struct {
		name  string
		value string
		kind  ValueKind
	}{
		{"none", "", ValueNone},
		{"empty", "", ValueQuoted},
		{"tok", "1", ValueToken},
		{"quoted", "a;b", ValueQuoted},
		{"again", "", ValueNone},
	}
	if p.Len() != len(exp) {
		t.Fatalf("unexpected number of parameters: %d; want %d", p.Len(), len(exp))
	}
	for i, e := range exp {
		act := p.At(i)
		if string(act.Name) != e.name ||
			string(act.Value) != e.value ||
			act.Kind != e.kind {
			t.Errorf(
				"unexpected %d-th parameter: %#q=%#q (%s); want %#q=%#q (%s)",
				i, string(act.Name), string(act.Value), act.Kind,
				e.name, e.value, e.kind,
			)
		}
	}
}

func TestParseOptionViewsOrderAndDup(t *testing.T) {
	data := []byte(`foo;a=1;b=2;a=3`)

	opts, ok := ParseOptionViews(data, nil)
	if !ok {
		t.Fatalf("ParseOptions returned ok=false")
	}
	p := opts[0].Parameters

	var names []string
	p.ForEach(func(v Parameter) bool {
		names = append(names, string(v.Name)+"="+string(v.Value))
		return true
	})
	want := []string{"a=1", "b=2", "a=3"}
	if len(names) != len(want) {
		t.Fatalf("unexpected parameters: %v; want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("unexpected %d-th parameter: %q; want %q", i, names[i], want[i])
		}
	}
}

func TestParametersViewProjectReject(t *testing.T) {
	data := []byte(`foo;a=1;b=2;a=3;c=1;c=2`)
	opts, _ := ParseOptionViews(data, nil)
	p := opts[0].Parameters

	v, found, conflict := p.Project("a", ProjectOptions{Duplicates: DuplicateReject})
	if !found || !conflict {
		t.Errorf("expected conflict for repeated a; got found=%v conflict=%v", found, conflict)
	}
	if string(v.Value) != "1" {
		t.Errorf("reject policy should report the first value: %q; want %q", v.Value, "1")
	}

	if _, found, conflict = p.Project("missing", ProjectOptions{Duplicates: DuplicateReject}); found || conflict {
		t.Errorf("missing key should not be found or conflicted; got found=%v conflict=%v", found, conflict)
	}

	if v, found, conflict = p.Project("b", ProjectOptions{Duplicates: DuplicateReject}); !found || conflict || string(v.Value) != "2" {
		t.Errorf("single b lookup failed: %q found=%v conflict=%v", v.Value, found, conflict)
	}

	if _, found, conflict = p.Project("c", ProjectOptions{Duplicates: DuplicateReject}); !found || !conflict {
		t.Errorf("expected conflict for repeated c")
	}
}

func TestParametersViewProjectFirstLast(t *testing.T) {
	data := []byte(`foo;A=1;a=2;A=3`)
	opts, _ := ParseOptionViews(data, nil)
	p := opts[0].Parameters

	// Case sensitive by default.
	if v, found, conflict := p.Project("A", ProjectOptions{Duplicates: DuplicateFirst}); !found || conflict || string(v.Value) != "1" {
		t.Errorf("first(A) = %q found=%v conflict=%v; want 1", v.Value, found, conflict)
	}
	if v, found, conflict := p.Project("A", ProjectOptions{Duplicates: DuplicateLast}); !found || conflict || string(v.Value) != "3" {
		t.Errorf("last(A) = %q found=%v conflict=%v; want 3", v.Value, found, conflict)
	}

	// Case folding groups A and a together.
	if v, found, conflict := p.Project("a", ProjectOptions{Duplicates: DuplicateFirst, FoldNames: true}); !found || conflict || string(v.Value) != "1" {
		t.Errorf("fold first(a) = %q found=%v conflict=%v; want 1", v.Value, found, conflict)
	}
	if v, found, conflict := p.Project("A", ProjectOptions{Duplicates: DuplicateLast, FoldNames: true}); !found || conflict || string(v.Value) != "3" {
		t.Errorf("fold last(A) = %q found=%v conflict=%v; want 3", v.Value, found, conflict)
	}
}

func TestOptionViewCloneIndependence(t *testing.T) {
	data := []byte(`foo;q="a;b";z`)
	opts, _ := ParseOptionViews(data, nil)

	cloned := opts[0].Clone()
	for i := range data {
		data[i] = 'x'
	}

	if string(cloned.Name) != "foo" {
		t.Errorf("cloned name mutated: %q; want foo", cloned.Name)
	}
	if cloned.Parameters.Len() != 2 {
		t.Fatalf("unexpected parameters count: %d", cloned.Parameters.Len())
	}
	if v := cloned.Parameters.At(0); string(v.Name) != "q" || string(v.Value) != "a;b" || v.Kind != ValueQuoted {
		t.Errorf("cloned q parameter corrupted: %#q=%#q %s", v.Name, v.Value, v.Kind)
	}
	if v := cloned.Parameters.At(1); string(v.Name) != "z" || v.Kind != ValueNone {
		t.Errorf("cloned z parameter corrupted: %#q=%#q %s", v.Name, v.Value, v.Kind)
	}
}

func TestOptionViewCloneHeap(t *testing.T) {
	// More than 8 parameters forces heap-backed storage.
	var view OptionView
	view.Name = []byte("foo")
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		view.Parameters.Set([]byte(n), []byte(n), ValueToken)
	}

	cloned := view.Clone()
	if cloned.Parameters.Len() != 10 {
		t.Fatalf("unexpected parameters count: %d", cloned.Parameters.Len())
	}
	for i := 0; i < 10; i++ {
		v := cloned.Parameters.At(i)
		if string(v.Name) != string(v.Value) || v.Kind != ValueToken {
			t.Errorf("unexpected %d-th cloned parameter: %#q=%#q %s", i, v.Name, v.Value, v.Kind)
		}
	}

	// Mutating the source must not affect the clone.
	for _, v := range view.Parameters.data() {
		for i := range v.Name {
			v.Name[i] = 'x'
		}
	}
	if got := cloned.Parameters.At(9).Name; string(got) != "j" {
		t.Errorf("clone shares memory with source: %q; want j", got)
	}
}

func TestWriteOptionViewsRoundtrip(t *testing.T) {
	in := `foo;none;empty="";quoted="a;b";tok=1`

	opts, ok := ParseOptionViews([]byte(in), nil)
	if !ok {
		t.Fatalf("parse ok=false")
	}

	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	if _, err := WriteOptionViews(bw, opts); err != nil {
		t.Fatalf("write error: %v", err)
	}
	bw.Flush()

	// The semicolon inside the quoted value must stay inside quotes and thus
	// must not split the parameter into two.
	exp := `foo;none;empty="";quoted="a;b";tok=1`
	if buf.String() != exp {
		t.Errorf("WriteOptionViews = %#q; want %#q", buf.String(), exp)
	}

	reparsed, ok := ParseOptionViews(buf.Bytes(), nil)
	if !ok {
		t.Fatalf("reparse ok=false")
	}
	p := reparsed[0].Parameters
	if p.Len() != 4 {
		t.Fatalf("unexpected parameters count after roundtrip: %d; want 4", p.Len())
	}
	if v := p.At(2); v.Kind != ValueQuoted || string(v.Value) != "a;b" {
		t.Errorf("quoted value not preserved: %#q %s", v.Value, v.Kind)
	}
	if v := p.At(1); v.Kind != ValueQuoted || len(v.Value) != 0 {
		t.Errorf("empty quoted value not preserved: %#q %s", v.Value, v.Kind)
	}
	if v := p.At(0); v.Kind != ValueNone {
		t.Errorf("bare parameter kind changed: %s", v.Kind)
	}
}

func TestOptionViewLegacyCompatibility(t *testing.T) {
	data := []byte(`foo;a=1;b=2,bar`)

	views, ok := ParseOptionViews(data, nil)
	if !ok {
		t.Fatalf("parse ok=false")
	}
	legacy, ok := ParseOptions(data, nil)
	if !ok {
		t.Fatalf("legacy parse ok=false")
	}

	if len(views) != len(legacy) {
		t.Fatalf("option count mismatch: %d vs %d", len(views), len(legacy))
	}
	for i := range views {
		if !views[i].Option().Equal(legacy[i]) {
			t.Errorf("%d-th converted option differs:\n got  %v\n want %v", i, views[i].Option(), legacy[i])
		}
	}

	// Legacy Get still works on converted parameters and returns first match.
	converted := views[0].Parameters.Parameters()
	if v, ok := converted.Get("a"); !ok || string(v) != "1" {
		t.Errorf(`legacy Get("a") = %q, %v; want 1, true`, v, ok)
	}

	// ScanOptionsValues callback semantics match ScanOptions tuples.
	var got []tuple
	ScanOptionsValues(data, func(index int, option, attribute, value []byte, kind ValueKind) Control {
		got = append(got, tuple{index, option, attribute, value})
		return ControlContinue
	})
	var want []tuple
	ScanOptions(data, func(index int, option, attribute, value []byte) Control {
		want = append(want, tuple{index, option, attribute, value})
		return ControlContinue
	})
	if len(got) != len(want) {
		t.Fatalf("callback tuple count mismatch: %d vs %d", len(got), len(want))
	}
	for i := range got {
		a, b := got[i], want[i]
		if a.index != b.index ||
			!bytes.Equal(a.option, b.option) ||
			!bytes.Equal(a.attribute, b.attribute) ||
			!bytes.Equal(a.value, b.value) {
			t.Errorf("%d-th tuple mismatch: %v vs %v", i, a, b)
		}
	}
}
