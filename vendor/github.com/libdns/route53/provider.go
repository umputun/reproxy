package route53

import (
	"context"
	"log/slog"
	"sync"
	"time"

	r53 "github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/libdns/libdns"
)

// Provider implements the libdns interfaces for Route53.
//
// By default, the provider loads the AWS configuration from the environment.
// To override these values, set the fields in the Provider struct.
type Provider struct {
	client *r53.Client

	// Region is the AWS Region to use. If not set, it will use AWS_REGION
	// environment variable.
	Region string `json:"region,omitempty"`

	// AWSProfile is the AWS Profile to use. If not set, it will use
	// AWS_PROFILE environment variable.
	Profile string `json:"profile,omitempty"`

	// AccessKeyId is the AWS Access Key ID to use. If not set, it will use
	// AWS_ACCESS_KEY_ID
	AccessKeyId string `json:"access_key_id,omitempty"` //nolint:revive,staticcheck // established public API, cannot change

	// SecretAccessKey is the AWS Secret Access Key to use. If not set, it will use
	// AWS_SECRET_ACCESS_KEY environment variable.
	SecretAccessKey string `json:"secret_access_key,omitempty"`

	// SessionToken is the AWS Session Token to use. If not set, it will use
	// AWS_SESSION_TOKEN environment variable.
	SessionToken string `json:"session_token,omitempty"`

	// MaxRetries is the maximum number of retries to make when a request
	// fails. If not set, it will use 5 retries.
	MaxRetries int `json:"max_retries,omitempty"`

	// Route53MaxWait is the maximum amount of time to wait for a record
	// to be propagated within AWS infrastructure. Default is 1 minute.
	Route53MaxWait time.Duration `json:"route53_max_wait,omitempty"`

	// WaitForRoute53Sync if set to true, it will wait for the record to be
	// propagated within AWS infrastructure before returning. This is not related
	// to DNS propagation, that could take much longer.
	WaitForRoute53Sync bool `json:"wait_for_route53_sync,omitempty"`

	// SkipRoute53SyncOnDelete if set to true, it will skip waiting for Route53
	// synchronization when deleting records, even if WaitForRoute53Sync is true.
	// This can speed up bulk delete operations where waiting is not necessary.
	SkipRoute53SyncOnDelete bool `json:"skip_route53_sync_on_delete,omitempty"`

	// HostedZoneID is the ID of the hosted zone to use. If not set, it will
	// be discovered from the zone name.
	//
	// This option should contain only the ID; the "/hostedzone/" prefix
	// will be added automatically.
	HostedZoneID string `json:"hosted_zone_id,omitempty"`

	// Logger receives structured log events emitted by the provider. If nil,
	// a discard handler is used. Wrappers (for example, the Caddy DNS module)
	// can adapt their own logger via slog.Handler — for zap, see
	// go.uber.org/zap/exp/zapslog.
	//
	// All events are emitted at Debug level except for ambiguous zone
	// resolution, which is Warn.
	Logger *slog.Logger `json:"-"`

	initOnce sync.Once
	// setLocks serializes read-modify-write critical sections per
	// (zoneID, name, recordType). Distinct keys parallelize; concurrent
	// callers touching the same key serialize. See lockSet.
	setLocks sync.Map
}

// GetRecords lists all the records in the zone.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	p.init(ctx)

	zoneID, err := p.getZoneID(ctx, zone)
	if err != nil {
		return nil, err
	}

	records, err := p.getRecords(ctx, zoneID, zone)
	if err != nil {
		return nil, err
	}

	return records, nil
}

// AppendRecords adds records to the zone. It returns the records that were added.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.init(ctx)

	zoneID, err := p.getZoneID(ctx, zone)
	if err != nil {
		return nil, err
	}

	// group records by name+type since Route53 treats them as a single ResourceRecordSet
	recordSets := p.groupRecordsByKey(records)

	var createdRecords []libdns.Record

	// process each record set
	for key, recordGroup := range recordSets {
		created, appendErr := p.appendRecordSet(ctx, zoneID, zone, key, recordGroup)
		if appendErr != nil {
			return nil, appendErr
		}
		createdRecords = append(createdRecords, created...)
	}

	return createdRecords, nil
}

// appendRecordSet appends records to a single ResourceRecordSet.
func (p *Provider) appendRecordSet(
	ctx context.Context,
	zoneID, zone string,
	key recordSetKey,
	recordGroup []libdns.Record,
) ([]libdns.Record, error) {
	if len(recordGroup) == 0 {
		return nil, nil
	}

	// Serialize the read-merge-UPSERT cycle for this (zone, name, type)
	// against any other goroutine doing the same. See lockSet.
	unlock := p.lockSet(setLockKey{zoneID: zoneID, name: key.name, recordType: key.recordType})
	defer unlock()

	// Retrieve existing records so we can merge and UPSERT.
	// This is necessary because Route53 treats a ResourceRecordSet as a single
	// entity — we must include all existing values when updating it. Using CREATE
	// would fail if the record set already exists (e.g. a stale ACME challenge
	// TXT record from a previous attempt).
	existingRecords, err := p.getRecords(ctx, zoneID, zone)
	if err != nil {
		return nil, err
	}

	// find existing records for this name+type. getRecords returns relative
	// names, and key.name is also relative (set by groupRecordsByKey from
	// the caller's input record), so we compare directly.
	var existingValues []libdns.Record
	for _, existing := range existingRecords {
		existingRR := existing.RR()
		if existingRR.Name == key.name && existingRR.Type == key.recordType {
			existingValues = append(existingValues, existing)
		}
	}

	// combine existing records with new ones
	allRecords := make([]libdns.Record, 0, len(existingValues)+len(recordGroup))
	allRecords = append(allRecords, existingValues...)
	allRecords = append(allRecords, recordGroup...)

	// use UPSERT to set all values at once
	err = p.setRecordSet(ctx, zoneID, zone, key.name, key.recordType, allRecords)
	if err != nil {
		return nil, err
	}

	// return only the new records that were added
	return recordGroup, nil
}

