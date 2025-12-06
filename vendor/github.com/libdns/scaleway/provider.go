package scaleway

import (
	"context"
	"time"

	"github.com/libdns/libdns"
)

type Provider struct {
	Client
	SecretKey      string `json:"secret_key,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
}

// GetRecords lists all the records in the zone.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	records, err := p.getDNSEntries(ctx, zone)
	if err != nil {
		return nil, err
	}

	return records, nil
}

// AppendRecords adds records to the zone. It returns the records that were added.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var appendedRecords []libdns.Record

	for _, record := range records {
		newRecord, err := p.addDNSEntry(ctx, zone, record)
		if err != nil {
			return nil, err
		}

		rr := newRecord.RR()
		rr.TTL = time.Duration(rr.TTL) * time.Second
		newRecord, err = rr.Parse()
		if err != nil {
			return nil, err
		}
		appendedRecords = append(appendedRecords, newRecord)
	}

	return appendedRecords, nil
}

// SetRecords sets the records in the zone, either by updating existing records or creating new ones.
// It returns the updated records.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var setRecords []libdns.Record
	for _, record := range records {
		setRecord, err := p.updateDNSEntry(ctx, zone, record)
		if err != nil {
			return setRecords, err
		}

		rr := setRecord.RR()
		rr.TTL = time.Duration(rr.TTL) * time.Second
		setRecord, err = rr.Parse()
		if err != nil {
			return nil, err
		}
		setRecords = append(setRecords, setRecord)
	}
	return setRecords, nil
}

// DeleteRecords deletes the records from the zone. It returns the records that were deleted.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var deletedRecords []libdns.Record

	for _, record := range records {
		deletedRecord, err := p.removeDNSEntry(ctx, zone, record)
		if err != nil {
			return nil, err
		}
		rr := deletedRecord.RR()
		rr.TTL = time.Duration(rr.TTL) * time.Second
		deletedRecord, err = rr.Parse()
		if err != nil {
			return nil, err
		}
		deletedRecords = append(deletedRecords, deletedRecord)
	}

	return deletedRecords, nil
}

// Interface guards
var (
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
)
