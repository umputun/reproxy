package proxy

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/umputun/reproxy/app/discovery"
)

// deadlineRecorder wraps httptest.ResponseRecorder and records SetReadDeadline / SetWriteDeadline
// calls so the routeTimeoutHandler tests can assert deadline behavior. It exposes the deadline
// setters directly so http.NewResponseController unwraps it via the documented setter-method
// interface, even though the embedded *httptest.ResponseRecorder is itself a deadline-less writer.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	mu          sync.Mutex
	readCalls   []time.Time
	writeCalls  []time.Time
	readSetErr  error
	writeSetErr error
}

func newDeadlineRecorder() *deadlineRecorder {
	return &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (d *deadlineRecorder) SetReadDeadline(t time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.readCalls = append(d.readCalls, t)
	return d.readSetErr
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writeCalls = append(d.writeCalls, t)
	return d.writeSetErr
}

func (d *deadlineRecorder) recorded() (read, write []time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]time.Time(nil), d.readCalls...), append([]time.Time(nil), d.writeCalls...)
}

func Test_headersHandler(t *testing.T) {
	wr := httptest.NewRecorder()
	handler := headersHandler([]string{"k1:v1", "k2:v2"}, []string{"r1", "r2"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		assert.Empty(t, r.Header.Get("r1"), "r1 header dropped")
		assert.Empty(t, r.Header.Get("r2"), "r2 header dropped")
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
	t.Run("good size", func(t *testing.T) {
		wr := httptest.NewRecorder()
		handler := maxReqSizeHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("req: %v", r)
		}))
		req, err := http.NewRequest("POST", "http://example.com", bytes.NewBufferString("123456"))
		require.NoError(t, err)
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusOK, wr.Result().StatusCode, "good size, full response")
	})

	t.Run("too large size", func(t *testing.T) {
		wr := httptest.NewRecorder()
		handler := maxReqSizeHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("req: %v", r)
		}))
		req, err := http.NewRequest("POST", "http://example.com", bytes.NewBufferString("123456789012345"))
		require.NoError(t, err)
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, wr.Result().StatusCode)
	})

	t.Run("zero max size", func(t *testing.T) {
		wr := httptest.NewRecorder()
		handler := maxReqSizeHandler(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("req: %v", r)
		}))
		req, err := http.NewRequest("POST", "http://example.com", bytes.NewBufferString("123456"))
		require.NoError(t, err)
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusOK, wr.Result().StatusCode, "good size, full response")
	})

	t.Run("too large request size", func(t *testing.T) {
		wr := httptest.NewRecorder()
		handler := maxReqSizeHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("req: %v", r)
		}))
		req, err := http.NewRequest("GET", "http://example.com?q=123456789012345", http.NoBody)
		require.NoError(t, err)
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusRequestURITooLong, wr.Result().StatusCode)
	})

	t.Run("good request size", func(t *testing.T) {
		wr := httptest.NewRecorder()
		handler := maxReqSizeHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("req: %v", r)
		}))
		req, err := http.NewRequest("GET", "http://example.com?q=12345678", http.NoBody)
		require.NoError(t, err)
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusOK, wr.Result().StatusCode)
	})
}

func Test_signatureHandler(t *testing.T) {
	t.Run("with signature", func(t *testing.T) {
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
	})

	t.Run("without signature", func(t *testing.T) {
		wr := httptest.NewRecorder()
		handler := signatureHandler(false, "v0.0.1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Logf("req: %v", r)
		}))
		req, err := http.NewRequest("POST", "http://example.com", bytes.NewBufferString("123456"))
		require.NoError(t, err)
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusOK, wr.Result().StatusCode)
		assert.Empty(t, wr.Result().Header.Get("App-Name"), wr.Result().Header)
		assert.Empty(t, wr.Result().Header.Get("Author"), wr.Result().Header)
		assert.Empty(t, wr.Result().Header.Get("App-Version"), wr.Result().Header)
	})
}

func Test_limiterSystemHandler(t *testing.T) {

	var passed atomic.Int32
	handler := limiterSystemHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		passed.Add(1)
	}))

	ts := httptest.NewServer(handler)
	var wg sync.WaitGroup
	wg.Add(100)
	for range 100 {
		go func() {
			defer wg.Done()
			req, err := http.NewRequest("GET", ts.URL, http.NoBody)
			assert.NoError(t, err)
			client := http.Client{}
			resp, err := client.Do(req)
			assert.NoError(t, err)
			if resp != nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(10), passed.Load())
}

