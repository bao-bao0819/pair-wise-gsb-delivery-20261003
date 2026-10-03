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
	// Path is the dotted struct path leading to the field that failed to
	// convert, starting at the specification type name. It equals FieldName
	// for top-level fields.
	Path      string
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

// Validator is implemented by configuration values that need to check
// cross-field conditions once their entire subtree has been populated, for
// example verifying a port range together with its mode, or that certificate
// and key paths are both set or both empty.
//
// Validate is invoked after every field of the (sub)tree has been converted,
// starting with the innermost nested structs and walking outwards. Conversion
// happens strictly before validation, so defaults, custom Decoders and nested
// pointers are all finalized before Validate is called.
//
// When conversion or validation fails inside a nested subtree, validation of
// every ancestor that depends on that subtree is skipped; errors from
// independent sibling subtrees are still collected and returned together.
// Each concrete instance has Validate called at most once per Process call.
type Validator interface {
	Validate() error
}

// ValidationError wraps an error returned by a Validator with the full dotted
// path of the (sub)tree that produced it, starting at the specification type
// name.
type ValidationError struct {
	Path string
	Err  error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("envconfig.Process: validating %s: %s", e.Path, e.Err)
}

// Unwrap exposes the underlying validation error.
func (e *ValidationError) Unwrap() error {
	return e.Err
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("envconfig.Process: assigning %[1]s to %[2]s: converting '%[3]s' to type %[4]s. details: %[5]s", e.KeyName, e.Path, e.Value, e.TypeName, e.Err)
}

// varInfo maintains information about the configuration variable
type varInfo struct {
	Name  string
	Alt   string
	Key   string
	Field reflect.Value
	Tags  reflect.StructTag
}

// GatherInfo gathers information about the specified struct
func gatherInfo(prefix string, spec interface{}) ([]varInfo, error) {
	s := reflect.ValueOf(spec)

	if s.Kind() != reflect.Ptr {
		return nil, ErrInvalidSpecification
	}
	s = s.Elem()
	if s.Kind() != reflect.Struct {
		return nil, ErrInvalidSpecification
	}
	typeOfSpec := s.Type()

	// over allocate an info array, we will extend if needed later
	infos := make([]varInfo, 0, s.NumField())
	for i := 0; i < s.NumField(); i++ {
		f := s.Field(i)
		ftype := typeOfSpec.Field(i)
		if !f.CanSet() || isTrue(ftype.Tag.Get("ignored")) {
			continue
		}

		for f.Kind() == reflect.Ptr {
			if f.IsNil() {
				if f.Type().Elem().Kind() != reflect.Struct {
					// nil pointer to a non-struct: leave it alone
					break
				}
				// nil pointer to struct: create a zero instance
				f.Set(reflect.New(f.Type().Elem()))
			}
			f = f.Elem()
		}

		// Capture information about the config variable
		info := varInfo{
			Name:  ftype.Name,
			Field: f,
			Tags:  ftype.Tag,
			Alt:   strings.ToUpper(ftype.Tag.Get("envconfig")),
		}

		// Default to the field name as the env var name (will be upcased)
		info.Key = info.Name

		// Best effort to un-pick camel casing as separate words
		if isTrue(ftype.Tag.Get("split_words")) {
			words := gatherRegexp.FindAllStringSubmatch(ftype.Name, -1)
			if len(words) > 0 {
				var name []string
				for _, words := range words {
					if m := acronymRegexp.FindStringSubmatch(words[0]); len(m) == 3 {
						name = append(name, m[1], m[2])
					} else {
						name = append(name, words[0])
					}
				}

				info.Key = strings.Join(name, "_")
			}
		}
		if info.Alt != "" {
			info.Key = info.Alt
		}
		if prefix != "" {
			info.Key = fmt.Sprintf("%s_%s", prefix, info.Key)
		}
		info.Key = strings.ToUpper(info.Key)
		infos = append(infos, info)

		if f.Kind() == reflect.Struct {
			// honor Decode if present
			if decoderFrom(f) == nil && setterFrom(f) == nil && textUnmarshaler(f) == nil && binaryUnmarshaler(f) == nil {
				innerPrefix := prefix
				if !ftype.Anonymous {
					innerPrefix = info.Key
				}

				embeddedPtr := f.Addr().Interface()
				embeddedInfos, err := gatherInfo(innerPrefix, embeddedPtr)
				if err != nil {
					return nil, err
				}
				infos = append(infos[:len(infos)-1], embeddedInfos...)

				continue
			}
		}
	}
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
	s := reflect.ValueOf(spec)

	if s.Kind() != reflect.Ptr {
		return ErrInvalidSpecification
	}
	s = s.Elem()
	if s.Kind() != reflect.Struct {
		return ErrInvalidSpecification
	}

	p := &processor{
		prefix:     prefix,
		root:       s.Type().Name(),
		aliases:    make(map[uintptr]struct{}),
		ptrRefs:    make(map[uintptr]int),
		directRefs: make(map[uintptr]struct{}),
	}
	if p.root == "" {
		p.root = "spec"
	}

	// Phase 1: convert every field in the tree. Nodes are recorded in
	// post-order so phase 2 can validate from the innermost subtree outwards.
	p.convertStruct(s, prefix, p.root, nil)

	for addr, n := range p.ptrRefs {
		if _, isDirect := p.directRefs[addr]; isDirect {
			n++
		}
		if n > 1 {
			p.aliases[addr] = struct{}{}
		}
	}

	// Phase 2: validate converted subtrees inside-out. Subtrees whose
	// conversion failed are already marked and skipped along with every
	// ancestor that depends on them.
	p.validateNodes()

	return joinErrors(p.errs)
}

