package fastjson

import (
	"errors"
	"reflect"
	"testing"
)

func TestWalkOrder(t *testing.T) {
	v := MustParse(`{"a": [1, {"b": 2}], "c": {"d": [3]}, "e": 4}`)

	var paths []string
	err := v.Walk(func(p Path, v *Value) (WalkAction, error) {
		paths = append(paths, p.String())
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []string{"", "a", "a[0]", "a[1]", "a[1].b", "c", "c.d", "c.d[0]", "e"}
	if !reflect.DeepEqual(paths, expected) {
		t.Fatalf("unexpected paths\ngot:  %q\nwant: %q", paths, expected)
	}
}

func TestWalkRootPathEmpty(t *testing.T) {
	v := MustParse(`123`)
	called := false
	err := v.Walk(func(p Path, v *Value) (WalkAction, error) {
		called = true
		if len(p) != 0 {
			t.Fatalf("expected empty root path; got %q", p.String())
		}
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !called {
		t.Fatal("callback wasn't called")
	}
}

func TestWalkSkip(t *testing.T) {
	v := MustParse(`{"a": {"x": 1}, "b": [2, 3], "c": 4}`)

	var paths []string
	err := v.Walk(func(p Path, v *Value) (WalkAction, error) {
		paths = append(paths, p.String())
		if p.String() == "a" || p.String() == "b" {
			return WalkSkip, nil
		}
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []string{"", "a", "b", "c"}
	if !reflect.DeepEqual(paths, expected) {
		t.Fatalf("unexpected paths\ngot:  %q\nwant: %q", paths, expected)
	}
}

func TestWalkStop(t *testing.T) {
	v := MustParse(`{"a": {"x": 1}, "b": 2}`)

	var paths []string
	err := v.Walk(func(p Path, v *Value) (WalkAction, error) {
		paths = append(paths, p.String())
		if p.String() == "a.x" {
			return WalkStop, nil
		}
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []string{"", "a", "a.x"}
	if !reflect.DeepEqual(paths, expected) {
		t.Fatalf("unexpected paths\ngot:  %q\nwant: %q", paths, expected)
	}
}

func TestWalkError(t *testing.T) {
	v := MustParse(`{"a": {"x": 1}, "b": 2}`)

	errTest := errors.New("test error")
	var paths []string
	err := v.Walk(func(p Path, v *Value) (WalkAction, error) {
		paths = append(paths, p.String())
		if p.String() == "a.x" {
			return WalkContinue, errTest
		}
		return WalkContinue, nil
	})
	if err != errTest {
		t.Fatalf("expected the original callback error; got %v", err)
	}

	expected := []string{"", "a", "a.x"}
	if !reflect.DeepEqual(paths, expected) {
		t.Fatalf("unexpected paths\ngot:  %q\nwant: %q", paths, expected)
	}
}

func TestWalkCycle(t *testing.T) {
	v := MustParse(`{"a": {"b": 1}}`)
	v.GetObject("a").Set("self", v.Get("a"))

	visited := 0
	err := v.Walk(func(p Path, v *Value) (WalkAction, error) {
		visited++
		if visited > 100 {
			t.Fatal("walk doesn't stop on cyclic reference")
		}
		return WalkContinue, nil
	})
	if !errors.Is(err, ErrWalkCycle) {
		t.Fatalf("expected ErrWalkCycle; got %v", err)
	}
}

func TestWalkCycleArray(t *testing.T) {
	v := MustParse(`[1, 2]`)
	v.SetArrayItem(0, v)

	err := v.Walk(func(p Path, v *Value) (WalkAction, error) {
		return WalkContinue, nil
	})
	if !errors.Is(err, ErrWalkCycle) {
		t.Fatalf("expected ErrWalkCycle; got %v", err)
	}
}

func TestWalkSharedSubtreeIsNotCycle(t *testing.T) {
	// The same subtree referenced from two places is a DAG, not a cycle.
	leaf := MustParse(`{"x": 1}`)
	v := MustParse(`{"a": null, "b": null}`)
	v.GetObject().Set("a", leaf)
	v.GetObject().Set("b", leaf)

	var paths []string
	err := v.Walk(func(p Path, v *Value) (WalkAction, error) {
		paths = append(paths, p.String())
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []string{"", "a", "a.x", "b", "b.x"}
	if !reflect.DeepEqual(paths, expected) {
		t.Fatalf("unexpected paths\ngot:  %q\nwant: %q", paths, expected)
	}
}

func TestWalkPathRetention(t *testing.T) {
	v := MustParse(`{"aaaa": {"bbbb": 1}, "cccc": {"dddd": 2}}`)

	var retained []Path
	err := v.Walk(func(p Path, v *Value) (WalkAction, error) {
		retained = append(retained, p)
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	var paths []string
	for _, p := range retained {
		paths = append(paths, p.String())
	}
	expected := []string{"", "aaaa", "aaaa.bbbb", "cccc", "cccc.dddd"}
	if !reflect.DeepEqual(paths, expected) {
		t.Fatalf("retained paths were modified\ngot:  %q\nwant: %q", paths, expected)
	}
}

func TestWalkPathElems(t *testing.T) {
	v := MustParse(`{"a": [10]}`)

	var elems []PathElem
	err := v.Walk(func(p Path, v *Value) (WalkAction, error) {
		if v.Type() == TypeNumber {
			elems = append(elems, p...)
		}
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []PathElem{
		{Key: "a", IsKey: true},
		{Index: 0},
	}
	if !reflect.DeepEqual(elems, expected) {
		t.Fatalf("unexpected path elems\ngot:  %+v\nwant: %+v", elems, expected)
	}
}
