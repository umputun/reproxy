package discovery

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
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
					Dst: "http://127.0.0.3:8080/blah3/xyz", ProviderID: PIDocker, OnlyFromIPs: []string{"127.0.0.1"}},
			}, nil
		},
	}

	p3 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			return make(chan ProviderID, 1)
		},
		ListFunc: func() ([]URLMapper, error) {
			return nil, errors.New("failed")
		},
	}

	svc := NewService([]Provider{p1, p2, p3}, time.Millisecond*10)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	mappers := svc.Mappers()
	assert.Equal(t, 3, len(mappers))
	assert.Equal(t, PIDocker, mappers[0].ProviderID)
	assert.Equal(t, "localhost", mappers[0].Server)
	assert.Equal(t, "/api/svc3/xyz", mappers[0].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.3:8080/blah3/xyz", mappers[0].Dst)
	assert.Equal(t, []string{"127.0.0.1"}, mappers[0].OnlyFromIPs)

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
				{Server: "m.example.com", SrcMatch: *regexp.MustCompile("^/api/svc4/(.*)"),
					Dst: "http://127.0.0.4:8080/blah2/$1/abc", MatchType: MTProxy, dead: true},

				{Server: "m.example.com", SrcMatch: *regexp.MustCompile("^/api/svc5/(.*)"),
					Dst: "http://127.0.0.5:8080/blah2/$1/abc", MatchType: MTProxy, dead: false},
				{Server: "m.example.com", SrcMatch: *regexp.MustCompile("^/api/svc5/(.*)"),
					Dst: "http://127.0.0.5:8080/blah2/$1/abc/2", MatchType: MTProxy, dead: false},
				{Server: "m.example.com", SrcMatch: *regexp.MustCompile("^/api/svc5/(.*)"),
					Dst: "http://127.0.0.5:8080/blah2/$1/abc/3", MatchType: MTProxy, dead: true},
			}, nil
		},
	}
	p2 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			return make(chan ProviderID, 1)
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				{SrcMatch: *regexp.MustCompile("/api/svc3/xyz"), Dst: "http://127.0.0.3:8080/blah3/xyz",
					OnlyFromIPs: []string{"127.0.0.1", "192.168.1.0/24"}, ProviderID: PIDocker},
				{SrcMatch: *regexp.MustCompile("/web"), Dst: "/var/web", ProviderID: PIDocker, MatchType: MTStatic,
					AssetsWebRoot: "/web", AssetsLocation: "/var/web"},
				{SrcMatch: *regexp.MustCompile("/www/"), Dst: "/var/web", ProviderID: PIDocker, MatchType: MTStatic,
					AssetsWebRoot: "/www", AssetsLocation: "/var/web"},
				{SrcMatch: *regexp.MustCompile("/path/"), Dst: "/var/web/path", ProviderID: PIDocker, MatchType: MTStatic},
				{SrcMatch: *regexp.MustCompile("/www2/"), Dst: "/var/web2", ProviderID: PIDocker, MatchType: MTStatic,
					AssetsWebRoot: "/www2", AssetsLocation: "/var/web2", AssetsSPA: true},
				{SrcMatch: *regexp.MustCompile(""), Dst: "", ProviderID: PIDocker, MatchType: MTStatic,
					AssetsWebRoot: "/", AssetsLocation: "/var/web0", AssetsSPA: true, Server: "m22.example.com"},
			}, nil
		},
	}
	svc := NewService([]Provider{p1, p2}, time.Millisecond*100)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	assert.Equal(t, 12, len(svc.Mappers()))

	tbl := []struct {
		server, src string
		res         Matches
	}{
		{"example.com", "/api/svc3/xyz/something", Matches{MTProxy, []MatchedRoute{
			{Destination: "http://127.0.0.3:8080/blah3/xyz/something", Alive: true,
				Mapper: URLMapper{OnlyFromIPs: []string{"127.0.0.1", "192.168.1.0/24"}}}}}},
		{"example.com", "/api/svc3/xyz", Matches{MTProxy, []MatchedRoute{{
			Destination: "http://127.0.0.3:8080/blah3/xyz", Alive: true,
			Mapper: URLMapper{OnlyFromIPs: []string{"127.0.0.1", "192.168.1.0/24"}}}}}},
		{"abc.example.com", "/api/svc1/1234", Matches{MTProxy, []MatchedRoute{
			{Destination: "http://127.0.0.1:8080/blah1/1234", Alive: true}}}},
		{"zzz.example.com", "/aaa/api/svc1/1234", Matches{MTProxy, nil}},
		{"m.example.com", "/api/svc2/1234", Matches{MTProxy, []MatchedRoute{
			{Destination: "http://127.0.0.2:8080/blah2/1234/abc", Alive: true}}}},
		{"m1.example.com", "/api/svc2/1234", Matches{MTProxy, nil}},
		{"m.example.com", "/api/svc4/id12345", Matches{MTProxy, []MatchedRoute{
			{Destination: "http://127.0.0.4:8080/blah2/id12345/abc", Alive: false}}}},

		{"m.example.com", "/api/svc5/num123456", Matches{MTProxy, []MatchedRoute{
			{Destination: "http://127.0.0.5:8080/blah2/num123456/abc", Alive: true},
			{Destination: "http://127.0.0.5:8080/blah2/num123456/abc/2", Alive: true},
			{Destination: "http://127.0.0.5:8080/blah2/num123456/abc/3", Alive: false},
		}}},

		{"m1.example.com", "/web/index.html", Matches{MTStatic, []MatchedRoute{{Destination: "/web:/var/web/:norm", Alive: true}}}},
		{"m1.example.com", "/web/", Matches{MTStatic, []MatchedRoute{{Destination: "/web:/var/web/:norm", Alive: true}}}},
		{"m1.example.com", "/www/something", Matches{MTStatic, []MatchedRoute{{Destination: "/www:/var/web/:norm", Alive: true}}}},
		{"m1.example.com", "/www/", Matches{MTStatic, []MatchedRoute{{Destination: "/www:/var/web/:norm", Alive: true}}}},
		{"m1.example.com", "/www", Matches{MTStatic, []MatchedRoute{{Destination: "/www:/var/web/:norm", Alive: true}}}},
		{"xyx.example.com", "/path/something", Matches{MTStatic, []MatchedRoute{{Destination: "/path:/var/web/path/:norm", Alive: true}}}},
		{"m1.example.com", "/www2", Matches{MTStatic, []MatchedRoute{{Destination: "/www2:/var/web2/:spa", Alive: true}}}},
		{"m22.example.com", "/someplace/index.html", Matches{MTStatic, []MatchedRoute{{Destination: "/:/var/web0/:spa", Alive: true}}}},
	}

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i)+"-"+tt.server, func(t *testing.T) {
			res := svc.Match(tt.server, tt.src)
			require.Equal(t, len(tt.res.Routes), len(res.Routes), res.Routes)
			for i := 0; i < len(res.Routes); i++ {
				assert.Equal(t, tt.res.Routes[i].Alive, res.Routes[i].Alive)
				assert.Equal(t, tt.res.Routes[i].Destination, res.Routes[i].Destination)
				assert.Equal(t, tt.res.Routes[i].Mapper.OnlyFromIPs, res.Routes[i].Mapper.OnlyFromIPs)
			}
			assert.Equal(t, tt.res.MatchType, res.MatchType)
		})
	}
}