// vnode records a struct (or a leaf value implementing Validator) that needs
// to be considered during the validation phase. Nodes are appended in
// post-order: all descendants precede their ancestors.
type vnode struct {
	field  reflect.Value
	path   string
	parent *vnode

	// failed is true once conversion or validation of this subtree fails.
	// It propagates up the ancestor chain so dependent parents are skipped,
	// without affecting independent sibling subtrees.
	failed bool
}

// processor drives the two-phase convert-then-validate walk of a spec.
type processor struct {
	prefix string
	root   string

	// nodes is the post-order list of validation candidates.
	nodes []*vnode

	// errs collects conversion and validation errors from sibling subtrees.
	errs []error

	// ptrRefs counts pointer-to-struct fields that already point at an
	// instance before conversion. Nil struct pointers are allocated by the
	// walk itself and are never shared, so they are not counted.
	ptrRefs map[uintptr]int

	// directRefs marks allocations that appear directly as addressable struct
	// fields in addition to any pointer edges targeting them.
	directRefs map[uintptr]struct{}

	// aliases lists targets referenced by two or more such pointer fields,
	// i.e. genuinely shared instances. A parent struct and its first embedded
	// field never qualify even though they share a base address.
	aliases map[uintptr]struct{}

	// validated records shared instances that have already run Validate.
	validated map[uintptr]struct{}
}

// convertStruct converts every field of the struct v and records validation
// nodes. It mirrors the field traversal performed by gatherInfo, including
// pointer unwrapping, embedded struct promotion and env key derivation.
func (p *processor) convertStruct(v reflect.Value, prefix, path string, parent *vnode) {
	node := &vnode{field: v, path: path, parent: parent}
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		f := v.Field(i)
		ft := t.Field(i)
		if !f.CanSet() || isTrue(ft.Tag.Get("ignored")) {
			continue
		}

		wasNonNilStructPtr := f.Kind() == reflect.Ptr &&
			!f.IsNil() && f.Type().Elem().Kind() == reflect.Struct
		for f.Kind() == reflect.Ptr {
			if f.IsNil() {
				if f.Type().Elem().Kind() != reflect.Struct {
					// nil pointer to a non-struct: leave it alone
					break
				}
				// nil pointer to struct: create a zero instance
				f.Set(reflect.New(f.Type().Elem()))
			}
			f = f.Elem()
		}

		if f.Kind() == reflect.Struct && f.CanAddr() {
			p.directRefs[f.Addr().Pointer()] = struct{}{}
		}
		if wasNonNilStructPtr {
			// A pre-existing struct pointer may alias another field; nil
			// pointers allocated by the walk itself can never alias.
			p.ptrRefs[f.Addr().Pointer()]++
		}

		fieldPath := joinPath(path, ft.Name)

		// Recurse into nested structs that do not handle their own decoding.
		if f.Kind() == reflect.Struct &&
			decoderFrom(f) == nil && setterFrom(f) == nil &&
			textUnmarshaler(f) == nil && binaryUnmarshaler(f) == nil {
			innerPrefix, _ := fieldKey(ft, prefix)
			if ft.Anonymous {
				// Promoted embedded fields share the parent prefix.
				innerPrefix = prefix
			}
			p.convertStruct(f, innerPrefix, fieldPath, node)
			continue
		}

		key, alt := fieldKey(ft, prefix)

		// `os.Getenv` cannot differentiate between an explicitly set empty
		// value and an unset value. `os.LookupEnv` is preferred to
		// `syscall.Getenv`, but it is only available in go1.5 or newer.
		value, ok := lookupEnv(key)
		if !ok && alt != "" {
			value, ok = lookupEnv(alt)
		}

		def := ft.Tag.Get("default")
		if def != "" && !ok {
			value = def
			ok = true
		}

		if !ok {
			if isTrue(ft.Tag.Get("required")) {
				reqKey := key
				if alt != "" {
					reqKey = alt
				}
				p.fail(node, &ParseError{
					Path:      fieldPath,
					KeyName:   key,
					FieldName: ft.Name,
					TypeName:  f.Type().String(),
					Value:     value,
					Err:       fmt.Errorf("required key %s missing value", reqKey),
				})
			}
			continue
		}

		if err := processField(value, f); err != nil {
			p.fail(node, &ParseError{
				Path:      fieldPath,
				KeyName:   key,
				FieldName: ft.Name,
				TypeName:  f.Type().String(),
				Value:     value,
				Err:       err,
			})
			continue
		}

		// A converted leaf (including custom Decoder/Setter values) may want
		// to validate itself; record it as a child candidate of this struct.
		if validatorFrom(f) != nil {
			p.nodes = append(p.nodes, &vnode{
				field:  f,
				path:   fieldPath,
				parent: node,
			})
		}
	}

	p.nodes = append(p.nodes, node)
}

