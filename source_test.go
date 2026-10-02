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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeySource_MergeOverrideChain(t *testing.T) {
	f, err := LoadSources(
		LoadOptions{TrackSource: true},
		NamedSource{Name: "defaults", Source: []byte("[server]\nhost = default.local\nport = 80\n")},
		NamedSource{Name: "user", Source: []byte("[server]\nhost = user.local\n")},
	)
	require.NoError(t, err)

	sec := f.Section("server")

	// Overridden key: chain shows actual parse order, last one wins.
	host := sec.Key("host")
	assert.Equal(t, "user.local", host.String())
	require.Len(t, host.Sources(), 2)
	assert.Equal(t, KeySource{Source: "defaults", Line: 2, Column: 1, Value: "default.local"}, host.Sources()[0])
	assert.Equal(t, KeySource{Source: "user", Line: 2, Column: 1, Value: "user.local"}, host.Sources()[1])

	src, ok := host.Source()
	require.True(t, ok)
	assert.Equal(t, "user", src.Source)
	assert.Equal(t, "user.local", src.Value)

	// Non-overridden key only has the record from the defaults.
	port := sec.Key("port")
	require.Len(t, port.Sources(), 1)
	assert.Equal(t, "defaults", port.Sources()[0].Source)
}

func TestKeySource_NamedReader(t *testing.T) {
	f, err := LoadSources(
		LoadOptions{TrackSource: true},
		NamedSource{Name: "env-base", Source: strings.NewReader("key = base\n")},
		NamedSource{Name: "env-user", Source: strings.NewReader("key = user\n")},
	)
	require.NoError(t, err)

	key := f.Section("").Key("key")
	require.Len(t, key.Sources(), 2)
	assert.Equal(t, "env-base", key.Sources()[0].Source)
	assert.Equal(t, "env-user", key.Sources()[1].Source)

	src, ok := key.Source()
	require.True(t, ok)
	assert.Equal(t, "env-user", src.Source)
}

func TestKeySource_FilePath(t *testing.T) {
	dir := t.TempDir()
	defaultFile := filepath.Join(dir, "default.ini")
	userFile := filepath.Join(dir, "user.ini")
	require.NoError(t, os.WriteFile(defaultFile, []byte("key = default\n"), 0644))
	require.NoError(t, os.WriteFile(userFile, []byte("key = user\n"), 0644))

	f, err := LoadSources(LoadOptions{TrackSource: true}, defaultFile, userFile)
	require.NoError(t, err)

	key := f.Section("").Key("key")
	require.Len(t, key.Sources(), 2)
	assert.Equal(t, defaultFile, key.Sources()[0].Source)
	assert.Equal(t, userFile, key.Sources()[1].Source)
}

func TestKeySource_UnnamedSources(t *testing.T) {
	f, err := LoadSources(
		LoadOptions{TrackSource: true},
		[]byte("key = data\n"),
		strings.NewReader("key = reader\n"),
	)
	require.NoError(t, err)

	key := f.Section("").Key("key")
	require.Len(t, key.Sources(), 2)
	assert.Equal(t, "data", key.Sources()[0].Source)
	assert.Equal(t, "reader", key.Sources()[1].Source)
}

func TestKeySource_MultilineValue(t *testing.T) {
	f, err := LoadSources(
		LoadOptions{TrackSource: true},
		NamedSource{Name: "mem", Source: []byte("first = 1\nmulti = line1\\\n line2\\\n line3\nlast = 2\n")},
	)
	require.NoError(t, err)

	sec := f.Section("")
	multi := sec.Key("multi")
	src, ok := multi.Source()
	require.True(t, ok)
	assert.Equal(t, 2, src.Line) // Start line of the multi-line assignment
	assert.Equal(t, "line1line2line3", src.Value)

	// Lines consumed by the multi-line value are accounted for.
	last, ok := sec.Key("last").Source()
	require.True(t, ok)
	assert.Equal(t, 5, last.Line)
}

