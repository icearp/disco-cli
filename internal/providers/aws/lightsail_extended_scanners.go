package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	lightsailtypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/icearp/disco-cli/store"
)

// lsLabel is a listed resource's display name: its name, or fallback when it
// has none.
func lsLabel(name *string, fallback string) string {
	if n := sv(name); n != "" {
		return n
	}
	return fallback
}

func scanLSAlarms(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetAlarmsOp, "alarms",
		func(ctx context.Context, token *string) (*lightsail.GetAlarmsOutput, error) {
			return client.GetAlarms(ctx, &lightsail.GetAlarmsInput{PageToken: token})
		},
		func(o *lightsail.GetAlarmsOutput) ([]lightsailtypes.Alarm, *string) { return o.Alarms, o.NextPageToken },
		func(e lightsailtypes.Alarm) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailAlarm, sv(e.Arn), lsLabel(e.Name, sv(e.Arn)), e, e.Tags)
		})
	return t, i, err
}

// scanLSBuckets returns the stored buckets that have a name, so the access-key
// phase can fan out (GetBucketAccessKeys requires BucketName).
func scanLSBuckets(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	stored, t, i, err := lsList(ctx, st, acct, region, lsGetBucketsOp, "buckets",
		func(ctx context.Context, token *string) (*lightsail.GetBucketsOutput, error) {
			return client.GetBuckets(ctx, &lightsail.GetBucketsInput{PageToken: token})
		},
		func(o *lightsail.GetBucketsOutput) ([]lightsailtypes.Bucket, *string) {
			return o.Buckets, o.NextPageToken
		},
		func(e lightsailtypes.Bucket) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailBucket, sv(e.Arn), lsLabel(e.Name, sv(e.Arn)), e, e.Tags)
		})
	var buckets []childParent
	for _, b := range stored {
		if name := sv(b.Name); name != "" {
			buckets = append(buckets, childParent{id: name, arn: sv(b.Arn)})
		}
	}
	return buckets, t, i, err
}

// scanLSCertificates stores the GetCertificates summaries; without
// IncludeCertificateDetails each carries its name, ARN, domain name and tags.
func scanLSCertificates(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetCertificatesOp, "certificates",
		func(ctx context.Context, token *string) (*lightsail.GetCertificatesOutput, error) {
			return client.GetCertificates(ctx, &lightsail.GetCertificatesInput{PageToken: token})
		},
		func(o *lightsail.GetCertificatesOutput) ([]lightsailtypes.CertificateSummary, *string) {
			return o.Certificates, o.NextPageToken
		},
		func(e lightsailtypes.CertificateSummary) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailCertificate, sv(e.CertificateArn), lsLabel(e.CertificateName, sv(e.DomainName)), e, e.Tags)
		})
	return t, i, err
}

func scanLSDatabaseSnapshots(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetRelationalDatabaseSnapshotsOp, "database snapshots",
		func(ctx context.Context, token *string) (*lightsail.GetRelationalDatabaseSnapshotsOutput, error) {
			return client.GetRelationalDatabaseSnapshots(ctx, &lightsail.GetRelationalDatabaseSnapshotsInput{PageToken: token})
		},
		func(o *lightsail.GetRelationalDatabaseSnapshotsOutput) ([]lightsailtypes.RelationalDatabaseSnapshot, *string) {
			return o.RelationalDatabaseSnapshots, o.NextPageToken
		},
		func(e lightsailtypes.RelationalDatabaseSnapshot) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailDatabaseSnapshot, sv(e.Arn), lsLabel(e.Name, sv(e.Arn)), e, e.Tags)
		})
	return t, i, err
}

func scanLSDisks(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetDisksOp, "disks",
		func(ctx context.Context, token *string) (*lightsail.GetDisksOutput, error) {
			return client.GetDisks(ctx, &lightsail.GetDisksInput{PageToken: token})
		},
		func(o *lightsail.GetDisksOutput) ([]lightsailtypes.Disk, *string) { return o.Disks, o.NextPageToken },
		func(e lightsailtypes.Disk) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailDisk, sv(e.Arn), lsLabel(e.Name, sv(e.Arn)), e, e.Tags)
		})
	return t, i, err
}

func scanLSDiskSnapshots(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetDiskSnapshotsOp, "disk snapshots",
		func(ctx context.Context, token *string) (*lightsail.GetDiskSnapshotsOutput, error) {
			return client.GetDiskSnapshots(ctx, &lightsail.GetDiskSnapshotsInput{PageToken: token})
		},
		func(o *lightsail.GetDiskSnapshotsOutput) ([]lightsailtypes.DiskSnapshot, *string) {
			return o.DiskSnapshots, o.NextPageToken
		},
		func(e lightsailtypes.DiskSnapshot) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailDiskSnapshot, sv(e.Arn), lsLabel(e.Name, sv(e.Arn)), e, e.Tags)
		})
	return t, i, err
}

