package route53

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/umputun/reproxy/app/dns"
)

var route53mockServer *httptest.Server

func setupRoute53Mock() {
	r := http.NewServeMux()

	getStatus := func(w http.ResponseWriter, r *http.Request) {
		changeID := strings.TrimPrefix(r.URL.Path, "/2013-04-01/change/")
		var (
			ID          string
			Status      string
			SubmittedAt string
		)
		switch changeID {
		case "valid,not_propagated":
			ID = "valid,pending"
			Status = "PENDING"
			SubmittedAt = "2015-08-30T12:36:00Z"
		case "valid,propagated":
			ID = "valid,success"
			Status = "INSYNC"
			SubmittedAt = "2015-08-30T12:36:00Z"
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("error"))
			return
		}

		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, fmt.Sprintf(`
			<?xml version="1.0" encoding="UTF-8"?>
			<GetChangeResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
				<ChangeInfo>
					<Id>%s</Id>
					<Status>%s</Status>
					<SubmittedAt>%s</SubmittedAt>
				</ChangeInfo>
			</GetChangeResponse>
		`, ID, Status, SubmittedAt))
	}

	addFn := func(w http.ResponseWriter, r *http.Request) {
		hostedZoneID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/2013-04-01/hostedzone/"), "/rrset/")
		xml := `
			<?xml version="1.0" encoding="UTF-8"?>
			<ChangeResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
				<ChangeInfo>
					<Id>/change/%s</Id>
					<Status>%s</Status>
					<SubmittedAt>2015-08-30T12:36:00Z</SubmittedAt>
				</ChangeInfo>
			</ChangeResourceRecordSetsResponse>
		`
		switch hostedZoneID {
		case "valid,not_propagated":
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, fmt.Sprintf(xml, "valid,not_propagated", "PENDING"))
			return
		case "valid,propagated":
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, fmt.Sprintf(xml, "valid,propagated", "INSYNC"))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("error"))
		}
	}

	removeFn := func(w http.ResponseWriter, r *http.Request) {
		hostedZoneID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/2013-04-01/hostedzone/"), "/rrset/")
		xml := `
			<?xml version="1.0" encoding="UTF-8"?>
			<ChangeResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
				<ChangeInfo>
					<Id>/change/%s</Id>
					<Status>%s</Status>
					<SubmittedAt>2015-08-30T12:36:00Z</SubmittedAt>
				</ChangeInfo>
			</ChangeResourceRecordSetsResponse>
		`
		switch hostedZoneID {
		case "valid,not_propagated":
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, fmt.Sprintf(xml, "valid,not_propagated", "PENDING"))
			return
		case "valid,propagated":
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, fmt.Sprintf(xml, "valid,propagated", "INSYNC"))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("error"))
		}
	}

	route := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/2013-04-01/change/"):
			getStatus(w, r)
		case strings.Contains(r.URL.Path, "/2013-04-01/hostedzone/"):
			defer r.Body.Close()
			body, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			var crr changeRecordsRequest
			err = xml.Unmarshal(body, &crr)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			fmt.Println(crr.Action)
			switch crr.Action {
			case "UPSERT":
				addFn(w, r)
			case "DELETE":
				removeFn(w, r)
			}
		}
	}

	r.HandleFunc("/", route)

	route53mockServer = httptest.NewServer(r)
	route53Endpoint = route53mockServer.URL
}

