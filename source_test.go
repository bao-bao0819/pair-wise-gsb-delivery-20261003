// Copyright 2026 Unknwon
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

package ini

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSource_DisabledByDefault(t *testing.T) {
	f, err := LoadSources(LoadOptions{},
		NamedSource{Name: "defaults", Source: []byte("key = a\n")},
		NamedSource{Name: "user", Source: []byte("key = b\n")},
	)
	require.NoError(t, err)

	k := f.Section("").Key("key")
	assert.Nil(t, k.ValueSource())
	assert.Nil(t, k.ValueSources())
	assert.Nil(t, f.SourceNames())
}

func TestSource_OverrideChain(t *testing.T) {
	f, err := LoadSources(LoadOptions{TrackSources: true},
		NamedSource{Name: "defaults", Source: []byte("key = a\n")},
		NamedSource{Name: "user", Source: []byte("key = b\n")},
	)
	require.NoError(t, err)

	k := f.Section("").Key("key")
	assert.Equal(t, "b", k.String())

	src := k.ValueSource()
	require.NotNil(t, src)
	assert.Equal(t, "user", src.Source())
	assert.Equal(t, 1, src.Line())
	assert.Equal(t, 1, src.Column())

	first := src.Overridden()
	require.NotNil(t, first)
	assert.Equal(t, "defaults", first.Source())
	assert.Equal(t, 1, first.Line())
	assert.Nil(t, first.Overridden())
	assert.Same(t, src, first.Overrides())

	chain := k.ValueSources()
	require.Len(t, chain, 2)
	assert.Equal(t, "defaults", chain[0].Source())
	assert.Equal(t, "user", chain[1].Source())

	assert.Equal(t, []string{"defaults", "user"}, f.SourceNames())
}

func TestSource_LineAndColumn(t *testing.T) {
	f, err := LoadSources(LoadOptions{TrackSources: true},
		NamedSource{Name: "cfg", Source: []byte(
			"# leading comment\n" +
				"\n" +
				"[sec]\n" +
				"first = 1\n" +
				"    second = 2\n" +
				"\tthird = 3\n",
		)},
	)
	require.NoError(t, err)

	cases := []struct {
		key          string
		line, column int
	}{
		{"first", 4, 1},
		{"second", 5, 5},
		{"third", 6, 2},
	}
	for _, c := range cases {
		src := f.Section("sec").Key(c.key).ValueSource()
		require.NotNil(t, src, c.key)
		assert.Equal(t, c.line, src.Line(), c.key)
		assert.Equal(t, c.column, src.Column(), c.key)
		assert.Equal(t, "cfg", src.Source())
	}
}

func TestSource_MultilineValues(t *testing.T) {
	t.Run("continuation lines", func(t *testing.T) {
		f, err := LoadSources(LoadOptions{TrackSources: true},
			NamedSource{Name: "cfg", Source: []byte(
				"first = 1\n" +
					"multi = a,\\\n" +
					"  b,\\\n" +
					"  c\n" +
					"last = 2\n",
			)},
		)
		require.NoError(t, err)

		multi := f.Section("").Key("multi")
		src := multi.ValueSource()
		assert.Equal(t, 2, src.Line())
		assert.Equal(t, 1, src.Column())

		last := f.Section("").Key("last").ValueSource()
		assert.Equal(t, 5, last.Line())
	})

	t.Run("quoted multiline", func(t *testing.T) {
		f, err := LoadSources(LoadOptions{TrackSources: true},
			NamedSource{Name: "cfg", Source: []byte(
				"a = 1\n" +
					"q = \"\"\"line1\n" +
					"line2\n" +
					"line3\"\"\"\n" +
					"b = 2\n",
			)},
		)
		require.NoError(t, err)

		src := f.Section("").Key("q").ValueSource()
		assert.Equal(t, 2, src.Line())
		assert.Equal(t, 5, f.Section("").Key("b").ValueSource().Line())
	})

	t.Run("python multiline", func(t *testing.T) {
		f, err := LoadSources(LoadOptions{
			TrackSources:               true,
			AllowPythonMultilineValues: true,
		}, NamedSource{Name: "cfg", Source: []byte(
			"a = 1\n" +
				"py = line1\n" +
				"  line2\n" +
				"  line3\n" +
				"b = 2\n",
		)})
		require.NoError(t, err)

		py := f.Section("").Key("py").ValueSource()
		assert.Equal(t, 2, py.Line())
		b := f.Section("").Key("b").ValueSource()
		assert.Equal(t, 5, b.Line())
	})
}

