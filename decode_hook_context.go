package mapstructure

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// PathSegmentKind describes what a single PathSegment addresses while
// walking the input or output data structures during decoding.
type PathSegmentKind int

const (
	// PathSegmentField is a struct field, addressed by its (tag-adjusted)
	// field name.
	PathSegmentField PathSegmentKind = iota

	// PathSegmentMapKey is a map entry, addressed by the original map key
	// value.
	PathSegmentMapKey

	// PathSegmentIndex is a slice or array element, addressed by its
	// numeric index.
	PathSegmentIndex
)

// String returns a human-readable representation of the segment kind.
func (k PathSegmentKind) String() string {
	switch k {
	case PathSegmentField:
		return "field"
	case PathSegmentMapKey:
		return "map key"
	case PathSegmentIndex:
		return "index"
	default:
		return "unknown"
	}
}

// PathSegment is a single step in a Path. Exactly one of Field, Key or
// Index is meaningful, depending on Kind. Keeping the segments structured
// (instead of joining them with dots) means arbitrary strings, including
// ones containing dots or brackets, round-trip unambiguously.
type PathSegment struct {
	Kind PathSegmentKind

	// Field is the struct field name. Set when Kind is PathSegmentField.
	Field string

	// Key is the original map key value. Set when Kind is PathSegmentMapKey.
	Key any

	// Index is the slice or array index. Set when Kind is PathSegmentIndex.
	Index int
}

// FieldSegment returns a PathSegment addressing a struct field.
func FieldSegment(name string) PathSegment {
	return PathSegment{Kind: PathSegmentField, Field: name}
}

// MapKeySegment returns a PathSegment addressing a map entry by its key.
func MapKeySegment(key any) PathSegment {
	return PathSegment{Kind: PathSegmentMapKey, Key: key}
}

// IndexSegment returns a PathSegment addressing a slice or array element.
func IndexSegment(i int) PathSegment {
	return PathSegment{Kind: PathSegmentIndex, Index: i}
}

// String returns a human-readable representation of the segment.
func (s PathSegment) String() string {
	switch s.Kind {
	case PathSegmentField:
		return s.Field
	case PathSegmentMapKey:
		if str, ok := s.Key.(string); ok {
			return "[" + strconv.Quote(str) + "]"
		}
		return fmt.Sprintf("[%v]", s.Key)
	case PathSegmentIndex:
		return "[" + strconv.Itoa(s.Index) + "]"
	default:
		return "[?]"
	}
}

// Path is a sequence of segments addressing a position inside a nested
// data structure. The zero value (nil) addresses the root.
//
// A Path received by a DecodeHookFuncContext is a snapshot: it is never
// modified by the decoder afterwards, so it is safe to retain.
type Path []PathSegment

// String returns a human-readable representation of the path, for example
// `credentials["a.b"][0]`. It is meant for logging and debugging only;
// use the structured segments for any kind of matching.
func (p Path) String() string {
	var sb strings.Builder
	for _, seg := range p {
		if seg.Kind == PathSegmentField && sb.Len() > 0 {
			sb.WriteByte('.')
		}
		sb.WriteString(seg.String())
	}
	return sb.String()
}

// clone returns a copy of the path that shares no backing array with the
// original.
func (p Path) clone() Path {
	if p == nil {
		return nil
	}
	out := make(Path, len(p))
	copy(out, p)
	return out
}

// appendSegment returns a new path with seg appended. The backing array is
// never shared with the receiver, so nested decoding and paths retained by
// hooks can never observe or cause in-place mutation.
func (p Path) appendSegment(seg PathSegment) Path {
	out := make(Path, len(p)+1)
	copy(out, p)
	out[len(p)] = seg
	return out
}

// DecodeHookContext describes where in the input and output structures a
// decode hook is currently being invoked.
type DecodeHookContext struct {
	// InputPath is the position of the value being converted within the
	// input data. Struct fields appear as PathSegmentField, map entries as
	// PathSegmentMapKey (carrying the original key) and slice or array
	// elements as PathSegmentIndex.
	InputPath Path

	// OutputPath is the position of the target the value is decoded into
	// within the result. For map values the trailing PathSegmentMapKey
	// carries the source key, since the decoded key is only known after
	// the hook ran.
	OutputPath Path

	// MapKey reports whether the value currently being converted is a map
	// key (as opposed to a map value or any other value).
	MapKey bool
}

// DecodeHookFuncContext is a DecodeHookFunc that, in addition to the source
// and target values, receives a DecodeHookContext describing where in the
// input and output structures the conversion is taking place.
//
// Like every other hook form it is invoked before any decoding and before
// a target type's Unmarshaler implementation. See DecoderConfig.DecodeHook
// for the full ordering guarantees.
type DecodeHookFuncContext func(ctx DecodeHookContext, from reflect.Value, to reflect.Value) (any, error)

// decodeContext is the internal, mutable-by-copy position tracker threaded
// through a single Decode operation. It is always passed by value and only
// ever extended through copy-on-append, so a nested Decode call (or a path
// slice retained by a hook) can never rewrite the context of an in-flight
// decode.
type decodeContext struct {
	inputPath  Path
	outputPath Path
	mapKey     bool
}

// input returns a copy of the context descended one step into the input.
func (c decodeContext) input(seg PathSegment) decodeContext {
	c.inputPath = c.inputPath.appendSegment(seg)
	return c
}

// output returns a copy of the context descended one step into the output.
func (c decodeContext) output(seg PathSegment) decodeContext {
	c.outputPath = c.outputPath.appendSegment(seg)
	return c
}

// asMapKey returns a copy of the context flagged as converting a map key.
func (c decodeContext) asMapKey(mapKey bool) decodeContext {
	c.mapKey = mapKey
	return c
}

// hookContext snapshots the context for handing out to a decode hook. The
// paths are cloned so the hook can retain them safely.
func (c decodeContext) hookContext() DecodeHookContext {
	return DecodeHookContext{
		InputPath:  c.inputPath.clone(),
		OutputPath: c.outputPath.clone(),
		MapKey:     c.mapKey,
	}
}
