package route53

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	r53 "github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/libdns/libdns"
)

// setLockKey identifies a critical section per (zoneID, name, recordType) —
// the granularity at which Route53 read-modify-write must be serialized.
type setLockKey struct {
	zoneID, name, recordType string
}

// lockSet acquires the per-tuple mutex and returns a function to release it.
// Distinct tuples parallelize; concurrent callers on the same tuple serialize.
//
// Holding the lock from getRecords through the ChangeResourceRecordSets call
// closes the read-modify-write window — without it, two callers can both
// observe pre-state and the later UPSERT clobbers the earlier (libdns
// concurrency contract violation; manifests as ACME challenge token loss
// when SAN certificates share a _acme-challenge RecordSet).
//
// The lock map grows with unique tuples touched by this Provider — bounded
// by the number of (name, type) pairs in the zones it manages, which is
// negligible in practice. Multi-process coordination is out of scope; users
// running multiple processes against the same zone must coordinate
// externally.
func (p *Provider) lockSet(k setLockKey) func() {
	actual, _ := p.setLocks.LoadOrStore(k, &sync.Mutex{})
	mu, ok := actual.(*sync.Mutex)
	if !ok {
		// unreachable: setLocks only ever stores *sync.Mutex.
		panic("route53: setLocks contains non-*sync.Mutex value")
	}
	mu.Lock()
	return mu.Unlock
}

type contextKey int

const (
	contextKeyIsDeleteOperation contextKey = iota
)

const (
	// defaultTTL is the default TTL for DNS records in seconds.
	defaultTTL = 300
	// maxTXTValueLength is the maximum length of a single TXT record value.
	maxTXTValueLength = 255
	// maxRecordsPerPage is the maximum number of records Route53 returns per page.
	maxRecordsPerPage = 1000
)

// changeRecordSet performs a specified action (UPSERT or DELETE) on a ResourceRecordSet.
func (p *Provider) changeRecordSet(
	ctx context.Context,
	zoneID, zone, name, recordType string,
	records []libdns.Record,
	action types.ChangeAction,
) error {
	var resourceRecords []types.ResourceRecord
	for _, record := range records {
		rr := record.RR()
		resourceRecords = append(resourceRecords, marshalRecord(rr)...)
	}

	// use the TTL from the first record
	ttl := int64(defaultTTL)
	if len(records) > 0 {
		ttl = int64(records[0].RR().TTL.Seconds())
	}

	input := &r53.ChangeResourceRecordSetsInput{
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{
				{
					Action: action,
					ResourceRecordSet: &types.ResourceRecordSet{
						Name:            aws.String(libdns.AbsoluteName(name, zone)),
						ResourceRecords: resourceRecords,
						TTL:             aws.Int64(ttl),
						Type:            types.RRType(recordType),
					},
				},
			},
		},
		HostedZoneId: aws.String(zoneID),
	}

	p.Logger.DebugContext(ctx, "applying Route53 record set change",
		"action", string(action),
		"zone", zone,
		"name", name,
		"type", recordType,
		"value_count", len(resourceRecords),
		"ttl_seconds", ttl)

	return p.applyChange(ctx, input)
}

func (p *Provider) setRecordSet(
	ctx context.Context,
	zoneID, zone, name, recordType string,
	records []libdns.Record,
) error {
	// use UPSERT to replace the entire record set
	return p.changeRecordSet(ctx, zoneID, zone, name, recordType, records, types.ChangeActionUpsert)
}

func (p *Provider) deleteRecordSet(
	ctx context.Context,
	zoneID, zone, name, recordType string,
	records []libdns.Record,
) error {
	// use DELETE action to remove the entire record set
	return p.changeRecordSet(ctx, zoneID, zone, name, recordType, records, types.ChangeActionDelete)
}

func (p *Provider) init(ctx context.Context) {
	// Logger fallback runs on every call, not just the first: the field
	// docs promise nil → discard handler, and a caller might clear Logger
	// after the provider was already initialized.
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}

	p.initOnce.Do(func() {
		if p.MaxRetries == 0 {
			p.MaxRetries = 5
		}

		if p.Route53MaxWait == 0 {
			p.Route53MaxWait = time.Minute
		}

		opts := make([]func(*config.LoadOptions) error, 0)
		opts = append(opts,
			config.WithRetryer(func() aws.Retryer {
				return retry.AddWithMaxAttempts(retry.NewStandard(), p.MaxRetries)
			}),
		)

		profile := p.Profile

		if profile != "" {
			opts = append(opts, config.WithSharedConfigProfile(profile))
		}

		if p.Region != "" {
			opts = append(opts, config.WithRegion(p.Region))
		}

		if p.AccessKeyId != "" && p.SecretAccessKey != "" {
			token := p.SessionToken

			opts = append(
				opts,
				config.WithCredentialsProvider(
					credentials.NewStaticCredentialsProvider(p.AccessKeyId, p.SecretAccessKey, token),
				),
			)
		}

		cfg, err := config.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			log.Fatalf("route53: unable to load AWS SDK config, %v", err)
		}

		p.client = r53.NewFromConfig(cfg)
	})
}

