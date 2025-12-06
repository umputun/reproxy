// Package namecheap implements a DNS record management client compatible
// with the libdns interfaces for namecheap.
package namecheap

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libdns/libdns"

	"github.com/libdns/namecheap/internal/namecheap"
)

func parseIntoHostRecord(record libdns.Record) namecheap.HostRecord {
	switch rr := record.(type) {
	case *libdns.Address:
		recordType := namecheap.A
		if rr.IP.Is6() {
			recordType = namecheap.AAAA
		}
		return namecheap.HostRecord{
			RecordType: recordType,
			Name:       rr.Name,
			TTL:        uint16(rr.TTL.Seconds()),
			Address:    rr.IP.String(),
		}
	case *libdns.CNAME:
		return namecheap.HostRecord{
			RecordType: namecheap.CNAME,
			Name:       rr.Name,
			TTL:        uint16(rr.TTL.Seconds()),
			Address:    rr.Target,
		}
	case *libdns.TXT:
		return namecheap.HostRecord{
			RecordType: namecheap.TXT,
			Name:       rr.Name,
			TTL:        uint16(rr.TTL.Seconds()),
			Address:    rr.Text,
		}
	case *libdns.MX:
		// Namecheap requires the target to end in '.'
		// otherwise it will silently fail to set the record.
		if !strings.HasSuffix(rr.Target, ".") {
			rr.Target = rr.Target + "."
		}
		return namecheap.HostRecord{
			RecordType: namecheap.MX,
			Name:       rr.Name,
			TTL:        uint16(rr.TTL.Seconds()),
			Address:    rr.Target,
			MXPref:     strconv.Itoa(int(rr.Preference)),
			EmailType:  "MX",
		}
	case *libdns.CAA:
		return namecheap.HostRecord{
			RecordType: namecheap.CAA,
			Name:       rr.Name,
			TTL:        uint16(rr.TTL.Seconds()),
			Address:    fmt.Sprintf("%d %s %s", rr.Flags, rr.Tag, rr.Value),
		}
	default:
		commonRR := record.RR()
		return namecheap.HostRecord{
			RecordType: namecheap.RecordType(commonRR.Type),
			Name:       commonRR.Name,
			TTL:        uint16(commonRR.TTL.Seconds()),
			Address:    commonRR.Data,
		}
	}
}

// parseFromHostRecord converts a namecheap.HostRecord to a libdns.Record.
func parseFromHostRecord(hostRecord namecheap.HostRecord) (libdns.Record, error) {
	switch hostRecord.RecordType {
	case namecheap.A, namecheap.AAAA:
		ip, err := netip.ParseAddr(hostRecord.Address)
		if err != nil {
			return nil, fmt.Errorf("invalid IP address: %s. Error: %s", hostRecord.Address, err)
		}
		return &libdns.Address{
			Name: hostRecord.Name,
			TTL:  time.Duration(hostRecord.TTL) * time.Second,
			IP:   ip,
		}, nil
	case namecheap.CNAME:
		return &libdns.CNAME{
			Name:   hostRecord.Name,
			TTL:    time.Duration(hostRecord.TTL) * time.Second,
			Target: hostRecord.Address,
		}, nil
	case namecheap.TXT:
		return &libdns.TXT{
			Name: hostRecord.Name,
			TTL:  time.Duration(hostRecord.TTL) * time.Second,
			Text: hostRecord.Address,
		}, nil
	case namecheap.MX:
		pref, _ := strconv.Atoi(hostRecord.MXPref)
		return &libdns.MX{
			Name:       hostRecord.Name,
			TTL:        time.Duration(hostRecord.TTL) * time.Second,
			Preference: uint16(pref),
			Target:     hostRecord.Address,
		}, nil
	case namecheap.NS:
		return &libdns.NS{
			Name:   hostRecord.Name,
			TTL:    time.Duration(hostRecord.TTL) * time.Second,
			Target: hostRecord.Address,
		}, nil
	case namecheap.CAA:
		// Of the form "0 issue letsencrypt.org"
		// or with quotes: "0 issue \"letsencrypt.org\""
		parts := strings.Split(hostRecord.Address, " ")
		if partLen := len(parts); partLen < 3 {
			return nil, fmt.Errorf("invalid CAA record: %s. Expected 3 parts, got %d", hostRecord.Address, partLen)
		}
		flag, err := strconv.ParseUint(parts[0], 10, 8)
		if err != nil {
			return nil, fmt.Errorf("invalid CAA record: %s. Error parsing flag: %w", hostRecord.Address, err)
		}
		tag := parts[1]
		address := parts[2]

		return &libdns.CAA{
			Name:  hostRecord.Name,
			TTL:   time.Duration(hostRecord.TTL) * time.Second,
			Flags: uint8(flag),
			Tag:   tag,
			Value: address,
		}, nil
	default:
		return &libdns.RR{
			Name: hostRecord.Name,
			Type: string(hostRecord.RecordType),
			Data: hostRecord.Address,
			TTL:  time.Duration(hostRecord.TTL) * time.Second,
		}, nil
	}
}