func Test_limiterClientHandlerNoMatches(t *testing.T) {

	var passed atomic.Int32
	handler := limiterUserHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		passed.Add(1)
	}))

	ts := httptest.NewServer(handler)
	var wg sync.WaitGroup
	wg.Add(100)
	for range 100 {
		go func() {
			defer wg.Done()
			req, err := http.NewRequest("GET", ts.URL, http.NoBody)
			assert.NoError(t, err)
			client := http.Client{}
			resp, err := client.Do(req)
			assert.NoError(t, err)
			if resp != nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(10), passed.Load())
}

func Test_limiterClientHandlerWithMatches(t *testing.T) {
	var passed atomic.Int32
	handler := limiterUserHandler(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		passed.Add(1)
	}))

	wrapWithContext := func(next http.Handler) http.Handler {
		var id atomic.Int32
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := int(id.Add(1))
			m := discovery.MatchedRoute{Mapper: discovery.URLMapper{Dst: strconv.Itoa(n % 2)}}
			ctx := context.WithValue(context.Background(), ctxMatchType, discovery.MTProxy)
			ctx = context.WithValue(ctx, ctxMatch, m)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	ts := httptest.NewServer(wrapWithContext(handler))

	var wg sync.WaitGroup
	wg.Add(100)
	for i := range 100 {
		go func(id int) {
			defer wg.Done()
			req, err := http.NewRequest("POST", ts.URL, bytes.NewBufferString("123456"))
			assert.NoError(t, err)
			m := discovery.MatchedRoute{Mapper: discovery.URLMapper{Dst: strconv.Itoa(id % 2)}}
			ctx := context.WithValue(context.Background(), ctxMatchType, discovery.MTProxy)
			ctx = context.WithValue(ctx, ctxMatch, m)
			req = req.WithContext(ctx)

			client := http.Client{}
			resp, err := client.Do(req)
			assert.NoError(t, err)
			resp.Body.Close()
		}(i)
	}
	wg.Wait()
	assert.Equal(t, int32(20), passed.Load())
}

