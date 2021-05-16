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
					Dst: "http://127.0.0.3:8080/blah3/xyz", ProviderID: PIDocker},
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
				{Server: "m.example.com", SrcMatch: *regexp.MustCompile("^/api/svc4/(.*)"),
					Dst: "http://127.0.0.4:8080/blah2/$1/abc", MatchType: MTProxy, dead: true},
				{Server: "m.example.com", SrcMatch: *regexp.MustCompile("^/api/svc5/(.*)"),
					Dst: "http://127.0.0.5:8080/blah2/$1/abc", MatchType: MTProxy, dead: false},
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
				{SrcMatch: *regexp.MustCompile("/web"), Dst: "/var/web", ProviderID: PIDocker, MatchType: MTStatic,
					AssetsWebRoot: "/web", AssetsLocation: "/var/web"},
				{SrcMatch: *regexp.MustCompile("/www/"), Dst: "/var/web", ProviderID: PIDocker, MatchType: MTStatic,
					AssetsWebRoot: "/www", AssetsLocation: "/var/web"},
				{SrcMatch: *regexp.MustCompile("/path/"), Dst: "/var/web/path", ProviderID: PIDocker, MatchType: MTStatic},
			}, nil
		},
	}
	svc := NewService([]Provider{p1, p2}, time.Millisecond*100)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx)
	require.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	assert.Equal(t, 8, len(svc.Mappers()))

	tbl := []struct {
		server, src string
		res         Matches
	}{

		{"example.com", "/api/svc3/xyz/something", Matches{MTProxy, []MatchedRoute{{"http://127.0.0.3:8080/blah3/xyz/something", true}}}},
		{"example.com", "/api/svc3/xyz", Matches{MTProxy, []MatchedRoute{{"http://127.0.0.3:8080/blah3/xyz", true}}}},
		{"abc.example.com", "/api/svc1/1234", Matches{MTProxy, []MatchedRoute{{"http://127.0.0.1:8080/blah1/1234", true}}}},
		{"zzz.example.com", "/aaa/api/svc1/1234", Matches{MTProxy, nil}},
		{"m.example.com", "/api/svc2/1234", Matches{MTProxy, []MatchedRoute{{"http://127.0.0.2:8080/blah2/1234/abc", true}}}},
		{"m1.example.com", "/api/svc2/1234", Matches{MTProxy, nil}},
		{"m.example.com", "/api/svc4/id12345", Matches{MTProxy, []MatchedRoute{{"http://127.0.0.4:8080/blah2/id12345/abc", false}}}},
		{"m.example.com", "/api/svc5/num123456", Matches{MTProxy, []MatchedRoute{{"http://127.0.0.5:8080/blah2/num123456/abc", true}}}},

		{"m1.example.com", "/web/index.html", Matches{MTStatic, []MatchedRoute{{"/web:/var/web/", true}}}},
		{"m1.example.com", "/web/", Matches{MTStatic, []MatchedRoute{{"/web:/var/web/", true}}}},
		{"m1.example.com", "/www/something", Matches{MTStatic, []MatchedRoute{{"/www:/var/web/", true}}}},
		{"m1.example.com", "/www/", Matches{MTStatic, []MatchedRoute{{"/www:/var/web/", true}}}},
		{"m1.example.com", "/www", Matches{MTStatic, []MatchedRoute{{"/www:/var/web/", true}}}},
		{"xyx.example.com", "/path/something", Matches{MTStatic, []MatchedRoute{{"/path:/var/web/path/", true}}}},
	}

	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			res := svc.Match(tt.server, tt.src)
			assert.Equal(t, tt.res, res)
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
	port := rand.Intn(10000) + 40000
	failPingULR := fmt.Sprintf("http://127.0.0.1:%d", port)

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

	svc := NewService([]Provider{p1, p2, p3}, time.Millisecond*10)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = svc.Run(ctx)
	mappers := svc.Mappers()
	fmt.Println(mappers)

	tests := []struct {
		name string
		want map[string]error
	}{
		{name: "case 1",
			want: map[string]error{
				ts.URL:      nil,
				ts2.URL:     nil,
				failPingULR: fmt.Errorf("some error"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.CheckHealth()
			for pingURL, err := range got {
				wantErr, ok := tt.want[pingURL]
				if !ok {
					t.Errorf("CheckHealth() = ping URL %s not found in test case", pingURL)
					continue
				}

				if (err != nil && wantErr == nil) ||
					(err == nil && wantErr != nil) {
					t.Errorf("CheckHealth() error = %v, wantErr %v", err, wantErr)
				}
			}
		})
	}
}
