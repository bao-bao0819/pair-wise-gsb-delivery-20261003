package gziphandler

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	smallTestBody = "aaabbcaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbc"
	testBody      = "aaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbccc aaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbccc aaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbccc aaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbccc aaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbccc aaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbccc aaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbccc"
)

func TestParseEncodings(t *testing.T) {
	examples := map[string]codings{

		// Examples from RFC 2616
		"compress, gzip":                     {"compress": 1.0, "gzip": 1.0},
		"":                                   {},
		"*":                                  {"*": 1.0},
		"compress;q=0.5, gzip;q=1.0":         {"compress": 0.5, "gzip": 1.0},
		"gzip;q=1.0, identity; q=0.5, *;q=0": {"gzip": 1.0, "identity": 0.5, "*": 0.0},

		// More random stuff
		"AAA;q=1":     {"aaa": 1.0},
		"BBB ; q = 2": {"bbb": 1.0},
	}

	for eg, exp := range examples {
		act, _ := parseEncodings(eg)
		assert.Equal(t, exp, act)
	}
}

func TestGzipHandler(t *testing.T) {
	// This just exists to provide something for GzipHandler to wrap.
	handler := newTestHandler(testBody)

	// requests without accept-encoding are passed along as-is

	req1, _ := http.NewRequest("GET", "/whatever", nil)
	resp1 := httptest.NewRecorder()
	handler.ServeHTTP(resp1, req1)
	res1 := resp1.Result()

	assert.Equal(t, 200, res1.StatusCode)
	assert.Equal(t, "", res1.Header.Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", res1.Header.Get("Vary"))
	assert.Equal(t, testBody, resp1.Body.String())

	// but requests with accept-encoding:gzip are compressed if possible

	req2, _ := http.NewRequest("GET", "/whatever", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	resp2 := httptest.NewRecorder()
	handler.ServeHTTP(resp2, req2)
	res2 := resp2.Result()

	assert.Equal(t, 200, res2.StatusCode)
	assert.Equal(t, "gzip", res2.Header.Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", res2.Header.Get("Vary"))
	assert.Equal(t, gzipStrLevel(testBody, gzip.DefaultCompression), resp2.Body.Bytes())

	// content-type header is correctly set based on uncompressed body

	req3, _ := http.NewRequest("GET", "/whatever", nil)
	req3.Header.Set("Accept-Encoding", "gzip")
	res3 := httptest.NewRecorder()
	handler.ServeHTTP(res3, req3)

	assert.Equal(t, http.DetectContentType([]byte(testBody)), res3.Header().Get("Content-Type"))
}

func TestGzipHandlerSmallBodyNoCompression(t *testing.T) {
	handler := newTestHandler(smallTestBody)

	req, _ := http.NewRequest("GET", "/whatever", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	res := resp.Result()

	// with less than 1400 bytes the response should not be gzipped

	assert.Equal(t, 200, res.StatusCode)
	assert.Equal(t, "", res.Header.Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", res.Header.Get("Vary"))
	assert.Equal(t, smallTestBody, resp.Body.String())

}

func TestGzipHandlerAlreadyCompressed(t *testing.T) {
	handler := newTestHandler(testBody)

	req, _ := http.NewRequest("GET", "/gzipped", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	assert.Equal(t, testBody, res.Body.String())
}

func TestNewGzipLevelHandler(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, testBody)
	})

	for lvl := gzip.BestSpeed; lvl <= gzip.BestCompression; lvl++ {
		wrapper, err := NewGzipLevelHandler(lvl)
		if !assert.Nil(t, err, "NewGzipLevleHandler returned error for level:", lvl) {
			continue
		}

		req, _ := http.NewRequest("GET", "/whatever", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		resp := httptest.NewRecorder()
		wrapper(handler).ServeHTTP(resp, req)
		res := resp.Result()

		assert.Equal(t, 200, res.StatusCode)
		assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
		assert.Equal(t, "Accept-Encoding", res.Header.Get("Vary"))
		assert.Equal(t, gzipStrLevel(testBody, lvl), resp.Body.Bytes())
	}
}

func TestNewGzipLevelHandlerReturnsErrorForInvalidLevels(t *testing.T) {
	var err error
	_, err = NewGzipLevelHandler(-42)
	assert.NotNil(t, err)

	_, err = NewGzipLevelHandler(42)
	assert.NotNil(t, err)
}

func TestMustNewGzipLevelHandlerWillPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("panic was not called")
		}
	}()

	_ = MustNewGzipLevelHandler(-42)
}

func TestGzipHandlerNoBody(t *testing.T) {
	tests := []struct {
		statusCode      int
		contentEncoding string
		expectEmptyBody bool
		body            []byte
		method          string
	}{
		// Body must be empty.
		{http.StatusNoContent, "", true, nil, http.MethodGet},
		{http.StatusNotModified, "", true, nil, http.MethodGet},
		{http.StatusOK, "", true, []byte{}, http.MethodGet},
		{http.StatusOK, "gzip", false, []byte(testBody), http.MethodGet},
	}

	for num, test := range tests {
		handler := GzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(test.statusCode)
			if test.body != nil {
				w.Write(test.body)
			}
		}))

		rec := httptest.NewRecorder()
		// TODO: in Go1.7 httptest.NewRequest was introduced this should be used
		// once 1.6 is not longer supported.
		req := &http.Request{
			Method:     "GET",
			URL:        &url.URL{Path: "/"},
			Proto:      "HTTP/1.1",
			ProtoMinor: 1,
			RemoteAddr: "192.0.2.1:1234",
			Header:     make(http.Header),
		}
		req.Method = test.method
		req.Header.Set("Accept-Encoding", "gzip")
		handler.ServeHTTP(rec, req)

		body, err := ioutil.ReadAll(rec.Body)
		if err != nil {
			t.Fatalf("Unexpected error reading response body: %v", err)
		}

		header := rec.Header()
		assert.Equal(t, test.contentEncoding, header.Get("Content-Encoding"), fmt.Sprintf("for test iteration %d", num))
		assert.Equal(t, "Accept-Encoding", header.Get("Vary"), fmt.Sprintf("for test iteration %d", num))
		if test.expectEmptyBody {
			assert.Empty(t, body, fmt.Sprintf("for test iteration %d", num))
		} else {
			assert.NotEmpty(t, body, fmt.Sprintf("for test iteration %d", num))
			assert.NotEqual(t, test.body, body, fmt.Sprintf("for test iteration %d", num))
		}
	}
}

