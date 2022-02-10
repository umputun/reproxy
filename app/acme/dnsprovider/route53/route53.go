package route53

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/umputun/reproxy/app/dns"
)

var route53Endpoint = "https://route53.amazonaws.com"

// changeRecordsReq is the payload for the ChangeResourceRecordSets API call.
type changeRecordsRequest struct {
	XMLName xml.Name `xml:"https://route53.amazonaws.com/doc/2013-04-01/ ChangeResourceRecordSetsRequest"`
	Comment string   `xml:"ChangeBatch>Comment,omitempty"`
	Action  string   `xml:"ChangeBatch>Changes>Change>Action"`
	Name    string   `xml:"ChangeBatch>Changes>Change>ResourceRecordSet>Name"`
	Type    string   `xml:"ChangeBatch>Changes>Change>ResourceRecordSet>Type"`
	TTL     int      `xml:"ChangeBatch>Changes>Change>ResourceRecordSet>TTL"`
	Value   string   `xml:"ChangeBatch>Changes>Change>ResourceRecordSet>ResourceRecords>ResourceRecord>Value"`
}

type changeRecordsResponse struct {
	XMLName     xml.Name `xml:"ChangeResourceRecordSetsResponse"`
	Comment     string   `xml:"ChangeInfo>Comment"`
	ID          string   `xml:"ChangeInfo>Id"`
	Status      string   `xml:"ChangeInfo>Status"`
	SubmittedAt string   `xml:"ChangeInfo>SubmittedAt"`
}

type getChangeResponse struct {
	XMLName     xml.Name `xml:"GetChangeResponse"`
	ID          string   `xml:"ChangeInfo>Id"`
	Status      string   `xml:"ChangeInfo>Status"`
	SubmittedAt string   `xml:"ChangeInfo>SubmittedAt"`
}

type kv struct {
	key   string
	value string
}

// awsRequestOpts are the parameters used to make an AWS request.
type awsRequestOpts struct {
	method      string
	uri         string
	amzTime     time.Time
	queryParams []kv
	headers     []kv
	payload     []byte
	region      string
	service     string
}

type route53Config struct {
	AccessKeyID     string `yaml:"access_key_id" env:"ROUTE53_ACCESS_KEY_ID"`
	SecretAccessKey string `yaml:"secret_access_key" env:"ROUTE53_SECRET_ACCESS_KEY"`
	HostedZoneID    string `yaml:"hosted_zone_id" env:"ROUTE53_HOSTED_ZONE_ID"`
	TTL             int    `yaml:"ttl" env:"ROUTE53_TTL"`
	Region          string `yaml:"region" env:"ROUTE53_REGION"`
}

type recordWithID struct {
	ID string
	dns.Record
}

type route53 struct {
	accessKeyID     string
	secretAccessKey string
	region          string
	hostedZoneID    string
	timeout         time.Duration
	poolingInterval time.Duration
	ttl             int
	client          *http.Client
	addedRecords    []recordWithID
}

// NewRoute53Provider creates a new Route53 provider.
func NewRoute53Provider(opts dns.Opts) (dns.Provider, error) {
	var conf route53Config

	// try to read config from file first and fallback to environment variables
	if err := cleanenv.ReadConfig(opts.ConfigPath, &conf); err != nil {
		if errc := cleanenv.ReadEnv(&conf); errc != nil {
			return nil, fmt.Errorf("route53: unable to read required parameters: %v", err)
		}
	}

	if conf.AccessKeyID == "" || conf.SecretAccessKey == "" || conf.HostedZoneID == "" {
		return nil, fmt.Errorf("route53: required parameters not found")
	}

	return &route53{
		accessKeyID:     conf.AccessKeyID,
		secretAccessKey: conf.SecretAccessKey,
		region:          conf.Region,
		hostedZoneID:    conf.HostedZoneID,
		addedRecords:    []recordWithID{},
		ttl:             conf.TTL,
		timeout:         opts.Timeout,
		poolingInterval: opts.PollingInterval,
		client:          &http.Client{Timeout: opts.Timeout},
	}, nil
}

