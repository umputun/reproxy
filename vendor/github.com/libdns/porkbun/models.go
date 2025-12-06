package porkbun

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/libdns/libdns"
)

type pkbnDomain struct {
	Domain       string `json:"domain"`
	Status       string `json:"status"`
	TLD          string `json:"tld"`
	CreateDate   string `json:"createDate"`
	ExpireDate   string `json:"expireDate"`
	SecurityLock string `json:"securityLock"`
	WhoisPrivacy string `json:"whoisPrivacy"`
	AutoRenew    string `json:"autoRenew"`
	NotLocal     int    `json:"notLocal"`
}

type pkbnRecord struct {
	Content string `json:"content"`
	Name    string `json:"name"`
	Notes   string `json:"notes"`
	Prio    string `json:"prio"`
	TTL     string `json:"ttl"`
	Type    string `json:"type"`
}

type pkbnRecordsResponse struct {
	Records []pkbnRecord `json:"records"`
	Status  string       `json:"status"`
}

type ApiCredentials struct {
	Apikey       string `json:"apikey"`
	Secretapikey string `json:"secretapikey"`
}

type pkbnResponseStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type pkbnDomainResponse struct {
	Status  string       `json:"status"`
	Domains []pkbnDomain `json:"domains"`
}
type pkbnPingResponse struct {
	pkbnResponseStatus
	YourIP string `json:"yourIp"`
}

type pkbnCreateResponse struct {
	pkbnResponseStatus
}

func (record pkbnRecord) toLibdnsRecord(zone string) (libdns.Record, error) {
	name := libdns.RelativeName(record.Name, zone)
	ttl, err := time.ParseDuration(record.TTL + "s")
	if err != nil {
		return libdns.RR{}, err
	}

	switch record.Type {
	case "A", "AAAA":
		ip, err := netip.ParseAddr(record.Content)
		if err != nil {
			return libdns.Address{}, err
		}
		return libdns.Address{
			Name: name,
			TTL:  ttl,
			IP:   ip,
		}, nil
	case "CAA":
		contentParts := strings.SplitN(record.Content, " ", 3)
		flags, err := strconv.Atoi(contentParts[0])
		if err != nil {
			return libdns.CAA{}, err
		}
		tag := contentParts[1]
		value := contentParts[2]
		return libdns.CAA{
			Name:  name,
			TTL:   ttl,
			Flags: uint8(flags),
			Tag:   tag,
			Value: value,
		}, nil
	case "CNAME":
		return libdns.CNAME{
			Name:   name,
			TTL:    ttl,
			Target: record.Content,
		}, nil
	case "SRV":
		priority, err := strconv.Atoi(record.Prio)
		if err != nil {
			return libdns.SRV{}, fmt.Errorf("invalid value for priority %v; expected format: '0'", record.Prio)
		}
		nameParts := strings.SplitN(name, ".", 2)
		if len(nameParts) < 2 {
			return libdns.SRV{}, fmt.Errorf("name %v does not contain enough fields; expected format: '_service._proto'", name)
		}
		contentParts := strings.SplitN(record.Content, " ", 3)
		if len(contentParts) < 3 {
			return libdns.SRV{}, fmt.Errorf("content %v does not contain enough fields; expected format: 'weight port target'", name)
		}
		weight, err := strconv.Atoi(contentParts[0])
		if err != nil {
			return libdns.SRV{}, fmt.Errorf("invalid value for weight %v; expected integer", record.Prio)
		}
		port, err := strconv.Atoi(contentParts[1])
		if err != nil {
			return libdns.SRV{}, fmt.Errorf("invalid value for port %v; expected integer", record.Prio)
		}

		return libdns.SRV{
			Service:   strings.TrimPrefix(nameParts[0], "_"),
			Transport: strings.TrimPrefix(nameParts[1], "_"),
			Name:      zone,
			TTL:       ttl,
			Priority:  uint16(priority),
			Weight:    uint16(weight),
			Port:      uint16(port),
			Target:    contentParts[2],
		}, nil
	case "TXT":
		return libdns.TXT{
			Name: name,
			TTL:  ttl,
			Text: record.Content,
		}, err
	default:
		return libdns.RR{}, fmt.Errorf("Unsupported record type: %s", record.Type)
	}
}

func porkbunRecordPayload(record libdns.Record, credentials *ApiCredentials, zone string) (pkbnRecordPayload, error) {
	rr := record.RR()
	if rr.TTL/time.Second < 600 {
		rr.TTL = 600 * time.Second
	}
	ttlInSeconds := int(rr.TTL / time.Second)
	trimmedName := LibdnsNameToPorkbunName(rr.Name, zone)
	var data string
	switch rec := record.(type) {
	case libdns.SRV:
		trimmedName = fmt.Sprintf("_%s._%s.%s", rec.Service, rec.Transport, rec.Name)
		data = fmt.Sprintf("%d %d %s", rec.Weight, rec.Port, rec.Target)
	default:
		data = rr.Data
	}

	return pkbnRecordPayload{credentials, data, trimmedName, strconv.Itoa(ttlInSeconds), rr.Type}, nil
}

func (a pkbnResponseStatus) Error() string {
	return fmt.Sprintf("%s: %s", a.Status, a.Message)
}

type pkbnRecordPayload struct {
	*ApiCredentials
	Content string `json:"content"`
	Name    string `json:"name"`
	TTL     string `json:"ttl"`
	Type    string `json:"type"`
}
