// Copyright (c) 2013 Kelsey Hightower. All rights reserved.
// Use of this source code is governed by the MIT License that can be found in
// the LICENSE file.

package envconfig

import (
	"errors"
	"os"
	"strings"
	"testing"
)

var errValidateBoom = errors.New("boom")

// validationOrder records the path of every Validate call, in order.
var validationOrder []string

// listenerConfig exercises cross-field conditions: the port range depends on
// the listen mode, and the TLS certificate and key paths appear as a pair.
type listenerConfig struct {
	Mode     string
	Port     int
	CertFile string
	KeyFile  string
}

func (c *listenerConfig) Validate() error {
	validationOrder = append(validationOrder, "listenerConfig")
	switch c.Mode {
	case "http":
		if c.Port < 1 || c.Port > 65535 {
			return errors.New("port out of range")
		}
	case "https":
		if c.Port < 1024 || c.Port > 65535 {
			return errors.New("https port must be in the 1024-65535 range")
		}
	default:
		return errors.New("unknown mode")
	}
	if (c.CertFile == "") != (c.KeyFile == "") {
		return errors.New("cert and key paths must be set together")
	}
	return nil
}

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	os.Clearenv()
	for k, v := range kv {
		if err := os.Setenv(k, v); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidatorAcceptsValidConfig(t *testing.T) {
	var c listenerConfig
	setEnv(t, map[string]string{
		"LISTENER_MODE":     "https",
		"LISTENER_PORT":     "8443",
		"LISTENER_CERTFILE": "/tmp/cert.pem",
		"LISTENER_KEYFILE":  "/tmp/key.pem",
	})
	if err := Process("listener", &c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatorCrossFieldFailureCarriesPath(t *testing.T) {
	var c listenerConfig
	setEnv(t, map[string]string{
		"LISTENER_MODE":     "https",
		"LISTENER_PORT":     "80",
		"LISTENER_CERTFILE": "/tmp/cert.pem",
		"LISTENER_KEYFILE":  "/tmp/key.pem",
	})
	err := Process("listener", &c)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Path != "listenerConfig" {
		t.Errorf("expected path %q, got %q", "listenerConfig", ve.Path)
	}
	if !strings.Contains(ve.Error(), "envconfig.Process: validating listenerConfig:") {
		t.Errorf("unexpected message: %v", ve)
	}
}

func TestValidatorCertificatePathPairing(t *testing.T) {
	var c listenerConfig
	setEnv(t, map[string]string{
		"LISTENER_MODE":     "http",
		"LISTENER_PORT":     "8080",
		"LISTENER_CERTFILE": "/tmp/cert.pem",
	})
	err := Process("listener", &c)
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(ve.Err.Error(), "together") {
		t.Errorf("expected pairing error, got %v", ve)
	}
}

// orderLeaf is a leaf struct implementing Validator.
type orderLeaf struct {
	Value string
}

func (l *orderLeaf) Validate() error {
	validationOrder = append(validationOrder, "leaf:"+l.Value)
	return nil
}

type orderDeep struct {
	Number int
}

func (d *orderDeep) Validate() error {
	validationOrder = append(validationOrder, "deep")
	return nil
}

type orderInner struct {
	InnerA *orderLeaf
	InnerB orderLeaf
	Deep   *orderDeep
}

func (i *orderInner) Validate() error {
	validationOrder = append(validationOrder, "inner")
	return nil
}

type orderMiddle struct {
	Inner orderInner
}

func (m *orderMiddle) Validate() error {
	validationOrder = append(validationOrder, "middle")
	return nil
}

type orderOuter struct {
	Middle  *orderMiddle
	Sibling orderLeaf
}

func (o *orderOuter) Validate() error {
	validationOrder = append(validationOrder, "outer")
	return nil
}

func TestValidationOrderInsideOutAcrossNestedPointers(t *testing.T) {
	validationOrder = nil
	var o orderOuter
	setEnv(t, map[string]string{
		"ORDER_MIDDLE_INNER_INNERA_VALUE": "a",
		"ORDER_MIDDLE_INNER_INNERB_VALUE": "b",
		"ORDER_MIDDLE_INNER_DEEP_NUMBER":  "7",
		"ORDER_SIBLING_VALUE":             "s",
	})
	if err := Process("order", &o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Every leaf/deep validator runs before the struct that contains it, all
	// the way up the nested-pointer chain. This order cannot pass merely from
	// relying on reflection field-traversal order.
	want := []string{
		"leaf:a",
		"leaf:b",
		"deep",
		"inner",
		"middle",
		"leaf:s",
		"outer",
	}
	if len(validationOrder) != len(want) {
		t.Fatalf("validation order mismatch:\n want %v\n got  %v", want, validationOrder)
	}
	for i := range want {
		if validationOrder[i] != want[i] {
			t.Fatalf("validation order mismatch at %d:\n want %v\n got  %v", i, want, validationOrder)
		}
	}
}

// sharedInstance is reachable through two fields; it must validate once.
type sharedInstance struct {
	Value string
	calls int
}

func (s *sharedInstance) Validate() error {
	s.calls++
	return nil
}

type sharedHolder struct {
	A sharedInstance
	B *sharedInstance
}

func TestEachInstanceValidatedAtMostOnce(t *testing.T) {
	var h sharedHolder
	h.B = &h.A
	setEnv(t, map[string]string{
		"SHARED_A_VALUE": "one",
		"SHARED_B_VALUE": "two",
	})
	if err := Process("shared", &h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.A.calls != 1 {
		t.Errorf("shared instance validated %d times, want 1", h.A.calls)
	}
}

// failLeaf always fails validation.
type failLeaf struct {
	Value string
}

func (f *failLeaf) Validate() error { return errValidateBoom }

type failInner struct {
	Bad       int
	Leaf      failLeaf
	validated bool
}

func (f *failInner) Validate() error {
	f.validated = true
	return nil
}

type siblingHolder struct {
	Left  failInner
	Right failLeaf
}

func (s *siblingHolder) Validate() error {
	validationOrder = append(validationOrder, "siblingHolder")
	return nil
}

func TestFailedSubtreeSkipsAncestorsButSiblingsAggregate(t *testing.T) {
	t.Run("conversion failure skips dependent parents", func(t *testing.T) {
		var s siblingHolder
		validationOrder = nil
		setEnv(t, map[string]string{
			"SIBLING_LEFT_BAD":    "not-an-int",
			"SIBLING_RIGHT_VALUE": "y",
		})
		err := Process("sibling", &s)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		msgs := err.Error()
		if !strings.Contains(msgs, "siblingHolder.Left.Bad") {
			t.Errorf("expected conversion path siblingHolder.Left.Bad in %q", msgs)
		}
		if s.Left.validated {
			t.Error("Left should not validate after child conversion failure")
		}
		for _, p := range validationOrder {
			if p == "siblingHolder" {
				t.Fatal("root must not validate after a dependent subtree failed")
			}
		}
		// The independent Right sibling is a healthy subtree and must still
		// convert and validate.
		if s.Right.Value != "y" {
			t.Errorf("independent sibling conversion skipped: %q", s.Right.Value)
		}
	})

	t.Run("child validation failures aggregate independent siblings", func(t *testing.T) {
		var s siblingHolder
		validationOrder = nil
		setEnv(t, map[string]string{
			"SIBLING_LEFT_LEAF_VALUE": "x",
			"SIBLING_RIGHT_VALUE":     "y",
		})
		err := Process("sibling", &s)
		if err == nil {
			t.Fatal("expected aggregated errors, got nil")
		}
		msgs := err.Error()
		if !strings.Contains(msgs, "siblingHolder.Left.Leaf") {
			t.Errorf("missing left path in %q", msgs)
		}
		if !strings.Contains(msgs, "siblingHolder.Right") {
			t.Errorf("missing independent sibling path in %q", msgs)
		}
		for _, p := range validationOrder {
			if p == "siblingHolder" {
				t.Fatal("root must not validate after dependent child failures")
			}
		}
		if s.Left.validated {
			t.Error("Left must not validate after its child Left.Leaf fails validation")
		}
	})
}

// defaultsConfig verifies defaults are applied before validation.
type defaultsConfig struct {
	Mode string `default:"https"`
	Port int    `default:"443"`
}

func (d *defaultsConfig) Validate() error {
	if d.Mode != "https" || d.Port != 443 {
		return errors.New("validation ran before defaults were applied")
	}
	return nil
}

func TestDefaultsParticipateInValidation(t *testing.T) {
	var d defaultsConfig
	os.Clearenv()
	if err := Process("defaults", &d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Mode != "https" || d.Port != 443 {
		t.Fatalf("defaults not applied: %+v", d)
	}
}

// decodingValidator is a custom Decoder that is also a Validator; the decoded
// value must be finalized before Validate runs.
type decodingValidator struct {
	decoded bool
	value   string
}

func (d *decodingValidator) Decode(value string) error {
	d.decoded = true
	d.value = value
	return nil
}

func (d *decodingValidator) Validate() error {
	if !d.decoded {
		return errors.New("Validate ran before Decode")
	}
	return nil
}

type decoderHolder struct {
	Sub decodingValidator
}

func TestCustomDecoderValidatesAfterDecode(t *testing.T) {
	var h decoderHolder
	setEnv(t, map[string]string{"DECODER_SUB": "ready"})
	if err := Process("decoder", &h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Sub.value != "ready" {
		t.Errorf("decoder value = %q, want ready", h.Sub.value)
	}
}

func TestNestedParseErrorCarriesFullPath(t *testing.T) {
	type inner struct {
		Port int
	}
	type outer struct {
		Inner inner
	}
	var o outer
	setEnv(t, map[string]string{"OUTER_INNER_PORT": "bad"})
	err := Process("outer", &o)
	pe, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
	if pe.Path != "outer.Inner.Port" {
		t.Errorf("expected outer.Inner.Port, got %q", pe.Path)
	}
	if pe.FieldName != "Port" {
		t.Errorf("expected field name Port, got %q", pe.FieldName)
	}
}
