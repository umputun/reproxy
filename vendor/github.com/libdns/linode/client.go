package linode

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/libdns/libdns"
	"github.com/linode/linodego"
)

var ErrUnsupportedType = errors.New("Unsupported DNS record type")

func (p *Provider) getDomainIDByZone(ctx context.Context, zone string) (int, error) {
	slog.Debug("Enter getDomainIDByZone", "zone", zone)
	f := linodego.Filter{}
	// Trim the trailing dot from the zone name because Linode seems to require it
	f.AddField(linodego.Eq, "domain", strings.TrimSuffix(libdns.AbsoluteName("@", zone), "."))
	filter, err := f.MarshalJSON()
	if err != nil {
		return 0, fmt.Errorf("failed to marshal filter: %w", err)
	}
	listOptions := linodego.NewListOptions(0, string(filter))
	domains, err := p.client.ListDomains(ctx, listOptions)
	if err != nil {
		return 0, fmt.Errorf("could not list domains: %w", err)
	}
	if len(domains) == 0 {
		return 0, fmt.Errorf("could not find the domain: 0 returned")
	}
	if len(domains) > 1 {
		return 0, fmt.Errorf("could not find the domain: >1 returned: [%v]", domains)
	}
	slog.Debug("Exit getDomainIDByZone", "zone", zone, "domainID", domains[0].ID)
	return domains[0].ID, nil
}

func (p *Provider) listDomainRecords(ctx context.Context, domainID int) ([]libdns.Record, error) {
	slog.Debug("Enter listDomainRecords", "domainID", domainID)
	linodeRecords, err := p.client.ListDomainRecords(ctx, domainID, nil)
	if err != nil {
		return nil, fmt.Errorf("could not list domain records: %w", err)
	}
	records := make([]libdns.Record, 0, len(linodeRecords))
	for _, linodeRecord := range linodeRecords {
		record, err := convertToLibdns(&linodeRecord)
		if err != nil {
			return nil, fmt.Errorf("could not convert record to libdns struct: %w", err)
		}
		records = append(records, record)
	}
	slog.Debug("Exit listDomainRecords", "domainID", domainID, "lenRecords", len(records))
	return records, nil
}

func (p *Provider) createOrUpdateDomainRecords(ctx context.Context, zone string, domainID int, records []libdns.Record) ([]libdns.Record, error) {
	slog.Debug("Enter createOrUpdateDomainRecords", "zone", zone, "domainID", domainID, "lenRecords", len(records))
	// According to the libdns interface, any (Name, Type) pairs in the input records should be the only records that
	// remain in the output for those (Name, Type) pairs.
	// Ex: (lifted from the libdns interface and annotated)
	//
	// Example 1:
	//
	//	;; Original zone
	//	example.com. 3600 IN A   192.0.2.1
	//	example.com. 3600 IN A   192.0.2.2
	//	example.com. 3600 IN TXT "hello world"
	//
	//	;; Input
	//	example.com. 3600 IN A   192.0.2.3
	//
	//	;; Resultant zone
	//	example.com. 3600 IN A   192.0.2.3 (consolidated, updated)
	//	example.com. 3600 IN TXT "hello world" (unchanged)
	//
	// Example 2:
	//
	//	;; Original zone
	//	alpha.example.com. 3600 IN AAAA 2001:db8::1
	//	alpha.example.com. 3600 IN AAAA 2001:db8::2
	//	beta.example.com.  3600 IN AAAA 2001:db8::3
	//	beta.example.com.  3600 IN AAAA 2001:db8::4
	//
	//	;; Input
	//	alpha.example.com. 3600 IN AAAA 2001:db8::1
	//	alpha.example.com. 3600 IN AAAA 2001:db8::2
	//	alpha.example.com. 3600 IN AAAA 2001:db8::5
	//
	//	;; Resultant zone
	//	alpha.example.com. 3600 IN AAAA 2001:db8::1 (unchanged, present in input)
	//	alpha.example.com. 3600 IN AAAA 2001:db8::2 (unchanged, present in input)
	//	alpha.example.com. 3600 IN AAAA 2001:db8::5 (updated)
	//	beta.example.com.  3600 IN AAAA 2001:db8::3 (unchanged, not present in input)
	//	beta.example.com.  3600 IN AAAA 2001:db8::4 (unchanged, not present in input)
	setRecords := make([]libdns.Record, 0, len(records))

	// First, make a map of (Name, Type) pairs from the input records
	pairs := make(map[string]map[string]struct{})
	for _, rec := range records {
		rr := rec.RR()
		// Set value for (Name, Type) pair
		pairs[rr.Name] = make(map[string]struct{})
		pairs[rr.Name][rr.Type] = struct{}{}
	}

	// Fetch existing records to determine which to delete
	// Use linode API (not libdns) to keep the record ID
	existingRecords, err := p.client.ListDomainRecords(ctx, domainID, nil)
	if err != nil {
		return nil, fmt.Errorf("could not list domain records: %w", err)
	}

	// Delete any records that match the (Name, Type) pairs in the input
	for _, record := range existingRecords {
		libRecord, err := convertToLibdns(&record)
		if err != nil {
			return nil, fmt.Errorf("could not convert record to libdns struct: %w", err)
		}
		rr := libRecord.RR()
		if _, ok := pairs[rr.Name]; ok {
			if _, ok := pairs[rr.Name][rr.Type]; ok {
				// Existing record matches (Name, Type) pair in input; delete it
				if err := p.client.DeleteDomainRecord(ctx, domainID, record.ID); err != nil {
					return setRecords, fmt.Errorf("could not delete domain record %d: %w", record.ID, err)
				}
			}
		}
	}

	// Finally, add the records from the input
	for _, record := range records {
		created, err := p.createDomainRecord(ctx, zone, domainID, record)
		if err != nil {
			return nil, fmt.Errorf("could not create domain record: %w", err)
		}
		setRecords = append(setRecords, created)
	}

	slog.Debug("Exit createOrUpdateDomainRecords", "zone", zone, "domainID", domainID, "lenSetRecords", len(setRecords))
	return setRecords, nil
}