// scanLSDistributions: Lightsail distribution APIs are us-east-1-only — AWS
// rejects calls from other regions with `InvalidInputException:
// Distribution-related APIs are only available in the us-east-1 Region`.
// Gated per-phase rather than per-region clienting, since the API is
// genuinely global, just exposed solely on us-east-1.
func scanLSDistributions(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if region != "us-east-1" {
		return 0, 0, nil
	}
	_, t, i, err := lsList(ctx, st, acct, region, lsGetDistributionsOp, "distributions",
		func(ctx context.Context, token *string) (*lightsail.GetDistributionsOutput, error) {
			return client.GetDistributions(ctx, &lightsail.GetDistributionsInput{PageToken: token})
		},
		func(o *lightsail.GetDistributionsOutput) ([]lightsailtypes.LightsailDistribution, *string) {
			return o.Distributions, o.NextPageToken
		},
		func(e lightsailtypes.LightsailDistribution) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailDistribution, sv(e.Arn), lsLabel(e.Name, sv(e.Arn)), e, e.Tags)
		})
	return t, i, err
}

// scanLSDomains: Lightsail domain APIs are us-east-1-only — same gate as
// scanLSDistributions; AWS rejects other regions with `InvalidInputException:
// Domain-related APIs are only available in the us-east-1 Region`.
func scanLSDomains(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if region != "us-east-1" {
		return 0, 0, nil
	}
	_, t, i, err := lsList(ctx, st, acct, region, lsGetDomainsOp, "domains",
		func(ctx context.Context, token *string) (*lightsail.GetDomainsOutput, error) {
			return client.GetDomains(ctx, &lightsail.GetDomainsInput{PageToken: token})
		},
		func(o *lightsail.GetDomainsOutput) ([]lightsailtypes.Domain, *string) {
			return o.Domains, o.NextPageToken
		},
		func(e lightsailtypes.Domain) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailDomain, sv(e.Arn), lsLabel(e.Name, sv(e.Arn)), e, e.Tags)
		})
	return t, i, err
}

func scanLSInstanceSnapshots(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetInstanceSnapshotsOp, "instance snapshots",
		func(ctx context.Context, token *string) (*lightsail.GetInstanceSnapshotsOutput, error) {
			return client.GetInstanceSnapshots(ctx, &lightsail.GetInstanceSnapshotsInput{PageToken: token})
		},
		func(o *lightsail.GetInstanceSnapshotsOutput) ([]lightsailtypes.InstanceSnapshot, *string) {
			return o.InstanceSnapshots, o.NextPageToken
		},
		func(e lightsailtypes.InstanceSnapshot) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailInstanceSnapshot, sv(e.Arn), lsLabel(e.Name, sv(e.Arn)), e, e.Tags)
		})
	return t, i, err
}

// scanLSLoadBalancers returns the names of the stored load balancers, so the
// TLS-certificate phase can fan out (GetLoadBalancerTlsCertificates requires
// LoadBalancerName).
func scanLSLoadBalancers(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	stored, t, i, err := lsList(ctx, st, acct, region, lsGetLoadBalancersOp, "load balancers",
		func(ctx context.Context, token *string) (*lightsail.GetLoadBalancersOutput, error) {
			return client.GetLoadBalancers(ctx, &lightsail.GetLoadBalancersInput{PageToken: token})
		},
		func(o *lightsail.GetLoadBalancersOutput) ([]lightsailtypes.LoadBalancer, *string) {
			return o.LoadBalancers, o.NextPageToken
		},
		func(e lightsailtypes.LoadBalancer) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailLoadBalancer, sv(e.Arn), lsLabel(e.Name, sv(e.Arn)), e, e.Tags)
		})
	var names []string
	for _, lb := range stored {
		if name := sv(lb.Name); name != "" {
			names = append(names, name)
		}
	}
	return names, t, i, err
}

// scanLSLBTlsCertificates lists each stored load balancer's TLS certificates
// (GetLoadBalancerTlsCertificates is not paginated). A load balancer deleted
// since GetLoadBalancers (NotFoundException, which the op models) is skipped;
// denials warn once; any other failure keeps the other load balancers'
// certificates and is returned after they are stored.
func scanLSLBTlsCertificates(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string, lbNames []string) (int, int, error) {
	var (
		batch  []*store.Resource
		denied skipTally
		noARN  skipTally
		errs   []error
	)
	for _, lb := range lbNames {
		out, err := client.GetLoadBalancerTlsCertificates(ctx, &lightsail.GetLoadBalancerTlsCertificatesInput{LoadBalancerName: &lb})
		switch {
		case err == nil:
		case isAPIErrorCode(err, "NotFoundException"):
			continue
		case isAccessDenied(err):
			denied.skipped(lb, err)
			continue
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", lsGetLoadBalancerTLSCertsOp, lb, err))
			continue
		}
		for _, c := range out.TlsCertificates {
			arn := sv(c.Arn)
			label := lsLabel(c.Name, sv(c.DomainName))
			if arn == "" {
				noARN.skipped(lb+"/"+label, nil)
				continue
			}
			batch = append(batch, lsRow(acct, region, scanID, TypeLightsailLoadBalancerTLSCertificate, arn, label, c, c.Tags))
		}
	}
	warnErr := lsWarnDenied(st, lsGetLoadBalancerTLSCertsOp, acct, region, &denied, "load balancers")
	warnNotStored(st, lsGetLoadBalancerTLSCertsOp, acct, region, &noARN, "TLS certificates have no ARN")
	t, i, uerr := upsertBatch(st, batch, "lightsail load-balancer-tls-certificates")
	return t, i, errors.Join(append(errs, warnErr, uerr)...)
}

