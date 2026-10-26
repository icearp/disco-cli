package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/redshift"
	redshifttypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRedshiftCluster, Service: "redshift", Redact: []redact.Rule{{Path: "MasterUserPassword", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeRedshiftSubnetGroup, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftClusterParameterGroup, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftEndpointAccess, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftEndpointAuthorization, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftEventSubscription, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftIntegration, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftScheduledAction, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftSnapshot, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftSnapshotSchedule, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftSnapshotCopyGrant, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftUsageLimit, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftHsmClientCertificate, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftHsmConfiguration, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftIdcApplication, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftDatashare, Service: "redshift"})
	// The profile content is free-form JDBC/ODBC driver options, which can
	// include a password, secret access key or OAuth client secret. It is one
	// JSON string, so the rule can only redact it whole.
	registerType(restype.Descriptor{Type: TypeRedshiftAuthenticationProfile, Service: "redshift", Redact: []redact.Rule{{Path: "AuthenticationProfileContent", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeRedshiftCustomDomainAssociation, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftQev2IdcApplication, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftPartner, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftReservedNode, Service: "redshift"})
	registerService(serviceEntry{
		name: "aws:redshift",
		fn:   scanRedshift,
	})
}

// redshiftAPI is the narrow set of Redshift operations called by the
// scanRedshift sub-phases.
type redshiftAPI interface {
	DescribeClusters(context.Context, *redshift.DescribeClustersInput, ...func(*redshift.Options)) (*redshift.DescribeClustersOutput, error)
	DescribeClusterSubnetGroups(context.Context, *redshift.DescribeClusterSubnetGroupsInput, ...func(*redshift.Options)) (*redshift.DescribeClusterSubnetGroupsOutput, error)
	DescribeClusterSnapshots(context.Context, *redshift.DescribeClusterSnapshotsInput, ...func(*redshift.Options)) (*redshift.DescribeClusterSnapshotsOutput, error)
	DescribeSnapshotSchedules(context.Context, *redshift.DescribeSnapshotSchedulesInput, ...func(*redshift.Options)) (*redshift.DescribeSnapshotSchedulesOutput, error)
	DescribeSnapshotCopyGrants(context.Context, *redshift.DescribeSnapshotCopyGrantsInput, ...func(*redshift.Options)) (*redshift.DescribeSnapshotCopyGrantsOutput, error)
	DescribeUsageLimits(context.Context, *redshift.DescribeUsageLimitsInput, ...func(*redshift.Options)) (*redshift.DescribeUsageLimitsOutput, error)
	DescribeHsmClientCertificates(context.Context, *redshift.DescribeHsmClientCertificatesInput, ...func(*redshift.Options)) (*redshift.DescribeHsmClientCertificatesOutput, error)
	DescribeHsmConfigurations(context.Context, *redshift.DescribeHsmConfigurationsInput, ...func(*redshift.Options)) (*redshift.DescribeHsmConfigurationsOutput, error)
	DescribeRedshiftIdcApplications(context.Context, *redshift.DescribeRedshiftIdcApplicationsInput, ...func(*redshift.Options)) (*redshift.DescribeRedshiftIdcApplicationsOutput, error)
	DescribeDataShares(context.Context, *redshift.DescribeDataSharesInput, ...func(*redshift.Options)) (*redshift.DescribeDataSharesOutput, error)
}

// scanRedshift discovers Redshift provisioned clusters and their satellites
// in one region, one phase at a time. The cluster phase hands the clusters it
// stored to the per-cluster phases, which run after every other phase so their
// errors cannot cost those rows. A failed phase does not stop the later ones;
// only a failed store write does, since every later write would fail too.
// Redshift Serverless workgroups + namespaces are tracked separately
// (different SDK package).
func scanRedshift(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := redshift.NewFromConfig(acct.cfg, func(o *redshift.Options) { o.Region = region })

	var clusters []childParent
	phases := []struct {
		op  string
		run func() (int, int, error)
	}{
		{"redshift:DescribeClusters", func() (t, i int, err error) {
			t, i, clusters, err = scanRedshiftClusters(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{"redshift:DescribeClusterSubnetGroups", func() (int, int, error) {
			return scanRedshiftClusterSubnetGroups(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeClusterParameterGroups", func() (int, int, error) {
			return scanRedshiftClusterParameterGroups(ctx, client, acct, region, st, scanID)
		}},
		// AWS::Redshift::ClusterSecurityGroup discontinued by AWS — modern
		// Redshift uses VPC SGs already covered via aws:ec2:security-group.
		// DescribeClusterSecurityGroups returns InvalidParameterValue.
		{"redshift:DescribeEndpointAccess", func() (int, int, error) {
			return scanRedshiftEndpointAccess(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeEndpointAuthorization", func() (int, int, error) {
			return scanRedshiftEndpointAuthorization(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeEventSubscriptions", func() (int, int, error) {
			return scanRedshiftEventSubscriptions(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeIntegrations", func() (int, int, error) {
			return scanRedshiftIntegrations(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeScheduledActions", func() (int, int, error) {
			return scanRedshiftScheduledActions(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeClusterSnapshots", func() (int, int, error) {
			return scanRedshiftSnapshots(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeSnapshotSchedules", func() (int, int, error) {
			return scanRedshiftSnapshotSchedules(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeSnapshotCopyGrants", func() (int, int, error) {
			return scanRedshiftSnapshotCopyGrants(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeUsageLimits", func() (int, int, error) {
			return scanRedshiftUsageLimits(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeHsmClientCertificates", func() (int, int, error) {
			return scanRedshiftHsmClientCertificates(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeHsmConfigurations", func() (int, int, error) {
			return scanRedshiftHsmConfigurations(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeRedshiftIdcApplications", func() (int, int, error) {
			return scanRedshiftIdcApplications(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeDataShares", func() (int, int, error) {
			return scanRedshiftDataShares(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeAuthenticationProfiles", func() (int, int, error) {
			return scanRedshiftAuthenticationProfiles(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeReservedNodes", func() (int, int, error) {
			return scanRedshiftReservedNodes(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeQev2IdcApplications", func() (int, int, error) {
			return scanRedshiftQev2IdcApplications(ctx, client, acct, region, st, scanID)
		}},
		{"redshift:DescribeCustomDomainAssociations", func() (int, int, error) {
			return scanRedshiftCustomDomainAssociations(ctx, client, acct, region, st, scanID, clusters)
		}},
		{"redshift:DescribePartners", func() (int, int, error) {
			return scanRedshiftPartners(ctx, client, acct, region, st, scanID, clusters)
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

func redshiftClusterARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:redshift:%s:%s:cluster:%s", region, accountID, name)
}

func redshiftSubnetGroupARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:redshift:%s:%s:subnetgroup:%s", region, accountID, name)
}

// redshiftIDARN is redshiftARN, or "" when id is empty, so that redshiftList
// warns about the element instead of storing it under an ARN with no id.
func redshiftIDARN(partition, region, acct, kind, id string) string {
	if id == "" {
		return ""
	}
	return redshiftARN(partition, region, acct, kind, id)
}

func redshiftTagKV(t redshifttypes.Tag) (key, value *string) { return t.Key, t.Value }

// redshiftList reads every page of a region-wide Redshift list op and stores
// one row per element. get sends the Marker; page returns a page's elements
// and the next Marker. A failed first page is classified by redshiftPageErr
// under codes. Once a page was read, only a denial is tolerated, with a
// warning: any other error, a skip code included, leaves the listing
// incomplete and is returned. Either way the rows already read are stored
// first. An element whose row has no NativeID is not stored and is warned
// about, idField naming the identifier it lacks. A repeated Marker ends the
// listing with errRepeatedNextToken: the SDK paginators would loop on it.
// listed is every row handed to the write, whether or not it succeeded: a
// failed write returns store.ErrStoreWrite, which stops scanRedshift.
func redshiftList[P, E any](
	ctx context.Context, st *store.Store, acct *account, region, op string, codes redshiftListCodes,
	get func(ctx context.Context, marker *string) (P, error),
	page func(P) (elems []E, next *string),
	row func(E) *store.Resource,
	what, idField string,
) (listed []*store.Resource, total, inserted int, err error) {
	var (
		batch   []*store.Resource
		missing skipTally
		marker  *string
		listErr error
	)
	for pageNum := 1; ; pageNum++ {
		out, gerr := get(ctx, marker)
		if gerr != nil {
			listErr = redshiftListPageErr(st, acct, region, op, what, marker != nil, gerr, codes)
			break
		}
		elems, next := page(out)
		for i, e := range elems {
			r := row(e)
			if r.NativeID == "" {
				missing.skipped(fmt.Sprintf("page %d element %d (Name %q)", pageNum, i+1, sv(r.Name)), nil)
				continue
			}
			batch = append(batch, r)
		}
		last, terr := lastPage(marker, next)
		if last {
			if terr != nil {
				listErr = fmt.Errorf("%s: %w", op, terr)
			}
			break
		}
		marker = next
	}
	warnNotStored(st, op, acct, region, &missing, what+" have no "+idField)
	total, inserted, uerr := upsertBatch(st, batch, op)
	return batch, total, inserted, errors.Join(listErr, uerr)
}

// redshiftListPageErr classifies a failed page of redshiftList: a first page
// through redshiftPageErr, a later one as an incomplete listing unless denied.
func redshiftListPageErr(st *store.Store, acct *account, region, op, what string, later bool, err error, codes redshiftListCodes) error {
	switch {
	case !later:
		return redshiftPageErr(st, acct, region, op, err, codes)
	case isAccessDenied(err):
		return skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf("%s on this and later pages not stored: %w", what, err))
	default:
		return fmt.Errorf("%s: %w", op, err)
	}
}

func scanRedshiftSnapshots(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeClusterSnapshots", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeClusterSnapshotsOutput, error) {
			return client.DescribeClusterSnapshots(ctx, &redshift.DescribeClusterSnapshotsInput{Marker: marker})
		},
		func(o *redshift.DescribeClusterSnapshotsOutput) ([]redshifttypes.Snapshot, *string) {
			return o.Snapshots, o.Marker
		},
		func(s redshifttypes.Snapshot) *store.Resource {
			name := sv(s.SnapshotIdentifier)
			nativeID := sv(s.SnapshotArn)
			if nativeID == "" && name != "" {
				nativeID = redshiftARN(acct.partition(), region, acct.ID, "snapshot", sv(s.ClusterIdentifier)+"/"+name)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftSnapshot, NativeID: nativeID, Name: &name,
				Region: &region, CreatedAt: tp(s.SnapshotCreateTime), Status: nonEmptyPtr(sv(s.Status)),
				AttributesJSON: mustJSON(s), TagsJSON: kvTagsJSON(s.Tags, redshiftTagKV), DiscoveredBy: scanID,
			}
		}, "snapshots", "SnapshotArn or SnapshotIdentifier")
	return total, inserted, err
}

func scanRedshiftSnapshotSchedules(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeSnapshotSchedules", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeSnapshotSchedulesOutput, error) {
			return client.DescribeSnapshotSchedules(ctx, &redshift.DescribeSnapshotSchedulesInput{Marker: marker})
		},
		func(o *redshift.DescribeSnapshotSchedulesOutput) ([]redshifttypes.SnapshotSchedule, *string) {
			return o.SnapshotSchedules, o.Marker
		},
		func(s redshifttypes.SnapshotSchedule) *store.Resource {
			name := sv(s.ScheduleIdentifier)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftSnapshotSchedule, NativeID: redshiftIDARN(acct.partition(), region, acct.ID, "snapshotschedule", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(s), TagsJSON: kvTagsJSON(s.Tags, redshiftTagKV),
				DiscoveredBy: scanID,
			}
		}, "snapshot schedules", "ScheduleIdentifier")
	return total, inserted, err
}

func scanRedshiftSnapshotCopyGrants(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeSnapshotCopyGrants", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeSnapshotCopyGrantsOutput, error) {
			return client.DescribeSnapshotCopyGrants(ctx, &redshift.DescribeSnapshotCopyGrantsInput{Marker: marker})
		},
		func(o *redshift.DescribeSnapshotCopyGrantsOutput) ([]redshifttypes.SnapshotCopyGrant, *string) {
			return o.SnapshotCopyGrants, o.Marker
		},
		func(g redshifttypes.SnapshotCopyGrant) *store.Resource {
			name := sv(g.SnapshotCopyGrantName)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftSnapshotCopyGrant, NativeID: redshiftIDARN(acct.partition(), region, acct.ID, "snapshotcopygrant", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(g), TagsJSON: kvTagsJSON(g.Tags, redshiftTagKV),
				DiscoveredBy: scanID,
			}
		}, "snapshot copy grants", "SnapshotCopyGrantName")
	return total, inserted, err
}

func scanRedshiftUsageLimits(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeUsageLimits", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeUsageLimitsOutput, error) {
			return client.DescribeUsageLimits(ctx, &redshift.DescribeUsageLimitsInput{Marker: marker})
		},
		func(o *redshift.DescribeUsageLimitsOutput) ([]redshifttypes.UsageLimit, *string) {
			return o.UsageLimits, o.Marker
		},
		func(u redshifttypes.UsageLimit) *store.Resource {
			name := sv(u.UsageLimitId)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftUsageLimit, NativeID: redshiftIDARN(acct.partition(), region, acct.ID, "usagelimit", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(u), TagsJSON: kvTagsJSON(u.Tags, redshiftTagKV),
				DiscoveredBy: scanID,
			}
		}, "usage limits", "UsageLimitId")
	return total, inserted, err
}

// redshiftHsmCodes: HSM encryption is being retired region-by-region (newer
// regions first); where it's gone the op returns UnsupportedOperation. Region
// gap — silent-skip. Self-heals as the deprecation rolls out.
var redshiftHsmCodes = redshiftListCodes{skip: []string{"UnsupportedOperation"}}

func scanRedshiftHsmClientCertificates(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeHsmClientCertificates", redshiftHsmCodes,
		func(ctx context.Context, marker *string) (*redshift.DescribeHsmClientCertificatesOutput, error) {
			return client.DescribeHsmClientCertificates(ctx, &redshift.DescribeHsmClientCertificatesInput{Marker: marker})
		},
		func(o *redshift.DescribeHsmClientCertificatesOutput) ([]redshifttypes.HsmClientCertificate, *string) {
			return o.HsmClientCertificates, o.Marker
		},
		func(c redshifttypes.HsmClientCertificate) *store.Resource {
			name := sv(c.HsmClientCertificateIdentifier)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftHsmClientCertificate, NativeID: redshiftIDARN(acct.partition(), region, acct.ID, "hsmclientcertificate", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(c), TagsJSON: kvTagsJSON(c.Tags, redshiftTagKV),
				DiscoveredBy: scanID,
			}
		}, "HSM client certificates", "HsmClientCertificateIdentifier")
	return total, inserted, err
}

func scanRedshiftHsmConfigurations(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeHsmConfigurations", redshiftHsmCodes,
		func(ctx context.Context, marker *string) (*redshift.DescribeHsmConfigurationsOutput, error) {
			return client.DescribeHsmConfigurations(ctx, &redshift.DescribeHsmConfigurationsInput{Marker: marker})
		},
		func(o *redshift.DescribeHsmConfigurationsOutput) ([]redshifttypes.HsmConfiguration, *string) {
			return o.HsmConfigurations, o.Marker
		},
		func(c redshifttypes.HsmConfiguration) *store.Resource {
			name := sv(c.HsmConfigurationIdentifier)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftHsmConfiguration, NativeID: redshiftIDARN(acct.partition(), region, acct.ID, "hsmconfiguration", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(c), TagsJSON: kvTagsJSON(c.Tags, redshiftTagKV),
				DiscoveredBy: scanID,
			}
		}, "HSM configurations", "HsmConfigurationIdentifier")
	return total, inserted, err
}

func scanRedshiftIdcApplications(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	codes := redshiftListCodes{
		skip: []string{"RedshiftIdcApplicationNotExists", "UnsupportedOperation"},
		warn: []string{"DependentServiceAccessDenied", "DependentServiceUnavailableFault"},
	}
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeRedshiftIdcApplications", codes,
		func(ctx context.Context, marker *string) (*redshift.DescribeRedshiftIdcApplicationsOutput, error) {
			return client.DescribeRedshiftIdcApplications(ctx, &redshift.DescribeRedshiftIdcApplicationsInput{Marker: marker})
		},
		func(o *redshift.DescribeRedshiftIdcApplicationsOutput) ([]redshifttypes.RedshiftIdcApplication, *string) {
			return o.RedshiftIdcApplications, o.Marker
		},
		func(a redshifttypes.RedshiftIdcApplication) *store.Resource {
			name := sv(a.RedshiftIdcApplicationName)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftIdcApplication, NativeID: sv(a.RedshiftIdcApplicationArn),
				Name: &name, Region: &region, Status: nonEmptyPtr(sv(a.IdcOnboardStatus)),
				AttributesJSON: mustJSON(a), TagsJSON: kvTagsJSON(a.Tags, redshiftTagKV), DiscoveredBy: scanID,
			}
		}, "IdC applications", "RedshiftIdcApplicationArn")
	return total, inserted, err
}

func scanRedshiftDataShares(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeDataShares", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeDataSharesOutput, error) {
			return client.DescribeDataShares(ctx, &redshift.DescribeDataSharesInput{Marker: marker})
		},
		func(o *redshift.DescribeDataSharesOutput) ([]redshifttypes.DataShare, *string) {
			return o.DataShares, o.Marker
		},
		func(d redshifttypes.DataShare) *store.Resource {
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftDatashare, NativeID: sv(d.DataShareArn), Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}
		}, "data shares", "DataShareArn")
	return total, inserted, err
}

// scanRedshiftClusters stores the region's clusters and returns them as the
// parents of the per-cluster phases. If the write fails it returns
// store.ErrStoreWrite, and scanRedshift runs no per-cluster phase.
func scanRedshiftClusters(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	rows, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeClusters", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeClustersOutput, error) {
			return client.DescribeClusters(ctx, &redshift.DescribeClustersInput{Marker: marker})
		},
		func(o *redshift.DescribeClustersOutput) ([]redshifttypes.Cluster, *string) {
			return o.Clusters, o.Marker
		},
		func(c redshifttypes.Cluster) *store.Resource {
			name := sv(c.ClusterIdentifier)
			var arn string
			if name != "" {
				arn = redshiftClusterARN(region, acct.ID, name)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftCluster, NativeID: arn,
				Name: &name, Region: &region, Status: nonEmptyPtr(sv(c.ClusterStatus)),
				AttributesJSON: mustJSON(c), TagsJSON: kvTagsJSON(c.Tags, redshiftTagKV), DiscoveredBy: scanID,
			}
		}, "clusters", "ClusterIdentifier")
	parents := make([]childParent, 0, len(rows))
	for _, r := range rows {
		parents = append(parents, childParent{id: sv(r.Name), arn: r.NativeID})
	}
	return total, inserted, parents, err
}

func scanRedshiftClusterSubnetGroups(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeClusterSubnetGroups", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeClusterSubnetGroupsOutput, error) {
			return client.DescribeClusterSubnetGroups(ctx, &redshift.DescribeClusterSubnetGroupsInput{Marker: marker})
		},
		func(o *redshift.DescribeClusterSubnetGroupsOutput) ([]redshifttypes.ClusterSubnetGroup, *string) {
			return o.ClusterSubnetGroups, o.Marker
		},
		func(g redshifttypes.ClusterSubnetGroup) *store.Resource {
			name := sv(g.ClusterSubnetGroupName)
			var arn string
			if name != "" {
				arn = redshiftSubnetGroupARN(region, acct.ID, name)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftSubnetGroup, NativeID: arn,
				Name: &name, Region: &region, Status: nonEmptyPtr(sv(g.SubnetGroupStatus)),
				AttributesJSON: mustJSON(g), TagsJSON: kvTagsJSON(g.Tags, redshiftTagKV), DiscoveredBy: scanID,
			}
		}, "cluster subnet groups", "ClusterSubnetGroupName")
	return total, inserted, err
}