func TestService_MatchServerRegex(t *testing.T) {
	mockProvider := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			res := make(chan ProviderID, 1)
			res <- PIFile
			return res
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				// invalid regex
				{Server: "[", SrcMatch: *regexp.MustCompile("^/"),
					Dst: "http://127.0.0.10:8080/", MatchType: MTProxy, dead: false},

				// regex servers
				{Server: "test-prefix\\.(.*)", SrcMatch: *regexp.MustCompile("^/(.*)"),
					Dst: "http://127.0.0.1:8080/$host/blah/$1", MatchType: MTProxy, dead: false},
				{Server: "test-prefix2\\.(.*)", SrcMatch: *regexp.MustCompile("^/(.*)"),
					Dst: "http://127.0.0.1:8080/${host}/blah/$1", MatchType: MTProxy, dead: false},
				{Server: "(.*)\\.test-domain\\.(com|org)", SrcMatch: *regexp.MustCompile("^/bar/(.*)"),
					Dst: "http://127.0.0.2:8080/$1/foo", MatchType: MTProxy, dead: false},
				{Server: "*.test-domain2.com", SrcMatch: *regexp.MustCompile("^/foo/(.*)"),
					Dst: "http://127.0.0.3:8080/$1/bar", MatchType: MTProxy, dead: false},

				// strict match
				{Server: "test-prefix.exact.com", SrcMatch: *regexp.MustCompile("/"),
					Dst: "http://127.0.0.4:8080", MatchType: MTProxy, dead: false},
			}, nil
		},
	}
	svc := NewService([]Provider{mockProvider}, time.Millisecond*100)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)

	tbl := []struct {
		name        string
		server, src string
		res         Matches
	}{
		{
			name:   "strict match",
			server: "test-prefix.exact.com",
			src:    "/",
			res:    Matches{MTProxy, []MatchedRoute{{Destination: "http://127.0.0.4:8080/", Alive: true}}},
		},
		{
			name:   "regex server with $host match",
			server: "test-prefix.example.com",
			src:    "/some",
			res:    Matches{MTProxy, []MatchedRoute{{Destination: "http://127.0.0.1:8080/test-prefix.example.com/blah/some", Alive: true}}},
		},
		{
			name:   "regex server with ${host} match",
			server: "test-prefix2.example.com",
			src:    "/some",
			res:    Matches{MTProxy, []MatchedRoute{{Destination: "http://127.0.0.1:8080/test-prefix2.example.com/blah/some", Alive: true}}},
		},
		{
			name:   "regex server without a match",
			server: "another-prefix.example.com",
			src:    "/",
			res:    Matches{MTProxy, nil},
		},
		{
			name:   "regex server with test-domain.org match",
			server: "another-prefix.test-domain.org",
			src:    "/bar/123",
			res:    Matches{MTProxy, []MatchedRoute{{Destination: "http://127.0.0.2:8080/123/foo", Alive: true}}},
		},
		{
			name:   "regex server with test-domain.net mismatch",
			server: "another-prefix.test-domain.net",
			src:    "/",
			res:    Matches{MTProxy, nil},
		},
		{
			name:   "pattern server with *.test-domain2.com match",
			server: "test.test-domain2.com",
			src:    "/foo/123",
			res:    Matches{MTProxy, []MatchedRoute{{Destination: "http://127.0.0.3:8080/123/bar", Alive: true}}},
		},
	}

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i)+"-"+tt.server, func(t *testing.T) {
			res := svc.Match(tt.server, tt.src)
			require.Equal(t, len(tt.res.Routes), len(res.Routes), res.Routes)
			for i := 0; i < len(res.Routes); i++ {
				assert.Equal(t, tt.res.Routes[i].Alive, res.Routes[i].Alive)
				assert.Equal(t, tt.res.Routes[i].Destination, res.Routes[i].Destination)
			}
			assert.Equal(t, tt.res.MatchType, res.MatchType)
		})
	}
}

