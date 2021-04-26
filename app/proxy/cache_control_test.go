package proxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheControl_MiddlewareDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/file.html", nil)
	w := httptest.NewRecorder()

	h := NewCacheControl(time.Hour).Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("something"))
	}))
	h.ServeHTTP(w, req)
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "public, max-age=3600", resp.Header.Get("Cache-Control"))
}

func TestCacheControl_MiddlewareDisabled(t *testing.T) {
	req := httptest.NewRequest("GET", "/file.html", nil)
	w := httptest.NewRecorder()

	h := NewCacheControl(0).Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("something"))
	}))
	h.ServeHTTP(w, req)
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "", resp.Header.Get("Cache-Control"))
}

func TestCacheControl_MiddlewareMime(t *testing.T) {

	cc := NewCacheControl(time.Hour)
	cc.AddMime("text/html", time.Hour*2)
	cc.AddMime("image/png", time.Hour*10)
	h := cc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("something"))
	}))

	{
		req := httptest.NewRequest("GET", "/file.html", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "public, max-age=7200", resp.Header.Get("Cache-Control"), "match on .html")
	}

	{
		req := httptest.NewRequest("GET", "/xyz/file.png?something=blah", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "public, max-age=36000", resp.Header.Get("Cache-Control"), "match on png")
	}

	{
		req := httptest.NewRequest("GET", "/xyz/file.gif?something=blah", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "public, max-age=3600", resp.Header.Get("Cache-Control"), "no match, default")
	}

	{
		req := httptest.NewRequest("GET", "/xyz/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "public, max-age=7200", resp.Header.Get("Cache-Control"), "match on empty (index)")
	}
}

func TestMakeCacheControl(t *testing.T) {

	type mimeAge struct {
		mime string
		age  time.Duration
	}

	tbl := []struct {
		opts     []string
		defAge   time.Duration
		mimeAges map[string]time.Duration
		err      error
	}{
		{nil, time.Duration(0), nil, nil},
		{[]string{"12h"}, 12 * time.Hour, nil, nil},
		{[]string{"default:12h"}, 12 * time.Hour, nil, nil},
		{[]string{"blah:12h"}, 0, nil, errors.New("first cache duration has to be for the default mime")},
		{[]string{"a12bad"}, 0, nil, errors.New(`can't parse default cache duration: time: invalid duration "a12bad"`)},
		{[]string{"default:a12bad"}, 0, nil, errors.New(`can't parse default cache duration: time: invalid duration "a12bad"`)},

		{[]string{"12h", "text/html:10h", "image/png:6h"}, 12 * time.Hour,
			map[string]time.Duration{"text/html": 10 * time.Hour, "image/png": 6 * time.Hour}, nil},
		{[]string{"12h", "10h", "image/png:6h"}, 0, nil, errors.New(`invalid mime:age entry "10h"`)},
		{[]string{"12h", "abc:10zzh", "image/png:6h"}, 0, nil,
			errors.New(`can't parse cache duration from abc:10zzh: time: unknown unit "zzh" in duration "10zzh"`)},
	}

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			res, err := MakeCacheControl(tt.opts)
			if tt.err != nil {
				require.EqualError(t, err, tt.err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.defAge, res.defaultMaxAge)
			for mime, age := range tt.mimeAges {
				assert.Equal(t, age, res.maxAges[mime])
			}
			assert.Equal(t, len(tt.mimeAges), len(res.maxAges))
		})
	}

}
