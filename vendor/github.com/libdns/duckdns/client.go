package duckdns

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/libdns/libdns"
	"github.com/miekg/dns"
)

func (p *Provider) getDomain(ctx context.Context, zone string) ([]libdns.Record, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	var libRecords []libdns.Record

	// we trim the dot at the end of the zone name to get the fqdn
	fqdn := strings.TrimRight(zone, ".")

	// DuckDNS' API is bad because it only has an `/update` endpoint which happens to
	// return current values, while also updating the values based on the incoming
	// request's IP address. So it's not safe to use for getting the current values
	// because it has side effects. So instead, we should just make simple DNS queries
	// to get the A, AAAA, and TXT records.
	resolver := p.Resolver
	if resolver == "" {
		resolver = defaultResolver
	}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 10 * time.Second}
			return d.DialContext(ctx, network, resolver)
		},
	}
	ips, err := r.LookupHost(ctx, fqdn)
	if err != nil {
		return libRecords, fmt.Errorf("DuckDNS unable to lookup host: %w", err)
	}

	for _, ip := range ips {
		parsedIp, err := netip.ParseAddr(ip)
		if err != nil {
			return libRecords, fmt.Errorf("DuckDNS unable to parse IP address '%s': %w", ip, err)
		}
		libRecords = append(libRecords, libdns.Address{
			Name: "@",
			IP:   parsedIp,
		})
	}

	txt, err := r.LookupTXT(ctx, fqdn)
	if err != nil {
		return libRecords, fmt.Errorf("DuckDNS unable to lookup TXT records for '%s': %w", fqdn, err)
	}
	for _, t := range txt {
		if t == "" {
			continue
		}
		libRecords = append(libRecords, libdns.TXT{
			Name: "@",
			Text: t,
		})
	}

	return libRecords, nil
}

func (p *Provider) setRecord(ctx context.Context, zone string, record libdns.Record, clear bool) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	rr, err := record.RR().Parse()
	if err != nil {
		return fmt.Errorf("DuckDNS unable to parse libdns.RR: %w", err)
	}

	// sanitize the domain, combines the zone and record names
	// the record name should typically be relative to the zone
	domain := libdns.AbsoluteName(record.RR().Name, zone)

	params := map[string]string{"verbose": "true"}

	switch rec := rr.(type) {
	case libdns.TXT:
		params["txt"] = rec.Text
	case libdns.Address:
		if rec.IP.Is6() {
			params["ipv6"] = rec.IP.String()
		} else {
			params["ip"] = rec.IP.String()
		}
	default:
		return fmt.Errorf("DuckDNS unsupported record type: %s", record.RR().Type)
	}

	if clear {
		params["clear"] = "true"
	}

	// make the request to duckdns to set the records according to the params
	_, err = p.doRequest(ctx, domain, params)
	if err != nil {
		return err
	}
	return nil
}

func (p *Provider) doRequest(ctx context.Context, domain string, params map[string]string) ([]string, error) {
	u, _ := url.Parse(duckDNSUpdateURL)

	// extract the main domain
	var mainDomain string
	if p.OverrideDomain != "" {
		mainDomain = p.OverrideDomain
	} else {
		mainDomain = getMainDomain(domain)
	}

	if len(mainDomain) == 0 {
		return nil, fmt.Errorf("DuckDNS unable to find the main domain for: %s", domain)
	}

	if len(p.APIToken) != 36 {
		return nil, fmt.Errorf("DuckDNS API token must be a 36 characters long UUID, got: '%s'", p.APIToken)
	}

	// set up the query with the params we always set
	query := u.Query()
	query.Set("domains", mainDomain)
	query.Set("token", p.APIToken)

	// add the remaining ones for this request
	for key, val := range params {
		query.Set(key, val)
	}

	// set the query back on the URL
	u.RawQuery = query.Encode()

	// make the request
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("DuckDNS unable to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DuckDNS unable to make request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("DuckDNS unable to read response body: %w", err)
	}

	body := string(bodyBytes)
	bodyParts := strings.Split(body, "\n")
	if bodyParts[0] != responseOK {
		return nil, fmt.Errorf("DuckDNS request failed, expected (OK) but got (%s), url: [%s], body: %s", bodyParts[0], u.String(), body)
	}

	return bodyParts, nil
}

// DuckDNS only lets you write to your subdomain.
// It must be in format subdomain.duckdns.org,
// not in format subsubdomain.subdomain.duckdns.org.
// So strip off everything that is not top 3 levels.
func getMainDomain(domain string) string {
	domain = strings.TrimSuffix(domain, ".")
	split := dns.Split(domain)
	if strings.HasSuffix(strings.ToLower(domain), duckDNSDomain) {
		if len(split) < 3 {
			return ""
		}

		firstSubDomainIndex := split[len(split)-3]
		return domain[firstSubDomainIndex:]
	}

	return domain[split[len(split)-1]:]
}

const (
	duckDNSUpdateURL = "https://www.duckdns.org/update"
	duckDNSDomain    = "duckdns.org"
	responseOK       = "OK"
	defaultResolver  = "8.8.8.8:53" // Google's public DNS server
)
