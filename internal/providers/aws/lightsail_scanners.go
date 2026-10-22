package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	lightsailtypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeLightsailInstance, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailDatabase, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailContainerService, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailAlarm, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailBucket, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailCertificate, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailDatabaseSnapshot, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailDisk, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailDiskSnapshot, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailDistribution, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailDomain, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailInstanceSnapshot, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailLoadBalancer, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailLoadBalancerTLSCertificate, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailStaticIP, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailKeyPair, Service: "lightsail"})
	registerType(restype.Descriptor{Type: TypeLightsailContactMethod, Service: "lightsail"})
	// GetBucketAccessKeys documents that it never returns the secret (only
	// CreateBucketAccessKey does), but its element is the shared AccessKey shape
	// that carries the field, so the rule holds if the API ever fills it.
	registerType(restype.Descriptor{
		Type: TypeLightsailBucketAccessKey, Service: "lightsail",
		Redact: []redact.Rule{{Path: "SecretAccessKey", Mode: redact.RedactScalar}},
	})
	registerService(serviceEntry{
		name: "aws:lightsail",
		fn:   scanLightsail,
	})
}

// lightsailAPI is the narrow set of Lightsail operations called by the
// scanLightsail sub-phases.
type lightsailAPI interface {
	GetInstances(context.Context, *lightsail.GetInstancesInput, ...func(*lightsail.Options)) (*lightsail.GetInstancesOutput, error)
	GetRelationalDatabases(context.Context, *lightsail.GetRelationalDatabasesInput, ...func(*lightsail.Options)) (*lightsail.GetRelationalDatabasesOutput, error)
	GetContainerServices(context.Context, *lightsail.GetContainerServicesInput, ...func(*lightsail.Options)) (*lightsail.GetContainerServicesOutput, error)
	GetAlarms(context.Context, *lightsail.GetAlarmsInput, ...func(*lightsail.Options)) (*lightsail.GetAlarmsOutput, error)
	GetBuckets(context.Context, *lightsail.GetBucketsInput, ...func(*lightsail.Options)) (*lightsail.GetBucketsOutput, error)
	GetCertificates(context.Context, *lightsail.GetCertificatesInput, ...func(*lightsail.Options)) (*lightsail.GetCertificatesOutput, error)
	GetRelationalDatabaseSnapshots(context.Context, *lightsail.GetRelationalDatabaseSnapshotsInput, ...func(*lightsail.Options)) (*lightsail.GetRelationalDatabaseSnapshotsOutput, error)
	GetDisks(context.Context, *lightsail.GetDisksInput, ...func(*lightsail.Options)) (*lightsail.GetDisksOutput, error)
	GetDiskSnapshots(context.Context, *lightsail.GetDiskSnapshotsInput, ...func(*lightsail.Options)) (*lightsail.GetDiskSnapshotsOutput, error)
	GetDistributions(context.Context, *lightsail.GetDistributionsInput, ...func(*lightsail.Options)) (*lightsail.GetDistributionsOutput, error)
	GetDomains(context.Context, *lightsail.GetDomainsInput, ...func(*lightsail.Options)) (*lightsail.GetDomainsOutput, error)
	GetInstanceSnapshots(context.Context, *lightsail.GetInstanceSnapshotsInput, ...func(*lightsail.Options)) (*lightsail.GetInstanceSnapshotsOutput, error)
	GetLoadBalancers(context.Context, *lightsail.GetLoadBalancersInput, ...func(*lightsail.Options)) (*lightsail.GetLoadBalancersOutput, error)
	GetLoadBalancerTlsCertificates(context.Context, *lightsail.GetLoadBalancerTlsCertificatesInput, ...func(*lightsail.Options)) (*lightsail.GetLoadBalancerTlsCertificatesOutput, error)
	GetStaticIps(context.Context, *lightsail.GetStaticIpsInput, ...func(*lightsail.Options)) (*lightsail.GetStaticIpsOutput, error)
	GetKeyPairs(context.Context, *lightsail.GetKeyPairsInput, ...func(*lightsail.Options)) (*lightsail.GetKeyPairsOutput, error)
	GetContactMethods(context.Context, *lightsail.GetContactMethodsInput, ...func(*lightsail.Options)) (*lightsail.GetContactMethodsOutput, error)
	GetBucketAccessKeys(context.Context, *lightsail.GetBucketAccessKeysInput, ...func(*lightsail.Options)) (*lightsail.GetBucketAccessKeysOutput, error)
}

