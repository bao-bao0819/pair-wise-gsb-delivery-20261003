package toml

import (
	"testing"

	"github.com/pelletier/go-toml/v2/internal/assert"
	"github.com/pelletier/go-toml/v2/unstable"
)

func TestKeyLocationEmptyKeyPanics(t *testing.T) {
	assert.Panics(t, func() {
		_ = keyLocation(&unstable.Node{Kind: unstable.Table})
	})
}

func TestStrictExempt(t *testing.T) {
	st := strict{Enabled: true, exceptions: []Key{{"plugins", "extra"}}}

	assert.True(t, st.exempt(Key{"plugins", "extra", "table", "name"}))
	assert.True(t, st.exempt(Key{"plugins", "extra", "name"}))
	assert.False(t, st.exempt(Key{"plugins.extra"}))
	assert.False(t, st.exempt(Key{"plugins", "extras"}))
	assert.False(t, st.exempt(Key{"other", "plugins", "extra"}))
}

func TestHasKeyPrefix(t *testing.T) {
	assert.True(t, hasKeyPrefix(Key{"plugins", "extra", "name"}, Key{"plugins", "extra"}))
	assert.False(t, hasKeyPrefix(Key{"plugins.extra"}, Key{"plugins", "extra"}))
	assert.False(t, hasKeyPrefix(Key{"plugins", "extras"}, Key{"plugins", "extra"}))
	assert.False(t, hasKeyPrefix(Key{"plugins"}, Key{"plugins", "extra"}))
	assert.False(t, hasKeyPrefix(Key{"plugins"}, Key{}))
	assert.False(t, hasKeyPrefix(Key{"plugins"}, nil))
}