// Provider facilitates DNS record manipulation with namecheap.
// The libdns methods that return updated structs do not have
// their ID fields set since this information is not returned
// by the namecheap API.
type Provider struct {
	// APIKey is your namecheap API key.
	// See: https://www.namecheap.com/support/api/intro/
	// for more details.
	APIKey string `json:"api_key,omitempty"`

	// User is your namecheap API user. This can be the same as your username.
	User string `json:"user,omitempty"`

	// APIEndpoint to use. If testing, you can use the "sandbox" endpoint
	// instead of the production one.
	APIEndpoint string `json:"api_endpoint,omitempty"`

	// ClientIP is the IP address of the requesting client.
	// If this is not set, a discovery service will be
	// used to determine the public ip of the machine.
	// You must first whitelist your IP in the namecheap console
	// before using the API.
	ClientIP string `json:"client_ip,omitempty"`

	// These should hardly ever change and are cached on first use.
	tlds []string

	// These are cached on first use.
	domains map[string]namecheap.Domain

	mu sync.Mutex
}

// getClient inititializes a new namecheap client.
func (p *Provider) getClient() (*namecheap.Client, error) {
	options := []namecheap.ClientOption{}
	if p.APIEndpoint != "" {
		options = append(options, namecheap.WithEndpoint(p.APIEndpoint))
	}

	if p.ClientIP == "" {
		options = append(options, namecheap.AutoDiscoverPublicIP())
	} else {
		options = append(options, namecheap.WithClientIP(p.ClientIP))
	}

	client, err := namecheap.NewClient(p.APIKey, p.User, options...)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (p *Provider) getTLDs(ctx context.Context) ([]string, error) {
	if p.tlds != nil {
		return p.tlds, nil
	}

	client, err := p.getClient()
	if err != nil {
		return nil, err
	}

	tlds, err := client.GetTLDs(ctx)
	if err != nil {
		return nil, err
	}

	p.tlds = make([]string, 0, len(tlds))
	for _, tld := range tlds {
		p.tlds = append(p.tlds, tld.Name)
	}

	return p.tlds, nil
}

// takes a zone and returns the domain (TLD + SLD separated out) from it.
func (p *Provider) getDomain(ctx context.Context, zone string) (namecheap.Domain, error) {
	if p.domains == nil {
		p.domains = make(map[string]namecheap.Domain)
	}

	// Normalize zone.
	zone = strings.TrimRight(zone, ".")

	if domain, ok := p.domains[zone]; ok {
		return domain, nil
	}

	tlds, err := p.getTLDs(ctx)
	if err != nil {
		return namecheap.Domain{}, err
	}

	// See if our zone is a substring match of any of the tlds.
	var domain namecheap.Domain
	for _, tld := range tlds {
		if strings.HasSuffix(zone, tld) {
			domain = namecheap.Domain{
				TLD: tld,
				SLD: strings.TrimSuffix(strings.TrimSuffix(zone, tld), "."),
			}
			break
		}
	}

	if domain.TLD == "" {
		return namecheap.Domain{}, fmt.Errorf("invalid zone: %s. Zone TLD was not found in list of known TLDs: %s", zone, strings.Join(tlds, ", "))
	}

	p.domains[zone] = domain
	return domain, nil
}

// GetRecords lists all the records in the zone.
// See https://pkg.go.dev/github.com/libdns/libdns#RecordGetter for more info.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	client, err := p.getClient()
	if err != nil {
		return nil, err
	}

	domain, err := p.getDomain(ctx, zone)
	if err != nil {
		return nil, err
	}

	hostRecords, err := client.GetHosts(ctx, domain)
	if err != nil {
		return nil, err
	}

	var records []libdns.Record
	for _, hr := range hostRecords {
		record, err := parseFromHostRecord(hr)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, nil
}

// AppendRecords adds records to the zone. It returns the records that were added.
// The records returned may not exactly match what the Namecheap API returns
// if you do GetRecords. The ordering of the records is not preserved.
// See https://pkg.go.dev/github.com/libdns/libdns#RecordAppender for more info.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	client, err := p.getClient()
	if err != nil {
		return nil, err
	}

	domain, err := p.getDomain(ctx, zone)
	if err != nil {
		return nil, err
	}

	// Need to first get the existing hosts before adding new ones since we can only "set hosts" in namecheap api.
	hosts, err := client.GetHosts(ctx, domain)
	if err != nil {
		return nil, err
	}

	existingHostSet := make(map[namecheap.HostRecordKey]struct{})
	for _, host := range hosts {
		existingHostSet[host.AppendKey()] = struct{}{}
	}

	// Filter any records (name, type, address) that already exist
	// since we only want to add new ones and not update existing ones.
	var appendedRecords []libdns.Record
	for _, record := range records {
		host := parseIntoHostRecord(record)
		if _, found := existingHostSet[host.AppendKey()]; !found {
			hosts = append(hosts, host)
		}
		appendedRecords = append(appendedRecords, record)
	}

	_, err = client.SetHosts(ctx, domain, hosts)
	if err != nil {
		return nil, err
	}

	return appendedRecords, nil
}