func TestGzipHandlerContentLength(t *testing.T) {
	testBodyBytes := []byte(testBody)
	tests := []struct {
		bodyLen    int
		bodies     [][]byte
		method     string
		compressed bool
	}{
		{len(testBody), [][]byte{testBodyBytes}, http.MethodGet, true},
		// each of these writes is less than the DefaultMinSize
		{len(testBody), [][]byte{testBodyBytes[:200], testBodyBytes[200:]}, http.MethodGet, true},
		// without a defined Content-Length it should still gzip
		{0, [][]byte{testBodyBytes[:200], testBodyBytes[200:]}, http.MethodGet, true},
		// simulate a HEAD request with an empty write (to populate headers)
		{len(testBody), [][]byte{nil}, http.MethodHead, false},
	}

	// httptest.NewRecorder doesn't give you access to the Content-Length
	// header so instead, we create a server on a random port and make
	// a request to that instead
	ln, err := net.Listen("tcp", "127.0.0.1:")
	if err != nil {
		t.Fatalf("failed creating listen socket: %v", err)
	}
	defer ln.Close()
	srv := &http.Server{
		Handler: nil,
	}
	go srv.Serve(ln)

	for num, test := range tests {
		srv.Handler = GzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if test.bodyLen > 0 {
				w.Header().Set("Content-Length", strconv.Itoa(test.bodyLen))
			}
			for _, b := range test.bodies {
				w.Write(b)
			}
		}))
		req := &http.Request{
			Method: test.method,
			URL:    &url.URL{Path: "/", Scheme: "http", Host: ln.Addr().String()},
			Header: make(http.Header),
			Close:  true,
		}
		req.Header.Set("Accept-Encoding", "gzip")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Unexpected error making http request in test iteration %d: %v", num, err)
		}
		defer res.Body.Close()

		body, err := ioutil.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("Unexpected error reading response body in test iteration %d: %v", num, err)
		}

		l, err := strconv.Atoi(res.Header.Get("Content-Length"))
		if err != nil {
			t.Fatalf("Unexpected error parsing Content-Length in test iteration %d: %v", num, err)
		}
		if !test.compressed {
			assert.Empty(t, body, fmt.Sprintf("for test iteration %d", num))
			expectedLength := 0
			if test.method == http.MethodHead {
				expectedLength = test.bodyLen
			}
			assert.Equal(t, expectedLength, l, fmt.Sprintf("for test iteration %d", num))
		} else {
			assert.Len(t, body, l, fmt.Sprintf("for test iteration %d", num))
		}
		expectedEncoding := ""
		if test.compressed {
			expectedEncoding = "gzip"
		}
		assert.Equal(t, expectedEncoding, res.Header.Get("Content-Encoding"), fmt.Sprintf("for test iteration %d", num))
		if test.compressed {
			assert.NotEqual(t, test.bodyLen, l, fmt.Sprintf("for test iteration %d", num))
		}
	}
}