const (
	lsGetInstancesOp                   = "lightsail:GetInstances"
	lsGetRelationalDatabasesOp         = "lightsail:GetRelationalDatabases"
	lsGetContainerServicesOp           = "lightsail:GetContainerServices"
	lsGetLoadBalancersOp               = "lightsail:GetLoadBalancers"
	lsGetAlarmsOp                      = "lightsail:GetAlarms"
	lsGetBucketsOp                     = "lightsail:GetBuckets"
	lsGetCertificatesOp                = "lightsail:GetCertificates"
	lsGetRelationalDatabaseSnapshotsOp = "lightsail:GetRelationalDatabaseSnapshots"
	lsGetDisksOp                       = "lightsail:GetDisks"
	lsGetDiskSnapshotsOp               = "lightsail:GetDiskSnapshots"
	lsGetDistributionsOp               = "lightsail:GetDistributions"
	lsGetDomainsOp                     = "lightsail:GetDomains"
	lsGetInstanceSnapshotsOp           = "lightsail:GetInstanceSnapshots"
	lsGetLoadBalancerTLSCertsOp        = "lightsail:GetLoadBalancerTlsCertificates"
	lsGetStaticIpsOp                   = "lightsail:GetStaticIps"
	lsGetKeyPairsOp                    = "lightsail:GetKeyPairs"
	lsGetContactMethodsOp              = "lightsail:GetContactMethods"
	lsGetBucketAccessKeysOp            = "lightsail:GetBucketAccessKeys"
)

// scanLightsail discovers every Lightsail type in one region. Phases run in
// order and do not stop at another phase's error, except a failed store write,
// which would fail every later phase too; the errors are joined by phaseErrs.
// The TLS-certificate and access-key phases fan out over the load balancers
// and buckets the earlier phases stored.
func scanLightsail(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := lightsail.NewFromConfig(acct.cfg, func(o *lightsail.Options) { o.Region = region })
	return scanLightsailWith(ctx, client, acct, region, st, scanID)
}