func (p *Provider) createDomainRecord(ctx context.Context, zone string, domainID int, record libdns.Record) (libdns.Record, error) {
	rr := record.RR()
	slog.Debug("Enter createDomainRecord", "zone", zone, "domainID", domainID, "name", rr.Name, "type", rr.Type)
	createOpts, err := convertToDomainRecord(record, zone)
	if err != nil {
		return nil, fmt.Errorf("could not convert record to linodego struct: %w", err)
	}
	addedLinodeRecord, err := p.client.CreateDomainRecord(ctx, domainID, createOpts)
	if err != nil {
		return nil, fmt.Errorf("could not create domain record: %w", err)
	}
	librec, err := convertToLibdns(addedLinodeRecord)
	slog.Debug("Exit createDomainRecord", "zone", zone, "domainID", domainID, "name", addedLinodeRecord.Name, "type", addedLinodeRecord.Type, "err", err)
	return librec, err
}

// deleteDomainRecords deletes each record from the zone. It returns the records that were deleted.
// As per the libdns interface, any deleted records must match exactly the input record (Name, Type, TTL, Value).
// If any of (Type, TTL, Value) are "", 0, or "", respectively, deleteDomainRecord will delete any records that match
// the other fields, regardless of the value of the fields that were left empty.
// Note: this does not apply to the Name field.
// Since there are wildcards for Type, TTL, and Value, it can delete multiple records for each input record.
func (p *Provider) deleteDomainRecords(ctx context.Context, domainID int, records []libdns.Record) ([]libdns.Record, error) {
	slog.Debug("Enter deleteDomainRecords", "domainID", domainID, "lenRecords", len(records))
	// Future improvement?: It should be possible to use the linodego.ListOptions to filter by Name, Type, TTL, and Value.
	// Though this would change the number of API calls from one (list all) to N, where N is the number of records to delete.
	// For now, we just list all records and delete them one by one.
	linodeRecords, err := p.client.ListDomainRecords(ctx, domainID, nil)
	if err != nil {
		return nil, fmt.Errorf("could not list domain records: %w", err)
	}
	deletedLinodeRecords := make([]bool, len(linodeRecords))

	deleted := make([]libdns.Record, 0)
	for _, record := range records {
		rr := record.RR()
		if rr.Name == "" {
			return nil, fmt.Errorf("record name is required")
		}

		for lrecI, lrec := range linodeRecords {
			if deletedLinodeRecords[lrecI] {
				continue // Already deleted
			}
			// Convert Linode record to libdns record for consistent comparison logic
			librec, err := convertToLibdns(&lrec)
			if err != nil {
				// Skip records that cannot be represented in libdns (e.g., PTR)
				if lrec.Type == linodego.RecordTypePTR {
					continue
				}
				return deleted, fmt.Errorf("could not convert record to libdns struct: %w", err)
			}
			lrr := librec.RR()

			// Name must always match exactly
			if lrr.Name != rr.Name {
				continue
			}
			// Type/TTL/Data support wildcards when zero values are provided in input
			if rr.Type != "" && lrr.Type != rr.Type {
				continue
			}
			if rr.TTL != 0 && lrr.TTL != rr.TTL {
				continue
			}
			if rr.Data != "" && lrr.Data != rr.Data {
				continue
			}

			// Delete the matching record
			if err := p.client.DeleteDomainRecord(ctx, domainID, lrec.ID); err != nil {
				return deleted, fmt.Errorf("could not delete domain record %d: %w", lrec.ID, err)
			}
			deletedLinodeRecords[lrecI] = true
			deleted = append(deleted, librec)
		}
	}

	slog.Debug("Exit deleteDomainRecords", "domainID", domainID, "lenDeleted", len(deleted))
	return deleted, nil
}

