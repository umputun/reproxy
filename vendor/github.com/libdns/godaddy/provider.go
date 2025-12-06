// Package godaddy implements methods for manipulating GoDaddy DNS records.
// based on GoDaddy Domains API https://developer.godaddy.com/doc/endpoint/domains#/v1
package godaddy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/libdns/libdns"
)

const (
	// RECORDPAGEMAX is the maximum number of records that can be returned per API call/
	RECORDPAGEMAX = 500
)

// Provider godaddy dns provider
type Provider struct {
	APIToken string `json:"api_token,omitempty"`
}

func getDomain(zone string) string {
	return strings.TrimSuffix(zone, ".")
}

func getRecordName(zone, name string) string {
	return strings.TrimSuffix(strings.TrimSuffix(name, zone), ".")
}

func (p *Provider) getApiHost() string {
	return "https://api.godaddy.com"
}

// GetRecords lists all the records in the zone.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	log.Println("GetRecords", zone)
	client := http.Client{}
	domain := getDomain(zone)
	var records []libdns.Record
	var resultObj []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Value string `json:"data"`
		TTL   int    `json:"ttl"`
	}

	// retrieve pages of up to 500 records each; continue incrementing the page counter
	// until the record count drops below the max 500 (final page)
	morePages := true
	for page := 1; morePages; page++ {
		url := p.getApiHost() + "/v1/domains/" + domain + "/records?offset=" + fmt.Sprint(page) + "&limit=" + fmt.Sprint(RECORDPAGEMAX)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Add("Authorization", "sso-key "+p.APIToken)
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		// successful page retrieval returns code 200; attempting a page beyond the final sometimes returns code 422 UnprocessableEntity
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnprocessableEntity {
			bodyBytes, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("could not get records: Domain: %s; Status: %v; Body: %s",
				domain, resp.StatusCode, string(bodyBytes))
		}

		if resp.StatusCode == http.StatusUnprocessableEntity {
			morePages = false // don't read any more pages; still return accumulated results
			break
		}

		result, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal(result, &resultObj)
		if err != nil {
			return nil, err
		}

		// if no records returned, we've attempted to read beyond the final page
		if len(resultObj) == 0 {
			morePages = false // don't read any more pages; still return accumulated results
			break
		}

		// accumulate all records retrieved in the current page
		for _, record := range resultObj {
			records = append(records, libdns.RR{
				Type: record.Type,
				Name: record.Name,
				Data: record.Value,
				TTL:  time.Duration(record.TTL) * time.Second,
			})
		}

		// if results returned were less than the max page size, then this was the final page
		if len(resultObj) < RECORDPAGEMAX {
			morePages = false // don't read any more pages; still return accumulated results
			break
		}
	}

	return records, nil
}

// AppendRecords adds records to the zone. It returns the records that were added.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	log.Println("AppendRecords", zone, records)
	var appendedRecords []libdns.Record

	for _, r := range records {
		record := r.RR()
		client := http.Client{}

		type PostRecord struct {
			Data string `json:"data"`
			TTL  int    `json:"ttl"`
		}

		if record.TTL < time.Duration(600)*time.Second {
			record.TTL = time.Duration(600) * time.Second
		}

		data, err := json.Marshal([]PostRecord{
			{
				Data: record.Data,
				TTL:  int(record.TTL / time.Second),
			},
		})
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequest(http.MethodPut, p.getApiHost()+"/v1/domains/"+getDomain(zone)+"/records/"+record.Type+"/"+getRecordName(zone, record.Name), bytes.NewBuffer(data))
		if err != nil {
			return nil, err
		}
		req.Header.Add("Authorization", "sso-key "+p.APIToken)
		req.Header.Add("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("could not append records: Domain: %s; Record: %s, Status: %v; Body: %s; PUT: %s",
				getDomain(zone), getRecordName(zone, record.Name), resp.StatusCode, string(bodyBytes), data)
		}

		_, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		appendedRecords = append(appendedRecords, record)
	}

	return appendedRecords, nil
}

// SetRecords sets the records in the zone, either by updating existing records
// or creating new ones. It returns the updated records.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	log.Println("SetRecords", zone, records)
	return p.AppendRecords(ctx, zone, records)
}

// DeleteRecords deletes the records from the zone.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	log.Println("DeleteRecords", zone, records)

	currentRecords, err := p.GetRecords(ctx, zone)
	if err != nil {
		return nil, err
	}

	var deletedRecords []libdns.Record

	// accumulate records verified to actually exist in the zone
	for _, r := range records {
		record := r.RR()
		for _, cr := range currentRecords {
			currentRecord := cr.RR()
			if currentRecord.Type == record.Type && currentRecord.Name == getRecordName(zone, record.Name) {
				deletedRecords = append(deletedRecords, currentRecord)
				break
			}
		}
	}

	// loop through and delete verified records with individual API calls
	for _, r := range deletedRecords {
		record := r.RR()
		req, err := http.NewRequest(http.MethodDelete, p.getApiHost()+"/v1/domains/"+getDomain(zone)+"/records/"+record.Type+"/"+record.Name, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Add("Authorization", "sso-key "+p.APIToken)
		req.Header.Add("Content-Type", "application/json")

		client := http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent {
			bodyBytes, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("could not delete requested records: Domain: %s; Records: %v, Status: %v; Body: %s",
				zone, deletedRecords, resp.StatusCode, string(bodyBytes))
		}

		_, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
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
