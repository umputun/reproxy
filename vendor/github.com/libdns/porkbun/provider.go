// Package porkbun implements a DNS record management client compatible
// with the libdns interfaces for Porkbun.
package porkbun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/libdns/libdns"
)

// Provider facilitates DNS record manipulation with Porkbun.
type Provider struct {
	APIKey       string `json:"api_key,omitempty"`
	APISecretKey string `json:"api_secret_key,omitempty"`
}

// GetRecords lists all the records in the zone.
func (p *Provider) GetRecords(_ context.Context, zone string) ([]libdns.Record, error) {
	trimmedZone := LibdnsZoneToPorkbunDomain(zone)

	credentialJson, err := json.Marshal(p.getCredentials())
	if err != nil {
		return nil, err
	}
	response, err := MakeApiRequest("/dns/retrieve/"+trimmedZone, bytes.NewReader(credentialJson), pkbnRecordsResponse{})

	if err != nil {
		return nil, err
	}

	if response.Status != "SUCCESS" {
		return nil, errors.New(fmt.Sprintf("Invalid response status %s", response.Status))
	}

	recs := make([]libdns.Record, 0, len(response.Records))
	for _, rec := range response.Records {
		libdnsRec, err := rec.toLibdnsRecord(zone)
		if err == nil {
			recs = append(recs, libdnsRec)
		}
	}
	return recs, nil
}

// AppendRecords adds records to the zone. It returns the records that were added.
func (p *Provider) AppendRecords(_ context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	credentials := p.getCredentials()
	trimmedZone := LibdnsZoneToPorkbunDomain(zone)

	var createdRecords []libdns.Record

	for _, record := range records {
		rr := record.RR()
		if rr.TTL/time.Second < 600 {
			rr.TTL = 600 * time.Second
		}
		ttlInSeconds := int(rr.TTL / time.Second)
		trimmedName := LibdnsNameToPorkbunName(rr.Name, zone)

		reqBody := pkbnRecordPayload{&credentials, rr.Data, trimmedName, strconv.Itoa(ttlInSeconds), rr.Type}
		reqJson, err := json.Marshal(reqBody)
		if err != nil {
			return createdRecords, err
		}

		response, err := MakeApiRequest(fmt.Sprintf("/dns/create/%s", trimmedZone), bytes.NewReader(reqJson), pkbnCreateResponse{})

		if err != nil {
			return createdRecords, err
		}

		if response.Status != "SUCCESS" {
			return createdRecords, errors.New(fmt.Sprintf("Invalid response status %s", response.Status))
		}

		createdRecords = append(createdRecords, record)
	}

	return createdRecords, nil
}

// SetRecords sets the records in the zone, either by updating existing records or creating new ones.
// It returns the updated records.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var updates []libdns.Record
	var creates []libdns.Record
	var results []libdns.Record
	existingRecords, err := p.GetRecords(ctx, zone)
	if err != nil {
		return nil, err
	}

	for _, r := range records {
		var found libdns.Record = nil
		for _, existingRecord := range existingRecords {
			if existingRecord.RR().Name == r.RR().Name && existingRecord.RR().Type == r.RR().Type {
				found = existingRecord
				break
			}
		}

		if found != nil {
			if found.RR() != r.RR() {
				updates = append(updates, r)
			} else {
				results = append(results, found)
			}
		} else {
			creates = append(creates, r)
		}
	}

	created, err := p.AppendRecords(ctx, zone, creates)
	if err != nil {
		return nil, err
	}
	updated, err := p.updateRecords(ctx, zone, updates)
	if err != nil {
		return nil, err
	}

	results = append(results, created...)
	results = append(results, updated...)
	return results, nil
}

// DeleteRecords deletes the records from the zone. It returns the records that were deleted.
func (p *Provider) DeleteRecords(_ context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	credentials := p.getCredentials()
	trimmedZone := LibdnsZoneToPorkbunDomain(zone)

	var deletedRecords []libdns.Record

	for _, record := range records {
		var queuedDeletes []libdns.Record
		queuedDeletes = append(queuedDeletes, record)

		reqJson, err := json.Marshal(credentials)
		if err != nil {
			return nil, err
		}

		for _, recordToDelete := range queuedDeletes {
			rr := recordToDelete.RR()
			trimmedName := LibdnsNameToPorkbunName(rr.Name, zone)
			_, err = MakeApiRequest(fmt.Sprintf("/dns/deleteByNameType/%s/%s/%s", trimmedZone, rr.Type, trimmedName), bytes.NewReader(reqJson), pkbnResponseStatus{})
			if err != nil {
				return deletedRecords, err
			}
			deletedRecords = append(deletedRecords, recordToDelete)
		}
	}

	return deletedRecords, nil
}

func (p *Provider) ListZones(ctx context.Context) ([]libdns.Zone, error) {
	zones, err := p.getZones(ctx)

	if err != nil {
		return nil, err
	}

	return zones, nil
}

// Interface guards
var (
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
	_ libdns.ZoneLister     = (*Provider)(nil)
)