func convertToLibdns(linodeRecord *linodego.DomainRecord) (libdns.Record, error) {
	slog.Debug("Enter convertToLibdns", "type", linodeRecord.Type, "name", linodeRecord.Name)
	switch linodeRecord.Type {
	case linodego.RecordTypeA:
		fallthrough
	case linodego.RecordTypeAAAA:
		record := libdns.Address{}
		record.Name = libdnsWantsAtSym(linodeRecord.Name)
		record.TTL = time.Duration(linodeRecord.TTLSec) * time.Second
		ip, err := netip.ParseAddr(linodeRecord.Target)
		if err != nil {
			return nil, fmt.Errorf("could not parse target as IP: %w", err)
		}
		record.IP = ip
		slog.Debug("Exit convertToLibdns", "type", linodeRecord.Type, "name", linodeRecord.Name, "as", "Address")
		return record, nil
	case linodego.RecordTypeNS:
		record := libdns.NS{}
		record.Name = libdnsWantsAtSym(linodeRecord.Name)
		record.TTL = time.Duration(linodeRecord.TTLSec) * time.Second
		record.Target = linodeRecord.Target
		slog.Debug("Exit convertToLibdns", "type", linodeRecord.Type, "name", linodeRecord.Name, "as", "NS")
		return record, nil
	case linodego.RecordTypeMX:
		record := libdns.MX{}
		record.Name = libdnsWantsAtSym(linodeRecord.Name)
		record.TTL = time.Duration(linodeRecord.TTLSec) * time.Second
		record.Preference = uint16(linodeRecord.Priority)
		record.Target = linodeRecord.Target
		slog.Debug("Exit convertToLibdns", "type", linodeRecord.Type, "name", linodeRecord.Name, "as", "MX")
		return record, nil
	case linodego.RecordTypeCNAME:
		record := libdns.CNAME{}
		record.Name = libdnsWantsAtSym(linodeRecord.Name)
		record.TTL = time.Duration(linodeRecord.TTLSec) * time.Second
		record.Target = linodeRecord.Target
		slog.Debug("Exit convertToLibdns", "type", linodeRecord.Type, "name", linodeRecord.Name, "as", "CNAME")
		return record, nil
	case linodego.RecordTypeTXT:
		record := libdns.TXT{}
		record.Name = libdnsWantsAtSym(linodeRecord.Name)
		record.TTL = time.Duration(linodeRecord.TTLSec) * time.Second
		record.Text = linodeRecord.Target
		slog.Debug("Exit convertToLibdns", "type", linodeRecord.Type, "name", linodeRecord.Name, "as", "TXT")
		return record, nil
	case linodego.RecordTypeSRV:
		record := libdns.SRV{}
		service := ""
		if linodeRecord.Service != nil {
			service = *linodeRecord.Service
		}
		record.Service = service
		transport := ""
		if linodeRecord.Protocol != nil {
			transport = *linodeRecord.Protocol
		}
		record.Transport = transport
		record.Name = libdnsWantsAtSym(linodeRecord.Name)
		record.TTL = time.Duration(linodeRecord.TTLSec) * time.Second
		record.Priority = uint16(linodeRecord.Priority)
		record.Weight = uint16(linodeRecord.Weight)
		record.Port = uint16(linodeRecord.Port)
		record.Target = linodeRecord.Target
		slog.Debug("Exit convertToLibdns", "type", linodeRecord.Type, "name", linodeRecord.Name, "as", "SRV")
		return record, nil
	case linodego.RecordTypePTR:
		// Can't be represented in libdns
		slog.Debug("Exit convertToLibdns", "type", linodeRecord.Type, "name", linodeRecord.Name, "as", "PTR")
		return nil, fmt.Errorf("libdns does not support PTR records")
	case linodego.RecordTypeCAA:
		record := libdns.CAA{}
		record.Name = libdnsWantsAtSym(linodeRecord.Name)
		record.TTL = time.Duration(linodeRecord.TTLSec) * time.Second
		// Linode does not support setting flags as of 2025/08/16
		// See https://www.linode.com/community/questions/20714/how-to-i-change-the-flag-in-a-caa-record
		record.Flags = 0
		if linodeRecord.Tag == nil {
			return nil, fmt.Errorf("linodeRecord.Tag is required for CAA records")
		}
		record.Tag = *linodeRecord.Tag
		record.Value = linodeRecord.Target
		slog.Debug("Exit convertToLibdns", "type", linodeRecord.Type, "name", linodeRecord.Name, "as", "CAA")
		return record, nil
	default:
		slog.Debug("Exit convertToLibdns", "type", linodeRecord.Type, "name", linodeRecord.Name, "as", "Unknown")
		return nil, fmt.Errorf("unknown record type: %v", linodeRecord.Type)
	}
}