func TestService_MatchServerRegexInvalidateCache(t *testing.T) {
	res := make(chan ProviderID)
	serverRegex := "test-(.*)"
	p1 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			return res
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				{Server: serverRegex, SrcMatch: *regexp.MustCompile("^/"), Dst: "http://127.0.0.1/foo"},
			}, nil
		},
	}

	svc := NewService([]Provider{p1}, time.Millisecond*10)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go func() {
		err := svc.Run(ctx)
		require.Error(t, err)
	}()

	res <- PIFile

	// wait for update
	time.Sleep(50 * time.Millisecond)

	match := svc.Match("test-server", "/")
	assert.Len(t, match.Routes, 1)

	serverRegex = "another-(.*)"
	res <- PIFile

	// wait for cache invalidation
	time.Sleep(50 * time.Millisecond)

	match = svc.Match("test-server", "/")
	assert.Len(t, match.Routes, 0)
}

func TestService_MatchConflictRegex(t *testing.T) {
	p1 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			res := make(chan ProviderID, 1)
			res <- PIFile
			return res
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				{SrcMatch: *regexp.MustCompile("^/api/svc1/(.*)"), Dst: "http://127.0.0.1:8080/blah/$1", ProviderID: PIFile},
				{SrcMatch: *regexp.MustCompile("^/api/svc1/cat"), Dst: "http://127.0.0.2:8080/cat", ProviderID: PIFile},
				{SrcMatch: *regexp.MustCompile("^/api/svc1/abcd"), Dst: "http://127.0.0.3:8080/abcd", ProviderID: PIFile},
			}, nil
		},
	}

	svc := NewService([]Provider{p1}, time.Millisecond*100)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	assert.Equal(t, 3, len(svc.Mappers()))

	tbl := []struct {
		server, src string
		res         Matches
	}{
		{"example.com", "/api/svc1/xyz/something", Matches{MTProxy, []MatchedRoute{
			{Destination: "http://127.0.0.1:8080/blah/xyz/something", Alive: true}}}},
		{"example.com", "/api/svc1/something", Matches{MTProxy, []MatchedRoute{
			{Destination: "http://127.0.0.1:8080/blah/something", Alive: true}}}},
		{"example.com", "/api/svc1/cat", Matches{MTProxy, []MatchedRoute{
			{Destination: "http://127.0.0.2:8080/cat", Alive: true}}}},
		{"example.com", "/api/svc1/abcd", Matches{MTProxy, []MatchedRoute{
			{Destination: "http://127.0.0.3:8080/abcd", Alive: true}}}},
	}

	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			res := svc.Match(tt.server, tt.src)
			require.Equal(t, len(tt.res.Routes), len(res.Routes), res.Routes)
			for i := 0; i < len(res.Routes); i++ {
				assert.Equal(t, tt.res.Routes[i].Alive, res.Routes[i].Alive)
				assert.Equal(t, tt.res.Routes[i].Destination, res.Routes[i].Destination)
			}
			assert.Equal(t, tt.res.MatchType, res.MatchType)
		})
	}
}

