package fastjson

import (
	"errors"
	"fmt"
	"testing"
)

type visitedValue struct {
	value string
	path  []PathElement
}

func TestValueWalkOrderAndPath(t *testing.T) {
	v := MustParse(`{"b":[{"x":1}],"a":"two","c":[3,4]}`)

	var visited []visitedValue
	err := v.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		visited = append(visited, visitedValue{
			value: v.String(),
			path:  cloneTestPath(path),
		})
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []visitedValue{
		{value: `{"b":[{"x":1}],"a":"two","c":[3,4]}`, path: nil},
		{value: `[{"x":1}]`, path: []PathElement{PathKey{Key: "b"}}},
		{value: `{"x":1}`, path: []PathElement{PathKey{Key: "b"}, PathIndex{Index: 0}}},
		{value: `1`, path: []PathElement{PathKey{Key: "b"}, PathIndex{Index: 0}, PathKey{Key: "x"}}},
		{value: `"two"`, path: []PathElement{PathKey{Key: "a"}}},
		{value: `[3,4]`, path: []PathElement{PathKey{Key: "c"}}},
		{value: `3`, path: []PathElement{PathKey{Key: "c"}, PathIndex{Index: 0}}},
		{value: `4`, path: []PathElement{PathKey{Key: "c"}, PathIndex{Index: 1}}},
	}
	assertVisitedValues(t, visited, expected)
}

func TestValueWalkRootPath(t *testing.T) {
	v := MustParse(`"root"`)

	called := false
	err := v.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		called = true
		if len(path) != 0 {
			t.Fatalf("unexpected root path: %#v", path)
		}
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !called {
		t.Fatalf("root callback was not called")
	}
}

func TestValueWalkUnescapesObjectKeys(t *testing.T) {
	v := MustParse(`{"a\"b":1}`)

	var paths [][]PathElement
	err := v.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		paths = append(paths, cloneTestPath(path))
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if len(paths) != 2 {
		t.Fatalf("unexpected number of visits: %d; want 2", len(paths))
	}
	assertPath(t, paths[1], []PathElement{PathKey{Key: `a"b`}})
}

func TestValueWalkPathIsStable(t *testing.T) {
	v := MustParse(`{"a":{"b":1},"c":2}`)

	var saved []PathElement
	var later []PathElement
	err := v.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		switch pathString(path) {
		case ".a.b":
			saved = cloneTestPath(path)
			path[0] = PathIndex{Index: 99}
		case ".c":
			later = cloneTestPath(path)
		}
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	assertPath(t, saved, []PathElement{PathKey{Key: "a"}, PathKey{Key: "b"}})
	assertPath(t, later, []PathElement{PathKey{Key: "c"}})
}

func TestValueWalkSkipChildren(t *testing.T) {
	v := MustParse(`{"skip":{"hidden":1},"kept":2}`)

	var visited []string
	err := v.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		visited = append(visited, pathString(path))
		if pathString(path) == ".skip" {
			return WalkSkipChildren, nil
		}
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []string{"", ".skip", ".kept"}
	assertStrings(t, visited, expected)
}

func TestValueWalkStop(t *testing.T) {
	v := MustParse(`{"a":1,"b":2,"c":3}`)

	wantErr := errors.New("stop sentinel")
	var visited []string
	err := v.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		visited = append(visited, pathString(path))
		if pathString(path) == ".a" {
			return WalkStop, nil
		}
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []string{"", ".a"}
	assertStrings(t, visited, expected)

	visited = nil
	err = v.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		visited = append(visited, pathString(path))
		if pathString(path) == ".a" {
			return WalkStop, wantErr
		}
		return WalkContinue, nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("unexpected error: %v; want %v", err, wantErr)
	}
	assertStrings(t, visited, expected)
}

func TestValueWalkCallbackErrorStopsSiblings(t *testing.T) {
	v := MustParse(`{"a":1,"b":{"c":2},"d":3}`)
	wantErr := errors.New("boom")

	var visited []string
	err := v.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		visited = append(visited, pathString(path))
		if pathString(path) == ".b.c" {
			return WalkContinue, wantErr
		}
		return WalkContinue, nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("unexpected error: %v; want %v", err, wantErr)
	}

	expected := []string{"", ".a", ".b", ".b.c"}
	assertStrings(t, visited, expected)
}

func TestValueWalkInvalidAction(t *testing.T) {
	v := MustParse(`{"a":1}`)

	called := false
	err := v.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		called = true
		return WalkAction(99), nil
	})
	if err == nil {
		t.Fatalf("expected error for invalid action")
	}
	if !called {
		t.Fatalf("callback was not called")
	}
}

func TestValueWalkCycle(t *testing.T) {
	var arena Arena
	root := arena.NewObject()
	child := arena.NewObject()
	root.Set("child", child)
	child.Set("root", root)

	var visited []string
	err := root.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		visited = append(visited, pathString(path))
		return WalkContinue, nil
	})
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("unexpected error: %v; want %v", err, ErrCycleDetected)
	}

	expected := []string{"", ".child"}
	assertStrings(t, visited, expected)
}

func TestValueWalkArrayCycle(t *testing.T) {
	var arena Arena
	root := arena.NewArray()
	child := arena.NewArray()
	root.SetArrayItem(0, child)
	child.SetArrayItem(0, root)

	var visited []string
	err := root.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		visited = append(visited, pathString(path))
		return WalkContinue, nil
	})
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("unexpected error: %v; want %v", err, ErrCycleDetected)
	}

	expected := []string{"", "[0]"}
	assertStrings(t, visited, expected)
}

func TestValueWalkNil(t *testing.T) {
	var v *Value
	if err := v.Walk(func(*Value, []PathElement) (WalkAction, error) {
		t.Fatalf("callback called for nil value")
		return WalkContinue, nil
	}); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if err := MustParse(`{}`).Walk(nil); err == nil {
		t.Fatalf("expected error for nil callback")
	}
}

func TestValueWalkRepeatedDAGValues(t *testing.T) {
	var arena Arena
	root := arena.NewObject()
	shared := arena.NewObject()
	shared.Set("value", arena.NewNumberInt(1))
	root.Set("first", shared)
	root.Set("second", shared)

	var count int
	err := root.Walk(func(v *Value, path []PathElement) (WalkAction, error) {
		count++
		return WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if count != 5 {
		t.Fatalf("unexpected number of visits: %d; want 5", count)
	}
}

func cloneTestPath(path []PathElement) []PathElement {
	if len(path) == 0 {
		return nil
	}
	cloned := make([]PathElement, len(path))
	copy(cloned, path)
	return cloned
}

func pathString(path []PathElement) string {
	result := ""
	for _, element := range path {
		switch element := element.(type) {
		case PathKey:
			result += "." + element.Key
		case PathIndex:
			result += fmt.Sprintf("[%d]", element.Index)
		default:
			result += ".<invalid>"
		}
	}
	return result
}

func assertVisitedValues(t *testing.T, got, want []visitedValue) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("unexpected number of visits: %d; want %d\ngot: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].value != want[i].value {
			t.Fatalf("unexpected value at %d: %s; want %s", i, got[i].value, want[i].value)
		}
		assertPath(t, got[i].path, want[i].path)
	}
}

func assertStrings(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("unexpected values: %#v; want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected value at %d: %q; want %q", i, got[i], want[i])
		}
	}
}

func assertPath(t *testing.T, got, want []PathElement) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("unexpected path length: %d; want %d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected path element at %d: %#v; want %#v", i, got[i], want[i])
		}
	}
}