func chunkString(s string, chunkSize int) []string {
	var chunks []string
	for i := 0; i < len(s); i += chunkSize {
		end := i + chunkSize
		if end > len(s) {
			end = len(s)
		}
		chunks = append(chunks, s[i:end])
	}
	return chunks
}

func parseRecordSet(set types.ResourceRecordSet, zone string) ([]libdns.Record, error) {
	records := make([]libdns.Record, 0)

	// Route53 returns TXT & SPF records with quotes around them.
	// https://docs.aws.amazon.com/Route53/latest/DeveloperGuide/ResourceRecordTypes.html#TXTFormat
	var ttl int64
	if set.TTL != nil {
		ttl = *set.TTL
	}

	rtype := string(set.Type)
	relativeName := libdns.RelativeName(*set.Name, zone)

	for _, record := range set.ResourceRecords {
		value := *record.Value
		switch rtype {
		case "TXT", "SPF":
			rows := strings.Split(value, "\n")
			for _, row := range rows {
				parts := strings.Split(row, `" "`)
				if len(parts) > 0 {
					parts[0] = strings.TrimPrefix(parts[0], `"`)
					parts[len(parts)-1] = strings.TrimSuffix(parts[len(parts)-1], `"`)
				}

				row = strings.Join(parts, "")
				row = unquote(row)

				rr := libdns.RR{
					Name: relativeName,
					Data: row,
					Type: rtype,
					TTL:  time.Duration(ttl) * time.Second,
				}
				parsedRecord, err := rr.Parse()
				if err != nil {
					return nil, fmt.Errorf("failed to parse %s record %s: %w", rtype, relativeName, err)
				}
				records = append(records, parsedRecord)
			}
		default:
			rr := libdns.RR{
				Name: relativeName,
				Data: value,
				Type: rtype,
				TTL:  time.Duration(ttl) * time.Second,
			}
			parsedRecord, err := rr.Parse()
			if err != nil {
				return nil, fmt.Errorf("failed to parse %s record %s: %w", rtype, relativeName, err)
			}
			records = append(records, parsedRecord)
		}
	}

	return records, nil
}

func marshalRecord(record libdns.RR) []types.ResourceRecord {
	resourceRecords := make([]types.ResourceRecord, 0)

	// Route53 requires TXT & SPF records to be quoted.
	// https://docs.aws.amazon.com/Route53/latest/DeveloperGuide/ResourceRecordTypes.html#TXTFormat
	switch record.Type {
	case "TXT", "SPF":
		strs := make([]string, 0)
		if len(record.Data) > maxTXTValueLength {
			strs = append(strs, chunkString(record.Data, maxTXTValueLength)...)
		} else {
			strs = append(strs, record.Data)
		}

		// Quote strings
		for i, str := range strs {
			strs[i] = quote(str)
		}

		// Finally join chunks with spaces
		resourceRecords = append(resourceRecords, types.ResourceRecord{
			Value: aws.String(strings.Join(strs, " ")),
		})
	default:
		resourceRecords = append(resourceRecords, types.ResourceRecord{
			Value: aws.String(record.Data),
		})
	}

	return resourceRecords
}

func (p *Provider) getRecords(ctx context.Context, zoneID string, zone string) ([]libdns.Record, error) {
	getRecordsInput := &r53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		MaxItems:     aws.Int32(maxRecordsPerPage),
	}

	var records []libdns.Record

	for {
		getRecordResult, err := p.client.ListResourceRecordSets(ctx, getRecordsInput)
		if err != nil {
			var nshze *types.NoSuchHostedZone
			var iie *types.InvalidInput
			switch {
			case errors.As(err, &nshze):
				return records, fmt.Errorf("NoSuchHostedZone: %w", err)
			case errors.As(err, &iie):
				return records, fmt.Errorf("InvalidInput: %w", err)
			default:
				return records, err
			}
		}

		for _, s := range getRecordResult.ResourceRecordSets {
			parsedRecords, parseErr := parseRecordSet(s, zone)
			if parseErr != nil {
				return records, fmt.Errorf("failed to parse record set: %w", parseErr)
			}
			records = append(records, parsedRecords...)
		}

		if getRecordResult.IsTruncated {
			getRecordsInput.StartRecordName = getRecordResult.NextRecordName
			getRecordsInput.StartRecordType = getRecordResult.NextRecordType
			getRecordsInput.StartRecordIdentifier = getRecordResult.NextRecordIdentifier
		} else {
			break
		}
	}

	return records, nil
}

