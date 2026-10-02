package httphead

import (
	"bytes"
	"errors"
	"io"
)

// ParamKind represents how a parameter value appeared in the parsed input.
type ParamKind byte

const (
	// ParamBare means that the parameter has no value at all
	// (e.g. "foo" in "a;foo").
	ParamBare ParamKind = iota

	// ParamEmpty means that the parameter value was an empty quoted-string
	// (e.g. foo="" in "a;foo="").
	ParamEmpty

	// ParamToken means that the parameter value was a regular token or
	// non-empty quoted-string (e.g. "bar" in "a;foo=bar").
	ParamToken
)

// ParamNoValue is an alias for ParamBare.
const ParamNoValue = ParamBare

// String represents kind as a string.
func (k ParamKind) String() string {
	switch k {
	case ParamBare:
		return "bare"
	case ParamEmpty:
		return "empty"
	case ParamToken:
		return "token"
	default:
		return "unknown"
	}
}

// Parameter is a single occurrence of an option parameter.
//
// Unlike the plain Parameters type it preserves the order of every parameter
// occurrence and remembers the exact shape of its value:
//   - bare parameter without a value has Kind == ParamBare and Value == nil;
//   - empty quoted-string foo="" has Kind == ParamEmpty and empty Value;
//   - ordinary value foo=bar or foo="bar" has Kind == ParamToken.
type Parameter struct {
	Name  []byte
	Value []byte
	Kind  ParamKind
}

// ParamView is an ordered view over option parameters.
//
// It does not fold parameters with the same name: every parameter occurrence
// is kept in the order it was parsed. Use Project to get an explicit
// single-value projection over the occurrences.
type ParamView struct {
	pos   int
	bytes int
	arr   [8]Parameter
	dyn   []Parameter
}

// Add appends a parameter of the given kind to the view.
func (p *ParamView) Add(name, value []byte, kind ParamKind) {
	p.bytes += len(name) + len(value)

	entry := Parameter{name, value, kind}
	if p.pos < len(p.arr) {
		p.arr[p.pos] = entry
		p.pos++
		return
	}

	if p.dyn == nil {
		p.dyn = make([]Parameter, len(p.arr), len(p.arr)+1)
		copy(p.dyn, p.arr[:])
	}
	p.dyn = append(p.dyn, entry)
}

// Len returns number of parameter occurrences.
func (p *ParamView) Len() int { return p.pos + len(p.dyn) }

func (p *ParamView) entries() []Parameter {
	if p.dyn != nil {
		return p.dyn
	}
	return p.arr[:p.pos]
}

// All returns all parameter occurrences in their original order.
//
// The returned slice references view's underlying storage and must not be
// modified.
func (p *ParamView) All() []Parameter { return p.entries() }

// ForEach iterates over every parameter occurrence in order and calls cb.
func (p *ParamView) ForEach(cb func(Parameter) bool) {
	for _, v := range p.entries() {
		if !cb(v) {
			return
		}
	}
}

// Values returns every value occurrence of the parameter named name in order.
// Comparison is case-sensitive unless fold is true.
func (p *ParamView) Values(name []byte, fold bool) []Parameter {
	var ret []Parameter
	for _, v := range p.entries() {
		if nameEqual(v.Name, name, fold) {
			ret = append(ret, v)
		}
	}
	return ret
}

// Has reports whether the parameter named name exists.
// Comparison is case-sensitive unless fold is true.
func (p *ParamView) Has(name []byte, fold bool) bool {
	for _, v := range p.entries() {
		if nameEqual(v.Name, name, fold) {
			return true
		}
	}
	return false
}

// Size returns number of bytes that needed to copy p.
func (p *ParamView) Size() int { return p.bytes }

// Copy copies all underlying []byte slices into dst and returns new ParamView.
// Note that dst must be at least of p.Size() length.
func (p *ParamView) Copy(dst []byte) (ParamView, []byte) {
	ret := ParamView{
		pos:   p.pos,
		bytes: p.bytes,
	}
	src := p.entries()
	out := make([]Parameter, len(src))
	for i, v := range src {
		n := copy(dst, v.Name)
		out[i].Name = dst[:n]
		dst = dst[n:]

		if v.Kind == ParamBare {
			out[i].Value = nil
		} else {
			m := copy(dst, v.Value)
			out[i].Value = dst[:m]
			dst = dst[m:]
		}
		out[i].Kind = v.Kind
	}
	if p.dyn != nil {
		ret.dyn = out
	} else {
		copy(ret.arr[:], out)
	}
	return ret, dst
}

// Clone returns a deep copy of p that does not reference any external buffer
// (such as the original parsed input).
func (p *ParamView) Clone() ParamView {
	ret, _ := p.Copy(make([]byte, p.bytes))
	return ret
}