func convertToDomainRecord(record libdns.Record, zone string) (linodego.DomainRecordCreateOptions, error) {
	rr := record.RR()
	slog.Debug("Enter convertToDomainRecord", "zone", zone, "name", rr.Name, "type", rr.Type)
	domainRecord := linodego.DomainRecordCreateOptions{
		Type:   linodego.DomainRecordType(rr.Type),
		Name:   linodeDoesntWantAtSym(libdns.RelativeName(rr.Name, zone)),
		Target: rr.Data, // This is often sufficient, but for some record types we have to fix this up later
		TTLSec: int(rr.TTL.Seconds()),
	}
	switch record.(type) {
	case libdns.Address:
		// All necessary fields are set
	case libdns.CAA:
		typeRecord := record.(libdns.CAA)
		// Linode doesn't support Flags; it assumes the value 0
		domainRecord.Tag = &typeRecord.Tag
		domainRecord.Target = typeRecord.Value
	case libdns.CNAME:
		// All necessary fields are set
	case libdns.MX:
		typeRecord := record.(libdns.MX)
		priority := int(typeRecord.Preference)
		domainRecord.Priority = &priority
		domainRecord.Target = typeRecord.Target
	case libdns.NS:
		// All necessary fields are set
	case libdns.SRV:
		typeRecord := record.(libdns.SRV)
		domainRecord.Name = "" // Name is not applicable for SRV records
		priority := int(typeRecord.Priority)
		domainRecord.Priority = &priority
		weight := int(typeRecord.Weight)
		domainRecord.Weight = &weight
		port := int(typeRecord.Port)
		domainRecord.Port = &port
		domainRecord.Target = typeRecord.Target
		service := typeRecord.Service
		domainRecord.Service = &service
		transport := typeRecord.Transport
		domainRecord.Protocol = &transport
	case libdns.ServiceBinding:
		// Not supported by Linode
		return linodego.DomainRecordCreateOptions{}, fmt.Errorf("linode does not support ServiceBinding records (%+v): %w", record, ErrUnsupportedType)
	case libdns.TXT:
		// All necessary fields are set
	}
	slog.Debug("Exit convertToDomainRecord", "zone", zone, "name", rr.Name, "type", rr.Type, "options", domainRecord)
	return domainRecord, nil
}

func libdnsWantsAtSym(name string) string {
	if name == "" {
		return "@"
	}
	return name
}

func linodeDoesntWantAtSym(name string) string {
	if name == "@" {
		return ""
	}
	return name
}
