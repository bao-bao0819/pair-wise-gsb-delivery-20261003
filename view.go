package httphead

import "bytes"

// ValueKind encodes the textual form of a parameter value.
type ValueKind int

const (
	// ValueNone reports that parameter has no value at all (e.g. "foo").
	ValueNone ValueKind = iota
	// ValueToken reports that parameter value is an unquoted token
	// (e.g. "foo=bar").
	ValueToken
	// ValueQuoted reports that parameter value was enclosed into the
	// double quotes (e.g. foo="" or foo="bar;baz"). An empty quoted string
	// is represented by an empty value and the ValueQuoted kind, which makes
	// it distinguishable from a parameter without a value (ValueNone).
	ValueQuoted
)

// String represents kind as a string.
func (k ValueKind) String() string {
	switch k {
	case ValueNone:
		return "none"
	case ValueToken:
		return "token"
	case ValueQuoted:
		return "quoted"
	default:
		return "unknown"
	}
}

// Parameter is a single parameter occurrence, as it was present in the parsed
// header value. Unlike key-value pair of the legacy Parameters type, Parameter
// preserves the actual value form and the order of its occurrence.
type Parameter struct {
	Name  []byte
	Value []byte
	Kind  ValueKind
}

// ParametersView is an order preserving view over option parameters.
// It keeps every parameter occurrence, including parameters with the same
// name, exactly in the order they appear in the source data.
//
// The zero value is ready to use. All byte slices are sub-slices of the data
// passed to the parser. Use Clone or Copy to obtain a self-contained copy.
type ParametersView struct {
	pos int
	arr [8]Parameter
	dyn []Parameter
}

// Len returns the number of parameter occurrences.
func (p *ParametersView) Len() int {
	if p.dyn == nil {
		return p.pos
	}
	return len(p.dyn)
}

// At returns i-th parameter occurrence. It panics if i is out of range.
func (p *ParametersView) At(i int) Parameter {
	return p.data()[i]
}

// Set appends a parameter occurrence.
func (p *ParametersView) Set(name, value []byte, kind ValueKind) {
	v := Parameter{name, value, kind}
	if p.pos < len(p.arr) {
		p.arr[p.pos] = v
		p.pos++
		return
	}
	if p.dyn == nil {
		p.dyn = make([]Parameter, len(p.arr), len(p.arr)+1)
		copy(p.dyn, p.arr[:p.pos])
	}
	p.dyn = append(p.dyn, v)
}

// ForEach iterates over parameters in the order of occurrence and calls cb for
// each one. Iteration stops if cb returns false.
func (p *ParametersView) ForEach(cb func(Parameter) bool) {
	for _, v := range p.data() {
		if !cb(v) {
			return
		}
	}
}

// Size returns number of bytes that needed to copy p with Copy.
func (p *ParametersView) Size() int {
	var n int
	for _, v := range p.data() {
		n += len(v.Name) + len(v.Value)
	}
	return n
}

// Copy copies all underlying []byte slices into dst and returns new
// ParametersView that does not share memory with the source.
// Note that dst must be at least of p.Size() length.
func (p *ParametersView) Copy(dst []byte) (ParametersView, []byte) {
	ret := ParametersView{}
	for _, v := range p.data() {
		n := copy(dst, v.Name)
		name := dst[:n]
		dst = dst[n:]
		m := copy(dst, v.Value)
		value := dst[:m]
		dst = dst[m:]
		ret.Set(name, value, v.Kind)
	}
	return ret, dst
}

// Clone returns a deep copy of p that does not depend on any input buffer.
func (p *ParametersView) Clone() ParametersView {
	ret, _ := p.Copy(make([]byte, p.Size()))
	return ret
}

// Parameters converts the view into the legacy Parameters type.
// The result keeps the same occurrence order, but loses information about the
// value form: both missing and empty values become empty byte slices.
func (p *ParametersView) Parameters() Parameters {
	var ret Parameters
	for _, v := range p.data() {
		ret.Set(v.Name, v.Value)
	}
	return ret
}

func (p *ParametersView) data() []Parameter {
	if p.dyn == nil {
		return p.arr[:p.pos]
	}
	return p.dyn
}

// DuplicatePolicy encodes the way of collapsing multiple occurrences of the
// same parameter name into a single value.
type DuplicatePolicy int

const (
	// DuplicateReject causes projection to report a conflict when more than
	// one parameter with the looked up name exists.
	DuplicateReject DuplicatePolicy = iota
	// DuplicateFirst causes projection to select the first occurrence.
	DuplicateFirst
	// DuplicateLast causes projection to select the last occurrence.
	DuplicateLast
)

// ProjectOptions configures single value projection over ParametersView.
type ProjectOptions struct {
	// Duplicates selects the way of handling repeated parameter names.
	Duplicates DuplicatePolicy

	// FoldNames causes parameter names to be compared case insensitively.
	FoldNames bool
}

// Project collapses all occurrences of the parameter named key into a single
// Parameter.
//
// It returns the selected parameter and true when the parameter exists. When
// the DuplicateReject policy is used and the parameter occurs more than once,
// conflict is reported: the returned Parameter is the first occurrence, found
// is true and conflict is true.
func (p *ParametersView) Project(key string, opts ProjectOptions) (param Parameter, found, conflict bool) {
	var count int
	for _, v := range p.data() {
		if !p.match(v.Name, key, opts.FoldNames) {
			continue
		}
		count++
		switch {
		case count == 1:
			param = v
		case opts.Duplicates == DuplicateLast:
			param = v
		}
	}
	if count == 0 {
		return Parameter{}, false, false
	}
	if count > 1 && opts.Duplicates == DuplicateReject {
		return param, true, true
	}
	return param, true, false
}