func TestHttp_basicAuthHandler(t *testing.T) {
	allowed := []string{
		"test:$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW",
		"test2:$2y$05$TLQqHh6VT4JxysdKGPOlJeSkkMsv.Ku/G45i7ssIm80XuouCrES12 ",
		"bad bad",
	}

	handler := globalBasicAuthHandler(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	handler = passThroughHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func Test_globalBasicAuthHandler_SkipsPerRouteAuth(t *testing.T) {
	// when route has per-route auth configured, global auth should skip
	allowed := []string{"globaluser:$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW"}

	handler := globalBasicAuthHandler(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("no context, requires global auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("route has per-route auth, global auth skipped", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
		req = req.WithContext(context.WithValue(req.Context(), ctxMatch,
			discovery.MatchedRoute{Mapper: discovery.URLMapper{AuthUsers: []string{"routeuser:hash"}}}))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		// should pass without requiring credentials since per-route auth is configured
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("route has no per-route auth, global auth applies", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
		req = req.WithContext(context.WithValue(req.Context(), ctxMatch,
			discovery.MatchedRoute{Mapper: discovery.URLMapper{AuthUsers: []string{}}}))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}

func Test_perRouteAuthHandler(t *testing.T) {
	// generate bcrypt hashes for test passwords
	hash1, err := bcrypt.GenerateFromPassword([]byte("passwd1"), bcrypt.DefaultCost)
	require.NoError(t, err)
	hash2, err := bcrypt.GenerateFromPassword([]byte("passwd2"), bcrypt.DefaultCost)
	require.NoError(t, err)

	tbl := []struct {
		name               string
		authUsers          []string
		setAuth            func(r *http.Request)
		expectedStatusCode int
	}{
		{name: "no auth required, no credentials", authUsers: []string{}, setAuth: func(r *http.Request) {}, expectedStatusCode: http.StatusOK},
		{name: "no auth required, credentials provided", authUsers: []string{}, setAuth: func(r *http.Request) { r.SetBasicAuth("user1", "passwd1") }, expectedStatusCode: http.StatusOK},
		{name: "auth required, valid credentials", authUsers: []string{"user1:" + string(hash1)}, setAuth: func(r *http.Request) { r.SetBasicAuth("user1", "passwd1") }, expectedStatusCode: http.StatusOK},
		{name: "auth required, no credentials", authUsers: []string{"user1:" + string(hash1)}, setAuth: func(r *http.Request) {}, expectedStatusCode: http.StatusUnauthorized},
		{name: "auth required, wrong password", authUsers: []string{"user1:" + string(hash1)}, setAuth: func(r *http.Request) { r.SetBasicAuth("user1", "wrongpasswd") }, expectedStatusCode: http.StatusUnauthorized},
		{name: "auth required, unknown user", authUsers: []string{"user1:" + string(hash1)}, setAuth: func(r *http.Request) { r.SetBasicAuth("unknownuser", "passwd1") }, expectedStatusCode: http.StatusUnauthorized},
		{name: "multiple users, first user valid", authUsers: []string{"user1:" + string(hash1), "user2:" + string(hash2)}, setAuth: func(r *http.Request) { r.SetBasicAuth("user1", "passwd1") }, expectedStatusCode: http.StatusOK},
		{name: "multiple users, second user valid", authUsers: []string{"user1:" + string(hash1), "user2:" + string(hash2)}, setAuth: func(r *http.Request) { r.SetBasicAuth("user2", "passwd2") }, expectedStatusCode: http.StatusOK},
		{name: "multiple users, both wrong", authUsers: []string{"user1:" + string(hash1), "user2:" + string(hash2)}, setAuth: func(r *http.Request) { r.SetBasicAuth("user3", "passwd3") }, expectedStatusCode: http.StatusUnauthorized},
		{name: "malformed auth entry ignored", authUsers: []string{"malformed", "user1:" + string(hash1)}, setAuth: func(r *http.Request) { r.SetBasicAuth("user1", "passwd1") }, expectedStatusCode: http.StatusOK},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			handler := perRouteAuthHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
			tt.setAuth(req)
			req = req.WithContext(context.WithValue(req.Context(),
				ctxMatch, discovery.MatchedRoute{Mapper: discovery.URLMapper{AuthUsers: tt.authUsers}}))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatusCode, rr.Code)
			if tt.expectedStatusCode == http.StatusUnauthorized {
				assert.Equal(t, `Basic realm="Restricted"`, rr.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func Test_perRouteAuthHandler_NoContext(t *testing.T) {
	// test when no context match is set (should pass through)
	handler := perRouteAuthHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func Test_validateBasicAuthCredentials(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	require.NoError(t, err)

	hashWithColon, err := bcrypt.GenerateFromPassword([]byte("pass:word:123"), bcrypt.DefaultCost)
	require.NoError(t, err)

	tbl := []struct {
		name     string
		username string
		password string
		allowed  []string
		expected bool
	}{
		{name: "valid credentials", username: "admin", password: "secret", allowed: []string{"admin:" + string(hash)}, expected: true},
		{name: "password with colons", username: "admin", password: "pass:word:123", allowed: []string{"admin:" + string(hashWithColon)}, expected: true},
		{name: "empty password fails against real hash", username: "admin", password: "", allowed: []string{"admin:" + string(hash)}, expected: false},
		{name: "invalid password", username: "admin", password: "wrong", allowed: []string{"admin:" + string(hash)}, expected: false},
		{name: "invalid username", username: "wrong", password: "secret", allowed: []string{"admin:" + string(hash)}, expected: false},
		{name: "empty allowed list", username: "admin", password: "secret", allowed: []string{}, expected: false},
		{name: "malformed entry", username: "admin", password: "secret", allowed: []string{"no-colon"}, expected: false},
		{name: "invalid bcrypt hash", username: "admin", password: "secret", allowed: []string{"admin:not-a-valid-bcrypt"}, expected: false},
		{name: "whitespace-only entry", username: "admin", password: "secret", allowed: []string{"   "}, expected: false},
		{name: "empty username with hash", username: "", password: "secret", allowed: []string{":" + string(hash)}, expected: false},
		{name: "username with colon rejected (htpasswd limitation)", username: "user:name", password: "secret", allowed: []string{"user:name:" + string(hash)}, expected: false},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			result := validateBasicAuthCredentials(tt.username, tt.password, tt.allowed)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_gzipHandler(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		handler := gzipHandler(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("test response"))
		}))
		req := httptest.NewRequest("GET", "http://example.com", http.NoBody)
		wr := httptest.NewRecorder()
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusOK, wr.Code)
		assert.Equal(t, "test response", wr.Body.String())
		assert.Empty(t, wr.Header().Get("Content-Encoding"))
	})

	t.Run("enabled with gzip accept", func(t *testing.T) {
		handler := gzipHandler(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// write enough data to trigger compression
			data := make([]byte, 1024)
			for i := range data {
				data[i] = 'a'
			}
			_, _ = w.Write(data)
		}))
		req := httptest.NewRequest("GET", "http://example.com", http.NoBody)
		req.Header.Set("Accept-Encoding", "gzip")
		wr := httptest.NewRecorder()
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusOK, wr.Code)
		assert.Equal(t, "gzip", wr.Header().Get("Content-Encoding"))
	})

	t.Run("enabled without gzip accept", func(t *testing.T) {
		handler := gzipHandler(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("test response"))
		}))
		req := httptest.NewRequest("GET", "http://example.com", http.NoBody)
		wr := httptest.NewRecorder()
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusOK, wr.Code)
		assert.Empty(t, wr.Header().Get("Content-Encoding"))
	})
}