// validateNodes walks the post-order node list and invokes Validator on each
// converted instance exactly once, skipping subtrees marked as failed.
func (p *processor) validateNodes() {
	for _, n := range p.nodes {
		if n.failed {
			continue
		}

		v := validatorFrom(n.field)
		if v == nil {
			continue
		}

		// Only genuinely shared instances (reachable through two field
		// paths) are deduplicated, so each concrete instance validates at
		// most once while ordinary parent/embedded structs still each run.
		if key, ok := validatorKey(n.field); ok {
			if _, shared := p.aliases[key]; shared {
				if _, done := p.validated[key]; done {
					continue
				}
				if p.validated == nil {
					p.validated = make(map[uintptr]struct{})
				}
				p.validated[key] = struct{}{}
			}
		}

		if err := v.Validate(); err != nil {
			p.fail(n, &ValidationError{Path: n.path, Err: err})
		}
	}
}

// validatorKey returns the identity key of the receiver used when invoking
// Validate: the pointer itself for a pointer-receiver Validator, and no key
// for a value-receiver Validator (whose receiver is a fresh copy).
func validatorKey(field reflect.Value) (uintptr, bool) {
	if field.Kind() == reflect.Ptr {
		return field.Pointer(), true
	}
	validatorType := reflect.TypeOf((*Validator)(nil)).Elem()
	vt := field.Type()
	if vt.Implements(validatorType) {
		return 0, false
	}
	if field.CanAddr() && reflect.PtrTo(vt).Implements(validatorType) {
		return field.Addr().Pointer(), true
	}
	return 0, false
}

// fail records an error and marks the subtree rooted at n (along with all of
// its ancestors) as failed, so their validation is skipped. Sibling subtrees
// remain untouched and keep contributing their own errors.
func (p *processor) fail(n *vnode, err error) {
	p.errs = append(p.errs, err)
	for ; n != nil; n = n.parent {
		n.failed = true
	}
}

// fieldKey derives the environment variable key and the uppercase alternate
// name for a field, following the same rules as gatherInfo.
func fieldKey(ft reflect.StructField, prefix string) (key, alt string) {
	alt = strings.ToUpper(ft.Tag.Get("envconfig"))

	key = ft.Name
	if isTrue(ft.Tag.Get("split_words")) {
		words := gatherRegexp.FindAllStringSubmatch(ft.Name, -1)
		if len(words) > 0 {
			var name []string
			for _, words := range words {
				if m := acronymRegexp.FindStringSubmatch(words[0]); len(m) == 3 {
					name = append(name, m[1], m[2])
				} else {
					name = append(name, words[0])
				}
			}
			key = strings.Join(name, "_")
		}
	}
	if alt != "" {
		key = alt
	}
	if prefix != "" {
		key = fmt.Sprintf("%s_%s", prefix, key)
	}
	key = strings.ToUpper(key)
	return key, alt
}

// joinErrors returns nil for no errors, the single error unchanged when there
// is only one (preserving concrete error types such as *ParseError), and a
// joined error carrying every sibling error otherwise.
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

func joinPath(base, name string) string {
	if base == "" {
		return name
	}
	return base + "." + name
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

func validatorFrom(field reflect.Value) (v Validator) {
	interfaceFrom(field, func(x interface{}, ok *bool) { v, *ok = x.(Validator) })
	return v
}

func textUnmarshaler(field reflect.Value) (t encoding.TextUnmarshaler) {
	interfaceFrom(field, func(v interface{}, ok *bool) { t, *ok = v.(encoding.TextUnmarshaler) })
	return t
}

func binaryUnmarshaler(field reflect.Value) (b encoding.BinaryUnmarshaler) {
	interfaceFrom(field, func(v interface{}, ok *bool) { b, *ok = v.(encoding.BinaryUnmarshaler) })
	return b
}

func isTrue(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}
