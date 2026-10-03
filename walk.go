package fastjson

import (
	"errors"
	"strconv"
)

// ErrWalkCycle is returned by Value.Walk when a cyclic reference is detected.
//
// Cycles cannot appear in parsed JSON, but they can be constructed manually
// via Object.Set and Value.SetArrayItem.
var ErrWalkCycle = errors.New("fastjson: cyclic reference detected during walk")

// WalkAction tells Value.Walk how to proceed after visiting a node.
type WalkAction int

const (
	// WalkContinue continues the traversal into the node's children.
	WalkContinue WalkAction = iota

	// WalkSkip skips the node's children and continues with the next sibling.
	WalkSkip

	// WalkStop stops the entire traversal. Walk returns nil.
	WalkStop
)

// PathElem is a single element of a Path.
//
// Exactly one of Key or Index is set, as indicated by IsKey.
type PathElem struct {
	// Key is the object key. Valid only if IsKey is true.
	Key string

	// Index is the array index. Valid only if IsKey is false.
	Index int

	// IsKey is true for object keys and false for array indexes.
	IsKey bool
}

// Path identifies the location of a value within a JSON tree.
//
// The path of the root value passed to Value.Walk is empty.
type Path []PathElem

// String returns a human-readable representation of p.
func (p Path) String() string {
	if len(p) == 0 {
		return ""
	}
	var b []byte
	for _, e := range p {
		if e.IsKey {
			if len(b) > 0 {
				b = append(b, '.')
			}
			b = append(b, e.Key...)
		} else {
			b = append(b, '[')
			b = strconv.AppendInt(b, int64(e.Index), 10)
			b = append(b, ']')
		}
	}
	return string(b)
}

// Walk traverses v in depth-first order, calling f for every node including
// v itself. Object members are visited in their storage order and array
// elements in their index order. The path passed to f for the root value is
// empty.
//
// The action returned by f controls the traversal: WalkContinue descends
// into the node's children, WalkSkip skips them, and WalkStop stops the
// whole traversal.
//
// If f returns a non-nil error, the traversal stops immediately and Walk
// returns this error to the caller without visiting the remaining siblings.
//
// Walk returns ErrWalkCycle if v contains a cyclic reference.
//
// The Path passed to f is safe to retain: it is never modified by subsequent
// traversal steps.
func (v *Value) Walk(f func(path Path, v *Value) (WalkAction, error)) error {
	err := walkValue(v, nil, f, make(map[*Value]struct{}))
	if err == errWalkStop {
		return nil
	}
	return err
}

var errWalkStop = errors.New("fastjson: walk stopped")

func walkValue(v *Value, path Path, f func(path Path, v *Value) (WalkAction, error), ancestors map[*Value]struct{}) error {
	action, err := f(path, v)
	if err != nil {
		return err
	}
	if action == WalkStop {
		return errWalkStop
	}
	if action == WalkSkip {
		return nil
	}

	var n int
	switch v.t {
	case TypeObject:
		n = len(v.o.kvs)
	case TypeArray:
		n = len(v.a)
	default:
		return nil
	}
	if n == 0 {
		return nil
	}

	if _, ok := ancestors[v]; ok {
		return ErrWalkCycle
	}
	ancestors[v] = struct{}{}
	defer delete(ancestors, v)

	if v.t == TypeObject {
		v.o.unescapeKeys()
		for i := range v.o.kvs {
			kv := &v.o.kvs[i]
			childPath := make(Path, len(path)+1)
			copy(childPath, path)
			childPath[len(path)] = PathElem{Key: kv.k, IsKey: true}
			if err := walkValue(kv.v, childPath, f, ancestors); err != nil {
				return err
			}
		}
		return nil
	}

	for i, child := range v.a {
		childPath := make(Path, len(path)+1)
		copy(childPath, path)
		childPath[len(path)] = PathElem{Index: i}
		if err := walkValue(child, childPath, f, ancestors); err != nil {
			return err
		}
	}
	return nil
}
