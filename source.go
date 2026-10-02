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

// ValueSource records where a single key assignment came from.
//
// A key may receive several assignments over its lifetime: the same key can be
// set multiple times inside one data source, and later data sources overwrite
// values of earlier ones. When shadow keys are allowed (see LoadOptions
// AllowShadows), repeated assignments become shadow values instead of
// overwriting; each assignment still keeps its own ValueSource record.
//
// Records that overwrite the same value slot are linked together: the
// overwritten assignment is available through Overridden and the assignment
// that overwrote it through Overrides. All records of a key, including its
// shadow values, are returned by Key.ValueSources in the actual order they
// were parsed. The records are captured while parsing, therefore the order
// reflects real parse order rather than being reconstructed by re-reading the
// sources.
type ValueSource struct {
	// source is the caller-defined name of the data source (see NamedSource),
	// or the file name for sources given as a file path. It is empty for
	// anonymous in-memory data.
	source string
	// line is the 1-based line number where the assignment starts. A
	// multi-line value (continuation, quoted or Python-style) reports the line
	// of its first line.
	line int
	// column is the 1-based column where the key name starts after leading
	// whitespace is removed.
	column int

	// isShadow indicates whether the record belongs to a shadow value rather
	// than to the key's primary value.
	isShadow bool

	// prev is the record of the previous assignment to the same value slot
	// (primary value or one shadow value), i.e. the assignment this one
	// overwrote.
	prev *ValueSource
	// next is the record of the assignment that overwrote this one, if any.
	next *ValueSource
}

// Source returns the name of the data source the assignment came from. It is
// the name given via NamedSource, the file path when the source was given as a
// file name, or an empty string for anonymous in-memory data.
func (s *ValueSource) Source() string {
	return s.source
}

// Line returns the 1-based line number where the assignment starts.
func (s *ValueSource) Line() int {
	return s.line
}

// Column returns the 1-based column where the key name starts.
func (s *ValueSource) Column() int {
	return s.column
}

// Shadow reports whether the record belongs to a shadow value.
func (s *ValueSource) Shadow() bool {
	return s.isShadow
}

// Overridden returns the source record of the previous assignment to the same
// value slot, i.e. the value this assignment overwrote. It returns nil for the
// first assignment.
func (s *ValueSource) Overridden() *ValueSource {
	return s.prev
}

// Overrides returns the source record of the assignment that overwrote this
// one. It returns nil when this record is the current effective assignment.
func (s *ValueSource) Overrides() *ValueSource {
	return s.next
}