// SetRecords sets the records in the zone, either by updating existing records or creating new ones.
// It returns the updated records. The records returned may not exactly match what the Namecheap API returns.
//
// For any (name, type) pair in the input, SetRecords ensures that the only
// records in the output zone with that (name, type) pair are those that were
// provided in the input. See https://pkg.go.dev/github.com/libdns/libdns#RecordSetter for more info.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var hostRecords []namecheap.HostRecord
	for _, r := range records {
		hostRecords = append(hostRecords, parseIntoHostRecord(r))
	}

	client, err := p.getClient()
	if err != nil {
		return nil, err
	}

	domain, err := p.getDomain(ctx, zone)
	if err != nil {
		return nil, err
	}

	existingHosts, err := client.GetHosts(ctx, domain)
	if err != nil {
		return nil, err
	}

	newHostSet := make(map[namecheap.HostRecordKey]struct{})
	for _, host := range hostRecords {
		newHostSet[host.SetKey()] = struct{}{}
	}

	// Remove existing hosts that have the same name+type as the new ones
	existingHosts = slices.DeleteFunc(existingHosts, func(h namecheap.HostRecord) bool {
		_, found := newHostSet[h.SetKey()]
		return found
	})

	allHosts := append(existingHosts, hostRecords...)

	_, err = client.SetHosts(ctx, domain, allHosts)
	if err != nil {
		return nil, err
	}

	return records, nil
}

// DeleteRecords deletes the records from the zone. It returns the records that were deleted.
// The records returned may not exactly match what the Namecheap API returns.
// See https://pkg.go.dev/github.com/libdns/libdns#RecordDeleter for more info.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	client, err := p.getClient()
	if err != nil {
		return nil, err
	}

	domain, err := p.getDomain(ctx, zone)
	if err != nil {
		return nil, err
	}

	existingHosts, err := client.GetHosts(ctx, domain)
	if err != nil {
		return nil, err
	}

	hostsToRemove := make(map[namecheap.HostRecordKey]namecheap.HostRecord)
	for _, record := range records {
		host := parseIntoHostRecord(record)
		hostsToRemove[host.DeleteKey()] = host
	}

	var deletedRecords []libdns.Record
	var updatedHosts []namecheap.HostRecord
	for _, host := range existingHosts {
		// Only add back the existing hosts we don't find.
		if _, found := hostsToRemove[host.DeleteKey()]; !found {
			updatedHosts = append(updatedHosts, host)
		} else {
			record, err := parseFromHostRecord(host)
			if err != nil {
				// If we can't parse the host record, fallback to a generic RR.
				record = libdns.RR{
					Name: host.Name,
					Type: string(host.RecordType),
					Data: host.Address,
					TTL:  time.Duration(host.TTL) * time.Second,
				}
			}
			deletedRecords = append(deletedRecords, record)
		}
	}

	_, err = client.SetHosts(ctx, domain, updatedHosts)
	if err != nil {
		return nil, err
	}

	return deletedRecords, nil
}

var (
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
)