func (p *Provider) getZoneID(ctx context.Context, zoneName string) (string, error) {
	if p.HostedZoneID != "" {
		p.Logger.DebugContext(ctx, "using preconfigured hosted zone id",
			"zone", zoneName, "hosted_zone_id", p.HostedZoneID)
		return "/hostedzone/" + p.HostedZoneID, nil
	}

	getZoneInput := &r53.ListHostedZonesByNameInput{
		DNSName:  aws.String(zoneName),
		MaxItems: aws.Int32(1),
	}

	getZoneResult, err := p.client.ListHostedZonesByName(ctx, getZoneInput)
	if err != nil {
		var idne *types.InvalidDomainName
		var iie *types.InvalidInput
		switch {
		case errors.As(err, &idne):
			return "", fmt.Errorf("InvalidDomainName: %w", err)
		case errors.As(err, &iie):
			return "", fmt.Errorf("InvalidInput: %w", err)
		default:
			return "", err
		}
	}

	matchingZones := []types.HostedZone{}

	if len(getZoneResult.HostedZones) > 0 {
		for z := range len(getZoneResult.HostedZones) {
			if *getZoneResult.HostedZones[z].Name == zoneName {
				matchingZones = append(matchingZones, getZoneResult.HostedZones[z])
			}
		}
	}

	if len(matchingZones) == 1 {
		p.Logger.DebugContext(ctx, "resolved hosted zone",
			"zone", zoneName, "hosted_zone_id", *matchingZones[0].Id)
		return *matchingZones[0].Id, nil
	}

	// If multiple zones matched the name
	if len(matchingZones) > 1 {
		// select the first public (i.e. ot-private) zone as a best guess.
		for _, zone := range matchingZones {
			if !zone.Config.PrivateZone {
				p.Logger.WarnContext(ctx, "multiple hosted zones match name; choosing first public zone",
					"zone", zoneName, "match_count", len(matchingZones), "hosted_zone_id", *zone.Id)
				return *zone.Id, nil
			}
		}
		// All zone were private, give up and return.
		// Historically we always returned the first match without checking for public/private
		p.Logger.WarnContext(ctx, "multiple private hosted zones match name; choosing first match",
			"zone", zoneName, "match_count", len(matchingZones), "hosted_zone_id", *matchingZones[0].Id)
		return *matchingZones[0].Id, nil
	}

	return "", fmt.Errorf("HostedZoneNotFound: No zones found for the domain %s", zoneName)
}

func (p *Provider) applyChange(ctx context.Context, input *r53.ChangeResourceRecordSetsInput) error {
	changeResult, err := p.client.ChangeResourceRecordSets(ctx, input)
	if err != nil {
		return err
	}

	changeID := aws.ToString(changeResult.ChangeInfo.Id)
	p.Logger.DebugContext(ctx, "Route53 change submitted",
		"change_id", changeID,
		"status", string(changeResult.ChangeInfo.Status))

	// Check if we should skip waiting for synchronization
	shouldWait := p.WaitForRoute53Sync
	skippedForDelete := false
	if shouldWait && p.SkipRoute53SyncOnDelete {
		// Check if this is a delete operation
		if isDelete, ok := ctx.Value(contextKeyIsDeleteOperation).(bool); ok && isDelete {
			shouldWait = false
			skippedForDelete = true
		}
	}

	// Wait for propagation if enabled and not skipped
	if shouldWait {
		changeInput := &r53.GetChangeInput{
			Id: changeResult.ChangeInfo.Id,
		}

		p.Logger.DebugContext(ctx, "waiting for Route53 sync",
			"change_id", changeID, "max_wait", p.Route53MaxWait)

		// Wait for the RecordSetChange status to be "INSYNC"
		waiter := r53.NewResourceRecordSetsChangedWaiter(p.client)
		err = waiter.Wait(ctx, changeInput, p.Route53MaxWait)
		if err != nil {
			return err
		}

		p.Logger.DebugContext(ctx, "Route53 sync complete", "change_id", changeID)
	} else if skippedForDelete {
		p.Logger.DebugContext(ctx, "skipping Route53 sync wait for delete operation",
			"change_id", changeID)
	}

	return nil
}
