// Copyright (c) 2013 Kelsey Hightower. All rights reserved.
// Use of this source code is governed by the MIT License that can be found in
// the LICENSE file.

package envconfig

import (
	"encoding"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidSpecification indicates that a specification is of the wrong type.
var ErrInvalidSpecification = errors.New("specification must be a struct pointer")

var gatherRegexp = regexp.MustCompile("([^A-Z]+|[A-Z]+[^A-Z]+|[A-Z]+)")
var acronymRegexp = regexp.MustCompile("([A-Z]+)([A-Z][^A-Z]+)")

// A ParseError occurs when an environment variable cannot be converted to
// the type required by a struct field during assignment.
type ParseError struct {
	KeyName   string
	FieldName string
	TypeName  string
	Value     string
	Err       error
}

// Decoder has the same semantics as Setter, but takes higher precedence.
// It is provided for historical compatibility.
type Decoder interface {
	Decode(value string) error
}

// Setter is implemented by types can self-deserialize values.
// Any type that implements flag.Value also implements Setter.
type Setter interface {
	Set(value string) error
}

// Validator is implemented by specification structs (or individual
// fields) that enforce cross-field constraints, such as a port range
// that only applies to a particular mode or a certificate/key pair
// that must be provided together.
//
// After every field of the specification has been converted
// (including defaults and custom Decoders), Validate is called on
// each implementing instance in inside-out order: nested and embedded
// structs are validated before the structs that contain them. Each
// instance is validated at most once. Errors returned by Validate are
// reported together with the full struct path of the instance.
type Validator interface {
	Validate() error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("envconfig.Process: assigning %[1]s to %[2]s: converting '%[3]s' to type %[4]s. details: %[5]s", e.KeyName, e.FieldName, e.Value, e.TypeName, e.Err)
}

// varInfo maintains information about the configuration variable
type varInfo struct {
	Name  string
	Alt   string
	Key   string
	Field reflect.Value
	Tags  reflect.StructTag
}

// specNode is a struct instance reached while walking the specification.
type specNode struct {
	value  reflect.Value
	fields []*specField
	failed bool // a field in this subtree could not be converted
}

// specField is one field of a specNode. It is either a leaf value or a
// nested struct; in the latter case child describes that struct.
type specField struct {
	info   varInfo
	field  reflect.Value
	child  *specNode
	failed bool // this leaf could not be converted
}

// buildSpec validates spec and builds the struct tree used for conversion
// and validation.
func buildSpec(prefix string, spec interface{}) (*specNode, error) {
	s := reflect.ValueOf(spec)

	if s.Kind() != reflect.Ptr {
		return nil, ErrInvalidSpecification
	}
	s = s.Elem()
	if s.Kind() != reflect.Struct {
		return nil, ErrInvalidSpecification
	}

	return buildNode(s, prefix), nil
}

// buildNode builds the node for the struct value f.
func buildNode(f reflect.Value, prefix string) *specNode {
	node := &specNode{value: f}
	typeOfSpec := f.Type()

	// over allocate, we will extend if needed later
	node.fields = make([]*specField, 0, f.NumField())
	for i := 0; i < f.NumField(); i++ {
		field := f.Field(i)
		ftype := typeOfSpec.Field(i)
		if !field.CanSet() || isTrue(ftype.Tag.Get("ignored")) {
			continue
		}

		field = derefStructPtr(field)

		innerPrefix := prefix
		if field.Kind() == reflect.Struct && !ftype.Anonymous {
			innerPrefix = baseKey(ftype)
			if prefix != "" {
				innerPrefix = prefix + "_" + innerPrefix
			}
		}

		info := varInfo{
			Name:  ftype.Name,
			Field: field,
			Tags:  ftype.Tag,
			Alt:   strings.ToUpper(ftype.Tag.Get("envconfig")),
			Key:   keyFor(ftype, prefix),
		}
		sf := &specField{info: info, field: field}

		if field.Kind() == reflect.Struct {
			// honor Decode if present
			if decoderFrom(field) == nil && setterFrom(field) == nil && textUnmarshaler(field) == nil && binaryUnmarshaler(field) == nil {
				sf.child = buildNode(field, innerPrefix)
			}
		}
		node.fields = append(node.fields, sf)
	}
	return node
}

// derefStructPtr allocates nil pointers to structs (creating zero
// instances) and unwraps pointer chains. Nil pointers to non-struct
// values are left alone.
func derefStructPtr(f reflect.Value) reflect.Value {
	for f.Kind() == reflect.Ptr {
		if f.IsNil() {
			if f.Type().Elem().Kind() != reflect.Struct {
				break
			}
			f.Set(reflect.New(f.Type().Elem()))
		}
		f = f.Elem()
	}
	return f
}

// baseKey returns the upcased, pre-split-word environment variable name of
// a field without the parent prefix.
func baseKey(ftype reflect.StructField) string {
	key := ftype.Name
	if isTrue(ftype.Tag.Get("split_words")) {
		words := gatherRegexp.FindAllStringSubmatch(ftype.Name, -1)
		if len(words) > 0 {
			var name []string
			for _, w := range words {
				if m := acronymRegexp.FindStringSubmatch(w[0]); len(m) == 3 {
					name = append(name, m[1], m[2])
				} else {
					name = append(name, w[0])
				}
			}
			key = strings.Join(name, "_")
		}
	}
	if alt := strings.ToUpper(ftype.Tag.Get("envconfig")); alt != "" {
		key = alt
	}
	return strings.ToUpper(key)
}

// keyFor computes the environment variable name for a struct field.
func keyFor(ftype reflect.StructField, prefix string) string {
	key := baseKey(ftype)
	if prefix != "" {
		key = fmt.Sprintf("%s_%s", prefix, key)
	}
	return strings.ToUpper(key)
}

// gatherInfo gathers information about the specified struct
func gatherInfo(prefix string, spec interface{}) ([]varInfo, error) {
	node, err := buildSpec(prefix, spec)
	if err != nil {
		return nil, err
	}

	infos := make([]varInfo, 0)
	var walk func(*specNode)
	walk = func(n *specNode) {
		for _, sf := range n.fields {
			if sf.child != nil {
				walk(sf.child)
			} else {
				infos = append(infos, sf.info)
			}
		}
	}
	walk(node)
	return infos, nil
}

// CheckDisallowed checks that no environment variables with the prefix are set
// that we don't know how or want to parse. This is likely only meaningful with
// a non-empty prefix.
func CheckDisallowed(prefix string, spec interface{}) error {
	infos, err := gatherInfo(prefix, spec)
	if err != nil {
		return err
	}

	vars := make(map[string]struct{})
	for _, info := range infos {
		vars[info.Key] = struct{}{}
	}

	if prefix != "" {
		prefix = strings.ToUpper(prefix) + "_"
	}

	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, prefix) {
			continue
		}
		v := strings.SplitN(env, "=", 2)[0]
		if _, found := vars[v]; !found {
			return fmt.Errorf("unknown environment variable %s", v)
		}
	}

	return nil
}

