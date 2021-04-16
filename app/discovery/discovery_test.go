package discovery

import (
	"context"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_Run(t *testing.T) {
	p1 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			res := make(chan ProviderID, 1)
			res <- PIFile
			return res
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				{Server: "*", SrcMatch: *regexp.MustCompile("^/api/svc1/(.*)"), Dst: "http://127.0.0.1:8080/blah1/$1",
					ProviderID: PIFile},
				{Server: "*", SrcMatch: *regexp.MustCompile("^/api/svc2/(.*)"),
					Dst: "http://127.0.0.2:8080/blah2/@1/abc", ProviderID: PIFile},
			}, nil
		},
	}
	p2 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			return make(chan ProviderID, 1)
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				{Server: "localhost", SrcMatch: *regexp.MustCompile("/api/svc3/xyz"),
					Dst: "http://127.0.0.3:8080/blah3/xyz", ProviderID: PIDocker},
			}, nil
		},
	}
	svc := NewService([]Provider{p1, p2}, time.Millisecond*10)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	mappers := svc.Mappers()
	assert.Equal(t, 3, len(mappers))
	assert.Equal(t, PIFile, mappers[0].ProviderID)
	assert.Equal(t, "*", mappers[0].Server)
	assert.Equal(t, "^/api/svc1/(.*)", mappers[0].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.1:8080/blah1/$1", mappers[0].Dst)

	assert.Equal(t, 1, len(p1.EventsCalls()))
	assert.Equal(t, 1, len(p2.EventsCalls()))

	assert.Equal(t, 1, len(p1.ListCalls()))
	assert.Equal(t, 1, len(p2.ListCalls()))
}

func TestService_Match(t *testing.T) {
	p1 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			res := make(chan ProviderID, 1)
			res <- PIFile
			return res
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				{SrcMatch: *regexp.MustCompile("^/api/svc1/(.*)"), Dst: "http://127.0.0.1:8080/blah1/$1", ProviderID: PIFile},
				{Server: "m.example.com", SrcMatch: *regexp.MustCompile("^/api/svc2/(.*)"),
					Dst: "http://127.0.0.2:8080/blah2/$1/abc", ProviderID: PIFile},
			}, nil
		},
	}
	p2 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			return make(chan ProviderID, 1)
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				{SrcMatch: *regexp.MustCompile("/api/svc3/xyz"), Dst: "http://127.0.0.3:8080/blah3/xyz", ProviderID: PIDocker},
				{SrcMatch: *regexp.MustCompile("/web"), Dst: "/var/web", ProviderID: PIDocker, MatchType: MTStatic},
				{SrcMatch: *regexp.MustCompile("/www/"), Dst: "/var/web", ProviderID: PIDocker, MatchType: MTStatic},
			}, nil
		},
	}
	svc := NewService([]Provider{p1, p2}, time.Millisecond*100)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	assert.Equal(t, 5, len(svc.Mappers()))

	tbl := []struct {
		server, src string
		dest        string
		mt          MatchType
		ok          bool
	}{
		{"example.com", "/api/svc3/xyz/something", "http://127.0.0.3:8080/blah3/xyz/something", MTProxy, true},
		{"example.com", "/api/svc3/xyz", "http://127.0.0.3:8080/blah3/xyz", MTProxy, true},
		{"abc.example.com", "/api/svc1/1234", "http://127.0.0.1:8080/blah1/1234", MTProxy, true},
		{"zzz.example.com", "/aaa/api/svc1/1234", "/aaa/api/svc1/1234", MTProxy, false},
		{"m.example.com", "/api/svc2/1234", "http://127.0.0.2:8080/blah2/1234/abc", MTProxy, true},
		{"m1.example.com", "/api/svc2/1234", "/api/svc2/1234", MTProxy, false},
		{"m1.example.com", "/web/index.html", "/web/:/var/web/", MTStatic, true},
		{"m1.example.com", "/web/", "/web/:/var/web/", MTStatic, true},
		{"m1.example.com", "/www", "/www/:/var/web/", MTStatic, true},
		{"m1.example.com", "/www/something", "/www/:/var/web/", MTStatic, true},
	}

	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			res, mt, ok := svc.Match(tt.server, tt.src)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.dest, res)
			if ok {
				assert.Equal(t, tt.mt, mt)
			}
		})
	}
}

func TestService_Servers(t *testing.T) {
	p1 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			res := make(chan ProviderID, 1)
			res <- PIFile
			return res
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				{SrcMatch: *regexp.MustCompile("^/api/svc1/(.*)"), Dst: "http://127.0.0.1:8080/blah1/$1", ProviderID: PIFile},
				{Server: "m.example.com", SrcMatch: *regexp.MustCompile("^/api/svc2/(.*)"),
					Dst: "http://127.0.0.2:8080/blah2/$1/abc", ProviderID: PIFile},
			}, nil
		},
	}
	p2 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			return make(chan ProviderID, 1)
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				{Server: "xx.reproxy.io", SrcMatch: *regexp.MustCompile("/api/svc3/xyz"),
					Dst: "http://127.0.0.3:8080/blah3/xyz", ProviderID: PIDocker},
			}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	svc := NewService([]Provider{p1, p2}, time.Millisecond*100)
	err := svc.Run(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	assert.Equal(t, 3, len(svc.mappers))

	servers := svc.Servers()
	assert.Equal(t, []string{"m.example.com", "xx.reproxy.io"}, servers)

}

func TestService_extendRule(t *testing.T) {

	tbl := []struct {
		inp URLMapper
		out URLMapper
	}{
		{
			URLMapper{SrcMatch: *regexp.MustCompile("/")},
			URLMapper{SrcMatch: *regexp.MustCompile("^/(.*)"), Dst: "/$1"},
		},
		{
			URLMapper{Server: "m.example.com", PingURL: "http://example.com/ping", ProviderID: "docker",
				SrcMatch: *regexp.MustCompile("/api/blah/"), Dst: "http://localhost:8080/"},
			URLMapper{Server: "m.example.com", PingURL: "http://example.com/ping", ProviderID: "docker",
				SrcMatch: *regexp.MustCompile("^/api/blah/(.*)"), Dst: "http://localhost:8080/$1"},
		},
		{
			URLMapper{Server: "m.example.com", PingURL: "http://example.com/ping", ProviderID: "docker",
				SrcMatch: *regexp.MustCompile("/api/blah/(.*)/xxx/(.*_)"), Dst: "http://localhost:8080/$1/$2"},
			URLMapper{Server: "m.example.com", PingURL: "http://example.com/ping", ProviderID: "docker",
				SrcMatch: *regexp.MustCompile("/api/blah/(.*)/xxx/(.*_)"), Dst: "http://localhost:8080/$1/$2"},
		},
		{
			URLMapper{Server: "m.example.com", PingURL: "http://example.com/ping", ProviderID: "docker",
				SrcMatch: *regexp.MustCompile("/api/blah"), Dst: "http://localhost:8080/xxx"},
			URLMapper{Server: "m.example.com", PingURL: "http://example.com/ping", ProviderID: "docker",
				SrcMatch: *regexp.MustCompile("/api/blah"), Dst: "http://localhost:8080/xxx"},
		},
	}

	svc := &Service{}
	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			res := svc.extendMapper(tt.inp)
			assert.Equal(t, tt.out, res)
		})
	}

}
