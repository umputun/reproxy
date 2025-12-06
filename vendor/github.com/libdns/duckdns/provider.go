package duckdns

import (
	"context"
	"sync"

	"github.com/libdns/libdns"
)

// Provider implements the libdns interfaces for Duck DNS.
type Provider struct {
	// The DuckDNS API token.
	APIToken string `json:"api_token,omitempty"`

	// An override to the domain, useful if the domain being
	// managed does not belong to DuckDNS, and instead is
	// pointing to DuckDNS using a CNAME record. This allows
	// using DuckDNS' API to manage records for other domains
	// which have worse or no programmable APIs.
	OverrideDomain string `json:"override_domain,omitempty"`

	// An optional resolver to use when doing DNS queries to
	// load the current records. By default, 8.8.8.8:53 is used,
	// i.e. Google's public DNS server.
	Resolver string `json:"resolver,omitempty"`

	mutex sync.Mutex
}

// GetRecords lists all the records in the zone.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	libRecords, err := p.getDomain(ctx, zone)
	if err != nil {
		return nil, err
	}

	return libRecords, nil
}

// AppendRecords adds records to the zone and returns the records that were created.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var appendedRecords []libdns.Record

	for _, rec := range records {
		err := p.setRecord(ctx, zone, rec, false)
		if err != nil {
			return nil, err
		}
		appendedRecords = append(appendedRecords, rec)
	}

	return appendedRecords, nil
}

// DeleteRecords deletes records from the zone and returns the records that were deleted.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var deletedRecords []libdns.Record

	for _, rec := range records {
		err := p.setRecord(ctx, zone, rec, true)
		if err != nil {
			return nil, err
		}
		deletedRecords = append(deletedRecords, rec)
	}

	return deletedRecords, nil
}

// SetRecords sets the records in the zone, either by updating existing records or creating new ones, and returns the recordsthat were updated.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var setRecords []libdns.Record

	for _, rec := range records {
		err := p.setRecord(ctx, zone, rec, false)
		if err != nil {
			return nil, err
		}
		setRecords = append(setRecords, rec)
	}

	return setRecords, nil
}

// Interface guards
var (
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
)
