package mapstructure

import (
	"fmt"
	"reflect"
)

// unusedPathSegment is a single segment of the path to a value in the
// input data. A segment is either a map key (of any type, preserving
// non-string keys) or a slice/array index.
type unusedPathSegment struct {
	key     any
	index   int
	isIndex bool
}

// unusedValue is an unconsumed input value recorded during decoding,
// together with the full path to it in the input data.
type unusedValue struct {
	path  []unusedPathSegment
	value any
}

// pushUnusedPath records descending into a child value. It is a no-op
// when metadata is not being tracked.
func (d *Decoder) pushUnusedPath(seg unusedPathSegment) {
	if d.config.Metadata == nil {
		return
	}
	d.unusedPath = append(d.unusedPath, seg)
}

// popUnusedPath undoes the most recent pushUnusedPath.
func (d *Decoder) popUnusedPath() {
	if d.config.Metadata == nil {
		return
	}
	d.unusedPath = d.unusedPath[:len(d.unusedPath)-1]
}

// recordUnusedValue records an unconsumed input value at the given path.
// The value is deep-copied so that the resulting tree is independent from
// the original input. It is a no-op when metadata is not being tracked.
func (d *Decoder) recordUnusedValue(path []unusedPathSegment, value any) {
	if d.config.Metadata == nil {
		return
	}

	copiedPath := make([]unusedPathSegment, len(path))
	copy(copiedPath, path)

	d.unusedValues = append(d.unusedValues, unusedValue{
		path:  copiedPath,
		value: deepCopyAny(value),
	})
}

// buildUnusedValueTree folds the recorded unconsumed values into a tree
// mirroring the structure of the input data.
func buildUnusedValueTree(entries []unusedValue) map[string]any {
	var root any = map[string]any{}
	for _, entry := range entries {
		root = setUnusedValue(root, entry.path, entry.value)
	}

	if m, ok := root.(map[string]any); ok {
		return m
	}

	// The root itself ended up with non-string keys (only possible when
	// decoding a struct directly from a map with non-string keys). There
	// is no way to represent that in a map[string]any, so stringify the
	// keys as a best effort.
	out := map[string]any{}
	if m, ok := root.(map[any]any); ok {
		for k, v := range m {
			out[metadataKeyString(k)] = v
		}
	}
	return out
}

// setUnusedValue sets value at the location described by path within
// node, creating intermediate nodes as needed, and returns the (possibly
// replaced) node. Map nodes are map[string]any unless a non-string key
// forces them to be map[any]any so that distinct keys never overwrite
// each other. Index segments create []any nodes.
func setUnusedValue(node any, path []unusedPathSegment, value any) any {
	if len(path) == 0 {
		return value
	}

	seg := path[0]
	if seg.isIndex {
		arr, _ := node.([]any)
		for len(arr) <= seg.index {
			arr = append(arr, nil)
		}
		arr[seg.index] = setUnusedValue(arr[seg.index], path[1:], value)
		return arr
	}

	if key, ok := seg.key.(string); ok {
		switch m := node.(type) {
		case map[string]any:
			m[key] = setUnusedValue(m[key], path[1:], value)
			return m
		case map[any]any:
			m[key] = setUnusedValue(m[key], path[1:], value)
			return m
		default:
			fresh := map[string]any{}
			fresh[key] = setUnusedValue(nil, path[1:], value)
			return fresh
		}
	}

	// Non-string keys (including keys of maps with interface key types)
	// require a map[any]any node so they are preserved exactly and cannot
	// collide with string keys.
	var m map[any]any
	switch existing := node.(type) {
	case map[any]any:
		m = existing
	case map[string]any:
		m = make(map[any]any, len(existing)+1)
		for k, v := range existing {
			m[k] = v
		}
	default:
		m = map[any]any{}
	}
	m[seg.key] = setUnusedValue(m[seg.key], path[1:], value)
	return m
}

// metadataKeyString converts a map key of any type to the string form
// used in the flat Metadata key lists (Keys, Unused, Unset).
func metadataKeyString(key any) string {
	if s, ok := key.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", key)
}

// mapIndexForKey returns the value of key in the map dataVal, tolerating
// nil keys in maps with interface key types.
func mapIndexForKey(dataVal reflect.Value, key any) reflect.Value {
	keyVal := reflect.ValueOf(key)
	if !keyVal.IsValid() {
		keyVal = reflect.Zero(dataVal.Type().Key())
	}
	return dataVal.MapIndex(keyVal)
}

// deepCopyAny returns a deep copy of src, preserving its types. Maps,
// slices, arrays, pointers and structs are copied recursively so that
// mutating the copy never affects the source.
func deepCopyAny(src any) any {
	if src == nil {
		return nil
	}
	return deepCopyValue(reflect.ValueOf(src)).Interface()
}

func deepCopyValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		return deepCopyValue(v.Elem())

	case reflect.Ptr:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		c := reflect.New(v.Type().Elem())
		c.Elem().Set(deepCopyValue(v.Elem()))
		return c

	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		c := reflect.MakeMapWithSize(v.Type(), v.Len())
		for _, k := range v.MapKeys() {
			c.SetMapIndex(deepCopyValue(k), deepCopyValue(v.MapIndex(k)))
		}
		return c

	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		c := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			c.Index(i).Set(deepCopyValue(v.Index(i)))
		}
		return c

	case reflect.Array:
		c := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			c.Index(i).Set(deepCopyValue(v.Index(i)))
		}
		return c

	case reflect.Struct:
		c := reflect.New(v.Type()).Elem()
		c.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if c.Field(i).CanSet() {
				c.Field(i).Set(deepCopyValue(v.Field(i)))
			}
		}
		return c

	default:
		// Scalars and values that cannot be copied (funcs, chans, ...)
		// are returned as-is.
		return v
	}
}
