package namecheap

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Provides some basic structs to interact with the Namecheap api with.
// From the docs: https://www.namecheap.com/support/api/methods/domains-dns/set-hosts/

func mustParse(endpoint string) *url.URL {
	u, err := url.Parse(endpoint)
	if err != nil {
		panic(err)
	}
	return u
}

const (
	defaultEndpoint         = "https://api.namecheap.com/xml.response"
	defaultDiscoveryAddress = "https://icanhazip.com"
)

var defaultEndpointURL = mustParse(defaultEndpoint)

// RecordType is the type of DNS Record.
type RecordType string

const (
	A      RecordType = "A"
	AAAA   RecordType = "AAAA"
	ALIAS  RecordType = "ALIAS"
	CAA    RecordType = "CAA"
	CNAME  RecordType = "CNAME"
	MX     RecordType = "MX"
	MXE    RecordType = "MXE"
	NS     RecordType = "NS"
	TXT    RecordType = "TXT"
	URL    RecordType = "URL"
	URL301 RecordType = "URL301"
	FRAME  RecordType = "FRAME"
)

type HostRecord struct {
	// The domain or subdomain for which host record is set.
	Name string

	// RecordType the type of DNS record e.g. A, AAAA
	RecordType RecordType

	// Possible values are URL or IP address. The value for this parameter is based on RecordType.
	Address string

	// MX preference for host. Applicable for MX records only.
	MXPref string

	// EmailType Possible values are:
	// MXE - to set up your custom MXE record.
	// MX - to set up custom MX records of your mail provider.
	// FWD - to set up MX records for our Free Email Forwarding service.
	// OX - to set up MX records for our Private Email service.
	EmailType string

	// 60 to 60000
	// Default Value: 1800
	TTL uint16

	// Flag is an unsigned integer between 0 and 255.
	// The flag value is an 8-bit number, the most significant bit of which indicates
	// the criticality of understanding of a record by a CA.
	// It's recommended to use '0'
	Flag uint8

	// Tag is a non-zero sequence of US-ASCII letters and numbers in lower case.
	// For CAA records, possible values are:
	// - issue: specifies the certification authority that is authorized to issue a certificate
	// - issuewild: specifies the certification authority that is allowed to issue a wildcard certificate
	// - iodef: specifies the e-mail address or URL a CA should use to notify a client
	Tag string

	// HostID is the unique ID of the host record.
	// Readonly field.
	HostID string
}

type HostRecordKey struct {
	Name       string
	RecordType RecordType
	TTL        uint16
	Address    string
}

type Domain struct {
	TLD string
	SLD string
}

func (h HostRecord) DeleteKey() HostRecordKey {
	return HostRecordKey{h.Name, h.RecordType, h.TTL, h.Address}
}

func (h HostRecord) SetKey() HostRecordKey {
	return HostRecordKey{Name: h.Name, RecordType: h.RecordType}
}

// AppendKey is used to determine if a host record is new or an existing one
// when appending records. The libdns spec doesn't specify for AppendRecords how to
// determine if a record is new or existing but namecheap lets you have multiple records
// with the same name + type as long as the address is different.
func (h HostRecord) AppendKey() HostRecordKey {
	return HostRecordKey{Name: h.Name, RecordType: h.RecordType, Address: h.Address}
}

// This gets unmarshalled from the server's XML response.
type getHostsResponseRecord struct {
	// HostID is the unique ID of the host record.
	HostID string `xml:"HostId,attr"`

	// The domain or subdomain for which host record is set
	Name string `xml:"Name,attr"`

	// RecordType the type of DNS record e.g. A, AAAA
	Type string `xml:"Type,attr"`

	// Possible values are URL or IP address. The value for this parameter is based on RecordType.
	Address string `xml:"Address,attr"`

	// MX preference for host. Applicable for MX records only.
	MXPref string `xml:"MXPref,attr"`

	// 60 to 60000
	// Default Value: 1800
	TTL uint16 `xml:"TTL,attr"`
}

// Converts the XML response into the public HostRecord struct.
func (r getHostsResponseRecord) ToHostRecord() HostRecord {
	// Confusingly, the Flag and Tag fields are not set on the response and are instead returned in the Address field.
	// If this is a CAA record, we need to parse the Address field to get the Flag and Tag.
	record := HostRecord{
		HostID:     r.HostID,
		Name:       r.Name,
		RecordType: RecordType(r.Type),
		Address:    r.Address,
		MXPref:     r.MXPref,
		TTL:        r.TTL,
	}
	return record
}

