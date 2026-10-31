package scaleway

import (
	"context"
	"sync"
	"time"

	"github.com/libdns/libdns"
	domain "github.com/scaleway/scaleway-sdk-go/api/domain/v2beta1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

type Client struct {
	client *scw.Client
	mutex  sync.Mutex
}

// getClient initializes the Scaleway API client if not already set.
func (p *Provider) getClient() error {
	if p.client == nil {
		var err error
		p.client, err = scw.NewClient(
			scw.WithAuth("SCWXXXXXXXXXXXXXXXXX", p.SecretKey),
			scw.WithDefaultOrganizationID(p.OrganizationID),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// convertRecord applies TTL conversion to a record.
// Scaleway API uses TTL in seconds, libdns uses time.Duration.
func convertRecord(record libdns.Record) (libdns.Record, error) {
	rr := record.RR()
	rr.TTL = time.Duration(rr.TTL) * time.Second
	return rr.Parse()
}

func (p *Provider) getDNSEntries(ctx context.Context, zone string) ([]libdns.Record, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	err := p.getClient()
	if err != nil {
		return nil, err
	}

	domainAPI := domain.NewAPI(p.client)
	var records []libdns.Record

	zoneRecords, err := domainAPI.ListDNSZoneRecords(&domain.ListDNSZoneRecordsRequest{
		DNSZone: zone,
	})
	if err != nil {
		return nil, err
	}

	for _, entry := range zoneRecords.Records {
		rr := libdns.RR{
			Name: entry.Name,
			Type: string(entry.Type),
			Data: entry.Data,
			TTL:  time.Duration(entry.TTL) * time.Second,
		}
		record, err := rr.Parse()
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (p *Provider) addDNSEntry(ctx context.Context, zone string, record libdns.Record) (libdns.Record, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	err := p.getClient()
	if err != nil {
		return record, err
	}

	rr := record.RR()

	domainAPI := domain.NewAPI(p.client)
	_, err = domainAPI.UpdateDNSZoneRecords(&domain.UpdateDNSZoneRecordsRequest{
		DNSZone: zone,
		Changes: []*domain.RecordChange{
			{
				Add: &domain.RecordChangeAdd{
					Records: []*domain.Record{
						{
							Name: rr.Name,
							Type: domain.RecordType(rr.Type),
							Data: rr.Data,
							TTL:  uint32(rr.TTL.Seconds()),
						},
					},
				},
			},
		},
	})
	if err != nil {
		return record, err
	}
	return convertRecord(record)
}

func (p *Provider) removeDNSEntry(ctx context.Context, zone string, record libdns.Record) (libdns.Record, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	err := p.getClient()
	if err != nil {
		return record, err
	}

	rr := record.RR()

	domainAPI := domain.NewAPI(p.client)
	_, err = domainAPI.UpdateDNSZoneRecords(&domain.UpdateDNSZoneRecordsRequest{
		DNSZone: zone,
		Changes: []*domain.RecordChange{
			{
				Delete: &domain.RecordChangeDelete{
					IDFields: &domain.RecordIdentifier{
						Name: rr.Name,
						Type: domain.RecordType(rr.Type),
						Data: &rr.Data,
						TTL:  scw.Uint32Ptr(uint32(rr.TTL.Seconds())),
					},
				},
			},
		},
	})
	if err != nil {
		return record, err
	}
	return convertRecord(record)
}

func (p *Provider) updateDNSEntry(ctx context.Context, zone string, record libdns.Record) (libdns.Record, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	err := p.getClient()
	if err != nil {
		return record, err
	}

	rr := record.RR()

	domainAPI := domain.NewAPI(p.client)
	_, err = domainAPI.UpdateDNSZoneRecords(&domain.UpdateDNSZoneRecordsRequest{
		DNSZone: zone,
		Changes: []*domain.RecordChange{
			{
				Set: &domain.RecordChangeSet{
					IDFields: &domain.RecordIdentifier{
						Name: rr.Name,
						Type: domain.RecordType(rr.Type),
						Data: &rr.Data,
						TTL:  scw.Uint32Ptr(uint32(rr.TTL.Seconds())),
					},
					Records: []*domain.Record{
						{
							Name: rr.Name,
							Type: domain.RecordType(rr.Type),
							Data: rr.Data,
							TTL:  uint32(rr.TTL.Seconds()),
						},
					},
				},
			},
		},
	})
	if err != nil {
		return record, err
	}
	return convertRecord(record)
}