// https://github.com/umputun/reproxy/issues/192
func TestService_Match192(t *testing.T) {
	p1 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			res := make(chan ProviderID, 1)
			res <- PIFile
			return res
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				{
					Server:     "*",
					SrcMatch:   *regexp.MustCompile("^/(.*)"),
					Dst:        "@temp https://site1.ru/",
					ProviderID: PIFile,
				},
				{
					Server:     "example1.ru",
					SrcMatch:   *regexp.MustCompile("^/(.*)"),
					Dst:        "@temp https://site2.ru/",
					ProviderID: PIFile,
				},
				{
					Server:     "example2.ru",
					SrcMatch:   *regexp.MustCompile("^/(.*)"),
					Dst:        "@temp https://site2.ru/",
					ProviderID: PIFile,
				},
			}, nil
		},
	}

	svc := NewService([]Provider{p1}, time.Millisecond*100)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	assert.Equal(t, 3, len(svc.Mappers()))

	tbl := []struct {
		server, src string
		res         Matches
	}{
		{"example2.ru", "/something", Matches{MTProxy, []MatchedRoute{
			{Destination: "https://site2.ru/", Alive: true}}}},
		{"example1.ru", "/something", Matches{MTProxy, []MatchedRoute{
			{Destination: "https://site2.ru/", Alive: true}}}},
		{"example123.ru", "/something", Matches{MTProxy, []MatchedRoute{
			{Destination: "https://site1.ru/", Alive: true}}}},
	}

	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			res := svc.Match(tt.server, tt.src)
			require.Equal(t, len(tt.res.Routes), len(res.Routes), res.Routes)
			for i := 0; i < len(res.Routes); i++ {
				assert.Equal(t, tt.res.Routes[i].Alive, res.Routes[i].Alive)
				assert.Equal(t, tt.res.Routes[i].Destination, res.Routes[i].Destination)
			}
			assert.Equal(t, tt.res.MatchType, res.MatchType)
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
				SrcMatch: *regexp.MustCompile("/api/blah"), Dst: "http://localhost:8080/xxx", RedirectType: RTPerm},
			URLMapper{Server: "m.example.com", PingURL: "http://example.com/ping", ProviderID: "docker",
				SrcMatch: *regexp.MustCompile("/api/blah"), Dst: "http://localhost:8080/xxx", RedirectType: RTPerm},
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

func TestService_redirects(t *testing.T) {
	tbl := []struct {
		inp URLMapper
		out URLMapper
	}{
		{
			URLMapper{Dst: "/blah"},
			URLMapper{Dst: "/blah", RedirectType: RTNone},
		},
		{
			URLMapper{Dst: "http://example.com/blah"},
			URLMapper{Dst: "http://example.com/blah", RedirectType: RTNone},
		},
		{
			URLMapper{Dst: "@301 http://example.com/blah"},
			URLMapper{Dst: "http://example.com/blah", RedirectType: RTPerm},
		},
		{
			URLMapper{Dst: "@perm http://example.com/blah"},
			URLMapper{Dst: "http://example.com/blah", RedirectType: RTPerm},
		},
		{
			URLMapper{Dst: "@302 http://example.com/blah"},
			URLMapper{Dst: "http://example.com/blah", RedirectType: RTTemp},
		},
		{
			URLMapper{Dst: "@tmp http://example.com/blah"},
			URLMapper{Dst: "http://example.com/blah", RedirectType: RTTemp},
		},
		{
			URLMapper{Dst: "@temp http://example.com/blah"},
			URLMapper{Dst: "http://example.com/blah", RedirectType: RTTemp},
		},
		{
			URLMapper{Dst: "@blah http://example.com/blah"},
			URLMapper{Dst: "@blah http://example.com/blah", RedirectType: RTNone},
		},
	}

	svc := &Service{}
	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			res := svc.redirects(tt.inp)
			assert.Equal(t, tt.out, res)
		})
	}
}