// AddRecord creates TXT records for the specified FQDN and value.
func (r *route53) AddRecord(record dns.Record) error {
	resp, err := r.changeRecord("UPSERT", record)
	if err != nil {
		return err
	}

	r.addedRecords = append(r.addedRecords, recordWithID{
		ID:     strings.TrimPrefix(resp.ID, "/change/"),
		Record: record,
	})

	return nil
}

// RemoveRecord removes the TXT records matching the specified FQDN and value.
func (r *route53) RemoveRecord(record dns.Record) error {
	_, err := r.changeRecord("DELETE", record)
	if err != nil {
		return err
	}

	recs := r.addedRecords[:0]
	for _, rec := range r.addedRecords {
		if rec.Host == record.Host && rec.Domain == record.Domain &&
			rec.Type == record.Type && rec.Value == record.Value {
			continue
		}
		recs = append(recs, rec)
	}
	r.addedRecords = recs

	return nil
}

// WaitUntilPropagated waits for the DNS records to propagate.
// The method will be called after creating TXT records. A provider API could be
// used to check propagation status.
func (r *route53) WaitUntilPropagated(ctx context.Context, record dns.Record) error {
	ticker := time.NewTicker(r.poolingInterval)
	timer := time.NewTimer(r.timeout)

	var changeID string
	for _, rec := range r.addedRecords {
		if rec.Host == record.Host && rec.Domain == record.Domain &&
			rec.Type == record.Type && rec.Value == record.Value {
			changeID = rec.ID
			break
		}
	}

	if changeID == "" {
		return fmt.Errorf("route53: changeID for record %s not found", record)
	}

	for {
		select {
		case <-ticker.C:
			updated, err := r.isUpdated(changeID)
			if err != nil {
				return err
			}
			if updated {
				return nil
			}
		case <-ctx.Done():
			return fmt.Errorf("route53: timeout waiting for DNS propagation")
		case <-timer.C:
			return fmt.Errorf("route53: timeout waiting for DNS propagation")
		}
	}
}

func (r *route53) isUpdated(changeID string) (bool, error) {
	t := time.Now().UTC()

	reqOpts := awsRequestOpts{
		method:  "GET",
		uri:     fmt.Sprintf("/2013-04-01/change/%s", changeID),
		amzTime: t,
		queryParams: []kv{
			{key: "Action", value: "GetChange"},
			{key: "Id", value: changeID},
			{key: "Version", value: "2013-04-01"},
		},
		headers: []kv{
			{key: "Host", value: "route53.amazonaws.com"},
			{key: "X-Amz-Date", value: t.Format("20060102T150405Z")},
		},
		payload: []byte(""),
		region:  r.region,
		service: "route53",
	}

	req, err := r.prepareRequest(reqOpts)
	if err != nil {
		return false, err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return false, err
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("route53: errorcode by retrieving record status %s", resp.Status)
	}

	var response getChangeResponse
	if err := xml.NewDecoder(resp.Body).Decode(&response); err != nil {
		return false, err
	}

	if response.Status != "INSYNC" {
		return false, nil
	}

	return true, nil
}

func (r *route53) changeRecord(action string, record dns.Record) (*changeRecordsResponse, error) {
	t := time.Now().UTC()

	payload := changeRecordsRequest{
		Action: action,
		Name:   fmt.Sprintf("%s.%s.", record.Host, record.Domain),
		Type:   record.Type,
		TTL:    r.ttl,
		Value:  fmt.Sprintf("%q", record.Value),
	}

	bp, err := xml.Marshal(payload)
	if err != nil {
		return nil, err
	}

	reqOpts := awsRequestOpts{
		method:  "POST",
		uri:     "/2013-04-01/hostedzone/" + r.hostedZoneID + "/rrset/",
		amzTime: t,
		queryParams: []kv{
			{key: "Action", value: "ChangeResourceRecordSets"},
			{key: "Id", value: r.hostedZoneID},
			{key: "Version", value: "2013-04-01"},
		},
		headers: []kv{
			{key: "Host", value: "route53.amazonaws.com"},
			{key: "X-Amz-Date", value: t.Format("20060102T150405Z")},
		},
		payload: bp,
		region:  r.region,
		service: "route53",
	}

	req, err := r.prepareRequest(reqOpts)
	if err != nil {
		return nil, err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("route53: unexpected status code by changing record %s", resp.Status)
	}

	var response changeRecordsResponse
	if err := xml.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return &response, nil
}