func Test_createCanonicalReq(t *testing.T) {
	ttime, err := time.Parse("20060102T150405Z", "20150830T123600Z")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args awsRequestOpts
		want string
	}{
		{"example from amazon documentation",
			awsRequestOpts{"GET", "/", ttime,
				[]kv{{"Version", "2010-05-08"}, {"Action", "ListUsers"}},
				[]kv{{"content-type", "application/x-www-form-urlencoded; charset=utf-8"}, {"host", "iam.amazonaws.com"}, {"x-amz-date", "20150830T123600Z"}},
				[]byte(""),
				"us-east-1",
				"iam"},
			"f536975d06c0309214f805bb90ccff089219ecd68b2577efef23edd43b7e1a59"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := createCanonicalReq(tt.args); got != tt.want {
				t.Errorf("createCanonicalReq() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_route53_calculateSignature(t *testing.T) {
	ttime, err := time.Parse("20060102T150405Z", "20150830T123600Z")
	if err != nil {
		t.Fatal(err)
	}

	type fields struct {
		secretAccessKey string
	}

	tests := []struct {
		name   string
		fields fields
		args   awsRequestOpts
		want   string
	}{
		{"example from amazon documentation",
			fields{"wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"},
			awsRequestOpts{"GET", "/", ttime,
				[]kv{{"Version", "2010-05-08"}, {"Action", "ListUsers"}},
				[]kv{{"content-type", "application/x-www-form-urlencoded; charset=utf-8"}, {"host", "iam.amazonaws.com"}, {"x-amz-date", "20150830T123600Z"}},
				[]byte(""),
				"us-east-1",
				"iam"},
			"5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &route53{secretAccessKey: tt.fields.secretAccessKey}
			if got := r.calculateSignature(tt.args); got != tt.want {
				t.Errorf("route53.calculateSignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_route53_prepareRequest(t *testing.T) {
	timeFromExample, err := time.Parse("20060102T150405Z", "20150830T123600Z")
	if err != nil {
		t.Fatal(err)
	}

	type fields struct {
		accessKeyID     string
		secretAccessKey string
	}
	type args struct {
		opts awsRequestOpts
	}
	tests := []struct {
		name     string
		endpoint string
		fields   fields
		args     args
		want     *http.Request
		wantErr  bool
	}{
		{"example from amazon documentation",
			"https://iam.amazonaws.com",
			fields{"AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"},
			args{awsRequestOpts{"GET", "/", timeFromExample,
				[]kv{{"Version", "2010-05-08"}, {"Action", "ListUsers"}},
				[]kv{{"content-type", "application/x-www-form-urlencoded; charset=utf-8"},
					{"host", "iam.amazonaws.com"},
					{"x-amz-date", "20150830T123600Z"}},
				[]byte(""),
				"us-east-1",
				"iam"}},
			&http.Request{
				Method:     "GET",
				URL:        &url.URL{Path: "/", RawQuery: "Action=ListUsers&Version=2010-05-08", Scheme: "https", Host: "iam.amazonaws.com"},
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header: http.Header{
					"Content-Type":  []string{"application/x-www-form-urlencoded; charset=utf-8"},
					"Host":          []string{"iam.amazonaws.com"},
					"X-Amz-Date":    []string{"20150830T123600Z"},
					"Authorization": []string{"AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20150830/us-east-1/iam/aws4_request, SignedHeaders=content-type;host;x-amz-date, Signature=5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"},
				},
				Body:          io.NopCloser(bytes.NewReader([]byte(""))),
				ContentLength: 0,
				Host:          "iam.amazonaws.com",
			}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route53Endpoint = tt.endpoint
			r := &route53{
				accessKeyID:     tt.fields.accessKeyID,
				secretAccessKey: tt.fields.secretAccessKey,
			}
			got, err := r.prepareRequest(tt.args.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("route53.prepareRequest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, tt.want.Method, got.Method)
			assert.Equal(t, tt.want.URL, got.URL)
			assert.Equal(t, tt.want.Proto, got.Proto)
			assert.Equal(t, tt.want.ProtoMajor, got.ProtoMajor)
			assert.Equal(t, tt.want.ProtoMinor, got.ProtoMinor)
			assert.Equal(t, tt.want.Header, got.Header)
			assert.Equal(t, tt.want.ContentLength, got.ContentLength)
			assert.Equal(t, tt.want.Host, got.Host)
		})
	}
}

func Test_NewRoute53Provider(t *testing.T) {
	type args struct {
		config route53Config
		opts   dns.Opts
	}
	tests := []struct {
		name    string
		args    args
		want    *route53
		wantErr bool
	}{
		{"valid config", args{
			config: route53Config{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
				HostedZoneID:    "Z2FDTNDATAQYW2",
				Region:          "us-east-1",
				TTL:             300,
			},
			opts: dns.Opts{
				Provider:        "route53",
				Timeout:         time.Second * 10,
				PollingInterval: time.Second * 1,
			},
		}, &route53{
			accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			secretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
			hostedZoneID:    "Z2FDTNDATAQYW2",
			region:          "us-east-1",
			ttl:             300,
		}, false},
		{"invalid config", args{
			config: route53Config{
				AccessKeyID:     "",
				SecretAccessKey: "",
				HostedZoneID:    "",
				Region:          "",
				TTL:             0,
			},
			opts: dns.Opts{
				Provider:        "route53",
				Timeout:         time.Second * 10,
				PollingInterval: time.Second * 1,
			},
		}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("ROUTE53_HOSTED_ZONE_ID", tt.args.config.HostedZoneID)
			os.Setenv("ROUTE53_REGION", tt.args.config.Region)
			os.Setenv("ROUTE53_TTL", strconv.Itoa(tt.args.config.TTL))
			os.Setenv("ROUTE53_ACCESS_KEY_ID", tt.args.config.AccessKeyID)
			os.Setenv("ROUTE53_SECRET_ACCESS_KEY", tt.args.config.SecretAccessKey)

			got, err := NewRoute53Provider(tt.args.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("newRoute53Provider() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if (err != nil) && tt.wantErr {
				return
			}

			route53Prov := got.(*route53)

			assert.Equal(t, tt.want.accessKeyID, route53Prov.accessKeyID)
			assert.Equal(t, tt.want.secretAccessKey, route53Prov.secretAccessKey)
			assert.Equal(t, tt.want.hostedZoneID, route53Prov.hostedZoneID)
			assert.Equal(t, tt.want.region, route53Prov.region)
			assert.Equal(t, tt.want.ttl, route53Prov.ttl)

			os.Unsetenv("ROUTE53_HOSTED_ZONE_ID")
			os.Unsetenv("ROUTE53_REGION")
			os.Unsetenv("ROUTE53_TTL")
			os.Unsetenv("ROUTE53_ACCESS_KEY_ID")
			os.Unsetenv("ROUTE53_SECRET_ACCESS_KEY")
		})
	}
}

func Test_route53_isUpdated(t *testing.T) {
	setupRoute53Mock()
	tests := []struct {
		name         string
		addedRecords []recordWithID
		hostedZoneID string
		changeID     string
		want         bool
		wantErr      bool
	}{
		{name: "valid,propagated", hostedZoneID: "valid,propagated", addedRecords: []recordWithID{
			{ID: "valid,propagated", Record: dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"}},
		}, changeID: "valid,propagated", want: true, wantErr: false},
		{name: "valid,not_propagated", hostedZoneID: "valid,not_propagated", addedRecords: []recordWithID{
			{ID: "valid,not_propagated", Record: dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"}},
		}, changeID: "valid,not_propagated", want: false, wantErr: false},
		{name: "invalid", hostedZoneID: "invalid", addedRecords: []recordWithID{
			{ID: "invalid", Record: dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"}},
		}, changeID: "invalid", want: false, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &route53{
				accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				secretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
				region:          "us-east-1",
				hostedZoneID:    tt.hostedZoneID,
				timeout:         time.Second * 10,
				poolingInterval: time.Second * 1,
				ttl:             300,
				client: &http.Client{
					Timeout: time.Second * 30,
				},
				addedRecords: tt.addedRecords,
			}
			got, err := r.isUpdated(tt.changeID)
			if (err != nil) != tt.wantErr {
				t.Errorf("route53.isUpdated() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("route53.isUpdated() = %v, want %v", got, tt.want)
			}
		})
	}
	route53mockServer.Close()
}

func Test_route53_changeRecord(t *testing.T) {
	setupRoute53Mock()
	tests := []struct {
		name         string
		record       dns.Record
		hostedZoneID string
		changeID     string
		wantErr      bool
	}{
		{name: "valid,propagated", hostedZoneID: "valid,propagated",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "valid,propagated", wantErr: false},
		{name: "valid,not_propagated", hostedZoneID: "valid,not_propagated",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "valid,not_propagated", wantErr: false},
		{name: "invalid", hostedZoneID: "invalid",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "invalid", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &route53{
				accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				secretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
				region:          "us-east-1",
				hostedZoneID:    tt.hostedZoneID,
				timeout:         time.Second * 10,
				poolingInterval: time.Second * 1,
				ttl:             300,
				client: &http.Client{
					Timeout: time.Second * 30,
				},
			}
			got, err := r.changeRecord("UPSERT", tt.record)
			if (err != nil) != tt.wantErr {
				t.Errorf("route53.changeRecord() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil && tt.wantErr {
				return
			}

			assert.Equal(t, "/change/"+tt.changeID, got.ID)
		})
	}
	route53mockServer.Close()
}

func Test_route53_AddRecord(t *testing.T) {
	setupRoute53Mock()
	tests := []struct {
		name         string
		record       dns.Record
		hostedZoneID string
		changeID     string
		wantErr      bool
	}{
		{name: "valid,propagated", hostedZoneID: "valid,propagated",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "valid,propagated", wantErr: false},
		{name: "valid,not_propagated", hostedZoneID: "valid,not_propagated",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "valid,not_propagated", wantErr: false},
		{name: "invalid", hostedZoneID: "invalid",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "invalid", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &route53{
				accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				secretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
				region:          "us-east-1",
				hostedZoneID:    tt.hostedZoneID,
				timeout:         time.Second * 10,
				poolingInterval: time.Second * 1,
				ttl:             300,
				client: &http.Client{
					Timeout: time.Second * 30,
				},
			}
			err := r.AddRecord(tt.record)
			if (err != nil) != tt.wantErr {
				t.Errorf("route53.AddRecord() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.wantErr {
				return
			}
			assert.Equal(t, []recordWithID{{ID: tt.changeID, Record: tt.record}}, r.addedRecords)
		})
	}
}

func Test_route53_RemoveRecord(t *testing.T) {
	setupRoute53Mock()

	addedRecords := []recordWithID{
		{ID: "valid,propagated", Record: dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"}},
		{ID: "valid,not_propagated", Record: dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example-1.com", Value: "1234"}},
		{ID: "some,record", Record: dns.Record{Type: "TXT", Host: "test", Domain: "one.com", Value: "1234"}},
	}

	tests := []struct {
		name         string
		record       dns.Record
		hostedZoneID string
		changeID     string
		wantErr      bool
	}{
		{name: "valid,propagated", hostedZoneID: "valid,propagated",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "valid,propagated", wantErr: false},
		{name: "valid,not_propagated", hostedZoneID: "valid,not_propagated",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "valid,not_propagated", wantErr: false},
		{name: "invalid", hostedZoneID: "invalid",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "invalid", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ar := make([]recordWithID, len(addedRecords))
			copy(ar, addedRecords)
			r := &route53{
				accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				secretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
				region:          "us-east-1",
				hostedZoneID:    tt.hostedZoneID,
				timeout:         time.Second * 10,
				poolingInterval: time.Second * 1,
				ttl:             300,
				client: &http.Client{
					Timeout: time.Second * 30,
				},
				addedRecords: ar,
			}
			err := r.RemoveRecord(tt.record)
			if (err != nil) != tt.wantErr {
				t.Errorf("route53.RemoveRecord() error = %v, wantErr %v", err, tt.wantErr)
			}

			if err != nil && tt.wantErr {
				return
			}

			assert.Equal(t, len(addedRecords)-1, len(r.addedRecords))
			assert.NotContains(t, r.addedRecords, recordWithID{ID: tt.changeID, Record: tt.record})
		})
	}

	route53mockServer.Close()
}

func Test_route53_WaitUntilPropagated(t *testing.T) {
	setupRoute53Mock()
	tests := []struct {
		name         string
		record       dns.Record
		hostedZoneID string
		changeID     string
		wantErr      bool
	}{
		{name: "valid,propagated", hostedZoneID: "valid,propagated",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "valid,propagated", wantErr: false},
		{name: "valid,not_propagated", hostedZoneID: "valid,not_propagated",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "valid,not_propagated", wantErr: true},
		{name: "invalid", hostedZoneID: "invalid",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "invalid", wantErr: true},
		{name: "no changeID", hostedZoneID: "invalid",
			record:   dns.Record{Type: "TXT", Host: "_acme-challenge", Domain: "example.com", Value: "1234"},
			changeID: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &route53{
				accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				secretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
				region:          "us-east-1",
				hostedZoneID:    "SDdosdjaHOSTZONEDI",
				timeout:         time.Second * 2,
				poolingInterval: time.Microsecond * 10,
				ttl:             300,
				client: &http.Client{
					Timeout: time.Second * 5,
				},
				addedRecords: []recordWithID{{ID: tt.changeID, Record: tt.record}},
			}
			if err := r.WaitUntilPropagated(context.Background(), tt.record); (err != nil) != tt.wantErr {
				t.Errorf("route53.WaitUntilPropagated() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
	route53mockServer.Close()
}