func TestHeaders_CSPParsing(t *testing.T) {
	tbl := []struct {
		name     string
		headers  []string
		expected map[string]string
	}{
		{
			name:     "simple headers",
			headers:  []string{"X-Frame-Options:SAMEORIGIN", "X-XSS-Protection:1; mode=block"},
			expected: map[string]string{"X-Frame-Options": "SAMEORIGIN", "X-XSS-Protection": "1; mode=block"},
		},
		{
			name:     "CSP header with multiple directives",
			headers:  []string{"Content-Security-Policy:default-src 'self'; style-src 'self' 'unsafe-inline': something"},
			expected: map[string]string{"Content-Security-Policy": "default-src 'self'; style-src 'self' 'unsafe-inline': something"},
		},
		{
			name:     "CSP header with quotes and colons",
			headers:  []string{"Content-Security-Policy:script-src 'unsafe-inline' 'unsafe-eval' 'self' https://example.com:443"},
			expected: map[string]string{"Content-Security-Policy": "script-src 'unsafe-inline' 'unsafe-eval' 'self' https://example.com:443"},
		},
		{
			name:     "multiple colons in value",
			headers:  []string{"Custom-Header:value:with:colons"},
			expected: map[string]string{"Custom-Header": "value:with:colons"},
		},
		{
			name:     "empty value after colon",
			headers:  []string{"Empty-Header:"},
			expected: map[string]string{"Empty-Header": ""},
		},
		{
			name:     "malformed no colon",
			headers:  []string{"Bad-Header-No-Colon"},
			expected: map[string]string{},
		},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			wr := httptest.NewRecorder()
			handler := headersHandler(tt.headers, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			req := httptest.NewRequest("GET", "http://example.com", http.NoBody)
			handler.ServeHTTP(wr, req)

			if len(tt.expected) == 0 {
				// for malformed headers, check they weren't set
				assert.Empty(t, wr.Header())
				return
			}

			for k, v := range tt.expected {
				assert.Equal(t, v, wr.Header().Get(k), "Header %s value mismatch", k)
			}
		})
	}
}

