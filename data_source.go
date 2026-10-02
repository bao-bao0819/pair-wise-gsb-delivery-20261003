// Copyright 2019 Unknwon
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
	"bytes"
	"fmt"
	"io"
	"os"
)

var (
	_ dataSource = (*sourceFile)(nil)
	_ dataSource = (*sourceData)(nil)
	_ dataSource = (*sourceReadCloser)(nil)
)

// dataSource is an interface that returns object which can be read and closed.
type dataSource interface {
	ReadCloser() (io.ReadCloser, error)
	// name returns the human-readable identifier of the data source used for
	// source tracking. File-backed sources use the file name, while anonymous
	// in-memory sources have an empty name.
	sourceName() string
}

// sourceFile represents an object that contains content on the local file system.
type sourceFile struct {
	name string
}

func (s sourceFile) ReadCloser() (_ io.ReadCloser, err error) {
	return os.Open(s.name)
}

func (s sourceFile) sourceName() string {
	return s.name
}

// sourceData represents an object that contains content in memory.
type sourceData struct {
	data []byte
}

func (s *sourceData) ReadCloser() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func (s *sourceData) sourceName() string {
	return ""
}

// sourceReadCloser represents an input stream with Close method.
type sourceReadCloser struct {
	reader io.ReadCloser
}

func (s *sourceReadCloser) ReadCloser() (io.ReadCloser, error) {
	return s.reader, nil
}

func (s *sourceReadCloser) sourceName() string {
	return ""
}

// NamedSource wraps a data source with a caller-defined name so that source
// tracking can report the origin of values without assuming every source is a
// file path. The wrapped Source accepts the same kinds of arguments as Load,
// i.e. a file name, []byte, io.Reader or io.ReadCloser.
type NamedSource struct {
	// Name is the identifier reported as the origin of parsed values.
	Name string
	// Source is the actual data source.
	Source interface{}
}

// namedDataSource decorates a dataSource with a caller-defined name.
type namedDataSource struct {
	dataSource
	name string
}

func (s *namedDataSource) sourceName() string {
	return s.name
}

func parseDataSource(source interface{}) (dataSource, error) {
	switch s := source.(type) {
	case NamedSource:
		return parseNamedSource(s)
	case *NamedSource:
		if s == nil {
			return nil, fmt.Errorf("error parsing data source: nil named source")
		}
		return parseNamedSource(*s)
	case string:
		return sourceFile{s}, nil
	case []byte:
		return &sourceData{s}, nil
	case io.ReadCloser:
		return &sourceReadCloser{s}, nil
	case io.Reader:
		return &sourceReadCloser{io.NopCloser(s)}, nil
	default:
		return nil, fmt.Errorf("error parsing data source: unknown type %q", s)
	}
}

func parseNamedSource(s NamedSource) (dataSource, error) {
	if _, ok := s.Source.(NamedSource); ok {
		return nil, fmt.Errorf("error parsing data source: nested named sources are not allowed")
	} else if _, ok := s.Source.(*NamedSource); ok {
		return nil, fmt.Errorf("error parsing data source: nested named sources are not allowed")
	}
	ds, err := parseDataSource(s.Source)
	if err != nil {
		return nil, err
	}
	return &namedDataSource{dataSource: ds, name: s.Name}, nil
}