func TestGzipHandlerMinSizeMustBePositive(t *testing.T) {
	_, err := NewGzipLevelAndMinSize(gzip.DefaultCompression, -1)
	assert.Error(t, err)
}

func TestGzipHandlerMinSize(t *testing.T) {
	responseLength := 0
	b := []byte{'x'}

	wrapper, _ := NewGzipLevelAndMinSize(gzip.DefaultCompression, 128)
	handler := wrapper(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			// Write responses one byte at a time to ensure that the flush
			// mechanism, if used, is working properly.
			for i := 0; i < responseLength; i++ {
				n, err := w.Write(b)
				assert.Equal(t, 1, n)
				assert.Nil(t, err)
			}
		},
	))

	r, _ := http.NewRequest("GET", "/whatever", &bytes.Buffer{})
	r.Header.Add("Accept-Encoding", "gzip")

	// Short response is not compressed
	responseLength = 127
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Result().Header.Get(contentEncoding) == "gzip" {
		t.Error("Expected uncompressed response, got compressed")
	}

	// Long response is not compressed
	responseLength = 128
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Result().Header.Get(contentEncoding) != "gzip" {
		t.Error("Expected compressed response, got uncompressed")
	}
}

func TestGzipDoubleClose(t *testing.T) {
	// reset the pool for the default compression so we can make sure duplicates
	// aren't added back by double close
	addLevelPool(gzip.DefaultCompression)

	handler := GzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// call close here and it'll get called again interally by
		// NewGzipLevelHandler's handler defer
		w.Write([]byte("test"))
		w.(io.Closer).Close()
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	// the second close shouldn't have added the same writer
	// so we pull out 2 writers from the pool and make sure they're different
	w1 := gzipWriterPools[poolIndex(gzip.DefaultCompression)].Get()
	w2 := gzipWriterPools[poolIndex(gzip.DefaultCompression)].Get()
	// assert.NotEqual looks at the value and not the address, so we use regular ==
	assert.False(t, w1 == w2)
}

type panicOnSecondWriteHeaderWriter struct {
	http.ResponseWriter
	headerWritten bool
}

func (w *panicOnSecondWriteHeaderWriter) WriteHeader(s int) {
	if w.headerWritten {
		panic("header already written")
	}
	w.headerWritten = true
	w.ResponseWriter.WriteHeader(s)
}

func TestGzipHandlerDoubleWriteHeader(t *testing.T) {
	handler := GzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "15000")
		// Specifically write the header here
		w.WriteHeader(304)
		// Ensure that after a Write the header isn't triggered again on close
		w.Write(nil)
	}))
	wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = &panicOnSecondWriteHeaderWriter{
			ResponseWriter: w,
		}
		handler.ServeHTTP(w, r)
	})

	rec := httptest.NewRecorder()
	// TODO: in Go1.7 httptest.NewRequest was introduced this should be used
	// once 1.6 is not longer supported.
	req := &http.Request{
		Method:     "GET",
		URL:        &url.URL{Path: "/"},
		Proto:      "HTTP/1.1",
		ProtoMinor: 1,
		RemoteAddr: "192.0.2.1:1234",
		Header:     make(http.Header),
	}
	req.Header.Set("Accept-Encoding", "gzip")
	wrapper.ServeHTTP(rec, req)
	body, err := ioutil.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("Unexpected error reading response body: %v", err)
	}
	assert.Empty(t, body)
	header := rec.Header()
	assert.Equal(t, "", header.Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", header.Get("Vary"))
	assert.Equal(t, 304, rec.Code)
}

func TestStatusCodes(t *testing.T) {
	handler := GzipHandler(http.NotFoundHandler())
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	result := w.Result()
	if result.StatusCode != 404 {
		t.Errorf("StatusCode should have been 404 but was %d", result.StatusCode)
	}
}

func TestFlushBeforeWrite(t *testing.T) {
	b := []byte(testBody)
	handler := GzipHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusNotFound)
		rw.(http.Flusher).Flush()
		rw.Write(b)
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	res := w.Result()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
	assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
	assert.NotEqual(t, b, w.Body.Bytes())
}