func scanLSKeyPairs(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetKeyPairsOp, "key pairs",
		func(ctx context.Context, token *string) (*lightsail.GetKeyPairsOutput, error) {
			return client.GetKeyPairs(ctx, &lightsail.GetKeyPairsInput{PageToken: token})
		},
		func(o *lightsail.GetKeyPairsOutput) ([]lightsailtypes.KeyPair, *string) {
			return o.KeyPairs, o.NextPageToken
		},
		func(e lightsailtypes.KeyPair) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailKeyPair, sv(e.Arn), lsLabel(e.Name, sv(e.Arn)), e, e.Tags)
		})
	return t, i, err
}

// scanLSContactMethods discovers the account's alarm-notification contact
// methods. GetContactMethods is not paginated.
func scanLSContactMethods(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetContactMethodsOp, "contact methods",
		func(ctx context.Context, _ *string) (*lightsail.GetContactMethodsOutput, error) {
			return client.GetContactMethods(ctx, &lightsail.GetContactMethodsInput{})
		},
		func(o *lightsail.GetContactMethodsOutput) ([]lightsailtypes.ContactMethod, *string) {
			return o.ContactMethods, nil
		},
		func(e lightsailtypes.ContactMethod) *store.Resource {
			r := lsRow(acct, region, scanID, TypeLightsailContactMethod, sv(e.Arn), lsLabel(e.Name, sv(e.ContactEndpoint)), e, e.Tags)
			r.Status = nonEmptyPtr(string(e.Status))
			return r
		})
	return t, i, err
}

// scanLSStaticIps: the StaticIp element carries no tags, and no Lightsail read
// op returns a static IP's tags, so its rows have none.
func scanLSStaticIps(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, t, i, err := lsList(ctx, st, acct, region, lsGetStaticIpsOp, "static IPs",
		func(ctx context.Context, token *string) (*lightsail.GetStaticIpsOutput, error) {
			return client.GetStaticIps(ctx, &lightsail.GetStaticIpsInput{PageToken: token})
		},
		func(o *lightsail.GetStaticIpsOutput) ([]lightsailtypes.StaticIp, *string) {
			return o.StaticIps, o.NextPageToken
		},
		func(e lightsailtypes.StaticIp) *store.Resource {
			return lsRow(acct, region, scanID, TypeLightsailStaticIP, sv(e.Arn), lsLabel(e.Name, sv(e.IpAddress)), e, nil)
		})
	return t, i, err
}

// scanLSBucketAccessKeys lists each scanned bucket's access keys, fanoutMed
// buckets at a time, and stores them under their bucket. A key has no ARN, so
// its NativeID is {bucketARN}/access-key/{AccessKeyId}; a key listed without
// one is warned about and not stored. A bucket deleted since GetBuckets
// (NotFoundException, which the op models) is skipped; denials warn once; any
// other failure keeps the other buckets' keys and is returned after they are
// stored. childFanOut is not used because it drops every bucket's keys on one
// bucket's failure.
func scanLSBucketAccessKeys(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string, buckets []childParent) (int, int, error) {
	var (
		mu           sync.Mutex
		children     []containedChild
		errs         []error
		denied, noID skipTally
		wg           sync.WaitGroup
	)
	sem := make(chan struct{}, fanoutMed)
	for _, b := range buckets {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			out, err := client.GetBucketAccessKeys(ctx, &lightsail.GetBucketAccessKeysInput{BucketName: &b.id})
			switch {
			case err == nil:
			case isAPIErrorCode(err, "NotFoundException"):
				return
			case isAccessDenied(err):
				denied.skipped(b.id, err)
				return
			default:
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s %s: %w", lsGetBucketAccessKeysOp, b.id, err))
				mu.Unlock()
				return
			}
			parentID := store.ResourceID("aws", acct.ID, b.arn)
			for _, k := range out.AccessKeys {
				id := sv(k.AccessKeyId)
				if id == "" {
					noID.skipped(b.id, nil)
					continue
				}
				row := &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeLightsailBucketAccessKey, NativeID: b.arn + "/access-key/" + id,
					Name: &id, Region: &region, Status: nonEmptyPtr(string(k.Status)), CreatedAt: tp(k.CreatedAt),
					AttributesJSON: mustJSON(k), DiscoveredBy: scanID,
				}
				mu.Lock()
				children = append(children, containedChild{row: row, parentID: parentID})
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	warnErr := lsWarnDenied(st, lsGetBucketAccessKeysOp, acct, region, &denied, "buckets")
	warnNotStored(st, lsGetBucketAccessKeysOp, acct, region, &noID, "access keys have no AccessKeyId (first names their bucket)")
	t, i, uerr := upsertContained(st, lsGetBucketAccessKeysOp, children)
	return t, i, errors.Join(append(errs, warnErr, uerr)...)
}