func Test_routeTimeoutHandler(t *testing.T) {
	t.Run("zero timeout passthrough, no deadlines touched", func(t *testing.T) {
		var called atomic.Int32
		handler := routeTimeoutHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called.Add(1)
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
		req = req.WithContext(context.WithValue(req.Context(), ctxMatch,
			discovery.MatchedRoute{Mapper: discovery.URLMapper{Timeout: 0}}))

		rec := newDeadlineRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, int32(1), called.Load())
		assert.Equal(t, http.StatusOK, rec.Result().StatusCode)
		reads, writes := rec.recorded()
		assert.Empty(t, reads, "no SetReadDeadline calls expected when Timeout=0")
		assert.Empty(t, writes, "no SetWriteDeadline calls expected when Timeout=0")
	})

	t.Run("no match in context, passthrough", func(t *testing.T) {
		var called atomic.Int32
		handler := routeTimeoutHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called.Add(1)
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
		rec := newDeadlineRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, int32(1), called.Load())
		assert.Equal(t, http.StatusOK, rec.Result().StatusCode)
		reads, writes := rec.recorded()
		assert.Empty(t, reads)
		assert.Empty(t, writes)
	})

	t.Run("timeout fires, downstream ctx canceled", func(t *testing.T) {
		handler := routeTimeoutHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
				http.Error(w, "ctx done", http.StatusGatewayTimeout)
			case <-time.After(500 * time.Millisecond):
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("late"))
			}
		}))

		req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
		req = req.WithContext(context.WithValue(req.Context(), ctxMatch,
			discovery.MatchedRoute{Mapper: discovery.URLMapper{Timeout: 100 * time.Millisecond}}))

		rec := newDeadlineRecorder()
		start := time.Now()
		handler.ServeHTTP(rec, req)
		elapsed := time.Since(start)

		assert.Equal(t, http.StatusGatewayTimeout, rec.Result().StatusCode)
		assert.Contains(t, rec.Body.String(), "ctx done")
		assert.NotContains(t, rec.Body.String(), "late")
		assert.Less(t, elapsed, 400*time.Millisecond, "should return well before 500ms upstream sleep")
	})

	t.Run("deadlines set when Timeout > 0", func(t *testing.T) {
		handler := routeTimeoutHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
		req = req.WithContext(context.WithValue(req.Context(), ctxMatch,
			discovery.MatchedRoute{Mapper: discovery.URLMapper{Timeout: 5 * time.Second}}))

		rec := newDeadlineRecorder()
		before := time.Now()
		handler.ServeHTTP(rec, req)
		after := time.Now()

		assert.Equal(t, http.StatusOK, rec.Result().StatusCode)
		reads, writes := rec.recorded()
		require.Len(t, reads, 1, "exactly one SetReadDeadline call expected")
		require.Len(t, writes, 1, "exactly one SetWriteDeadline call expected")

		earliest := before.Add(5 * time.Second).Add(-100 * time.Millisecond)
		latest := after.Add(5 * time.Second).Add(100 * time.Millisecond)
		assert.True(t, !reads[0].Before(earliest) && !reads[0].After(latest),
			"read deadline %v outside expected window [%v, %v]", reads[0], earliest, latest)
		assert.True(t, !writes[0].Before(earliest) && !writes[0].After(latest),
			"write deadline %v outside expected window [%v, %v]", writes[0], earliest, latest)
	})

	t.Run("ErrNotSupported tolerated, ctx-cancel still fires", func(t *testing.T) {
		handler := routeTimeoutHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
				http.Error(w, "ctx done", http.StatusGatewayTimeout)
			case <-time.After(500 * time.Millisecond):
				w.WriteHeader(http.StatusOK)
			}
		}))

		req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
		req = req.WithContext(context.WithValue(req.Context(), ctxMatch,
			discovery.MatchedRoute{Mapper: discovery.URLMapper{Timeout: 100 * time.Millisecond}}))

		rec := newDeadlineRecorder()
		rec.readSetErr = http.ErrNotSupported
		rec.writeSetErr = http.ErrNotSupported

		require.NotPanics(t, func() { handler.ServeHTTP(rec, req) })
		assert.Equal(t, http.StatusGatewayTimeout, rec.Result().StatusCode,
			"ctx deadline still fires when deadline setters report ErrNotSupported")
	})
}

