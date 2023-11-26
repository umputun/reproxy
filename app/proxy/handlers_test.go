package proxy

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/discovery"
)

func Test_headersHandler(t *testing.T) {
	wr := httptest.NewRecorder()
	handler := headersHandler([]string{"k1:v1", "k2:v2"}, []string{"r1", "r2"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		assert.Equal(t, "", r.Header.Get("r1"), "r1 header dropped")
		assert.Equal(t, "", r.Header.Get("r2"), "r2 header dropped")
		assert.Equal(t, "rv3", r.Header.Get("r3"), "r3 kept")
	}))
	req, err := http.NewRequest("GET", "http://example.com", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("r1", "rv1")
	req.Header.Set("r2", "rv2")
	req.Header.Set("r3", "rv3")
	handler.ServeHTTP(wr, req)
	assert.Equal(t, "v1", wr.Result().Header.Get("k1"))
	assert.Equal(t, "v2", wr.Result().Header.Get("k2"))
}

func Test_maxReqSizeHandler(t *testing.T) {
	{
		wr := httptest.NewRecorder()
		handler := maxReqSizeHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("req: %v", r)
		}))
		req, err := http.NewRequest("POST", "http://example.com", bytes.NewBufferString("123456"))
		require.NoError(t, err)
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusOK, wr.Result().StatusCode, "good size, full response")
	}
	{
		wr := httptest.NewRecorder()
		handler := maxReqSizeHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("req: %v", r)
		}))
		req, err := http.NewRequest("POST", "http://example.com", bytes.NewBufferString("123456789012345"))
		require.NoError(t, err)
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, wr.Result().StatusCode)
	}
	{
		wr := httptest.NewRecorder()
		handler := maxReqSizeHandler(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("req: %v", r)
		}))
		req, err := http.NewRequest("POST", "http://example.com", bytes.NewBufferString("123456"))
		require.NoError(t, err)
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusOK, wr.Result().StatusCode, "good size, full response")
	}
}

func Test_signatureHandler(t *testing.T) {
	{
		wr := httptest.NewRecorder()
		handler := signatureHandler(true, "v0.0.1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("req: %v", r)
		}))
		req, err := http.NewRequest("POST", "http://example.com", bytes.NewBufferString("123456"))
		require.NoError(t, err)
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusOK, wr.Result().StatusCode)
		assert.Equal(t, "reproxy", wr.Result().Header.Get("App-Name"), wr.Result().Header)
		assert.Equal(t, "umputun", wr.Result().Header.Get("Author"), wr.Result().Header)
		assert.Equal(t, "v0.0.1", wr.Result().Header.Get("App-Version"), wr.Result().Header)
	}
	{
		wr := httptest.NewRecorder()
		handler := signatureHandler(false, "v0.0.1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("req: %v", r)
		}))
		req, err := http.NewRequest("POST", "http://example.com", bytes.NewBufferString("123456"))
		require.NoError(t, err)
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusOK, wr.Result().StatusCode)
		assert.Equal(t, "", wr.Result().Header.Get("App-Name"), wr.Result().Header)
		assert.Equal(t, "", wr.Result().Header.Get("Author"), wr.Result().Header)
		assert.Equal(t, "", wr.Result().Header.Get("App-Version"), wr.Result().Header)
	}
}

func Test_limiterSystemHandler(t *testing.T) {

	var passed int32
	handler := limiterSystemHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&passed, 1)
	}))

	ts := httptest.NewServer(handler)
	var wg sync.WaitGroup
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func() {
			defer wg.Done()
			req, err := http.NewRequest("GET", ts.URL, http.NoBody)
			require.NoError(t, err)
			client := http.Client{}
			resp, err := client.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(10), atomic.LoadInt32(&passed))
}

func Test_limiterClientHandlerNoMatches(t *testing.T) {

	var passed int32
	handler := limiterUserHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&passed, 1)
	}))

	ts := httptest.NewServer(handler)
	var wg sync.WaitGroup
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func() {
			defer wg.Done()
			req, err := http.NewRequest("GET", ts.URL, http.NoBody)
			require.NoError(t, err)
			client := http.Client{}
			resp, err := client.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(10), atomic.LoadInt32(&passed))
}

func Test_limiterClientHandlerWithMatches(t *testing.T) {
	var passed int32
	handler := limiterUserHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&passed, 1)
	}))

	wrapWithContext := func(next http.Handler) http.Handler {
		var id int32
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := int(atomic.AddInt32(&id, 1))
			m := discovery.MatchedRoute{Mapper: discovery.URLMapper{Dst: strconv.Itoa(n % 2)}}
			ctx := context.WithValue(context.Background(), ctxMatchType, discovery.MTProxy)
			ctx = context.WithValue(ctx, ctxMatch, m)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	ts := httptest.NewServer(wrapWithContext(handler))

	var wg sync.WaitGroup
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func(id int) {
			defer wg.Done()
			req, err := http.NewRequest("POST", ts.URL, bytes.NewBufferString("123456"))
			require.NoError(t, err)
			m := discovery.MatchedRoute{Mapper: discovery.URLMapper{Dst: strconv.Itoa(id % 2)}}
			ctx := context.WithValue(context.Background(), ctxMatchType, discovery.MTProxy)
			ctx = context.WithValue(ctx, ctxMatch, m)
			req = req.WithContext(ctx)

			client := http.Client{}
			resp, err := client.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
		}(i)
	}
	wg.Wait()
	assert.Equal(t, int32(20), atomic.LoadInt32(&passed))
}

func TestHttp_basicAuthHandler(t *testing.T) {
	allowed := []string{
		"test:$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW",
		"test2:$2y$05$TLQqHh6VT4JxysdKGPOlJeSkkMsv.Ku/G45i7ssIm80XuouCrES12 ",
		"bad bad",
	}

	handler := basicAuthHandler(true, allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
	}))
	ts := httptest.NewServer(handler)

	client := http.Client{}

	tbl := []struct {
		reqFn func(r *http.Request)
		ok    bool
	}{
		{func(r *http.Request) {}, false},
		{func(r *http.Request) { r.SetBasicAuth("test", "passwd") }, true},
		{func(r *http.Request) { r.SetBasicAuth("test", "passwdbad") }, false},
		{func(r *http.Request) { r.SetBasicAuth("test2", "passwd2") }, true},
		{func(r *http.Request) { r.SetBasicAuth("test2", "passwbad") }, false},
		{func(r *http.Request) { r.SetBasicAuth("testbad", "passwbad") }, false},
	}

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			req, err := http.NewRequest("GET", ts.URL, http.NoBody)
			require.NoError(t, err)
			tt.reqFn(req)
			resp, err := client.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
			if tt.ok {
				require.Equal(t, http.StatusOK, resp.StatusCode)
				return
			}
			require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		})
	}

	handler = basicAuthHandler(false, allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
	}))
	ts2 := httptest.NewServer(handler)
	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			req, err := http.NewRequest("GET", ts2.URL, http.NoBody)
			require.NoError(t, err)
			tt.reqFn(req)
			resp, err := client.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}
