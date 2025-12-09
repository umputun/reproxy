package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/umputun/reproxy/app/discovery"
)

func TestPerRouteAuth_Handler(t *testing.T) {
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
		{
			name:               "no auth required, no credentials",
			authUsers:          []string{},
			setAuth:            func(r *http.Request) {},
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "no auth required, credentials provided",
			authUsers:          []string{},
			setAuth:            func(r *http.Request) { r.SetBasicAuth("user1", "passwd1") },
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "auth required, valid credentials",
			authUsers:          []string{"user1:" + string(hash1)},
			setAuth:            func(r *http.Request) { r.SetBasicAuth("user1", "passwd1") },
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "auth required, no credentials",
			authUsers:          []string{"user1:" + string(hash1)},
			setAuth:            func(r *http.Request) {},
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:               "auth required, wrong password",
			authUsers:          []string{"user1:" + string(hash1)},
			setAuth:            func(r *http.Request) { r.SetBasicAuth("user1", "wrongpasswd") },
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:               "auth required, unknown user",
			authUsers:          []string{"user1:" + string(hash1)},
			setAuth:            func(r *http.Request) { r.SetBasicAuth("unknownuser", "passwd1") },
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:               "multiple users, first user valid",
			authUsers:          []string{"user1:" + string(hash1), "user2:" + string(hash2)},
			setAuth:            func(r *http.Request) { r.SetBasicAuth("user1", "passwd1") },
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "multiple users, second user valid",
			authUsers:          []string{"user1:" + string(hash1), "user2:" + string(hash2)},
			setAuth:            func(r *http.Request) { r.SetBasicAuth("user2", "passwd2") },
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "multiple users, both wrong",
			authUsers:          []string{"user1:" + string(hash1), "user2:" + string(hash2)},
			setAuth:            func(r *http.Request) { r.SetBasicAuth("user3", "passwd3") },
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:               "malformed auth entry ignored",
			authUsers:          []string{"malformed", "user1:" + string(hash1)},
			setAuth:            func(r *http.Request) { r.SetBasicAuth("user1", "passwd1") },
			expectedStatusCode: http.StatusOK,
		},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			auth := NewPerRouteAuth()
			handler := auth.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

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

func TestPerRouteAuth_Handler_NoContext(t *testing.T) {
	// test when no context match is set (should pass through)
	auth := NewPerRouteAuth()
	handler := auth.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	tbl := []struct {
		name     string
		username string
		password string
		allowed  []string
		expected bool
	}{
		{name: "valid credentials", username: "admin", password: "secret", allowed: []string{"admin:" + string(hash)}, expected: true},
		{name: "invalid password", username: "admin", password: "wrong", allowed: []string{"admin:" + string(hash)}, expected: false},
		{name: "invalid username", username: "wrong", password: "secret", allowed: []string{"admin:" + string(hash)}, expected: false},
		{name: "empty allowed list", username: "admin", password: "secret", allowed: []string{}, expected: false},
		{name: "malformed entry", username: "admin", password: "secret", allowed: []string{"no-colon"}, expected: false},
		{name: "invalid bcrypt hash", username: "admin", password: "secret", allowed: []string{"admin:not-a-valid-bcrypt"}, expected: false},
		{name: "whitespace-only entry", username: "admin", password: "secret", allowed: []string{"   "}, expected: false},
		{name: "empty username with hash", username: "", password: "secret", allowed: []string{":" + string(hash)}, expected: false},
		{name: "username with colon", username: "user:name", password: "secret", allowed: []string{"user:name:" + string(hash)}, expected: false},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			result := validateBasicAuthCredentials(tt.username, tt.password, tt.allowed)
			assert.Equal(t, tt.expected, result)
		})
	}
}