func Test_limiterUserHandler_PerRoute(t *testing.T) {
	makeReq := func(remoteAddr string, mapper discovery.URLMapper, matchType discovery.MatchType) *http.Request {
		req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
		req.RemoteAddr = remoteAddr
		ctx := context.WithValue(req.Context(), ctxMatch, discovery.MatchedRoute{Mapper: mapper})
		ctx = context.WithValue(ctx, ctxMatchType, matchType)
		return req.WithContext(ctx)
	}

	t.Run("route throttle fires after burst", func(t *testing.T) {
		var passed atomic.Int32
		handler := limiterUserHandler(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			passed.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		mapper := discovery.URLMapper{Server: "*", SrcMatch: *regexp.MustCompile("^/route$"), Dst: "http://up", Throttle: 2}

		statuses := make([]int, 0, 3)
		for range 3 {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, makeReq("1.2.3.4:1234", mapper, discovery.MTProxy))
			statuses = append(statuses, rec.Code)
		}
		assert.Equal(t, http.StatusOK, statuses[0])
		assert.Equal(t, http.StatusOK, statuses[1])
		assert.Equal(t, http.StatusTooManyRequests, statuses[2])
		assert.Equal(t, int32(2), passed.Load())
	})

	t.Run("key isolation across routes", func(t *testing.T) {
		var passed atomic.Int32
		handler := limiterUserHandler(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			passed.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		mapperA := discovery.URLMapper{Server: "*", SrcMatch: *regexp.MustCompile("^/a$"), Dst: "http://upA", Throttle: 1}
		mapperB := discovery.URLMapper{Server: "*", SrcMatch: *regexp.MustCompile("^/b$"), Dst: "http://upB", Throttle: 1}

		recA := httptest.NewRecorder()
		handler.ServeHTTP(recA, makeReq("1.2.3.4:1", mapperA, discovery.MTProxy))
		recB := httptest.NewRecorder()
		handler.ServeHTTP(recB, makeReq("1.2.3.4:2", mapperB, discovery.MTProxy))

		assert.Equal(t, http.StatusOK, recA.Code, "route A first hit passes")
		assert.Equal(t, http.StatusOK, recB.Code, "route B first hit passes (different cache entry)")
		assert.Equal(t, int32(2), passed.Load())
	})

	t.Run("per-user budget preserved", func(t *testing.T) {
		var passed atomic.Int32
		handler := limiterUserHandler(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			passed.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		mapper := discovery.URLMapper{Server: "*", SrcMatch: *regexp.MustCompile("^/multi$"), Dst: "http://up", Throttle: 2}

		statuses := make([]int, 0, 4)
		for _, ip := range []string{"1.1.1.1:1", "1.1.1.1:1", "2.2.2.2:1", "2.2.2.2:1"} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, makeReq(ip, mapper, discovery.MTProxy))
			statuses = append(statuses, rec.Code)
		}
		for i, s := range statuses {
			assert.Equal(t, http.StatusOK, s, "request %d for distinct IPs should pass", i)
		}
		assert.Equal(t, int32(4), passed.Load())
	})

	t.Run("fallback to global when route throttle is zero", func(t *testing.T) {
		var passed atomic.Int32
		handler := limiterUserHandler(5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			passed.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		mapper := discovery.URLMapper{Server: "*", SrcMatch: *regexp.MustCompile("^/glb$"), Dst: "http://up", Throttle: 0}

		statuses := make([]int, 0, 6)
		for range 6 {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, makeReq("9.9.9.9:1", mapper, discovery.MTProxy))
			statuses = append(statuses, rec.Code)
		}
		assert.Equal(t, http.StatusTooManyRequests, statuses[5], "6th request hits global rate=5")
		assert.LessOrEqual(t, int(passed.Load()), 5, "global limiter must reject at least one of the 6 requests")
	})

	t.Run("per-route works when global is zero", func(t *testing.T) {
		var passed atomic.Int32
		handler := limiterUserHandler(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			passed.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		mapper := discovery.URLMapper{Server: "*", SrcMatch: *regexp.MustCompile("^/rg$"), Dst: "http://up", Throttle: 3}

		statuses := make([]int, 0, 4)
		for range 4 {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, makeReq("3.3.3.3:1", mapper, discovery.MTProxy))
			statuses = append(statuses, rec.Code)
		}
		assert.Equal(t, http.StatusOK, statuses[0])
		assert.Equal(t, http.StatusOK, statuses[1])
		assert.Equal(t, http.StatusOK, statuses[2])
		assert.Equal(t, http.StatusTooManyRequests, statuses[3], "per-route throttle fires even though global=0")
		assert.Equal(t, int32(3), passed.Load())
	})

	t.Run("rate-change cache key", func(t *testing.T) {
		var passed atomic.Int32
		handler := limiterUserHandler(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			passed.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		src := regexp.MustCompile("^/rate$")
		mapper2 := discovery.URLMapper{Server: "*", SrcMatch: *src, Dst: "http://up", Throttle: 2}
		mapper5 := discovery.URLMapper{Server: "*", SrcMatch: *src, Dst: "http://up", Throttle: 5}

		first := make([]int, 0, 3)
		for range 3 {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, makeReq("7.7.7.7:1", mapper2, discovery.MTProxy))
			first = append(first, rec.Code)
		}
		assert.Equal(t, http.StatusOK, first[0])
		assert.Equal(t, http.StatusOK, first[1])
		assert.Equal(t, http.StatusTooManyRequests, first[2], "rate=2 rejects 3rd request")

		second := make([]int, 0, 6)
		for range 6 {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, makeReq("8.8.8.8:1", mapper5, discovery.MTProxy))
			second = append(second, rec.Code)
		}
		for i := range 5 {
			assert.Equal(t, http.StatusOK, second[i], "rate=5 allows requests %d under burst", i)
		}
		assert.Equal(t, http.StatusTooManyRequests, second[5], "rate=5 cache entry rejects 6th from a separate IP, proving new limiter at rate 5 (not reused rate-2 entry)")
	})
}