func TestSource_Shadows(t *testing.T) {
	t.Run("shadows keep independent records", func(t *testing.T) {
		f, err := LoadSources(LoadOptions{
			TrackSources: true,
			AllowShadows: true,
		},
			NamedSource{Name: "defaults", Source: []byte("v = 1\nv = 2\n")},
			NamedSource{Name: "user", Source: []byte("v = 3\n")},
		)
		require.NoError(t, err)

		k := f.Section("").Key("v")
		assert.Equal(t, []string{"1", "2", "3"}, k.ValueWithShadows())

		primary := k.ValueSource()
		assert.Equal(t, "defaults", primary.Source())
		assert.False(t, primary.Shadow())

		all := k.ValueSources()
		require.Len(t, all, 3)
		assert.Equal(t, "defaults", all[0].Source())
		assert.False(t, all[0].Shadow())
		assert.Equal(t, "defaults", all[1].Source())
		assert.True(t, all[1].Shadow())
		assert.Equal(t, "user", all[2].Source())
		assert.True(t, all[2].Shadow())
		assert.Equal(t, 1, all[2].Line())

		// Each shadow value has its own record reachable from its shadow key.
		require.Len(t, k.shadows, 2)
		assert.Same(t, all[1], k.shadows[0].ValueSource())
		assert.Same(t, all[2], k.shadows[1].ValueSource())
	})

	t.Run("deduplicated shadow adds no record", func(t *testing.T) {
		f, err := LoadSources(LoadOptions{
			TrackSources: true,
			AllowShadows: true,
		}, NamedSource{Name: "cfg", Source: []byte("v = 1\nv = 1\n")})
		require.NoError(t, err)

		k := f.Section("").Key("v")
		assert.Len(t, k.ValueSources(), 1)
	})
}

func TestSource_Insensitive(t *testing.T) {
	f, err := LoadSources(LoadOptions{
		TrackSources: true,
		Insensitive:  true,
	}, NamedSource{Name: "defaults", Source: []byte("[Sec]\nKey = a\n")},
		NamedSource{Name: "user", Source: []byte("[sEc]\nkEy = b\n")},
	)
	require.NoError(t, err)

	k := f.Section("sec").Key("key")
	assert.Equal(t, "b", k.String())
	src := k.ValueSource()
	assert.Equal(t, "user", src.Source())
	assert.Equal(t, 2, src.Line())

	first := src.Overridden()
	require.NotNil(t, first)
	assert.Equal(t, "defaults", first.Source())
	assert.Equal(t, 2, first.Line())
}

func TestSource_NonUniqueSections(t *testing.T) {
	f, err := LoadSources(LoadOptions{
		TrackSources:           true,
		AllowNonUniqueSections: true,
	},
		NamedSource{Name: "a", Source: []byte("[s]\nk = 1\n")},
		NamedSource{Name: "b", Source: []byte("[s]\nk = 2\n")},
	)
	require.NoError(t, err)

	secs, err := f.SectionsByName("s")
	require.NoError(t, err)
	require.Len(t, secs, 2)

	assert.Equal(t, "a", secs[0].Key("k").ValueSource().Source())
	assert.Equal(t, "b", secs[1].Key("k").ValueSource().Source())
}

func TestSource_BooleanKeys(t *testing.T) {
	f, err := LoadSources(LoadOptions{
		TrackSources:     true,
		AllowBooleanKeys: true,
	}, NamedSource{Name: "cfg", Source: []byte("[s]\n  enabled\n")})
	require.NoError(t, err)

	src := f.Section("s").Key("enabled").ValueSource()
	require.NotNil(t, src)
	assert.Equal(t, 2, src.Line())
	assert.Equal(t, 3, src.Column())
}

func TestSource_ReaderName(t *testing.T) {
	r := strings.NewReader("key = from-reader\n")
	f, err := LoadSources(LoadOptions{TrackSources: true},
		NamedSource{Name: "remote", Source: r},
	)
	require.NoError(t, err)

	src := f.Section("").Key("key").ValueSource()
	require.NotNil(t, src)
	assert.Equal(t, "remote", src.Source())
	assert.Equal(t, []string{"remote"}, f.SourceNames())
}