// Process populates the specified struct based on environment variables
func Process(prefix string, spec interface{}) error {
	root, err := buildSpec(prefix, spec)
	if err != nil {
		return err
	}

	var errs []error

	// Phase one: convert every field in the whole tree, including
	// defaults and custom Decoders. A subtree whose conversion fails is
	// marked so that validation of its ancestors can be skipped; errors
	// from independent sibling subtrees are still collected.
	var convert func(*specNode)
	convert = func(n *specNode) {
		for _, sf := range n.fields {
			if sf.child != nil {
				convert(sf.child)
				if sf.child.failed {
					n.failed = true
				}
				continue
			}

			if err := convertField(sf); err != nil {
				sf.failed = true
				n.failed = true
				errs = append(errs, err)
			}
		}
	}
	convert(root)

	// Phase two: validate inside out. Nested instances are validated
	// before their parents, and each instance is visited exactly once.
	var validate func(n *specNode, path string)
	validate = func(n *specNode, path string) {
		for _, sf := range n.fields {
			if sf.child != nil {
				validate(sf.child, joinPath(path, sf.info.Name))
			} else if v := validatorFrom(sf.field); v != nil && !sf.failed {
				if err := v.Validate(); err != nil {
					errs = append(errs, withPath(err, joinPath(path, sf.info.Name)))
				}
			}
		}

		if !n.failed {
			if v := validatorFrom(n.value); v != nil {
				if err := v.Validate(); err != nil {
					errs = append(errs, withPath(err, path))
				}
			}
		}
	}
	validate(root, "")

	return joinErrors(errs)
}

// convertField resolves and converts a single leaf field.
func convertField(sf *specField) error {
	info := sf.info

	// `os.Getenv` cannot differentiate between an explicitly set empty value
	// and an unset value. `os.LookupEnv` is preferred to `syscall.Getenv`,
	// but it is only available in go1.5 or newer. We're using Go build tags
	// here to use os.LookupEnv for >=go1.5
	value, ok := lookupEnv(info.Key)
	if !ok && info.Alt != "" {
		value, ok = lookupEnv(info.Alt)
	}

	def := info.Tags.Get("default")
	if def != "" && !ok {
		value = def
	}

	req := info.Tags.Get("required")
	if !ok && def == "" {
		if isTrue(req) {
			key := info.Key
			if info.Alt != "" {
				key = info.Alt
			}
			return fmt.Errorf("required key %s missing value", key)
		}
		return nil
	}

	if err := processField(value, info.Field); err != nil {
		return &ParseError{
			KeyName:   info.Key,
			FieldName: info.Name,
			TypeName:  info.Field.Type().String(),
			Value:     value,
			Err:       err,
		}
	}
	return nil
}

