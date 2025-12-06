package porkbun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/libdns/libdns"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

const ApiBase = "https://api.porkbun.com/api/json/v3"

// LibdnsZoneToPorkbunDomain Strips the trailing dot from a Zone
func LibdnsZoneToPorkbunDomain(zone string) string {
	return strings.TrimSuffix(zone, ".")
}

// Converts libdns' name representation to porkbun's
func LibdnsNameToPorkbunName(name string, zone string) string {
	relativeName := libdns.RelativeName(name, zone)
	if relativeName == "@" {
		return ""
	} else {
		return relativeName
	}
}

// CheckCredentials allows verifying credentials work in test scripts
func (p *Provider) CheckCredentials(_ context.Context) (string, error) {
	credentialJson, err := json.Marshal(p.getCredentials())
	if err != nil {
		return "", err
	}

	response, err := MakeApiRequest("/ping", bytes.NewReader(credentialJson), pkbnPingResponse{})

	if err != nil {
		return "", err
	}

	if response.Status != "SUCCESS" {
		return "", err
	}

	return response.YourIP, nil
}

func (p *Provider) getCredentials() ApiCredentials {
	return ApiCredentials{p.APIKey, p.APISecretKey}
}

// UpdateRecords adds records to the zone. It returns the records that were added.
func (p *Provider) updateRecords(_ context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	credentials := p.getCredentials()
	trimmedZone := LibdnsZoneToPorkbunDomain(zone)

	var createdRecords []libdns.Record

	for _, record := range records {
		trimmedName := LibdnsNameToPorkbunName(record.RR().Name, zone)
		reqBody, err := porkbunRecordPayload(record, &credentials, zone)
		reqJson, err := json.Marshal(reqBody)
		if err != nil {
			return nil, err
		}
		response, err := MakeApiRequest(fmt.Sprintf("/dns/editByNameType/%s/%s/%s", trimmedZone, record.RR().Type, trimmedName), bytes.NewReader(reqJson), pkbnResponseStatus{})
		if err != nil {
			return nil, err
		}

		if response.Status != "SUCCESS" {
			return nil, fmt.Errorf(
				"invalid response status %s %s", response.Status, response.Message,
			)
		}
		createdRecords = append(createdRecords, record)
	}

	return createdRecords, nil
}

func (p *Provider) getZones(_ context.Context) ([]libdns.Zone, error) {
	credentials := p.getCredentials()

	reqJson, err := json.Marshal(credentials)
	if err != nil {
		return nil, err
	}

	response, err := MakeApiRequest("/domain/listAll", bytes.NewReader(reqJson), pkbnDomainResponse{})
	if err != nil {
		return nil, err
	}

	if response.Status != "SUCCESS" {
		return nil, fmt.Errorf("Invalid response status %s", response.Status)
	}

	var zones []libdns.Zone

	for _, zone := range response.Domains {
		asZone := libdns.Zone{
			Name: zone.Domain,
		}
		zones = append(zones, asZone)
	}

	return zones, nil
}

func MakeApiRequest[T any](endpoint string, body io.Reader, responseType T) (T, error) {
	client := http.Client{}

	fullUrl := ApiBase + endpoint
	u, err := url.Parse(fullUrl)
	if err != nil {
		return responseType, err
	}

	req, err := http.NewRequest("POST", u.String(), body)
	if err != nil {
		return responseType, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return responseType, err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Fatal("Couldn't close body")
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		err = errors.New("Invalid http response status, " + string(bodyBytes) + " for endpoint " + endpoint)
		return responseType, err
	}

	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return responseType, err
	}

	err = json.Unmarshal(result, &responseType)

	if err != nil {
		return responseType, err
	}

	return responseType, nil
}
