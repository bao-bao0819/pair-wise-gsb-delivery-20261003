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
	_ dataSource = (*sourceNamed)(nil)
)

// dataSource is an interface that returns object which can be read and closed.
type dataSource interface {
	ReadCloser() (io.ReadCloser, error)
	// Name returns the identifier of the data source, which is used in source
	// tracking (see LoadOptions.TrackSource).
	Name() string
}

// sourceFile represents an object that contains content on the local file system.
type sourceFile struct {
	name string
}

func (s sourceFile) ReadCloser() (_ io.ReadCloser, err error) {
	return os.Open(s.name)
}

func (s sourceFile) Name() string {
	return s.name
}

// sourceData represents an object that contains content in memory.
type sourceData struct {
	data []byte
}

func (s *sourceData) ReadCloser() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func (s *sourceData) Name() string {
	return "data"
}

// sourceReadCloser represents an input stream with Close method.
type sourceReadCloser struct {
	reader io.ReadCloser
}

func (s *sourceReadCloser) ReadCloser() (io.ReadCloser, error) {
	return s.reader, nil
}

func (s *sourceReadCloser) Name() string {
	return "reader"
}

// sourceNamed represents a data source with a caller-provided name.
type sourceNamed struct {
	name string
	dataSource
}

func (s *sourceNamed) Name() string {
	return s.name
}

// NamedSource wraps any supported data source (file path, []byte, io.Reader
// or io.ReadCloser) with a caller-provided name. The name is used as the
// source identifier in source tracking (see LoadOptions.TrackSource) instead
// of the default identifier, which is especially useful for sources that do
// not have a file path.
type NamedSource struct {
	Name   string
	Source interface{}
}

func parseDataSource(source interface{}) (dataSource, error) {
	switch s := source.(type) {
	case string:
		return sourceFile{s}, nil
	case []byte:
		return &sourceData{s}, nil
	case NamedSource:
		return wrapNamedSource(s.Name, s.Source)
	case *NamedSource:
		return wrapNamedSource(s.Name, s.Source)
	case io.ReadCloser:
		return &sourceReadCloser{s}, nil
	case io.Reader:
		return &sourceReadCloser{io.NopCloser(s)}, nil
	default:
		return nil, fmt.Errorf("error parsing data source: unknown type %q", s)
	}
}

func wrapNamedSource(name string, source interface{}) (dataSource, error) {
	ds, err := parseDataSource(source)
	if err != nil {
		return nil, err
	}
	return &sourceNamed{name: name, dataSource: ds}, nil
}
