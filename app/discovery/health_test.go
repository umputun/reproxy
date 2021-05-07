package discovery

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
			_, err := ping(tt.args.m)
			if (err != nil) != tt.wantErr {
				t.Errorf("ping() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestCheckHealth(t *testing.T) {
	port := rand.Intn(10000) + 40000
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))
	defer ts.Close()

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))
	defer ts2.Close()

	type args struct {
		mappers []URLMapper
	}
	tests := []struct {
		name string
		args args
		want CheckResult
	}{
		{name: "case 1", args: args{mappers: []URLMapper{{PingURL: ts.URL}, {PingURL: ts2.URL}, {PingURL: fmt.Sprintf("127.0.0.1:%d", port)}}},
			want: CheckResult{Ok: false, Total: 3, Valid: 2, mappers: []URLMapper{{PingURL: ts.URL, dead: false}, {PingURL: ts2.URL, dead: false},
				{PingURL: fmt.Sprintf("127.0.0.1:%d", port), dead: true}}}},
		{name: "case 2", args: args{mappers: []URLMapper{{PingURL: ts.URL}, {PingURL: ts2.URL}}},
			want: CheckResult{Ok: true, Total: 2, Valid: 2, mappers: []URLMapper{{PingURL: ts.URL, dead: false}, {PingURL: ts2.URL, dead: false}}}},
		{name: "case 3", args: args{mappers: []URLMapper{{PingURL: ts.URL, MatchType: MTStatic}, {PingURL: ts2.URL}}},
			want: CheckResult{Ok: true, Total: 1, Valid: 1, mappers: []URLMapper{{PingURL: ts.URL, dead: false}, {PingURL: ts2.URL, dead: false}}}},
		{name: "case 4", args: args{mappers: []URLMapper{{}, {PingURL: ts2.URL}}},
			want: CheckResult{Ok: true, Total: 2, Valid: 1, mappers: []URLMapper{{PingURL: ts.URL, dead: false}, {PingURL: ts2.URL, dead: true}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckHealth(tt.args.mappers)
			got.Errs = got.Errs[:0]
			if got.Ok != tt.want.Ok || got.Total != tt.want.Total || got.Valid != tt.want.Valid {
				t.Errorf("CheckHealth() = %v, want %v", got, tt.want)
			}
		})
	}
}
