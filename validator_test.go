// Copyright (c) 2013 Kelsey Hightower. All rights reserved.
// Use of this source code is governed by the MIT License that can be found in
// the LICENSE file.

package envconfig

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

var validateCallOrder []string

type validateInner struct {
	Value string
}

func (v *validateInner) Validate() error {
	validateCallOrder = append(validateCallOrder, "Inner")
	return nil
}

type validateMiddle struct {
	Inner validateInner
}

func (m *validateMiddle) Validate() error {
	validateCallOrder = append(validateCallOrder, "Middle")
	return nil
}

type validateRoot struct {
	Middle *validateMiddle
}

func (r *validateRoot) Validate() error {
	validateCallOrder = append(validateCallOrder, "Root")
	return nil
}

// TestValidateInsideOutNestedPointers uses nested pointers so the inside-out
// ordering cannot pass merely by coincidental reflection field traversal.
func TestValidateInsideOutNestedPointers(t *testing.T) {
	validateCallOrder = nil
	var s validateRoot
	os.Clearenv()

	if err := Process("", &s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"Inner", "Middle", "Root"}
	if !reflect.DeepEqual(validateCallOrder, want) {
		t.Errorf("validate order = %v, want %v", validateCallOrder, want)
	}
}

type tlsConfig struct {
	Mode     string
	Port     int
	CertFile string
	KeyFile  string
}

func (c *tlsConfig) Validate() error {
	switch c.Mode {
	case "http", "https", "":
	default:
		return errors.New("TLS.Mode: unsupported mode")
	}
	if c.Mode == "https" && c.CertFile == "" {
		return errors.New("TLS.CertFile: required in https mode")
	}
	if (c.CertFile == "") != (c.KeyFile == "") {
		return errors.New("TLS.CertFile/TLS.KeyFile: must be provided together")
	}
	return nil
}

type serviceSpec struct {
	TLS *tlsConfig
}

type validatedDefault struct {
	Mode string `default:"http"`
	Port int    `default:"8080"`
	seen bool
}

func (v *validatedDefault) Validate() error {
	v.seen = true
	if v.Mode != "http" || v.Port != 8080 {
		return errors.New("conversion had not completed")
	}
	return nil
}

func TestValidateRunsAfterDefaultConversion(t *testing.T) {
	var s validatedDefault
	os.Clearenv()
	if err := Process("", &s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.seen {
		t.Error("Validate was not called")
	}
}

type decodeThenValidate struct {
	Value string
}

func (d *decodeThenValidate) Decode(value string) error {
	d.Value = "decoded:" + value
	return nil
}

func (d *decodeThenValidate) Validate() error {
	if d.Value != "decoded:x" {
		return errors.New("Decode did not run before Validate")
	}
	return nil
}

func TestValidateAfterCustomDecoder(t *testing.T) {
	var s struct {
		F *decodeThenValidate
	}
	os.Clearenv()
	os.Setenv("F", "x")
	if err := Process("", &s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.F == nil || s.F.Value != "decoded:x" {
		t.Fatalf("decoder not applied: %+v", s.F)
	}
}

type countedValidator struct {
	calls int
	A     string
	B     string
}

func (c *countedValidator) Validate() error {
	c.calls++
	return nil
}

func TestEachInstanceValidatedOnce(t *testing.T) {
	var s struct {
		One countedValidator
		Two *countedValidator
	}
	os.Clearenv()
	if err := Process("", &s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.One.calls != 1 {
		t.Errorf("One validated %d times, want 1", s.One.calls)
	}
	if s.Two == nil || s.Two.calls != 1 {
		t.Errorf("Two calls = %v, want 1", s.Two)
	}
}

type leafValidator struct {
	Value string
}

func (l *leafValidator) Validate() error {
	return errors.New("leaf validation failed")
}

func TestValidationErrorHasStructPath(t *testing.T) {
	var s struct {
		Server struct {
			TLS *tlsConfig
		}
	}
	os.Clearenv()
	os.Setenv("SERVER_TLS_MODE", "https")

	err := Process("", &s)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Server.TLS:") {
		t.Errorf("error missing full struct path: %v", err)
	}
}

func TestLeafValidationErrorHasPath(t *testing.T) {
	var s struct {
		Field leafValidator
	}
	os.Clearenv()
	err := Process("", &s)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Field: leaf validation failed") {
		t.Errorf("error missing leaf path: %v", err)
	}
}

type rootValidator struct {
	Called bool
	Port   int
}

func (r *rootValidator) Validate() error {
	r.Called = true
	return errors.New("root validated despite failed child")
}

// TestFailedSubtreeSkipsParentValidation: when a subtree fails to convert,
// its own and every ancestor Validate must be skipped.
func TestFailedSubtreeSkipsParentValidation(t *testing.T) {
	var s struct {
		Root rootValidator
	}
	os.Clearenv()
	os.Setenv("ROOT_PORT", "not-a-number")

	err := Process("", &s)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if s.Root.Called {
		t.Errorf("parent Validate called after child conversion failure: %v", err)
	}
	if _, ok := err.(*ParseError); !ok {
		t.Errorf("expected single *ParseError, got %T: %v", err, err)
	}
}

type siblingValidator struct {
	name string
}

func (s *siblingValidator) Validate() error {
	return errors.New(s.name + " invalid")
}

// TestIndependentSiblingErrorsAggregated: a broken subtree skips its parents
// but errors from independent sibling subtrees are still reported together.
func TestIndependentSiblingErrorsAggregated(t *testing.T) {
	var s struct {
		Bad  *rootValidator
		Good *siblingValidator
	}
	s.Good = &siblingValidator{name: "Good"}
	s.Bad = &rootValidator{}
	os.Clearenv()
	os.Setenv("BAD_PORT", "nope")

	err := Process("", &s)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "converting") {
		t.Errorf("missing conversion error: %v", err)
	}
	if !strings.Contains(msg, "Good invalid") {
		t.Errorf("sibling validation error not aggregated: %v", err)
	}
	if strings.Contains(msg, "root validated despite failed child") {
		t.Errorf("failed subtree parent was validated: %v", err)
	}

	var parseErr *ParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("aggregated error should unwrap to *ParseError: %v", err)
	}
}

