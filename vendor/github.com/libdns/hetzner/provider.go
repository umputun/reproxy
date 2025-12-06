package hetzner

import (
	"context"
	"fmt"
	"sync"

	"github.com/libdns/libdns"
)

// Provider implements the libdns interfaces for Hetzner.
type Provider struct {
	// AuthAPIToken is the Hetzner Auth API token - see https://dns.hetzner.com/api-docs#section/Authentication/Auth-API-Token
	AuthAPIToken string `json:"auth_api_token"`

	client *Client
	once   sync.Once
}

// New returns a new libdns provider for Hetzner.
func New(token string) *Provider {
	return &Provider{
		AuthAPIToken: token,
	}
}

// GetRecords  implements the libdns.RecordGetter interface.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	records, err := p.getClient().GetAllRecords(ctx, unFQDN(zone))

	if err != nil {
		return nil, err
	}

	results := make([]libdns.Record, 0, len(records))

	for _, r := range records {
		rr, err := r.Parse(zone)

		if err != nil {
			return nil, fmt.Errorf("failed to parse record: %w", err)
		}

		results = append(results, rr)
	}

	return results, nil
}

// AppendRecords implements the libdns.RecordAppender interface.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	appendedRecords := make([]libdns.Record, 0, len(records))

	for _, r := range records {
		rr := r.RR()
		response, err := p.getClient().CreateRecord(ctx, unFQDN(zone), Record{
			Type:  rr.Type,
			Name:  rr.Name,
			Value: rr.Data,
			TTL:   int(rr.TTL.Seconds()),
		})

		if err != nil {
			return appendedRecords, err
		}

		record, err := response.Parse(zone)

		if err != nil {
			return appendedRecords, fmt.Errorf("failed to parse record: %w", err)
		}

		appendedRecords = append(appendedRecords, record)
	}

	return appendedRecords, nil
}

// DeleteRecords implements the libdns.RecordDeleter interface.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	allRecords, err := p.getClient().GetAllRecords(ctx, unFQDN(zone))

	if err != nil {
		return nil, err
	}

	deletedRecords := make([]libdns.Record, 0, len(records))

	for _, r := range records {
		id := p.findRecordID(allRecords, r)

		if id == "" {
			return deletedRecords, fmt.Errorf("record ID not found: %s", r.RR().Name)
		}

		if err := p.getClient().DeleteRecord(ctx, id); err != nil {
			return deletedRecords, err
		}

		deletedRecords = append(deletedRecords, r)
	}

	return deletedRecords, nil
}

// SetRecords implements the libdns.RecordSetter interface.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	allRecords, err := p.getClient().GetAllRecords(ctx, unFQDN(zone))

	if err != nil {
		return nil, err
	}

	setRecords := make([]libdns.Record, 0, len(records))

	for _, r := range records {
		var response Record
		var err error
		rr := r.RR()
		id := p.findRecordID(allRecords, r)

		if id == "" {
			response, err = p.getClient().CreateRecord(ctx, unFQDN(zone), Record{
				Type:  rr.Type,
				Name:  rr.Name,
				Value: rr.Data,
				TTL:   int(rr.TTL.Seconds()),
			})
		} else {
			response, err = p.getClient().UpdateRecord(ctx, unFQDN(zone), Record{
				ID:    id,
				Type:  rr.Type,
				Name:  rr.Name,
				Value: rr.Data,
				TTL:   int(rr.TTL.Seconds()),
			})
		}

		if err != nil {
			return setRecords, err
		}

		result, err := response.Parse(zone)

		if err != nil {
			return setRecords, fmt.Errorf("failed to parse record: %w", err)
		}

		setRecords = append(setRecords, result)
	}

	return setRecords, nil
}

// getClient initializes the client for the provider.
func (p *Provider) getClient() *Client {
	p.once.Do(func() {
		if p.AuthAPIToken == "" {
			panic("hetzner: api token missing")
		}

		p.client = NewClient(p.AuthAPIToken)
	})

	return p.client
}

// findRecordID searches for a record using the name and type of the record to be found.
// It returns the record ID if found, otherwise an empty string.
func (p *Provider) findRecordID(allRecords []Record, r libdns.Record) string {
	rr := r.RR()
	var id string

	for _, record := range allRecords {
		if record.Name == rr.Name && record.Type == rr.Type {
			id = record.ID
			break
		}
	}

	return id
}

// Interface guards
var (
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
)
