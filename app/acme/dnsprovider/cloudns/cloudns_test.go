package cloudns

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/umputun/reproxy/app/dns"
)

const (
	envPrefix = "CLOUDNS_"

	envAuthID                     = envPrefix + "AUTH_ID"
	envSubAuthID                  = envPrefix + "SUB_AUTH_ID"
	envAuthPassword               = envPrefix + "AUTH_PASSWORD"
	envTTL                        = envPrefix + "TTL"
	envDNSPropagationTimeout      = envPrefix + "DNS_PROPAGATION_TIMEOUT"
	envDNSPropagationCheckInteval = envPrefix + "DNS_PROPAGATION_CHECK_INTERVAL"
)

var numWaitUntilPropagatedCalled = 0

var cloudnsMockServer *httptest.Server

func TestMain(m *testing.M) {
	setupCloudnsMock()
	os.Exit(m.Run())
}

func setupCloudnsMock() {
	r := http.NewServeMux()

	r.HandleFunc("/addrecord", func(w http.ResponseWriter, r *http.Request) {
		var res struct {
			Status            string `json:"status"`
			StatusDescription string `json:"statusDescription"`
			Data              struct {
				ID int `json:"id"`
			} `json:"data"`
		}

		host := r.URL.Query().Get("host")

		id, err := strconv.Atoi(host)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "for this test the host should be an integer %v", err)
			return
		}
		res.Data.ID = id

		switch host {
		case "2":
			res.Status = "error"
			res.StatusDescription = "host not found"
		case "":
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("bad request"))
			return
		default:
			res.Status = "Success"
			res.StatusDescription = "success"

		}

		d, err := json.Marshal(res)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal server error"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(d)
	})

	r.HandleFunc("/removerecord", func(w http.ResponseWriter, r *http.Request) {
		var res struct {
			Status            string `json:"status"`
			StatusDescription string `json:"statusDescription"`
		}

		recordID, err := strconv.Atoi(r.URL.Query().Get("record-id"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "for this test the record-id should be an integer %v", err)
			return
		}
		switch recordID {
		case 0:
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("bad request"))
			return
		case 2:
			res.Status = "error"
			res.StatusDescription = "record not found"
		default:
			res.Status = "Success"
			res.StatusDescription = "success"
		}

		d, err := json.Marshal(res)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal server error"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(d)
	})

	r.HandleFunc("/updatestatus", func(w http.ResponseWriter, r *http.Request) {
		numWaitUntilPropagatedCalled++
		type server struct {
			Server  string `json:"server"`
			IP4     string `json:"ip4"`
			IP6     string `json:"ip6"`
			Updated bool   `json:"updated"`
		}

		servers := []server{}

		domain := r.URL.Query().Get("domain-name")
		switch domain {
		case "":
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("bad request"))
			return
		case "mycompany1.com":
			servers = []server{
				{"ns1.cloudns.com", "", "", true},
				{"ns2.cloudns.com", "", "", true},
			}
		case "mycompany2.com":
			servers = []server{
				{"ns1.cloudns.com", "", "", true},
				{"ns2.cloudns.com", "", "", false},
			}
		case "mycompany3.com":
			servers = []server{
				{"ns1.cloudns.com", "", "", true},
				{"ns2.cloudns.com", "", "", numWaitUntilPropagatedCalled > 3},
			}
		}

		d, err := json.Marshal(servers)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal server error"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(d)
	})

	cloudnsMockServer = httptest.NewServer(r)
	baseEndpointURL = cloudnsMockServer.URL
	addRecordURL = baseEndpointURL + "/addrecord"
	removeRecordURL = baseEndpointURL + "/removerecord"
	updateStatusURL = baseEndpointURL + "/updatestatus"
}

func Test_NewCloudnsProvider(t *testing.T) {
	type envs struct {
		authID       string
		subAuthID    string
		authPassword string
		TTL          string
	}

	tests := []struct {
		name    string
		envs    envs
		wantErr bool
	}{
		{"envs for authID and subAuthID not set",
			envs{"", "", "", ""},
			true},
		{"env for password not set",
			envs{"account", "subaccount", "", ""},
			true},
		{"with default optional parameters",
			envs{"account", "subaccount", "init1234", ""},
			false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(envAuthID, tt.envs.authID)
			setEnv(envSubAuthID, tt.envs.subAuthID)
			setEnv(envAuthPassword, tt.envs.authPassword)
			setEnv(envTTL, tt.envs.TTL)

			got, err := NewCloudnsProvider(dns.Opts{})
			if (err != nil) && !tt.wantErr {
				t.Errorf("newCloudnsProvider() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil && tt.wantErr {
				return
			}

			cloundsProv := got.(*cloudnsProvider)

			assert.Equal(t, tt.envs.authID, cloundsProv.authID, "authID")
			assert.Equal(t, tt.envs.subAuthID, cloundsProv.subAuthID, "subAuthID")
			assert.Equal(t, tt.envs.authPassword, cloundsProv.authPassword, "authPassword")

			os.Unsetenv(envAuthID)
			os.Unsetenv(envSubAuthID)
			os.Unsetenv(envAuthPassword)
			os.Unsetenv(envTTL)
			os.Unsetenv(envDNSPropagationTimeout)
			os.Unsetenv(envDNSPropagationCheckInteval)

			if err != nil && tt.wantErr {
				return
			}
		})
	}
}

