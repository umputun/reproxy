package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHeaders(t *testing.T) {
	req := httptest.NewRequest("GET", "/something", nil)
	w := httptest.NewRecorder()

	h := Headers("h1:v1", "bad", "h2:v2")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h.ServeHTTP(w, req)
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	t.Logf("%+v", req.Header)
	assert.Equal(t, "v1", req.Header.Get("h1"))
	assert.Equal(t, "v2", req.Header.Get("h2"))
	assert.Equal(t, 2, len(req.Header))
}