func scanLightsailWith(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		lbNames []string
		buckets []childParent
	)
	phases := []struct {
		op  string
		run func() (int, int, error)
	}{
		{lsGetInstancesOp, func() (int, int, error) { return scanLightsailInstances(ctx, client, acct, region, st, scanID) }},
		{lsGetRelationalDatabasesOp, func() (int, int, error) { return scanLightsailDatabases(ctx, client, acct, region, st, scanID) }},
		{lsGetContainerServicesOp, func() (int, int, error) {
			return scanLightsailContainerServices(ctx, client, acct, region, st, scanID)
		}},
		{lsGetLoadBalancersOp, func() (t, i int, err error) {
			lbNames, t, i, err = scanLSLoadBalancers(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{lsGetAlarmsOp, func() (int, int, error) { return scanLSAlarms(ctx, client, acct, region, st, scanID) }},
		{lsGetBucketsOp, func() (t, i int, err error) {
			buckets, t, i, err = scanLSBuckets(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{lsGetCertificatesOp, func() (int, int, error) { return scanLSCertificates(ctx, client, acct, region, st, scanID) }},
		{lsGetRelationalDatabaseSnapshotsOp, func() (int, int, error) {
			return scanLSDatabaseSnapshots(ctx, client, acct, region, st, scanID)
		}},
		{lsGetDisksOp, func() (int, int, error) { return scanLSDisks(ctx, client, acct, region, st, scanID) }},
		{lsGetDiskSnapshotsOp, func() (int, int, error) { return scanLSDiskSnapshots(ctx, client, acct, region, st, scanID) }},
		{lsGetDistributionsOp, func() (int, int, error) { return scanLSDistributions(ctx, client, acct, region, st, scanID) }},
		{lsGetDomainsOp, func() (int, int, error) { return scanLSDomains(ctx, client, acct, region, st, scanID) }},
		{lsGetInstanceSnapshotsOp, func() (int, int, error) {
			return scanLSInstanceSnapshots(ctx, client, acct, region, st, scanID)
		}},
		{lsGetLoadBalancerTLSCertsOp, func() (int, int, error) {
			return scanLSLBTlsCertificates(ctx, client, acct, region, st, scanID, lbNames)
		}},
		{lsGetStaticIpsOp, func() (int, int, error) { return scanLSStaticIps(ctx, client, acct, region, st, scanID) }},
		{lsGetKeyPairsOp, func() (int, int, error) { return scanLSKeyPairs(ctx, client, acct, region, st, scanID) }},
		{lsGetContactMethodsOp, func() (int, int, error) { return scanLSContactMethods(ctx, client, acct, region, st, scanID) }},
		{lsGetBucketAccessKeysOp, func() (int, int, error) {
			return scanLSBucketAccessKeys(ctx, client, acct, region, st, scanID, buckets)
		}},
	}
	var errs phaseErrs
	for _, p := range phases {
		t, i, perr := p.run()
		total += t
		inserted += i
		errs.add(p.op, perr)
		if errors.Is(perr, store.ErrStoreWrite) {
			break
		}
	}
	return total, inserted, errs.join(st, acct, region)
}

// lsList reads every page of a Lightsail Get* list op (manual pageToken
// pagination; an op without one returns a nil next token) and stores a row per
// element. It returns the elements it stored, so a later phase can fan out over
// them; when the write fails scanLightsailWith runs no later phase.
//
// An element whose row has no NativeID (ARN) is not stored and is warned
// about. A page that fails after earlier pages were read keeps their rows: a
// denial warns and returns nil, any other error is returned after storing.
func lsList[O, E any](
	ctx context.Context, st *store.Store, acct *account, region, op, what string,
	get func(ctx context.Context, token *string) (O, error),
	page func(O) (items []E, next *string),
	row func(E) *store.Resource,
) (stored []E, total, inserted int, err error) {
	var (
		listed  []E
		batch   []*store.Resource
		noARN   skipTally
		token   *string
		listErr error
	)
	for {
		out, gerr := get(ctx, token)
		if gerr != nil {
			listErr = gerr
			if isAccessDenied(gerr) {
				if token != nil {
					gerr = fmt.Errorf("%s on this and later pages not stored: %w", what, gerr)
				}
				listErr = skipIfAccessDenied(st, op, acct.ID, region, gerr)
			}
			break
		}
		items, next := page(out)
		for _, it := range items {
			r := row(it)
			if r.NativeID == "" {
				noARN.skipped(sv(r.Name), nil)
				continue
			}
			listed = append(listed, it)
			batch = append(batch, r)
		}
		last, terr := lastPage(token, next)
		if last {
			listErr = terr
			break
		}
		token = next
	}
	warnNotStored(st, op, acct, region, &noARN, what+" have no ARN")
	total, inserted, err = upsertListed(st, batch, op, listErr)
	return listed, total, inserted, err
}

// lsWarnDenied warns once, scoped to the account and region, for every key of
// a fan-out whose call was denied; what names the keys. It returns
// skipIfAccessDenied's result, which is always nil.
func lsWarnDenied(st *store.Store, op string, acct *account, region string, t *skipTally, what string) error {
	if t.count == 0 {
		return nil
	}
	return skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf("%d %s denied, first %s: %w", t.count, what, t.first, t.err))
}

// lsRow builds the row of a listed Lightsail resource. Every Lightsail type
// that carries tags carries them on its listed element.
func lsRow(acct *account, region, scanID, rtype, arn, name string, element any, tags []lightsailtypes.Tag) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: arn, Name: &name, Region: &region,
		AttributesJSON: mustJSON(element), TagsJSON: lsTagsJSON(tags), DiscoveredBy: scanID,
	}
}

func lsTagsJSON(tags []lightsailtypes.Tag) *string {
	return kvTagsJSON(tags, func(t lightsailtypes.Tag) (*string, *string) { return t.Key, t.Value })
}

func scanLightsailInstances(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetInstancesOp, "instances",
		func(ctx context.Context, token *string) (*lightsail.GetInstancesOutput, error) {
			return client.GetInstances(ctx, &lightsail.GetInstancesInput{PageToken: token})
		},
		func(o *lightsail.GetInstancesOutput) ([]lightsailtypes.Instance, *string) {
			return o.Instances, o.NextPageToken
		},
		func(e lightsailtypes.Instance) *store.Resource {
			r := lsRow(acct, region, scanID, TypeLightsailInstance, sv(e.Arn), sv(e.Name), e, e.Tags)
			if e.State != nil {
				r.Status = nonEmptyPtr(sv(e.State.Name))
			}
			return r
		})
	return t, i, err
}

func scanLightsailDatabases(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetRelationalDatabasesOp, "databases",
		func(ctx context.Context, token *string) (*lightsail.GetRelationalDatabasesOutput, error) {
			return client.GetRelationalDatabases(ctx, &lightsail.GetRelationalDatabasesInput{PageToken: token})
		},
		func(o *lightsail.GetRelationalDatabasesOutput) ([]lightsailtypes.RelationalDatabase, *string) {
			return o.RelationalDatabases, o.NextPageToken
		},
		func(e lightsailtypes.RelationalDatabase) *store.Resource {
			r := lsRow(acct, region, scanID, TypeLightsailDatabase, sv(e.Arn), sv(e.Name), e, e.Tags)
			r.Status = nonEmptyPtr(sv(e.State))
			return r
		})
	return t, i, err
}

// scanLightsailContainerServices: GetContainerServices is not paginated.
func scanLightsailContainerServices(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetContainerServicesOp, "container services",
		func(ctx context.Context, _ *string) (*lightsail.GetContainerServicesOutput, error) {
			return client.GetContainerServices(ctx, &lightsail.GetContainerServicesInput{})
		},
		func(o *lightsail.GetContainerServicesOutput) ([]lightsailtypes.ContainerService, *string) {
			return o.ContainerServices, nil
		},
		func(e lightsailtypes.ContainerService) *store.Resource {
			r := lsRow(acct, region, scanID, TypeLightsailContainerService, sv(e.Arn), sv(e.ContainerServiceName), e, e.Tags)
			r.Status = nonEmptyPtr(string(e.State))
			return r
		})
	return t, i, err
}
