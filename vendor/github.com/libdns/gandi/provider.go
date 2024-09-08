package gandi

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/libdns/libdns"
)

// Provider implements the libdns interfaces for Gandi.
type Provider struct {
	BearerToken string `json:"bearer_token,omitempty"`

	domains map[string]gandiDomain
	mutex   sync.Mutex
}

// GetRecords lists all the records in the zone.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	domain, err := p.getDomain(ctx, zone)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", domain.DomainRecordsHref, nil)
	if err != nil {
		return nil, err
	}

	var gandiRecords []gandiRecord
	_, err = p.doRequest(req, &gandiRecords)
	if err != nil {
		return nil, err
	}

	var libRecords []libdns.Record
	for _, rec := range gandiRecords {
		for _, val := range rec.RRSetValues {
			rec := libdns.Record{
				Type:  rec.RRSetType,
				Name:  rec.RRSetName,
				TTL:   time.Duration(rec.RRSetTTL) * time.Second,
				Value: val,
			}

			libRecords = append(libRecords, rec)
		}
	}

	return libRecords, nil
}

// AppendRecords adds records to the zone and returns the records that were created.
// Due to technical limitations of the LiveDNS API, it may affect the TTL of similar records
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	domain, err := p.getDomain(ctx, zone)
	if err != nil {
		return nil, err
	}

	for _, rec := range records {
		err := p.setRecord(ctx, zone, rec, domain)
		if err != nil {
			return nil, err
		}
	}

	return records, nil
}

// DeleteRecords deletes records from the zone and returns the records that were deleted.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	domain, err := p.getDomain(ctx, zone)
	if err != nil {
		return nil, err
	}

	for _, rec := range records {
		err := p.deleteRecord(ctx, zone, rec, domain)
		if err != nil {
			return nil, err
		}
	}

	return records, nil
}

// SetRecords sets the records in the zone, either by updating existing records or creating new ones, and returns the recordsthat were updated.
// Due to technical limitations of the LiveDNS API, it may affect the TTL of similar records.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	domain, err := p.getDomain(ctx, zone)
	if err != nil {
		return nil, err
	}

	for _, rec := range records {
		err := p.setRecord(ctx, zone, rec, domain)
		if err != nil {
			return nil, err
		}
	}

	return records, nil
}

// Interface guards
var (
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
)
