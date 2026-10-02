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

// KeySource describes a single assignment of a key's value recorded during
// parsing. It is only available when LoadOptions.TrackSource is enabled.
type KeySource struct {
	// Source identifies the data source the assignment was parsed from. It is
	// the file path for file sources, the caller-provided name for sources
	// wrapped with NamedSource, or a default identifier ("data" or "reader")
	// for other in-memory sources.
	Source string
	// Line is the 1-based line number where the key assignment starts. For
	// multi-line values, it is the line of the first line of the assignment.
	Line int
	// Column is the 1-based byte column of the first non-whitespace character
	// of the line, i.e. where the key name starts.
	Column int
	// Value is the raw value assigned to the key, before any ValueMapper
	// transformation or variable substitution.
	Value string
}

// Sources returns the source chain of the key in the actual parse order. The
// last element describes the assignment of the currently effective value,
// and every earlier element was overridden by the one that follows it. Each
// shadow value carries its own records on the shadow keys returned by
// Key.Shadows.
//
// The chain is recorded while parsing and reflects the real parse order; it
// is not derived by re-reading the data sources. It returns nil unless
// LoadOptions.TrackSource was enabled at load time.
func (k *Key) Sources() []KeySource {
	if len(k.sources) == 0 {
		return nil
	}
	sources := make([]KeySource, len(k.sources))
	copy(sources, k.sources)
	return sources
}

// Source returns the source record of the key's currently effective value,
// i.e. the last element of Sources. The second return value is false when no
// source information was recorded. Querying the source never triggers
// ValueMapper or variable substitution.
func (k *Key) Source() (KeySource, bool) {
	if len(k.sources) == 0 {
		return KeySource{}, false
	}
	return k.sources[len(k.sources)-1], true
}

// Shadows returns the shadow keys of the key. Each shadow key carries its own
// source records, accessible via Sources and Source.
func (k *Key) Shadows() []*Key {
	if len(k.shadows) == 0 {
		return nil
	}
	shadows := make([]*Key, len(k.shadows))
	copy(shadows, k.shadows)
	return shadows
}