func TestSource_FileSource(t *testing.T) {
	f, err := LoadSources(LoadOptions{TrackSources: true}, "testdata/minimal.ini")
	require.NoError(t, err)

	src := f.Section("author").Key("E-MAIL").ValueSource()
	require.NotNil(t, src)
	assert.Equal(t, "testdata/minimal.ini", src.Source())
	assert.Equal(t, 2, src.Line())
	assert.Equal(t, 1, src.Column())
}

func TestSource_AnonymousBytes(t *testing.T) {
	f, err := LoadSources(LoadOptions{TrackSources: true},
		[]byte("key = a\n"),
		NamedSource{Name: "named", Source: []byte("key = b\n")},
	)
	require.NoError(t, err)

	chain := f.Section("").Key("key").ValueSources()
	require.Len(t, chain, 2)
	assert.Equal(t, "", chain[0].Source())
	assert.Equal(t, "named", chain[1].Source())
	assert.Equal(t, []string{"", "named"}, f.SourceNames())
}

func TestSource_DoesNotInvokeValueMapper(t *testing.T) {
	calls := 0
	f, err := LoadSources(LoadOptions{TrackSources: true},
		NamedSource{Name: "defaults", Source: []byte("key = a\n")},
		NamedSource{Name: "user", Source: []byte("key = b\n")},
	)
	require.NoError(t, err)
	f.ValueMapper = func(in string) string {
		calls++
		return strings.ToUpper(in)
	}

	k := f.Section("").Key("key")
	for i := 0; i < 5; i++ {
		_ = k.ValueSource()
		_ = k.ValueSources()
	}
	assert.Equal(t, 0, calls)

	assert.Equal(t, "B", k.String())
	assert.Equal(t, 1, calls)
}

func TestSource_Reload(t *testing.T) {
	f, err := LoadSources(LoadOptions{TrackSources: true},
		NamedSource{Name: "defaults", Source: []byte("key = a\n")},
		NamedSource{Name: "user", Source: []byte("key = b\n")},
	)
	require.NoError(t, err)

	require.NoError(t, f.Reload())

	k := f.Section("").Key("key")
	assert.Equal(t, "b", k.String())

	chain := k.ValueSources()
	require.Len(t, chain, 2, "reload must not accumulate stale records")
	assert.Equal(t, "defaults", chain[0].Source())
	assert.Equal(t, "user", chain[1].Source())
	assert.Same(t, chain[1], chain[0].Overrides())

	assert.Equal(t, []string{"defaults", "user"}, f.SourceNames())
}

func TestSource_ShortCircuit(t *testing.T) {
	f, err := LoadSources(LoadOptions{
		TrackSources: true,
		ShortCircuit: true,
	},
		NamedSource{Name: "first", Source: []byte("key = a\n")},
		NamedSource{Name: "second", Source: []byte("key = b\n")},
	)
	require.NoError(t, err)

	assert.Equal(t, "a", f.Section("").Key("key").String())
	assert.Equal(t, []string{"first"}, f.SourceNames())
}

func TestSource_ProgrammaticOverrides(t *testing.T) {
	f, err := LoadSources(LoadOptions{TrackSources: true},
		NamedSource{Name: "cfg", Source: []byte("key = a\n")},
	)
	require.NoError(t, err)

	k := f.Section("").Key("key")
	require.NotNil(t, k.ValueSource())

	// Programmatic changes do not fabricate a source record, but they do not
	// destroy the parsed chain either.
	k.SetValue("b")
	src := k.ValueSource()
	require.NotNil(t, src)
	assert.Equal(t, "cfg", src.Source())
	assert.Len(t, k.ValueSources(), 1)
}

func TestSource_SameValueOverride(t *testing.T) {
	// Without shadows, a repeated assignment is an override even when the
	// value does not change, so each assignment keeps a record.
	f, err := LoadSources(LoadOptions{TrackSources: true},
		NamedSource{Name: "defaults", Source: []byte("key = a\n")},
		NamedSource{Name: "user", Source: []byte("key = a\n")},
	)
	require.NoError(t, err)

	k := f.Section("").Key("key")
	chain := k.ValueSources()
	require.Len(t, chain, 2)
	assert.Equal(t, "defaults", chain[0].Source())
	assert.Equal(t, "user", chain[1].Source())
	assert.Same(t, chain[1], k.ValueSource())
}