func TestImplementCloseNotifier(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(acceptEncoding, "gzip")
	GzipHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		_, ok := rw.(http.CloseNotifier)
		assert.True(t, ok, "response writer must implement http.CloseNotifier")
	})).ServeHTTP(&mockRWCloseNotify{}, request)
}

func TestImplementFlusherAndCloseNotifier(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(acceptEncoding, "gzip")
	GzipHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		_, okCloseNotifier := rw.(http.CloseNotifier)
		assert.True(t, okCloseNotifier, "response writer must implement http.CloseNotifier")
		_, okFlusher := rw.(http.Flusher)
		assert.True(t, okFlusher, "response writer must implement http.Flusher")
	})).ServeHTTP(&mockRWCloseNotify{}, request)
}

func TestNotImplementCloseNotifier(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(acceptEncoding, "gzip")
	GzipHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		_, ok := rw.(http.CloseNotifier)
		assert.False(t, ok, "response writer must not implement http.CloseNotifier")
	})).ServeHTTP(httptest.NewRecorder(), request)
}

type mockRWCloseNotify struct{}

func (m *mockRWCloseNotify) CloseNotify() <-chan bool {
	panic("implement me")
}

func (m *mockRWCloseNotify) Header() http.Header {
	return http.Header{}
}

func (m *mockRWCloseNotify) Write([]byte) (int, error) {
	panic("implement me")
}

func (m *mockRWCloseNotify) WriteHeader(int) {
	panic("implement me")
}

func TestIgnoreSubsequentWriteHeader(t *testing.T) {
	handler := GzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.WriteHeader(404)
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	result := w.Result()
	if result.StatusCode != 500 {
		t.Errorf("StatusCode should have been 500 but was %d", result.StatusCode)
	}
}

func TestDontWriteWhenNotWrittenTo(t *testing.T) {
	// When using gzip as middleware without ANY writes in the handler,
	// ensure the gzip middleware doesn't touch the actual ResponseWriter
	// either.

	handler0 := GzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	}))

	handler1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler0.ServeHTTP(w, r)
		w.WriteHeader(404) // this only works if gzip didn't do a WriteHeader(200)
	})

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler1.ServeHTTP(w, r)

	result := w.Result()
	if result.StatusCode != 404 {
		t.Errorf("StatusCode should have been 404 but was %d", result.StatusCode)
	}
}

var contentTypeTests = []struct {
	name                 string
	contentType          string
	acceptedContentTypes []string
	expectedGzip         bool
}{
	{
		name:                 "Always gzip when content types are empty",
		contentType:          "",
		acceptedContentTypes: []string{},
		expectedGzip:         true,
	},
	{
		name:                 "MIME match",
		contentType:          "application/json",
		acceptedContentTypes: []string{"application/json"},
		expectedGzip:         true,
	},
	{
		name:                 "MIME no match",
		contentType:          "text/xml",
		acceptedContentTypes: []string{"application/json"},
		expectedGzip:         false,
	},
	{
		name:                 "MIME match with no other directive ignores non-MIME directives",
		contentType:          "application/json; charset=utf-8",
		acceptedContentTypes: []string{"application/json"},
		expectedGzip:         true,
	},
	{
		name:                 "MIME match with other directives requires all directives be equal, different charset",
		contentType:          "application/json; charset=ascii",
		acceptedContentTypes: []string{"application/json; charset=utf-8"},
		expectedGzip:         false,
	},
	{
		name:                 "MIME match with other directives requires all directives be equal, same charset",
		contentType:          "application/json; charset=utf-8",
		acceptedContentTypes: []string{"application/json; charset=utf-8"},
		expectedGzip:         true,
	},
	{
		name:                 "MIME match with other directives requires all directives be equal, missing charset",
		contentType:          "application/json",
		acceptedContentTypes: []string{"application/json; charset=ascii"},
		expectedGzip:         false,
	},
	{
		name:                 "MIME match case insensitive",
		contentType:          "Application/Json",
		acceptedContentTypes: []string{"application/json"},
		expectedGzip:         true,
	},
	{
		name:                 "MIME match ignore whitespace",
		contentType:          "application/json;charset=utf-8",
		acceptedContentTypes: []string{"application/json;            charset=utf-8"},
		expectedGzip:         true,
	},
}