// recordSetKey uniquely identifies a Route53 ResourceRecordSet by name and type.
type recordSetKey struct {
	name       string
	recordType string
}

// DeleteRecords deletes the records from the zone. If a record does not have an ID,
// it will be looked up. It returns the records that were deleted.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.init(ctx)

	// mark this context as a delete operation
	ctx = context.WithValue(ctx, contextKeyIsDeleteOperation, true)

	zoneID, err := p.getZoneID(ctx, zone)
	if err != nil {
		return nil, err
	}

	// group records by name+type and process each set under its own per-tuple
	// lock. Reading existing values is done inside that lock so concurrent
	// callers cannot observe stale state.
	toDelete := p.groupRecordsByKey(records)

	var deletedRecords []libdns.Record
	for key, deleteGroup := range toDelete {
		deleted, deleteErr := p.processRecordSetDeletion(ctx, zoneID, zone, key, deleteGroup)
		if deleteErr != nil {
			return nil, deleteErr
		}
		deletedRecords = append(deletedRecords, deleted...)
	}

	return deletedRecords, nil
}

// groupRecordsByKey groups records by their name and type.
func (p *Provider) groupRecordsByKey(records []libdns.Record) map[recordSetKey][]libdns.Record {
	grouped := make(map[recordSetKey][]libdns.Record)
	for _, record := range records {
		rr := record.RR()
		key := recordSetKey{
			name:       rr.Name,
			recordType: rr.Type,
		}
		grouped[key] = append(grouped[key], record)
	}
	return grouped
}

// processRecordSetDeletion handles the deletion of records from a single
// ResourceRecordSet. The existing values are read inside the per-tuple lock
// so concurrent callers cannot race the read-modify-write cycle.
func (p *Provider) processRecordSetDeletion(
	ctx context.Context,
	zoneID, zone string,
	key recordSetKey,
	deleteGroup []libdns.Record,
) ([]libdns.Record, error) {
	unlock := p.lockSet(setLockKey{zoneID: zoneID, name: key.name, recordType: key.recordType})
	defer unlock()

	// fetch current state of this record set under the lock. getRecords
	// returns relative names, matching key.name's format.
	existingRecords, err := p.getRecords(ctx, zoneID, zone)
	if err != nil {
		return nil, err
	}
	var existingValues []libdns.Record
	for _, existing := range existingRecords {
		existingRR := existing.RR()
		if existingRR.Name == key.name && existingRR.Type == key.recordType {
			existingValues = append(existingValues, existing)
		}
	}
	if len(existingValues) == 0 {
		return nil, nil
	}

	// build set of values to delete
	deleteValues := make(map[string]bool)
	for _, rec := range deleteGroup {
		deleteValues[rec.RR().Data] = true
	}

	// determine which records to keep and which to delete
	var remainingValues, deletedRecords []libdns.Record
	for _, existing := range existingValues {
		if deleteValues[existing.RR().Data] {
			deletedRecords = append(deletedRecords, existing)
		} else {
			remainingValues = append(remainingValues, existing)
		}
	}

	// apply the appropriate operation
	if len(remainingValues) == 0 {
		// delete the entire record set
		if delErr := p.deleteRecordSet(ctx, zoneID, zone, key.name, key.recordType, existingValues); delErr != nil {
			return nil, delErr
		}
	} else {
		// update the record set with remaining values
		if setErr := p.setRecordSet(ctx, zoneID, zone, key.name, key.recordType, remainingValues); setErr != nil {
			return nil, setErr
		}
	}

	return deletedRecords, nil
}

// SetRecords sets the records in the zone. For each (name, type) tuple
// represented in the input, the corresponding ResourceRecordSet is replaced
// with exactly the values provided — other tuples in the zone are not
// touched. It returns the records that were set.
//
// Multiple input records sharing the same (name, type) are combined into a
// single UPSERT carrying all their values, matching libdns semantics.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.init(ctx)

	zoneID, err := p.getZoneID(ctx, zone)
	if err != nil {
		return nil, err
	}

	// group by (name, type) so that values sharing a tuple end up in one
	// UPSERT — otherwise a per-record loop would last-write-wins each one.
	grouped := p.groupRecordsByKey(records)

	var updatedRecords []libdns.Record
	for key, group := range grouped {
		if setErr := p.setRecordSetLocked(ctx, zoneID, zone, key, group); setErr != nil {
			return nil, setErr
		}
		updatedRecords = append(updatedRecords, group...)
	}

	return updatedRecords, nil
}

// setRecordSetLocked UPSERTs a single ResourceRecordSet under the per-tuple
// lock, isolating concurrent SetRecords callers on the same (name, type).
func (p *Provider) setRecordSetLocked(
	ctx context.Context,
	zoneID, zone string,
	key recordSetKey,
	group []libdns.Record,
) error {
	unlock := p.lockSet(setLockKey{zoneID: zoneID, name: key.name, recordType: key.recordType})
	defer unlock()
	return p.setRecordSet(ctx, zoneID, zone, key.name, key.recordType, group)
}

// Interface guards.
var (
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
)
