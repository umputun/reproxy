package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheControl_MiddlewareDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/file.html", http.NoBody)
	w := httptest.NewRecorder()

	h := NewCacheControl(time.Hour).Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("something"))
	}))
	h.ServeHTTP(w, req)
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "public, max-age=3600", resp.Header.Get("Cache-Control"))
}

func TestCacheControl_MiddlewarePrivate(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("something")) })
	for _, cc := range []*CacheControl{NewCacheControl(time.Hour), {defaultMaxAge: time.Hour, maxAges: map[string]time.Duration{"text/html": time.Minute}}} {
		req := httptest.NewRequest("GET", "/file.html", http.NoBody)
		req = req.WithContext(context.WithValue(req.Context(), ctxPrivateAsset, true))
		w := httptest.NewRecorder()
		cc.Middleware(next).ServeHTTP(w, req)
		assert.True(t, strings.HasPrefix(w.Header().Get("Cache-Control"), "private, max-age="), w.Header().Get("Cache-Control"))
	}
}

func TestCacheControl_MiddlewareKeepsRestriction(t *testing.T) {
	tbl := []struct {
		name    string
		values  []string
		private bool
		want    []string
	}{
		{name: "no-store kept", values: []string{"no-store"}, want: []string{"no-store"}},
		{name: "directive case ignored", values: []string{"No-Store, max-age=0"}, want: []string{"No-Store, max-age=0"}},
		{name: "bare private kept", values: []string{"private, max-age=10"}, want: []string{"private, max-age=10"}},
		{name: "second field value", values: []string{"max-age=60", "no-store"}, want: []string{"max-age=60", "no-store"}},
		{name: "qualified private replaced", values: []string{`private="Set-Cookie"`}, private: true, want: []string{"private, max-age=3600"}},
		{name: "qualified no-store replaced", values: []string{`no-store="x"`}, private: true, want: []string{"private, max-age=3600"}},
		{name: "quoted no-store replaced", values: []string{`no-cache="no-store, private"`}, want: []string{"public, max-age=3600"}},
		{name: "plain max-age replaced", values: []string{"max-age=60"}, want: []string{"public, max-age=3600"}},
	}
	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			next := NewCacheControl(time.Hour).Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("something"))
			}))
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, v := range tt.values {
					w.Header().Add("Cache-Control", v)
				}
				next.ServeHTTP(w, r)
			})
			req := httptest.NewRequest("GET", "/file.html", http.NoBody)
			if tt.private {
				req = req.WithContext(context.WithValue(req.Context(), ctxPrivateAsset, true))
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.Equal(t, tt.want, w.Header().Values("Cache-Control"))
		})
	}
}

func TestCacheControl_MiddlewareDisabled(t *testing.T) {
	req := httptest.NewRequest("GET", "/file.html", http.NoBody)
	w := httptest.NewRecorder()

	h := NewCacheControl(0).Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("something"))
	}))
	h.ServeHTTP(w, req)
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Cache-Control"))
}

func TestCacheControl_MiddlewareMime(t *testing.T) {
	cc := NewCacheControl(time.Hour)
	cc.AddMime("text/html", time.Hour*2)
	cc.AddMime("image/png", time.Hour*10)
	h := cc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("something"))
	}))

	t.Run("match on html", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/file.html", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "public, max-age=7200", resp.Header.Get("Cache-Control"), "match on .html")
	})

	t.Run("match on png", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/xyz/file.png?something=blah", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "public, max-age=36000", resp.Header.Get("Cache-Control"), "match on png")
	})

	t.Run("no match, default", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/xyz/file.gif?something=blah", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "public, max-age=3600", resp.Header.Get("Cache-Control"), "no match, default")
	})

	t.Run("no match, empty", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/xyz/", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "public, max-age=7200", resp.Header.Get("Cache-Control"), "match on empty (index)")
	})
}

func TestMakeCacheControl(t *testing.T) {

	tbl := []struct {
		opts     []string
		defAge   time.Duration
		mimeAges map[string]time.Duration
		err      error
	}{
		{nil, time.Duration(0), nil, nil},
		{[]string{"12h"}, 12 * time.Hour, nil, nil},
		{[]string{"12d"}, 12 * 24 * time.Hour, nil, nil},
		{[]string{"a12d"}, 0, nil,
			errors.New(`can't parse default cache duration: can't parse "a12d" as duration: strconv.Atoi: parsing "a12": invalid syntax`)},
		{[]string{"default:12h"}, 12 * time.Hour, nil, nil},
		{[]string{"blah:12h"}, 0, nil, errors.New("first cache duration has to be for the default mime")},
		{[]string{"a12nop"}, 0, nil, errors.New(`can't parse default cache duration: time: invalid duration "a12nop"`)},
		{[]string{"default:a12badone"}, 0, nil, errors.New(`can't parse default cache duration: time: invalid duration "a12badone"`)},

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
			assert.Len(t, res.maxAges, len(tt.mimeAges))
		})
	}

}
