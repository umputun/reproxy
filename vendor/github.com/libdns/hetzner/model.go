package hetzner

import (
	"github.com/libdns/libdns"
	"time"
)

// Zone is the zone type for Hetzner.
type Zone struct {
	ID string `json:"id"`
}

// Record is the record type for Hetzner implementing the libdns.Record interface.
type Record struct {
	ID     string `json:"id,omitempty"`
	ZoneID string `json:"zone_id,omitempty"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	TTL    int    `json:"ttl"`
}

// RR implements the libdns.Record interface.
func (r *Record) RR() libdns.RR {
	return libdns.RR{
		Name: r.Name,
		TTL:  time.Duration(r.TTL) * time.Second,
		Type: r.Type,
		Data: r.Value,
	}
}

// Parse parses a record from the Hetzner API response into libdns.Record.
func (r *Record) Parse(zone string) (libdns.Record, error) {
	rr, err := libdns.RR{
		Name: libdns.RelativeName(r.Name, zone),
		TTL:  time.Duration(r.TTL) * time.Second,
		Type: r.Type,
		Data: r.Value,
	}.Parse()

	if err != nil {
		return nil, err
	}

	return rr, nil
}
