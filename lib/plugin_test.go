package lib

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlugin_Do(t *testing.T) {
	var postCalls, deleteCalls int32
	tsConductor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			atomic.AddInt32(&postCalls, 1)
		case "DELETE":
			atomic.AddInt32(&deleteCalls, 1)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
		t.Logf("registration: %+v", r)
	}))
	defer tsConductor.Close()

	u, err := url.Parse(tsConductor.URL)
	require.NoError(t, err)
	p := Plugin{Name: "Test1", Address: "localhost:12345", Methods: []string{"H1", "H2"}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = p.Do(ctx, "http://"+u.Host, new(TestingHandler))
	assert.EqualError(t, err, "context deadline exceeded")
	assert.Equal(t, int32(1), atomic.LoadInt32(&postCalls))
	assert.Equal(t, int32(1), atomic.LoadInt32(&deleteCalls))
}

func TestPlugin_DoFailed(t *testing.T) {
	tsConductor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer tsConductor.Close()

	u, err := url.Parse(tsConductor.URL)
	require.NoError(t, err)
	p := Plugin{Name: "Test2", Address: "localhost:12345", Methods: []string{"H1", "H2"}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = p.Do(ctx, "http://"+u.Host, new(TestingHandler))
	assert.EqualError(t, err, "context canceled")
}

// TestingHandler is an example of middleware handler altering headers and stastus
type TestingHandler struct{}

// HeaderThing adds key:val header to the response
func (h *TestingHandler) H1(req Request, res *Response) (err error) {
	log.Printf("req: %+v", req)
	res.HeadersOut = http.Header{}
	res.HeadersOut.Add("key", "val")
	res.HeadersIn = http.Header{}
	res.HeadersIn.Add("token", "something")
	res.StatusCode = 200 // each handler has to set status code
	return
}

// ErrorThing returns status 500 on "/fail" url. This terminated processing chain on reproxy side immediately
func (h *TestingHandler) H2(req Request, res *Response) (err error) {
	log.Printf("req: %+v", req)
	if req.URL == "/fail" {
		res.StatusCode = 500
		return
	}
	res.StatusCode = 200
	return
}
