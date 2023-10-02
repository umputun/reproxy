package cloudns

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ilyakaznacheev/cleanenv"

	"github.com/umputun/reproxy/app/dns"
)

var (
	baseEndpointURL = "https://api.cloudns.net/dns/"

	addRecordURL    = baseEndpointURL + "add-record.json"
	updateStatusURL = baseEndpointURL + "update-status.json"
	removeRecordURL = baseEndpointURL + "delete-record.json"
)

type cloudnsConfig struct {
	AuthID    string `yaml:"auth_id" env:"CLOUDNS_AUTH_ID" env-default:""`
	SubAuthID string `yaml:"sub_auth_id" env:"CLOUDNS_SUB_AUTH_ID" env-default:""`
	Password  string `yaml:"password" env:"CLOUDNS_AUTH_PASSWORD"`
	TTL       int    `yaml:"ttl" env:"CLOUDNS_TTL" env-default:"300"`
}

type recordWithID struct {
	id     int
	record dns.Record
}

type cloudnsProvider struct {
	authID          string
	subAuthID       string
	authPassword    string
	timeout         time.Duration
	poolingInterval time.Duration
	TTL             int
	client          *http.Client
	addedRecords    []recordWithID
}

// NewCloudnsProvider creates a new CloudnsProvider DNS provider
func NewCloudnsProvider(opts dns.Opts) (dns.Provider, error) {
	var conf cloudnsConfig

	// try to read config from file first and fallback to environment variables
	if err := cleanenv.ReadConfig(opts.ConfigPath, &conf); err != nil {
		if errc := cleanenv.ReadEnv(&conf); errc != nil {
			return nil, fmt.Errorf("cloudns: unable to read required parameters: %v", err)
		}
	}

	c := &cloudnsProvider{
		authID:          conf.AuthID,
		subAuthID:       conf.SubAuthID,
		authPassword:    conf.Password,
		timeout:         opts.Timeout,
		poolingInterval: opts.PollingInterval,
		client:          &http.Client{Timeout: opts.Timeout},
		TTL:             conf.TTL,
	}

	if c.authID == "" && c.subAuthID == "" {
		return nil, fmt.Errorf("authID or subAuthID must be provided")
	}
	return c, nil
}

func (p *cloudnsProvider) AddRecord(record dns.Record) error {
	params := map[string]string{
		"record-type": "TXT",
		"record":      record.Value,
		"domain-name": record.Domain,
		"ttl":         strconv.Itoa(p.TTL),
		"host":        record.Host,
	}

	b, err := p.doRequest("POST", addRecordURL, params)
	if err != nil {
		return err
	}

	res := struct {
		Status            string `json:"status"`
		StatusDescription string `json:"statusDescription"`
		Data              struct {
			ID int `json:"id"`
		} `json:"data"`
	}{}

	if err := json.Unmarshal(b, &res); err != nil {
		return err
	}

	if res.Status != "Success" {
		return fmt.Errorf("%s: %s", res.Status, res.StatusDescription)
	}

	if p.addedRecords == nil {
		p.addedRecords = make([]recordWithID, 0, 1)
	}
	p.addedRecords = append(p.addedRecords, recordWithID{id: res.Data.ID, record: record})

	return nil
}

func (p *cloudnsProvider) RemoveRecord(record dns.Record) error {
	var r *recordWithID
	for i := range p.addedRecords {
		r = &p.addedRecords[i]
		if r.record.Host == record.Host &&
			r.record.Domain == record.Domain &&
			r.record.Value == record.Value {
			break
		}
	}

	if r == nil {
		return fmt.Errorf("cloudns: recordID not found for %s", fmt.Sprintf("%s.%s", record.Host, record.Domain))
	}

	params := map[string]string{
		"domain-name": record.Domain,
		"record-id":   strconv.Itoa(r.id),
	}

	b, err := p.doRequest(http.MethodPost, removeRecordURL, params)
	if err != nil {
		return fmt.Errorf("cloudns: unable to remove record: %v", err)
	}

	res := struct {
		Status            string `json:"status"`
		StatusDescription string `json:"statusDescription"`
	}{}

	if err := json.Unmarshal(b, &res); err != nil {
		return fmt.Errorf("cloudns: unable to unmarshal response: %v", err)
	}

	if res.Status != "Success" {
		return fmt.Errorf("cloudns: status not Success: %s: %s", res.Status, res.StatusDescription)
	}

	recs := p.addedRecords[:0]
	for _, rec := range p.addedRecords {
		if rec.id != r.id {
			recs = append(recs, rec)
		}
	}
	p.addedRecords = recs

	return nil
}

func (p *cloudnsProvider) WaitUntilPropagated(ctx context.Context, record dns.Record) error {
	ticker := time.NewTicker(p.poolingInterval)
	timer := time.NewTimer(p.timeout)

	for {
		select {
		case <-ticker.C:
			updated, err := p.isUpdated(record.Domain)
			if err != nil {
				return err
			}
			if updated {
				return nil
			}
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for DNS propagation")
		case <-timer.C:
			return fmt.Errorf("timeout waiting for records update")
		}
	}
}

func (p *cloudnsProvider) isUpdated(domain string) (bool, error) {
	params := map[string]string{
		"domain-name": domain,
	}

	b, err := p.doRequest("GET", updateStatusURL, params)
	if err != nil {
		return false, err
	}

	servers := []struct {
		Server  string `json:"server"`
		IP4     string `json:"ip4"`
		IP6     string `json:"ip6"`
		Updated bool   `json:"updated"`
	}{}

	if err := json.Unmarshal(b, &servers); err != nil {
		return false, err
	}

	updated := 0
	for _, server := range servers {
		if server.Updated {
			updated++
		}
	}
	return updated == len(servers), nil
}

func (p *cloudnsProvider) doRequest(method, endpoint string, params map[string]string) (json.RawMessage, error) {
	reqURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := reqURL.Query()

	for k, v := range params {
		q.Set(k, v)
	}

	// these should be set for all requests
	q.Set("sub-auth-id", p.subAuthID)
	q.Set("auth-id", p.authID)
	q.Set("auth-password", p.authPassword)

	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequest(method, reqURL.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invalid status code %v", resp.Status)
	}

	defer resp.Body.Close() //nolint

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}
