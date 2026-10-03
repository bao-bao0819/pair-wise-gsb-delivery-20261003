package fastjson

import "errors"

var (
	// ErrCycleDetected is returned when a Value contains a cycle.
	ErrCycleDetected = errors.New("cycle detected in Value")

	errWalkStopped = errors.New("walk stopped")
)

// WalkAction controls how Walk continues after visiting a value.
type WalkAction int

const (
	// WalkContinue continues walking the current value's children and siblings.
	WalkContinue WalkAction = iota

	// WalkSkipChildren skips the current value's children and continues with siblings.
	WalkSkipChildren

	// WalkStop stops the whole walk.
	WalkStop
)

// PathElement is one element of a Value path.
// It is either a PathKey or a PathIndex.
type PathElement interface {
	pathElement()
}

// PathKey identifies an object entry by its storage key.
type PathKey struct {
	Key string
}

func (PathKey) pathElement() {}

// PathIndex identifies an array entry by its position.
type PathIndex struct {
	Index int
}

func (PathIndex) pathElement() {}

// WalkFunc is called by Walk for every visited value.
// Return WalkSkipChildren to skip v's children or WalkStop to stop walking.
type WalkFunc func(v *Value, path []PathElement) (WalkAction, error)

// Walk visits v and all of its descendants in depth-first, pre-order.
//
// Object entries are visited in their storage order and array entries in index
// order. The root value has an empty path. Every path passed to fn is an
// independent copy and may be retained after fn returns.
//
// If fn returns an error, Walk returns that error unchanged and visits no later
// nodes. If fn returns WalkStop without an error, Walk returns nil. A manually
// constructed cycle causes Walk to return ErrCycleDetected instead of looping.
func (v *Value) Walk(fn WalkFunc) error {
	if v == nil {
		return nil
	}
	if fn == nil {
		return errors.New("walk function cannot be nil")
	}
	if err := walkValue(v, nil, make(map[*Value]struct{}), fn); err != nil && !errors.Is(err, errWalkStopped) {
		return err
	}
	return nil
}

func walkValue(v *Value, path []PathElement, active map[*Value]struct{}, fn WalkFunc) error {
	if v == nil {
		v = valueNull
	}

	if v.t == TypeObject || v.t == TypeArray {
		if _, ok := active[v]; ok {
			return ErrCycleDetected
		}
	}

	action, err := fn(v, clonePath(path))
	if err != nil {
		return err
	}

	switch action {
	case WalkContinue:
	case WalkSkipChildren:
		return nil
	case WalkStop:
		return errWalkStopped
	default:
		return errors.New("invalid walk action")
	}

	switch v.Type() {
	case TypeObject:
		v.o.unescapeKeys()
		active[v] = struct{}{}
		defer delete(active, v)

		kvs := v.o.kvs
		for i := range kvs {
			childPath := childPath(path, PathKey{Key: kvs[i].k})
			if err := walkValue(kvs[i].v, childPath, active, fn); err != nil {
				return err
			}
		}
	case TypeArray:
		active[v] = struct{}{}
		defer delete(active, v)

		for i, child := range v.a {
			childPath := childPath(path, PathIndex{Index: i})
			if err := walkValue(child, childPath, active, fn); err != nil {
				return err
			}
		}
	}

	return nil
}

func clonePath(path []PathElement) []PathElement {
	if len(path) == 0 {
		return nil
	}
	cloned := make([]PathElement, len(path))
	copy(cloned, path)
	return cloned
}

func childPath(path []PathElement, element PathElement) []PathElement {
	child := make([]PathElement, len(path)+1)
	copy(child, path)
	child[len(path)] = element
	return child
}