func TestContentTypes(t *testing.T) {
	for _, tt := range contentTypeTests {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", tt.contentType)
			io.WriteString(w, testBody)
		})

		wrapper, err := GzipHandlerWithOpts(ContentTypes(tt.acceptedContentTypes))
		if !assert.Nil(t, err, "NewGzipHandlerWithOpts returned error", tt.name) {
			continue
		}

		req, _ := http.NewRequest("GET", "/whatever", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		resp := httptest.NewRecorder()
		wrapper(handler).ServeHTTP(resp, req)
		res := resp.Result()

		assert.Equal(t, 200, res.StatusCode)
		if tt.expectedGzip {
			assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"), tt.name)
		} else {
			assert.NotEqual(t, "gzip", res.Header.Get("Content-Encoding"), tt.name)
		}
	}
}

func TestETagPolicy(t *testing.T) {
	tests := []struct {
		name     string
		header   []string
		policy   ETagPolicyFunc
		expected []string
	}{
		{
			name:     "delete strong",
			header:   []string{`"v1"`},
			policy:   DeleteStrongETag,
			expected: nil,
		},
		{
			name:     "delete preserves weak",
			header:   []string{`W/"v1"`},
			policy:   DeleteStrongETag,
			expected: []string{`W/"v1"`},
		},
		{
			name:     "weaken strong",
			header:   []string{`"v1"`},
			policy:   WeakenStrongETag,
			expected: []string{`W/"v1"`},
		},
		{
			name:     "weaken does not duplicate prefix",
			header:   []string{`W/"v1"`},
			policy:   WeakenStrongETag,
			expected: []string{`W/"v1"`},
		},
		{
			name:     "caller replaces tag by encoding",
			header:   []string{`"v1"`},
			policy:   func(tag, encoding string) string { return `"` + encoding + `:v1"` },
			expected: []string{`"gzip:v1"`},
		},
		{
			name:     "caller preserves tag",
			header:   []string{`"v1"`},
			policy:   func(tag, encoding string) string { return tag },
			expected: []string{`"v1"`},
		},
		{
			name:     "multiple values in one header are removed",
			header:   []string{`"v1", "v2"`},
			policy:   WeakenStrongETag,
			expected: nil,
		},
		{
			name:     "multiple header values are removed",
			header:   []string{`"v1"`, `"v2"`},
			policy:   WeakenStrongETag,
			expected: nil,
		},
		{
			name:     "invalid tag is removed",
			header:   []string{`v1`},
			policy:   WeakenStrongETag,
			expected: nil,
		},
		{
			name:     "invalid policy result is removed",
			header:   []string{`"v1"`},
			policy:   func(tag, encoding string) string { return "v1" },
			expected: nil,
		},
	}

	for _, test := range tests {
		wrapper, err := GzipHandlerWithOpts(
			MinSize(1),
			ETagPolicy(test.policy),
		)
		if !assert.NoError(t, err, test.name) {
			continue
		}

		handler := wrapper(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, value := range test.header {
				w.Header().Add("Etag", value)
			}
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, testBody)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, "gzip", rec.Header().Get(contentEncoding), test.name)
		assert.Equal(t, test.expected, rec.Header()["Etag"], test.name)
	}
}

func TestETagPolicyPassthrough(t *testing.T) {
	calls := 0
	policy := func(tag, encoding string) string {
		calls++
		return `W/"new"`
	}

	tests := []struct {
		name       string
		method     string
		statusCode int
		body       string
		minSize    int
		etag       string
	}{
		{"below threshold", http.MethodGet, http.StatusOK, smallTestBody, 1400, `"original"`},
		{"malformed below threshold", http.MethodGet, http.StatusOK, smallTestBody, 1400, `original`},
		{"204", http.MethodGet, http.StatusNoContent, "", 1, `"original"`},
		{"304", http.MethodGet, http.StatusNotModified, "", 1, `"original"`},
		{"HEAD", http.MethodHead, http.StatusOK, testBody, 1, `"original"`},
	}

	for _, test := range tests {
		calls = 0
		wrapper, err := GzipHandlerWithOpts(
			MinSize(test.minSize),
			ETagPolicy(policy),
		)
		if !assert.NoError(t, err, test.name) {
			continue
		}

		handler := wrapper(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Etag", test.etag)
			w.WriteHeader(test.statusCode)
			io.WriteString(w, test.body)
		}))

		req := httptest.NewRequest(test.method, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, "", rec.Header().Get(contentEncoding), test.name)
		assert.Equal(t, test.etag, rec.Header().Get("Etag"), test.name)
		assert.Equal(t, 0, calls, test.name)
	}
}