func TestKeySource_Column(t *testing.T) {
	f, err := LoadSources(
		LoadOptions{TrackSource: true},
		NamedSource{Name: "mem", Source: []byte("[sec]\n  indented = v\n")},
	)
	require.NoError(t, err)

	src, ok := f.Section("sec").Key("indented").Source()
	require.True(t, ok)
	assert.Equal(t, 2, src.Line)
	assert.Equal(t, 3, src.Column)
}

func TestKeySource_Shadows(t *testing.T) {
	f, err := LoadSources(
		LoadOptions{TrackSource: true, AllowShadows: true},
		NamedSource{Name: "first", Source: []byte("key = a\nkey = b\n")},
		NamedSource{Name: "second", Source: []byte("key = c\n")},
	)
	require.NoError(t, err)

	key := f.Section("").Key("key")
	assert.Equal(t, []string{"a", "b", "c"}, key.ValueWithShadows())

	// The primary key has its own record, shadows each have their own.
	src, ok := key.Source()
	require.True(t, ok)
	assert.Equal(t, KeySource{Source: "first", Line: 1, Column: 1, Value: "a"}, src)

	shadows := key.Shadows()
	require.Len(t, shadows, 2)

	src, ok = shadows[0].Source()
	require.True(t, ok)
	assert.Equal(t, KeySource{Source: "first", Line: 2, Column: 1, Value: "b"}, src)

	src, ok = shadows[1].Source()
	require.True(t, ok)
	assert.Equal(t, KeySource{Source: "second", Line: 1, Column: 1, Value: "c"}, src)
}

func TestKeySource_Insensitive(t *testing.T) {
	f, err := LoadSources(
		LoadOptions{TrackSource: true, Insensitive: true},
		NamedSource{Name: "defaults", Source: []byte("[Server]\nHOST = default\n")},
		NamedSource{Name: "user", Source: []byte("[SERVER]\nHost = user\n")},
	)
	require.NoError(t, err)

	key := f.Section("SERVER").Key("HOST")
	assert.Equal(t, "user", key.String())
	require.Len(t, key.Sources(), 2)
	assert.Equal(t, "defaults", key.Sources()[0].Source)
	assert.Equal(t, "user", key.Sources()[1].Source)
}

func TestKeySource_NonUniqueSections(t *testing.T) {
	f, err := LoadSources(
		LoadOptions{TrackSource: true, AllowNonUniqueSections: true},
		NamedSource{Name: "mem", Source: []byte("[sec]\nkey = 1\n[sec]\nkey = 2\n")},
	)
	require.NoError(t, err)

	secs, err := f.SectionsByName("sec")
	require.NoError(t, err)
	require.Len(t, secs, 2)

	src, ok := secs[0].Key("key").Source()
	require.True(t, ok)
	assert.Equal(t, KeySource{Source: "mem", Line: 2, Column: 1, Value: "1"}, src)

	src, ok = secs[1].Key("key").Source()
	require.True(t, ok)
	assert.Equal(t, KeySource{Source: "mem", Line: 4, Column: 1, Value: "2"}, src)
}

func TestKeySource_DoesNotTriggerValueMapper(t *testing.T) {
	f, err := LoadSources(
		LoadOptions{TrackSource: true},
		NamedSource{Name: "defaults", Source: []byte("key = default\n")},
		NamedSource{Name: "user", Source: []byte("key = user\n")},
	)
	require.NoError(t, err)

	calls := 0
	f.ValueMapper = func(in string) string {
		calls++
		return in
	}
	t.Cleanup(func() { f.ValueMapper = nil })

	key := f.Section("").Key("key")
	_ = key.Sources()
	_, _ = key.Source()
	assert.Equal(t, 0, calls)

	// Sanity check: reading the value does go through the ValueMapper.
	_ = key.String()
	assert.Equal(t, 1, calls)

	// Source queries still do not change the call count afterwards.
	_ = key.Sources()
	_, _ = key.Source()
	assert.Equal(t, 1, calls)
}

func TestKeySource_Disabled(t *testing.T) {
	f, err := Load([]byte("key = value\n"))
	require.NoError(t, err)

	key := f.Section("").Key("key")
	assert.Nil(t, key.Sources())
	_, ok := key.Source()
	assert.False(t, ok)
}
