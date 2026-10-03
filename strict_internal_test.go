package toml

import (
	"testing"

	"github.com/pelletier/go-toml/v2/internal/assert"
)

func TestStrictIsExempt(t *testing.T) {
	s := strict{
		Enabled:     true,
		exemptPaths: []Key{{"plugins", "extra"}, {"plugins.extra"}},
	}

	assert.True(t, s.isExempt(Key{"plugins", "extra"}))
	assert.True(t, s.isExempt(Key{"plugins", "extra", "nested"}))
	assert.True(t, s.isExempt(Key{"plugins", "extra", "deeply", "nested"}))
	assert.True(t, s.isExempt(Key{"plugins.extra"}))

	// Segment-wise matching: dotted single keys do not match multi-segment
	// rules, and vice versa.
	assert.False(t, s.isExempt(Key{"plugins_extra"}))
	assert.False(t, s.isExempt(Key{"plugins", "extras"}))
	assert.False(t, s.isExempt(Key{"plugins"}))

	// Empty rules never match, even an empty key.
	empty := strict{Enabled: true}
	assert.False(t, empty.isExempt(Key{}))
	assert.False(t, empty.isExempt(Key{"anything"}))

	// A rule longer than the key cannot be a prefix of it.
	assert.False(t, s.isExempt(Key{"plugins"}))

	// Disabled strict mode never consults callers, but matching itself stays
	// usable.
	s.Enabled = false
	assert.True(t, s.isExempt(Key{"plugins", "extra"}))
}