func TestSource_ShadowChainOrderAcrossSources(t *testing.T) {
	f, err := LoadSources(LoadOptions{
		TrackSources: true,
		AllowShadows: true,
	},
		NamedSource{Name: "one", Source: []byte("v = 1\nv = 2\n")},
		NamedSource{Name: "two", Source: []byte("v = 3\nv = 4\n")},
	)
	require.NoError(t, err)

	k := f.Section("").Key("v")
	all := k.ValueSources()
	require.Len(t, all, 4)

	names := make([]string, len(all))
	for i, src := range all {
		names[i] = src.Source()
	}
	assert.Equal(t, []string{"one", "one", "two", "two"}, names)

	// The primary value's record is never marked as shadow.
	assert.False(t, all[0].Shadow())
	for _, src := range all[1:] {
		assert.True(t, src.Shadow())
	}
}

func TestSource_NestedValuesKeepParentPosition(t *testing.T) {
	f, err := LoadSources(LoadOptions{
		TrackSources:      true,
		AllowNestedValues: true,
	}, NamedSource{Name: "cfg", Source: []byte(
		"key =\n" +
			"  nested = 1\n" +
			"  nested = 2\n",
	)})
	require.NoError(t, err)

	src := f.Section("").Key("key").ValueSource()
	require.NotNil(t, src)
	assert.Equal(t, 1, src.Line())
	assert.Equal(t, 1, src.Column())
}

func TestSource_NamedSectionMergeAcrossSources(t *testing.T) {
	f, err := LoadSources(LoadOptions{TrackSources: true},
		NamedSource{Name: "base", Source: []byte("[s]\na = 1\nb = 2\n")},
		NamedSource{Name: "override", Source: []byte("[s]\nb = 3\n")},
	)
	require.NoError(t, err)

	sec := f.Section("s")
	assert.Equal(t, "1", sec.Key("a").String())
	assert.Equal(t, "3", sec.Key("b").String())

	bChain := sec.Key("b").ValueSources()
	require.Len(t, bChain, 2)
	assert.Equal(t, "base", bChain[0].Source())
	assert.Equal(t, 3, bChain[0].Line())
	assert.Equal(t, "override", bChain[1].Source())
	assert.Equal(t, 2, bChain[1].Line())
	assert.Same(t, bChain[1], bChain[0].Overrides())
}

func TestSource_InsensitiveKeysOnly(t *testing.T) {
	f, err := LoadSources(LoadOptions{
		TrackSources:    true,
		InsensitiveKeys: true,
	},
		NamedSource{Name: "base", Source: []byte("KEY = a\n")},
		NamedSource{Name: "override", Source: []byte("key = b\n")},
	)
	require.NoError(t, err)

	k := f.Section("").Key("key")
	assert.Equal(t, "b", k.String())
	src := k.ValueSource()
	assert.Equal(t, "override", src.Source())
	require.NotNil(t, src.Overridden())
	assert.Equal(t, "base", src.Overridden().Source())
}

func TestSource_Append(t *testing.T) {
	f, err := LoadSources(LoadOptions{TrackSources: true},
		NamedSource{Name: "base", Source: []byte("key = a\n")},
	)
	require.NoError(t, err)

	require.NoError(t, f.Append(NamedSource{Name: "extra", Source: []byte("key = b\n")}))

	k := f.Section("").Key("key")
	assert.Equal(t, "b", k.String())
	assert.Equal(t, []string{"base", "extra"}, f.SourceNames())

	chain := k.ValueSources()
	require.Len(t, chain, 2)
	assert.Equal(t, "base", chain[0].Source())
	assert.Equal(t, "extra", chain[1].Source())
}

func TestSource_LooseMissingFile(t *testing.T) {
	f, err := LoadSources(LoadOptions{TrackSources: true, Loose: true},
		NamedSource{Name: "missing", Source: "definitely-not-here.ini"},
		NamedSource{Name: "present", Source: []byte("key = a\n")},
	)
	require.NoError(t, err)

	assert.Equal(t, "a", f.Section("").Key("key").String())
	// The nonexistent source is not reported as parsed.
	assert.Equal(t, []string{"present"}, f.SourceNames())
}

func TestSource_ProgrammaticKeysHaveNoRecord(t *testing.T) {
	f, err := LoadSources(LoadOptions{TrackSources: true},
		NamedSource{Name: "cfg", Source: []byte("parsed = 1\n")},
	)
	require.NoError(t, err)

	created, err := f.Section("").NewKey("added", "x")
	require.NoError(t, err)
	assert.Nil(t, created.ValueSource())
	assert.Nil(t, created.ValueSources())

	assert.NotNil(t, f.Section("").Key("parsed").ValueSource())
}