func (r *route53) prepareRequest(opts awsRequestOpts) (*http.Request, error) {
	req, err := http.NewRequest(opts.method, route53Endpoint+opts.uri, bytes.NewReader(opts.payload))
	if err != nil {
		return nil, err
	}

	signature := r.calculateSignature(opts)

	hdrs := make([]string, 0, len(opts.headers))
	for _, h := range opts.headers {
		hdrs = append(hdrs, h.key)
	}

	shdrs := strings.Join(hdrs, ";")
	cred := fmt.Sprintf("%s/%s/%s/%s/aws4_request", r.accessKeyID, opts.amzTime.Format("20060102"), opts.region, opts.service)

	req.Header.Set("Authorization",
		fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s, SignedHeaders=%s, Signature=%s",
			cred, shdrs, signature))

	for _, h := range opts.headers {
		req.Header.Set(h.key, h.value)
	}

	q := req.URL.Query()
	for _, p := range opts.queryParams {
		q.Add(p.key, p.value)
	}
	req.URL.RawQuery = q.Encode()

	return req, nil
}

func (r *route53) calculateSignature(params awsRequestOpts) string {
	canonicalReq := createCanonicalReq(params)

	stringToSign := "AWS4-HMAC-SHA256\n"
	stringToSign += params.amzTime.Format("20060102T150405Z") + "\n"
	stringToSign += fmt.Sprintf("%s/%s/%s/aws4_request", params.amzTime.Format("20060102"), params.region, params.service) + "\n"
	stringToSign += canonicalReq

	date := hmac.New(sha256.New, []byte("AWS4"+r.secretAccessKey))
	date.Write([]byte(params.amzTime.Format("20060102")))

	region := hmac.New(sha256.New, date.Sum(nil))
	region.Write([]byte(params.region))

	service := hmac.New(sha256.New, region.Sum(nil))
	service.Write([]byte(params.service))

	signing := hmac.New(sha256.New, service.Sum(nil))
	signing.Write([]byte("aws4_request"))

	hmacSignature := hmac.New(sha256.New, signing.Sum(nil))
	hmacSignature.Write([]byte(stringToSign))

	signature := hex.EncodeToString(hmacSignature.Sum(nil))

	return signature
}

func createCanonicalReq(params awsRequestOpts) string {
	// sort by value, url.Values.Encode takes care of sorting by key
	sort.Slice(params.queryParams, func(i, j int) bool {
		return params.queryParams[i].value > params.queryParams[j].value
	})

	q := url.Values{}
	for _, p := range params.queryParams {
		q.Add(p.key, p.value)
	}
	canParam := q.Encode()

	canHead := ""
	headKeys := make([]string, 0, len(params.headers))
	for _, h := range params.headers {
		canHead += fmt.Sprintf("%s:%s\n", strings.ToLower(h.key), strings.TrimSpace(h.value))
		headKeys = append(headKeys, strings.ToLower(h.key))
	}

	canHKeys := strings.Join(headKeys, ";")

	phash := sha256.New()
	phash.Write(params.payload)
	payloadHashed := strings.ToLower(fmt.Sprintf("%x", phash.Sum(nil)))

	canReq := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s", params.method, params.uri, canParam, canHead, canHKeys, payloadHashed)
	reqHash := sha256.New()
	reqHash.Write([]byte(canReq))
	canReqHashed := strings.ToLower(fmt.Sprintf("%x", reqHash.Sum(nil)))

	return canReqHashed
}