func TestService_ScheduleHealthCheck(t *testing.T) {
	randomPort := rand.Intn(10000) + 40000

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))
	defer ts.Close()

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))
	defer ts2.Close()

	wantMappers := []URLMapper{
		{SrcMatch: *regexp.MustCompile("/api/svc3/xyz"), Dst: "http://127.0.0.3:8080/blah3/xyz", ProviderID: PIDocker, PingURL: ts.URL},
		{SrcMatch: *regexp.MustCompile("/api/svc3/xyz"), Dst: "http://127.0.0.3:8080/blah3/xyz", ProviderID: PIDocker, PingURL: fmt.Sprintf("127.0.0.1:%d", randomPort)},
		{SrcMatch: *regexp.MustCompile("/api/svc3/xyz"), Dst: "http://127.0.0.3:8080/blah3/xyz", ProviderID: PIDocker, PingURL: ts2.URL},
	}

	p := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			res := make(chan ProviderID, 1)
			res <- PIFile
			return res
		},
		ListFunc: func() ([]URLMapper, error) {
			return wantMappers, nil
		},
	}

	svc := NewService([]Provider{p}, time.Millisecond*10)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	mappers := svc.Mappers()
	assert.Equal(t, 3, len(mappers))
	assert.Equal(t, wantMappers, mappers)

	svc.ScheduleHealthCheck(context.Background(), time.Microsecond*2)
	time.Sleep(time.Millisecond * 10)

	mappers = svc.Mappers()
	assert.Equal(t, false, mappers[0].dead)
	assert.Equal(t, true, mappers[1].dead)
	assert.Equal(t, false, mappers[2].dead)
}

func Test_ping(t *testing.T) {
	port := rand.Intn(10000) + 40000
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))
	defer ts.Close()

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer ts2.Close()

	type args struct {
		m URLMapper
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{name: "test server, expected OK", args: args{m: URLMapper{PingURL: ts.URL}}, want: "", wantErr: false},
		{name: "random port, expected error", args: args{m: URLMapper{PingURL: fmt.Sprintf("127.0.0.1:%d", port)}}, want: "", wantErr: true},
		{name: "error code != 200", args: args{m: URLMapper{PingURL: ts2.URL}}, want: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.args.m.ping()
			if (err != nil) != tt.wantErr {
				t.Errorf("ping() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestCheckHealth(t *testing.T) {
	failPingULR := "http://127.0.0.1:4321"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))
	defer ts.Close()

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))
	defer ts2.Close()

	p1 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			res := make(chan ProviderID, 1)
			res <- PIFile
			return res
		},
		ListFunc: func() ([]URLMapper, error) {
			return []URLMapper{
				{Server: "*", SrcMatch: *regexp.MustCompile("^/api/svc1/(.*)"), Dst: "http://127.0.0.1:8080/blah1/$1",
					ProviderID: PIFile, PingURL: ts.URL},
				{Server: "*", SrcMatch: *regexp.MustCompile("^/api/svc2/(.*)"),
					Dst: "http://127.0.0.2:8080/blah2/@1/abc", ProviderID: PIFile, PingURL: ts2.URL},
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
					Dst: "http://127.0.0.3:8080/blah3/xyz", ProviderID: PIDocker, PingURL: failPingULR},
			}, nil
		},
	}

	p3 := &ProviderMock{
		EventsFunc: func(ctx context.Context) <-chan ProviderID {
			return make(chan ProviderID, 1)
		},
		ListFunc: func() ([]URLMapper, error) {
			return nil, errors.New("failed")
		},
	}

	svc := NewService([]Provider{p1, p2, p3}, time.Millisecond*50)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = svc.Run(ctx)
	mappers := svc.Mappers()
	t.Logf("mappers: %v", mappers)

	res := svc.CheckHealth()
	assert.Equal(t, 3, len(res))
	assert.Error(t, res[failPingULR])
	assert.NoError(t, res[ts.URL])
	assert.NoError(t, res[ts2.URL])
}

func TestParseOnlyFrom(t *testing.T) {
	tbl := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "single IP",
			input:    "192.168.1.1",
			expected: []string{"192.168.1.1"},
		},
		{
			name:     "multiple IPs",
			input:    "192.168.1.1, 192.168.1.2, 192.168.1.3, 10.0.0.0/16",
			expected: []string{"192.168.1.1", "192.168.1.2", "192.168.1.3", "10.0.0.0/16"},
		},
		{
			name:     "multiple IPs with extra spaces",
			input:    " 192.168.1.1 , 192.168.1.2 , 192.168.1.3 ",
			expected: []string{"192.168.1.1", "192.168.1.2", "192.168.1.3"},
		},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseOnlyFrom(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