// joinPath appends a Go struct field name to a dotted struct path.
func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// withPath annotates err with the struct path of the instance that
// reported it. Errors at the root (empty path) are returned unchanged.
func withPath(err error, path string) error {
	if path == "" {
		return err
	}
	return fmt.Errorf("%s: %w", path, err)
}

// joinErrors returns nil for no errors, the single error unchanged so
// concrete types such as *ParseError stay inspectable, or all errors
// joined together.
func joinErrors(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return errors.Join(errs...)
	}
}

// MustProcess is the same as Process but panics if an error occurs
func MustProcess(prefix string, spec interface{}) {
	if err := Process(prefix, spec); err != nil {
		panic(err)
	}
}

func processField(value string, field reflect.Value) error {
	typ := field.Type()

	decoder := decoderFrom(field)
	if decoder != nil {
		return decoder.Decode(value)
	}
	// look for Set method if Decode not defined
	setter := setterFrom(field)
	if setter != nil {
		return setter.Set(value)
	}

	if t := textUnmarshaler(field); t != nil {
		return t.UnmarshalText([]byte(value))
	}

	if b := binaryUnmarshaler(field); b != nil {
		return b.UnmarshalBinary([]byte(value))
	}

	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
		if field.IsNil() {
			field.Set(reflect.New(typ))
		}
		field = field.Elem()
	}

	switch typ.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var (
			val int64
			err error
		)
		if field.Kind() == reflect.Int64 && typ.PkgPath() == "time" && typ.Name() == "Duration" {
			var d time.Duration
			d, err = time.ParseDuration(value)
			val = int64(d)
		} else {
			val, err = strconv.ParseInt(value, 0, typ.Bits())
		}
		if err != nil {
			return err
		}

		field.SetInt(val)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		val, err := strconv.ParseUint(value, 0, typ.Bits())
		if err != nil {
			return err
		}
		field.SetUint(val)
	case reflect.Bool:
		val, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(val)
	case reflect.Float32, reflect.Float64:
		val, err := strconv.ParseFloat(value, typ.Bits())
		if err != nil {
			return err
		}
		field.SetFloat(val)
	case reflect.Slice:
		sl := reflect.MakeSlice(typ, 0, 0)
		if typ.Elem().Kind() == reflect.Uint8 {
			sl = reflect.ValueOf([]byte(value))
		} else if strings.TrimSpace(value) != "" {
			vals := strings.Split(value, ",")
			sl = reflect.MakeSlice(typ, len(vals), len(vals))
			for i, val := range vals {
				err := processField(val, sl.Index(i))
				if err != nil {
					return err
				}
			}
		}
		field.Set(sl)
	case reflect.Map:
		mp := reflect.MakeMap(typ)
		if strings.TrimSpace(value) != "" {
			pairs := strings.Split(value, ",")
			for _, pair := range pairs {
				kvpair := strings.Split(pair, ":")
				if len(kvpair) != 2 {
					return fmt.Errorf("invalid map item: %q", pair)
				}
				k := reflect.New(typ.Key()).Elem()
				err := processField(kvpair[0], k)
				if err != nil {
					return err
				}
				v := reflect.New(typ.Elem()).Elem()
				err = processField(kvpair[1], v)
				if err != nil {
					return err
				}
				mp.SetMapIndex(k, v)
			}
		}
		field.Set(mp)
	}

	return nil
}

func interfaceFrom(field reflect.Value, fn func(interface{}, *bool)) {
	// it may be impossible for a struct field to fail this check
	if !field.CanInterface() {
		return
	}
	var ok bool
	fn(field.Interface(), &ok)
	if !ok && field.CanAddr() {
		fn(field.Addr().Interface(), &ok)
	}
}

func decoderFrom(field reflect.Value) (d Decoder) {
	interfaceFrom(field, func(v interface{}, ok *bool) { d, *ok = v.(Decoder) })
	return d
}

func setterFrom(field reflect.Value) (s Setter) {
	interfaceFrom(field, func(v interface{}, ok *bool) { s, *ok = v.(Setter) })
	return s
}

func textUnmarshaler(field reflect.Value) (t encoding.TextUnmarshaler) {
	interfaceFrom(field, func(v interface{}, ok *bool) { t, *ok = v.(encoding.TextUnmarshaler) })
	return t
}

func binaryUnmarshaler(field reflect.Value) (b encoding.BinaryUnmarshaler) {
	interfaceFrom(field, func(v interface{}, ok *bool) { b, *ok = v.(encoding.BinaryUnmarshaler) })
	return b
}

func validatorFrom(field reflect.Value) (v Validator) {
	if field.Kind() == reflect.Ptr && field.IsNil() {
		return nil
	}
	interfaceFrom(field, func(x interface{}, ok *bool) { v, *ok = x.(Validator) })
	return v
}

func isTrue(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}