func (p *ParametersView) match(name []byte, key string, fold bool) bool {
	if fold {
		return len(name) == len(key) && bytes.EqualFold(name, []byte(key))
	}
	return string(name) == key
}

// OptionView is an option with order preserving parameters view.
type OptionView struct {
	Name       []byte
	Parameters ParametersView
}

// Size returns number of bytes that needed to copy o with Copy.
func (o OptionView) Size() int {
	return len(o.Name) + o.Parameters.Size()
}

// Copy copies all underlying []byte slices into dst and returns new
// OptionView that does not share memory with the source.
// Note that dst must be at least of o.Size() length.
func (o OptionView) Copy(dst []byte) OptionView {
	n := copy(dst, o.Name)
	o.Name = dst[:n]
	o.Parameters, _ = o.Parameters.Copy(dst[n:])
	return o
}

// Clone returns a deep copy of o that does not depend on any input buffer.
func (o OptionView) Clone() OptionView {
	return o.Copy(make([]byte, o.Size()))
}

// Option converts the view into the legacy Option type.
func (o OptionView) Option() Option {
	return Option{
		Name:       o.Name,
		Parameters: o.Parameters.Parameters(),
	}
}

// ScanOptionsValues has the same grammar as ScanOptions, but additionally
// reports the actual form of the parameter value via kind argument.
//
// The kind argument is ValueNone for parameters without a value, ValueToken
// for unquoted token values and ValueQuoted for quoted-string values (an empty
// quoted string results in empty non-nil value and ValueQuoted kind).
//
// It returns false if data is malformed.
func ScanOptionsValues(data []byte, it func(index int, option, attribute, value []byte, kind ValueKind) Control) bool {
	lexer := &Scanner{data: data}

	var ok bool
	var state int
	const (
		stateKey = iota
		stateParamBeforeName
		stateParamName
		stateParamBeforeValue
		stateParamValue
	)

	var (
		index             int
		key, param, value []byte
		kind              ValueKind
		mustCall          bool
	)
	for lexer.Next() {
		var (
			call      bool
			growIndex int
		)

		t := lexer.Type()
		v := lexer.Bytes()

		switch t {
		case ItemToken:
			switch state {
			case stateKey, stateParamBeforeName:
				key = v
				state = stateParamBeforeName
				mustCall = true
			case stateParamName:
				param = v
				state = stateParamBeforeValue
				mustCall = true
			case stateParamValue:
				value = v
				kind = ValueToken
				state = stateParamBeforeName
				call = true
			default:
				return false
			}

		case ItemString:
			if state != stateParamValue {
				return false
			}
			value = v
			kind = ValueQuoted
			state = stateParamBeforeName
			call = true

		case ItemSeparator:
			switch {
			case isComma(v) && state == stateKey:
				// Nothing to do.

			case isComma(v) && state == stateParamBeforeName:
				state = stateKey
				// Make call only if we have not called this key yet.
				call = mustCall
				if !call {
					// If we have already called callback with the key
					// that just ended.
					index++
				} else {
					// Else grow the index after calling callback.
					growIndex = 1
				}

			case isComma(v) && state == stateParamBeforeValue:
				state = stateKey
				growIndex = 1
				call = true

			case isSemicolon(v) && state == stateParamBeforeName:
				state = stateParamName

			case isSemicolon(v) && state == stateParamBeforeValue:
				state = stateParamName
				call = true

			case isEquality(v) && state == stateParamBeforeValue:
				state = stateParamValue

			default:
				return false
			}

		default:
			return false
		}

		if call {
			switch it(index, key, param, value, kind) {
			case ControlBreak:
				// User want to stop to parsing parameters.
				return true

			case ControlSkip:
				// User want to skip current param.
				state = stateKey
				lexer.SkipEscaped(',')

			case ControlContinue:
				// User is interested in rest of parameters.
				// Nothing to do.

			default:
				panic("unexpected control value")
			}
			ok = true
			param = nil
			value = nil
			kind = ValueNone
			mustCall = false
			index += growIndex
		}
	}
	if mustCall {
		ok = true
		it(index, key, param, value, kind)
	}

	return ok && !lexer.err
}

// ParseOptionViews parses all header options and appends them to the given
// slice of OptionView.
//
// Unlike ParseOptions, the resulting parameters preserve every occurrence in
// the source order and distinguish missing, quoted and token values.
//
// Note that appended views all consist of subslices of data. That is, mutation
// of data will mutate appended views. Use OptionView.Clone on results to make
// them self-contained.
//
// It returns false if data is malformed.
func ParseOptionViews(data []byte, options []OptionView) ([]OptionView, bool) {
	var i int
	index := -1
	ok := ScanOptionsValues(data, func(idx int, name, attr, val []byte, kind ValueKind) Control {
		if idx != index {
			index = idx
			i = len(options)
			options = append(options, OptionView{Name: name})
		}
		if attr != nil {
			options[i].Parameters.Set(attr, val, kind)
		}
		return ControlContinue
	})
	return options, ok
}