// Equal reports whether p is equal to b, including occurrence order and
// value kinds.
func (p ParamView) Equal(b ParamView) bool {
	ad, bd := p.entries(), b.entries()
	if len(ad) != len(bd) {
		return false
	}
	for i := range ad {
		if ad[i].Kind != bd[i].Kind ||
			!bytes.Equal(ad[i].Name, bd[i].Name) ||
			!bytes.Equal(ad[i].Value, bd[i].Value) {
			return false
		}
	}
	return true
}

// String represents parameters as a string.
func (p *ParamView) String() string {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, v := range p.entries() {
		if i > 0 {
			buf.WriteByte(' ')
		}
		buf.Write(v.Name)
		switch v.Kind {
		case ParamBare:
			// nothing
		case ParamEmpty:
			buf.WriteString(`=""`)
		default:
			buf.WriteByte(':')
			buf.Write(v.Value)
		}
	}
	buf.WriteByte(']')
	return buf.String()
}

// WriteTo serializes parameters back into the
//
//	param = token [ "=" (token | quoted-string) ]
//
// form. Empty quoted values are written as foo="" and values containing
// non-token characters (such as semicolon) are quoted and escaped, so a quoted
// semicolon can never be parsed again as a parameter separator.
func (p *ParamView) WriteTo(w io.Writer) (int64, error) {
	bw := writer{w: w}
	for _, v := range p.entries() {
		bw.write(semicolon)
		writeTokenSanitized(&bw, v.Name)
		switch v.Kind {
		case ParamBare:
			// nothing
		case ParamEmpty:
			bw.write(equality)
			bw.write(quote)
			bw.write(quote)
		default:
			bw.write(equality)
			writeTokenSanitized(&bw, v.Value)
		}
	}
	n, err := bw.result()
	return int64(n), err
}

// DuplicatePolicy encodes how Project handles repeated parameter names.
type DuplicatePolicy byte

const (
	// DuplicateReject causes Project to return an error when a parameter name
	// occurs more than once.
	DuplicateReject DuplicatePolicy = iota

	// DuplicateFirst causes Project to keep the first occurrence of a repeated
	// parameter name.
	DuplicateFirst

	// DuplicateLast causes Project to keep the last occurrence of a repeated
	// parameter name.
	DuplicateLast
)

// RejectDuplicates, FirstWins and LastWins are aliases for the
// DuplicatePolicy values.
const (
	RejectDuplicates = DuplicateReject
	FirstWins        = DuplicateFirst
	LastWins         = DuplicateLast
)

// String represents policy as a string.
func (d DuplicatePolicy) String() string {
	switch d {
	case DuplicateReject:
		return "reject"
	case DuplicateFirst:
		return "first"
	case DuplicateLast:
		return "last"
	default:
		return "unknown"
	}
}

// ProjectOptions configures the single-value projection of a ParamView.
type ProjectOptions struct {
	// Duplicate selects how repeated parameter names are handled.
	// Zero value is DuplicateReject.
	Duplicate DuplicatePolicy

	// FoldName makes parameter name comparison case-insensitive.
	FoldName bool
}

// SingleParams is a single-value projection of a ParamView.
type SingleParams struct {
	params []Parameter
	fold   bool
}

// Project collapses parameter occurrences into at most one value per name.
//
// It returns an error wrapping ErrDuplicateParameter when the configured
// Duplicate policy rejects a repeated name.
func (p *ParamView) Project(opts ProjectOptions) (*SingleParams, error) {
	ret := &SingleParams{fold: opts.FoldName}
	for _, v := range p.entries() {
		if i := ret.indexOf(v.Name); i >= 0 {
			switch opts.Duplicate {
			case DuplicateFirst:
				continue
			case DuplicateLast:
				ret.params[i] = v
				continue
			default:
				return nil, &DuplicateParameterError{Name: append([]byte(nil), v.Name...)}
			}
		}
		ret.params = append(ret.params, v)
	}
	return ret, nil
}

func (s *SingleParams) indexOf(name []byte) int {
	for i, v := range s.params {
		if nameEqual(v.Name, name, s.fold) {
			return i
		}
	}
	return -1
}

// Len returns number of distinct parameter names.
func (s *SingleParams) Len() int { return len(s.params) }

// Get returns the projected value by name and a flag about its existence.
// Comparison honors the FoldName option of the projection.
func (s *SingleParams) Get(name []byte) (Parameter, bool) {
	if i := s.indexOf(name); i >= 0 {
		return s.params[i], true
	}
	return Parameter{}, false
}

// ForEach iterates over projected parameters in first-occurrence order.
func (s *SingleParams) ForEach(cb func(Parameter) bool) {
	for _, v := range s.params {
		if !cb(v) {
			return
		}
	}
}

