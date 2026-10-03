package toml_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/internal/assert"
)

type strictExemptDoc struct {
	Version string             `toml:"version"`
	Plugins strictExemptPlugin `toml:"plugins"`
}

type strictExemptPlugin struct {
	Name  string             `toml:"name"`
	Extra []strictExemptItem `toml:"extra"`
}

type strictExemptItem struct {
	ID string `toml:"id"`
}

func strictErrorKeys(t *testing.T, err error) []toml.Key {
	t.Helper()
	var details *toml.StrictMissingError
	if !errors.As(err, &details) {
		t.Fatalf("err should be a *toml.StrictMissingError, got %s (%T)", err, err)
	}
	keys := make([]toml.Key, 0, len(details.Errors))
	for _, e := range details.Errors {
		keys = append(keys, e.Key())
	}
	return keys
}

func TestAllowUnknownFieldsSubtree(t *testing.T) {
	// An extension table managed by an external component: the whole
	// "plugins.extra" subtree is unknown to the target struct.
	doc := `
version = "1.0"

[plugins]
name = "core"

[plugins.extra]
enabled = true

[plugins.extra.nested]
value = 42
`
	var cfg strictExemptDoc
	err := toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins", "extra"}).
		Decode(&cfg)
	assert.NoError(t, err)
	assert.Equal(t, "1.0", cfg.Version)
	assert.Equal(t, "core", cfg.Plugins.Name)
}

func TestAllowUnknownFieldsArrayTableAllElements(t *testing.T) {
	// A rule on the array table name exempts unknown fields inside every
	// element, not just the first one.
	doc := `
[[plugins.extra]]
id = "a"
external = 1

[[plugins.extra]]
id = "b"
external = 2
`
	var cfg strictExemptDoc
	err := toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins", "extra"}).
		Decode(&cfg)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(cfg.Plugins.Extra))
	assert.Equal(t, "a", cfg.Plugins.Extra[0].ID)
	assert.Equal(t, "b", cfg.Plugins.Extra[1].ID)
}

func TestAllowUnknownFieldsSegmentBoundary(t *testing.T) {
	// Rules match decoded segments: a quoted key that happens to contain a
	// dot is not the same as the nested "plugins" -> "extra" subtree.
	doc := `
"plugins.extra" = 1
`
	var cfg strictExemptDoc
	err := toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins", "extra"}).
		Decode(&cfg)
	keys := strictErrorKeys(t, err)
	assert.Equal(t, 1, len(keys))
	assert.Equal(t, toml.Key{"plugins.extra"}, keys[0])

	// Conversely, the single-segment rule matches the literal key.
	err = toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins.extra"}).
		Decode(&cfg)
	assert.NoError(t, err)
}

func TestAllowUnknownFieldsPrefixDoesNotOverreach(t *testing.T) {
	// A similar prefix is not a subtree match: "plugins_extra" and
	// "pluginsExtra" must still be reported.
	doc := `
[plugins_extra]
foo = 1

[pluginsExtra]
bar = 2
`
	var cfg strictExemptDoc
	err := toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins"}).
		Decode(&cfg)
	keys := strictErrorKeys(t, err)
	assert.Equal(t, 2, len(keys))
	assert.Equal(t, toml.Key{"plugins_extra"}, keys[0])
	assert.Equal(t, toml.Key{"pluginsExtra"}, keys[1])
}

func TestAllowUnknownFieldsStillReportsSiblings(t *testing.T) {
	// Unknown fields outside the exempt subtree are aggregated as usual,
	// including a typo of an ordinary field.
	doc := `
version = "1.0"
verson = "typo"

[plugins]
name = "core"

[plugins.extra]
enabled = true
`
	var cfg strictExemptDoc
	err := toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins", "extra"}).
		Decode(&cfg)
	keys := strictErrorKeys(t, err)
	assert.Equal(t, 1, len(keys))
	assert.Equal(t, toml.Key{"verson"}, keys[0])
	assert.Equal(t, "1.0", cfg.Version)
}

func TestAllowUnknownFieldsDoesNotLegalizeDuplicates(t *testing.T) {
	doc := `
[plugins.extra]
enabled = true
enabled = false
`
	var cfg strictExemptDoc
	err := toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins", "extra"}).
		Decode(&cfg)
	assert.Error(t, err)

	var strictErr *toml.StrictMissingError
	assert.False(t, errors.As(err, &strictErr))
}

func TestAllowUnknownFieldsMalformedDocument(t *testing.T) {
	doc := `
[plugins.extra]
enabled =
`
	var cfg strictExemptDoc
	err := toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins", "extra"}).
		Decode(&cfg)
	assert.Error(t, err)

	var strictErr *toml.StrictMissingError
	assert.False(t, errors.As(err, &strictErr))
}

func TestAllowUnknownFieldsNoEffectWithoutStrictMode(t *testing.T) {
	// Exemption rules do not change the default, lenient behavior.
	doc := `
verson = "ignored"
[plugins.extra]
enabled = true
`
	var cfg strictExemptDoc
	err := toml.NewDecoder(strings.NewReader(doc)).
		AllowUnknownFields(toml.Key{"plugins", "extra"}).
		Decode(&cfg)
	assert.NoError(t, err)

	// Strict mode without rules keeps reporting everything.
	err = toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		Decode(&cfg)
	assert.Error(t, err)
}

func TestAllowUnknownFieldsMultipleRules(t *testing.T) {
	type doc struct {
		Version string `toml:"version"`
	}
	data := `
version = "1.0"
[alpha]
x = 1
[beta]
y = 2
`
	var cfg doc
	err := toml.NewDecoder(strings.NewReader(data)).
		DisallowUnknownFields().
		AllowUnknownFields(
			toml.Key{"alpha"},
			toml.Key{"beta"},
		).
		Decode(&cfg)
	assert.NoError(t, err)
}

func TestAllowUnknownFieldsDottedKey(t *testing.T) {
	// A subtree reached through a dotted key follows the same segment
	// matching as a table header.
	doc := `
version = "1.0"
plugins.name = "core"
plugins.extra.enabled = true
plugins.extra.nested.value = 42
`
	var cfg strictExemptDoc
	err := toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins", "extra"}).
		Decode(&cfg)
	assert.NoError(t, err)
	assert.Equal(t, "core", cfg.Plugins.Name)

	// A sibling unknown dotted key is still reported.
	doc += "plugins.unkwnown = 1\n"
	err = toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins", "extra"}).
		Decode(&cfg)
	keys := strictErrorKeys(t, err)
	assert.Equal(t, 1, len(keys))
	assert.Equal(t, toml.Key{"plugins", "unkwnown"}, keys[0])
}

func TestAllowUnknownFieldsDoesNotLeakAcrossDecodes(t *testing.T) {
	doc := `
[plugins.extra]
enabled = true
`
	// A strict decoder with an exemption accepts the document.
	var cfg strictExemptDoc
	err := toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins", "extra"}).
		Decode(&cfg)
	assert.NoError(t, err)

	// A different strict decoder without an exemption must still report the
	// unknown table, even though pooled decoders are shared internally.
	err = toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		Decode(&cfg)
	assert.Error(t, err)
	var details *toml.StrictMissingError
	assert.True(t, errors.As(err, &details))
}
