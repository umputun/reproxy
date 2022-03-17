package dns

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// Opts contains configuration for a DNS provider.
type Opts struct {
	Provider        string
	ConfigPath      string
	Timeout         time.Duration
	PollingInterval time.Duration
}

// Record is a DNS record.
type Record struct {
	Type   string
	Host   string
	Domain string
	Value  string
}

// Provider is the interface that wraps the methods required to implement a
// DNS provider for the ACME DNS challenge.
type Provider interface {
	// AddRecord creates TXT records for the specified FQDN and value.
	AddRecord(record Record) error

	// RemoveRecord removes the TXT records matching the specified FQDN and value.
	RemoveRecord(record Record) error

	// WaitUntilPropagated waits for the DNS records to propagate.
	// The method will be called after creating TXT records. A provider API could be
	// used to check propagation status.
	WaitUntilPropagated(ctx context.Context, record Record) error

	// // GetTimeout returns timeout and interval for the DNS propagation check.
	// GetTimeout() (timeout time.Duration, interval time.Duration)
}

// LookupTXTRecord checks if the TXT record exists and has the specified value.
// If the record does not exist, the function returns an error.
func LookupTXTRecord(record Record, nameserver string) error {
	ns := nameserver
	if !strings.Contains(ns, ":") {
		ns = fmt.Sprintf("%s:53", nameserver)
	}

	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, network, ns)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	vals, err := r.LookupTXT(ctx, fmt.Sprintf("%s.%s", record.Host, record.Domain))
	if err != nil {
		return fmt.Errorf("nameserver %s: error looking up TXT record %s: %s",
			nameserver, record, err)
	}

	for _, val := range vals {
		if val == record.Value {
			return nil
		}
	}

	maskedValue := ""
	if len(record.Value) > 5 {
		maskedValue = record.Value[len(record.Value)-4:]
	}
	return fmt.Errorf("nameserver %s: could not find TXT record %s with value ..%s",
		nameserver, fmt.Sprintf("%s.%s", record.Host, record.Domain), maskedValue)
}