// addToValues adds the HostRecord fields to values. Ignores read only fields.
func addToValues(host HostRecord, hostNumber int, values *url.Values) {
	setValueIfPresent := func(key, value string) {
		if value != "" && value != "0" {
			keyWithNumber := fmt.Sprintf("%s%d", key, hostNumber)
			values.Set(keyWithNumber, value)
		}
	}

	setValueIfPresent("HostName", host.Name)
	setValueIfPresent("RecordType", string(host.RecordType))
	setValueIfPresent("Address", string(host.Address))
	setValueIfPresent("MXPref", host.MXPref)
	setValueIfPresent("TTL", strconv.Itoa(int(host.TTL)))
	setValueIfPresent("EmailType", host.EmailType)
	setValueIfPresent("Flag", strconv.Itoa(int(host.Flag)))
	setValueIfPresent("Tag", host.Tag)
}

// getPublicIP tries to determine the public ip of the machine by
// making a request to an external service that returns the public
// IP of the caller.
func getPublicIP(discoveryAddress string) (string, error) {
	resp, err := http.Get(discoveryAddress)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(body)), nil
}

type Client struct {
	// apiKey is the namecheap api key.
	// See: https://www.namecheap.com/support/api/intro/ for more info.
	apiKey string

	// Username required to access the API
	apiUser string

	// The Username on which a command is executed. Generally, the values of ApiUser and UserName parameters are the same.
	username string

	// The API endpoint to talk to. Don't modify this. Instead create a new URL from this one.
	endpointURL *url.URL

	// An IP address of the server from which our system receives API calls (only IPv4 can be used).
	clientIP string

	// Used to determine external IP of client.
	discoveryAddress string

	// Will determine the PublicIP of the client by calling a service.
	autoDiscoverPublicIP bool
}

type ClientOption func(*Client) error

func WithEndpoint(endpoint string) ClientOption {
	return func(c *Client) error {
		u, err := url.Parse(endpoint)
		if err != nil {
			return err
		}

		c.endpointURL = u
		return nil
	}
}

func WithClientIP(ip string) ClientOption {
	return func(c *Client) error {
		c.clientIP = ip
		return nil
	}
}

func WithDiscoveryAddress(address string) ClientOption {
	return func(c *Client) error {
		c.discoveryAddress = address
		return nil
	}
}

func AutoDiscoverPublicIP() ClientOption {
	return func(c *Client) error {
		c.autoDiscoverPublicIP = true
		return nil
	}
}

func NewClient(apiKey, apiUser string, opts ...ClientOption) (*Client, error) {
	client := &Client{
		apiKey:           apiKey,
		apiUser:          apiUser,
		endpointURL:      defaultEndpointURL,
		username:         apiUser,
		discoveryAddress: defaultDiscoveryAddress,
	}

	for _, opt := range opts {
		if err := opt(client); err != nil {
			return nil, fmt.Errorf("error while applying option. Err: %s", err)
		}
	}

	if client.autoDiscoverPublicIP {
		ip, err := getPublicIP(client.discoveryAddress)
		if err != nil {
			return nil, fmt.Errorf("unable to determine public IP automatically. Err: %s", err)
		}
		client.clientIP = ip
	}

	if client.clientIP == "" {
		return nil, fmt.Errorf("clientIP is not set. Either provide one through the 'WithClientIP' option or have it set automatically with the 'AutoDiscoverPublicIP' option")
	}

	return client, nil
}