func TestETagPolicyReceivesCurrentTagAndEncoding(t *testing.T) {
	var gotTag string
	var gotEncoding string

	wrapper, err := GzipHandlerWithOpts(
		MinSize(1),
		ETagPolicy(func(tag, encoding string) string {
			gotTag = tag
			gotEncoding = encoding
			return tag
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	handler := wrapper(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", `"v1"`)
		io.WriteString(w, testBody)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, `"v1"`, gotTag)
	assert.Equal(t, "gzip", gotEncoding)
	assert.Equal(t, `"v1"`, rec.Header().Get("Etag"))
}

func TestETagDefaultsToPreservingHeader(t *testing.T) {
	wrapper, err := GzipHandlerWithOpts(MinSize(1))
	if err != nil {
		t.Fatal(err)
	}

	handler := wrapper(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", `"v1"`)
		io.WriteString(w, testBody)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, "gzip", rec.Header().Get(contentEncoding))
	assert.Equal(t, `"v1"`, rec.Header().Get("Etag"))
}

type etagCommitRecorder struct {
	*httptest.ResponseRecorder
	committed string
}

func (w *etagCommitRecorder) WriteHeader(statusCode int) {
	w.committed = w.Header().Get("Etag")
	w.ResponseRecorder.WriteHeader(statusCode)
}

func TestETagPolicyRunsBeforeHeaderCommit(t *testing.T) {
	wrapper, err := GzipHandlerWithOpts(
		MinSize(1),
		ETagPolicy(WeakenStrongETag),
	)
	if err != nil {
		t.Fatal(err)
	}

	handler := wrapper(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", `"original"`)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, testBody)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := &etagCommitRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(rec, req)

	assert.Equal(t, `W/"original"`, rec.committed)
	assert.Equal(t, `W/"original"`, rec.Header().Get("Etag"))
}

func TestParseETag(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{`"strong"`, true},
		{`W/"weak"`, true},
		{` W/"weak" `, true},
		{`W/W/"weak"`, false},
		{`w/"weak"`, false},
		{`"missing-quote`, false},
		{`"has,comma"`, true},
		{`""`, false},
		{``, false},
	}

	for _, test := range tests {
		_, valid := parseETag(test.value)
		assert.Equal(t, test.valid, valid, test.value)
	}
}

func TestHasMultipleETags(t *testing.T) {
	assert.True(t, hasMultipleETags(`"a", "b"`))
	assert.True(t, hasMultipleETags(`W/"a", W/"b"`))
	assert.False(t, hasMultipleETags(`"a,b"`))
	assert.False(t, hasMultipleETags(`W/"a"`))
}

// --------------------------------------------------------------------

func BenchmarkGzipHandler_S2k(b *testing.B)   { benchmark(b, false, 2048) }
func BenchmarkGzipHandler_S20k(b *testing.B)  { benchmark(b, false, 20480) }
func BenchmarkGzipHandler_S100k(b *testing.B) { benchmark(b, false, 102400) }
func BenchmarkGzipHandler_P2k(b *testing.B)   { benchmark(b, true, 2048) }
func BenchmarkGzipHandler_P20k(b *testing.B)  { benchmark(b, true, 20480) }
func BenchmarkGzipHandler_P100k(b *testing.B) { benchmark(b, true, 102400) }

// --------------------------------------------------------------------

func gzipStrLevel(s string, lvl int) []byte {
	var b bytes.Buffer
	w, _ := gzip.NewWriterLevel(&b, lvl)
	io.WriteString(w, s)
	w.Close()
	return b.Bytes()
}

func benchmark(b *testing.B, parallel bool, size int) {
	bin, err := ioutil.ReadFile("testdata/benchmark.json")
	if err != nil {
		b.Fatal(err)
	}

	req, _ := http.NewRequest("GET", "/whatever", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler := newTestHandler(string(bin[:size]))

	if parallel {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				runBenchmark(b, req, handler)
			}
		})
	} else {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			runBenchmark(b, req, handler)
		}
	}
}

func runBenchmark(b *testing.B, req *http.Request, handler http.Handler) {
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if code := res.Code; code != 200 {
		b.Fatalf("Expected 200 but got %d", code)
	} else if blen := res.Body.Len(); blen < 500 {
		b.Fatalf("Expected complete response body, but got %d bytes", blen)
	}
}

func newTestHandler(body string) http.Handler {
	return GzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gzipped":
			w.Header().Set("Content-Encoding", "gzip")
			io.WriteString(w, body)
		default:
			io.WriteString(w, body)
		}
	}))
}
