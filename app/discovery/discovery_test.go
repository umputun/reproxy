package discovery

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_Do(t *testing.T) {
	p1 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan struct{} {
			res := make(chan struct{}, 1)
			res <- struct{}{}
			return res
		},
		ListFunc: func() ([]UrlMapper, error) {
			return []UrlMapper{
				{SrcMatch: regexp.MustCompile("^/api/svc1/(.*)"), Dst: "http://127.0.0.1:8080/blah1/$1"},
				{SrcMatch: regexp.MustCompile("^/api/svc2/(.*)"), Dst: "http://127.0.0.2:8080/blah2/$1/abc"},
			}, nil
		},
	}
	p2 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan struct{} {
			return make(chan struct{}, 1)
		},
		ListFunc: func() ([]UrlMapper, error) {
			return []UrlMapper{
				{SrcMatch: regexp.MustCompile("/api/svc3/xyz"), Dst: "http://127.0.0.3:8080/blah3/xyz"},
			}, nil
		},
	}
	svc := NewService([]Provider{p1, p2})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := svc.Do(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	assert.Equal(t, 3, len(svc.mappers))

	assert.Equal(t, 1, len(p1.EventsCalls()))
	assert.Equal(t, 1, len(p2.EventsCalls()))
	assert.Equal(t, 1, len(p1.ListCalls()))
	assert.Equal(t, 1, len(p2.ListCalls()))
}

func TestService_Match(t *testing.T) {
	p1 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan struct{} {
			res := make(chan struct{}, 1)
			res <- struct{}{}
			return res
		},
		ListFunc: func() ([]UrlMapper, error) {
			return []UrlMapper{
				{SrcMatch: regexp.MustCompile("^/api/svc1/(.*)"), Dst: "http://127.0.0.1:8080/blah1/$1"},
				{SrcMatch: regexp.MustCompile("^/api/svc2/(.*)"), Dst: "http://127.0.0.2:8080/blah2/$1/abc"},
			}, nil
		},
	}
	p2 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan struct{} {
			return make(chan struct{}, 1)
		},
		ListFunc: func() ([]UrlMapper, error) {
			return []UrlMapper{
				{SrcMatch: regexp.MustCompile("/api/svc3/xyz"), Dst: "http://127.0.0.3:8080/blah3/xyz"},
			}, nil
		},
	}
	svc := NewService([]Provider{p1, p2})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := svc.Do(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	assert.Equal(t, 3, len(svc.mappers))

	{
		res, ok := svc.Match("/api/svc3/xyz")
		assert.True(t, ok)
		assert.Equal(t, "http://127.0.0.3:8080/blah3/xyz", res)
	}
	{
		res, ok := svc.Match("/api/svc1/1234")
		assert.True(t, ok)
		assert.Equal(t, "http://127.0.0.1:8080/blah1/1234", res)
	}
	{
		res, ok := svc.Match("/aaa/api/svc1/1234")
		assert.False(t, ok)
		assert.Equal(t, "/aaa/api/svc1/1234", res)
	}
}