func TestCrossFieldPortModeAndCertPair(t *testing.T) {
	var s serviceSpec
	os.Clearenv()
	os.Setenv("TLS_MODE", "https")
	os.Setenv("TLS_CERTFILE", "/etc/ssl/server.crt")

	err := Process("", &s)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "TLS:") {
		t.Errorf("error missing TLS struct path: %v", err)
	}
	if !strings.Contains(err.Error(), "must be provided together") {
		t.Errorf("expected cert/key pair error: %v", err)
	}

	// A valid https config passes.
	os.Setenv("TLS_KEYFILE", "/etc/ssl/server.key")
	if err := Process("", &s); err != nil {
		t.Errorf("unexpected error for valid config: %v", err)
	}
}

type portRangeConfig struct {
	Mode string
	Port int
}

func (c *portRangeConfig) Validate() error {
	switch c.Mode {
	case "http", "https":
	default:
		return errors.New("Mode: must be http or https")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("Port: out of range")
	}
	return nil
}

func TestPortRangeAndModeValidation(t *testing.T) {
	var s struct {
		Listen *portRangeConfig
	}
	os.Clearenv()

	// Bad mode and out of range port: both constraints are observable.
	os.Setenv("LISTEN_MODE", "ftp")
	os.Setenv("LISTEN_PORT", "70000")
	err := Process("", &s)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "Listen:") {
		t.Errorf("error missing Listen struct path: %v", err)
	}

	os.Clearenv()
	os.Setenv("LISTEN_MODE", "https")
	os.Setenv("LISTEN_PORT", "8443")
	if err := Process("", &s); err != nil {
		t.Errorf("unexpected error for valid config: %v", err)
	}
}

type validateDeep struct {
	X string
}

func (v *validateDeep) Validate() error {
	validateCallOrder = append(validateCallOrder, "Deep")
	return nil
}

// TestValidateDeepPointerChain verifies validation order through multiple
// levels of nil-allocated nested pointers.
func TestValidateDeepPointerChain(t *testing.T) {
	validateCallOrder = nil
	var s struct {
		A *struct {
			B *struct {
				C *validateDeep
			}
		}
	}
	os.Clearenv()

	if err := Process("", &s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := validateCallOrder; len(got) != 1 || got[0] != "Deep" {
		t.Errorf("validate order = %v, want [Deep]", got)
	}
	if s.A == nil || s.A.B == nil || s.A.B.C == nil {
		t.Errorf("nested pointers not allocated: %+v", s)
	}
}