func (c *Client) buildRequest(ctx context.Context, command string, opts requestParams) (*http.Request, error) {
	q := url.Values{}
	q.Set("ApiUser", c.apiUser)
	q.Set("ApiKey", c.apiKey)
	q.Set("UserName", c.username)
	q.Set("ClientIp", c.clientIP)
	q.Set("Command", command)

	if opts.Domain != nil {
		q.Set("TLD", opts.Domain.TLD)
		q.Set("SLD", opts.Domain.SLD)
	}

	for i, host := range opts.Hosts {
		addToValues(host, i+1, &q)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL.String(), strings.NewReader(q.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return req, nil
}

// GetHosts returns the host records for the given domain.
func (c *Client) GetHosts(ctx context.Context, domain Domain) ([]HostRecord, error) {
	req, err := c.buildRequest(ctx, "namecheap.domains.dns.getHosts", requestParams{Domain: &domain})
	if err != nil {
		return nil, err
	}

	apiResp, err := doRequest(req)
	if err != nil {
		return nil, err
	}

	var records []HostRecord
	for _, host := range apiResp.CommandResponse.DomainDNSGetHostsResult.Hosts {
		records = append(records, host.ToHostRecord())
	}

	return records, nil
}

func (c *Client) SetHosts(ctx context.Context, domain Domain, hosts []HostRecord) ([]HostRecord, error) {
	req, err := c.buildRequest(ctx, "namecheap.domains.dns.setHosts", requestParams{Domain: &domain, Hosts: hosts})
	if err != nil {
		return nil, err
	}

	_, err = doRequest(req)
	return hosts, err
}

// TODO: If this grows any more, create a 'requestBuilder' following the builder pattern for this.
type requestParams struct {
	Domain *Domain
	Hosts  []HostRecord
}

// GetTLDs returns all TLDs available for namecheap.
func (c *Client) GetTLDs(ctx context.Context) ([]TLD, error) {
	req, err := c.buildRequest(ctx, "namecheap.domains.getTldList", requestParams{})
	if err != nil {
		return nil, err
	}

	apiResp, err := doRequest(req)
	if err != nil {
		return nil, err
	}

	return apiResp.CommandResponse.TLDs.TLDs, nil
}

type apiErrors []apiError

func (e apiErrors) String() string {
	var errMsg string
	for i, apiError := range e {
		errMsg += fmt.Sprintf("Error%d: %s\t", i, apiError.Err)
	}
	return errMsg
}

// Go XML doesn't support unmarshaling self closing tags e.g. <Errors /> so need to
// define our own unmarshaling.
func (a *apiErrors) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	errors := make(apiErrors, 0)
	e := &apiError{}

	for {
		err := d.Decode(e)

		if err == io.EOF {
			break
		}

		if err != nil {
			return err
		}

		errors = append(errors, *e)
	}

	*a = errors

	return nil
}

type apiError struct {
	Number string `xml:"Number,attr"`
	Err    string `xml:",innerxml"`
}

type apiResponse struct {
	XMLName          xml.Name        `xml:"ApiResponse"`
	Status           string          `xml:"Status,attr"`
	Errors           apiErrors       `xml:"Errors,omitempty"`
	RequestedCommand string          `xml:"RequestedCommand"`
	CommandResponse  commandResponse `xml:"CommandResponse"`
	Server           string          `xml:"Server"`
	// Let's just ignore the other fields because we probably don't need them..
}

type TLD struct {
	Name string `xml:"Name,attr"`
	// There's more fields but we only use the name for now.
}

type tlds struct {
	TLDs []TLD `xml:"Tld"`
}

type commandResponse struct {
	Type                    string                   `xml:"Type,attr"`
	DomainDNSSetHostsResult *domainDNSSetHostsResult `xml:"DomainDNSSetHostsResult,omitempty"`
	DomainDNSGetHostsResult *domainDNSGetHostsResult `xml:"DomainDNSGetHostsResult,omitempty"`
	TLDs                    tlds                     `xml:"Tlds,omitempty"`
}

type domainDNSSetHostsResult struct {
	Domain    string `xml:"Domain,attr"`
	IsSuccess bool   `xml:"IsSuccess,attr"`
}

type domainDNSGetHostsResult struct {
	Domain        string                   `xml:"Domain,attr"`
	IsUsingOurDNS bool                     `xml:"IsUsingOurDNS,attr"`
	Hosts         []getHostsResponseRecord `xml:",any"`
}

func doRequest(req *http.Request) (*apiResponse, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp apiResponse
	err = xml.Unmarshal(body, &apiResp)
	if err != nil {
		return nil, err
	}

	if len(apiResp.Errors) > 0 {
		return nil, fmt.Errorf("namecheap api returned error in response. Err: %s", apiResp.Errors)
	}

	return &apiResp, nil
}