// All returns projected parameters in first-occurrence order.
// The returned slice must not be modified.
func (s *SingleParams) All() []Parameter { return s.params }

// ErrDuplicateParameter reports that a parameter name occurred more than once
// while projecting with DuplicateReject policy.
var ErrDuplicateParameter = errors.New("duplicate parameter")

// DuplicateParameterError wraps ErrDuplicateParameter and carries the repeated
// parameter name.
type DuplicateParameterError struct {
	Name []byte
}

// Error implements the error interface.
func (e *DuplicateParameterError) Error() string {
	return "httphead: duplicate parameter: " + string(e.Name)
}

// Unwrap exposes ErrDuplicateParameter for errors.Is/As.
func (e *DuplicateParameterError) Unwrap() error { return ErrDuplicateParameter }

func nameEqual(a, b []byte, fold bool) bool {
	if fold {
		return bytes.EqualFold(a, b)
	}
	return bytes.Equal(a, b)
}

// ViewOption is an option together with its ordered parameter view.
type ViewOption struct {
	Name   []byte
	Params ParamView
}

// Size returns number of bytes need to be allocated for use in Copy.
func (o ViewOption) Size() int {
	return len(o.Name) + o.Params.bytes
}

// Copy copies all underlying []byte slices into dst and returns new ViewOption.
// Note that dst must be at least of o.Size() length.
func (o ViewOption) Copy(dst []byte) ViewOption {
	n := copy(dst, o.Name)
	o.Name = dst[:n]
	o.Params, _ = o.Params.Copy(dst[n:])
	return o
}

// Clone returns a deep copy of o that does not reference the input buffer.
func (o ViewOption) Clone() ViewOption {
	return o.Copy(make([]byte, o.Size()))
}

// Equal reports whether o is equal to b.
func (o ViewOption) Equal(b ViewOption) bool {
	return bytes.Equal(o.Name, b.Name) && o.Params.Equal(b.Params)
}

// String represents view option as a string.
func (o ViewOption) String() string {
	return "{" + string(o.Name) + " " + o.Params.String() + "}"
}

// Option converts the view option into the legacy Option type, keeping every
// parameter occurrence (Parameters does not record value kinds).
func (o ViewOption) Option() Option {
	ret := Option{Name: o.Name}
	for _, v := range o.Params.entries() {
		ret.Parameters.Set(v.Name, v.Value)
	}
	return ret
}

// WriteTo serializes the option (name followed by its parameters) to w.
func (o ViewOption) WriteTo(w io.Writer) (int64, error) {
	bw := writer{w: w}
	writeTokenSanitized(&bw, o.Name)
	n, err := bw.result()
	if err != nil {
		return int64(n), err
	}
	m, err := o.Params.WriteTo(w)
	return int64(n) + m, err
}

// ParseConfig configures ParseViewOptions.
type ParseConfig struct {
	// Copy makes returned options copy the parsed bytes instead of
	// referencing the input slice.
	Copy bool
}

// ParseViewOptions parses header options preserving every parameter occurrence
// and its value kind. It returns the parsed options and a flag of successful
// (wellformed input) parsing.
//
// Unless cfg.Copy is true the returned options contain sub-slices of data:
// mutation of data mutates the result, exactly like ParseOptions does.
func ParseViewOptions(data []byte, cfg ParseConfig) ([]ViewOption, bool) {
	var options []ViewOption
	var i int
	index := -1
	ok := scanOptions(data, func(idx int, name, attr, val []byte, quoted bool) Control {
		if idx != index {
			index = idx
			i = len(options)
			options = append(options, ViewOption{Name: name})
		}
		if attr != nil {
			options[i].Params.Add(attr, val, paramKind(quoted, val))
		}
		return ControlContinue
	})
	if cfg.Copy && ok {
		for i := range options {
			options[i] = options[i].Clone()
		}
	}
	return options, ok
}

// WriteViewOptions writes view options to dest using the same form as
// WriteOptions.
func WriteViewOptions(dest io.Writer, options []ViewOption) (int, error) {
	w := writer{w: dest}
	for i, opt := range options {
		if i > 0 {
			w.write(comma)
		}
		writeTokenSanitized(&w, opt.Name)
		for _, v := range opt.Params.entries() {
			w.write(semicolon)
			writeTokenSanitized(&w, v.Name)
			switch v.Kind {
			case ParamBare:
				// nothing
			case ParamEmpty:
				w.write(equality)
				w.write(quote)
				w.write(quote)
			default:
				w.write(equality)
				writeTokenSanitized(&w, v.Value)
			}
		}
	}
	return w.result()
}

func paramKind(quoted bool, value []byte) ParamKind {
	switch {
	case value == nil:
		return ParamBare
	case quoted && len(value) == 0:
		return ParamEmpty
	default:
		return ParamToken
	}
}