func setEnv(env, value string) {
	if value != "" {
		os.Setenv(env, value)
	}
}

func Test_cloudnsProvider_AddRecord(t *testing.T) {
	type args struct {
		record dns.Record
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{"correct", args{dns.Record{Host: "1", Domain: "mycompany1.com", Value: "jkdewjr89234", Type: "TXT"}}, false},
		{"failed add a record", args{dns.Record{Host: "2", Domain: "mycompany2.com", Value: "sadfkjfdho", Type: "TXT"}}, true},
		{"bad request", args{dns.Record{}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &cloudnsProvider{client: &http.Client{}}
			err := p.AddRecord(tt.args.record)
			if err != nil && !tt.wantErr {
				t.Errorf("cloudnsProvider.AddRecord() error = %v, wantErr %v", err, tt.wantErr)
			}

			if err != nil && tt.wantErr {
				return
			}

			id, _ := strconv.Atoi(tt.args.record.Host)
			expected := recordWithID{
				id:     id,
				record: tt.args.record,
			}

			assert.Equal(t, expected, p.addedRecords[0], "record")
		})
	}
}

func Test_cloudnsProvider_RemoveRecord(t *testing.T) {
	addedRecords := []recordWithID{
		{id: 1, record: dns.Record{Host: "1", Domain: "mycompany1.com", Value: "jkdewjr89234", Type: "TXT"}},
		{id: 2, record: dns.Record{Host: "2", Domain: "mycompany2.com", Value: "sadfkjfdho", Type: "TXT"}},
	}
	type args struct {
		record dns.Record
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{"correct", args{dns.Record{Host: "1", Domain: "mycompany1.com", Value: "jkdewjr89234", Type: "TXT"}}, false},
		{"failed remove a record", args{dns.Record{Host: "2", Domain: "mycompany2.com", Value: "sadfkjfdho", Type: "TXT"}}, true},
		{"bad request", args{dns.Record{}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := make([]recordWithID, len(addedRecords))
			copy(rec, addedRecords)

			p := &cloudnsProvider{
				client:       &http.Client{},
				addedRecords: rec,
			}
			err := p.RemoveRecord(tt.args.record)
			if (err != nil) && !tt.wantErr {
				t.Errorf("cloudnsProvider.RemoveRecord() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil && tt.wantErr {
				return
			}

			assert.Equal(t, len(addedRecords)-1, len(p.addedRecords), "addedRecords")
			for _, r := range p.addedRecords {
				if r.record.Host == tt.args.record.Host &&
					r.record.Domain == tt.args.record.Domain &&
					r.record.Type == tt.args.record.Type &&
					r.record.Value == tt.args.record.Value {
					t.Errorf("record has not been removed %v", r)
				}
			}

		})
	}
}

func Test_cloudnsProvider_isUpdated(t *testing.T) {
	type args struct {
		domain string
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{"correct", args{domain: "mycompany1.com"}, true, false},
		{"one server not updated", args{domain: "mycompany2.com"}, false, false},
		{"bad request", args{domain: ""}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &cloudnsProvider{
				client: &http.Client{},
			}
			got, err := p.isUpdated(tt.args.domain)
			if (err != nil) && !tt.wantErr {
				t.Errorf("cloudnsProvider.isUpdated() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("cloudnsProvider.isUpdated() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_cloudnsProvider_WaitUntilPropagated(t *testing.T) {
	tests := []struct {
		name    string
		record  dns.Record
		wantErr bool
	}{
		{"correct", dns.Record{Domain: "mycompany1.com"}, false},
		{"timeout waiting", dns.Record{Domain: "mycompany2.com"}, true},
		{"status updated while waiting", dns.Record{Domain: "mycompany3.com"}, false},
	}
	for _, tt := range tests {
		numWaitUntilPropagatedCalled = 0

		p := &cloudnsProvider{
			timeout:         time.Second * 4,
			poolingInterval: time.Millisecond * 100,
			client:          &http.Client{},
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		err := p.WaitUntilPropagated(ctx, tt.record)
		cancel()
		if err != nil && !tt.wantErr {
			t.Errorf("cloudnsProvider.WaitUntilPropagated() error = %v, wantErr %v", err, tt.wantErr)
		}

	}
}
