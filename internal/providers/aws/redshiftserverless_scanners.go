package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/redshiftserverless"
	rsstypes "github.com/aws/aws-sdk-go-v2/service/redshiftserverless/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRedshiftServerlessNamespace, Service: "redshift-serverless"})
	registerType(restype.Descriptor{Type: TypeRedshiftServerlessSnapshot, Service: "redshift-serverless"})
	registerType(restype.Descriptor{Type: TypeRedshiftServerlessWorkgroup, Service: "redshift-serverless"})
	registerType(restype.Descriptor{Type: TypeRedshiftServerlessEndpointAccess, Service: "redshift-serverless", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeRedshiftServerlessRecoveryPoint, Service: "redshift-serverless"})
	registerType(restype.Descriptor{Type: TypeRedshiftServerlessManagedWorkgroup, Service: "redshift-serverless", Managed: true, Untaggable: true})
	registerType(restype.Descriptor{Type: TypeRedshiftServerlessCustomDomainAssociation, Service: "redshift-serverless"})
	registerType(restype.Descriptor{Type: TypeRedshiftServerlessReservation, Service: "redshift-serverless"})
	registerType(restype.Descriptor{Type: TypeRedshiftServerlessScheduledAction, Service: "redshift-serverless"})
	registerType(restype.Descriptor{Type: TypeRedshiftServerlessSnapshotCopyConfiguration, Service: "redshift-serverless"})
	registerType(restype.Descriptor{Type: TypeRedshiftServerlessUsageLimit, Service: "redshift-serverless"})
	registerService(serviceEntry{
		name: "aws:redshift-serverless",
		fn:   scanRedshiftServerless,
	})
}

type redshiftServerlessAPI interface {
	ListNamespaces(context.Context, *redshiftserverless.ListNamespacesInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListNamespacesOutput, error)
	ListSnapshots(context.Context, *redshiftserverless.ListSnapshotsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListSnapshotsOutput, error)
	ListWorkgroups(context.Context, *redshiftserverless.ListWorkgroupsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListWorkgroupsOutput, error)
	ListEndpointAccess(context.Context, *redshiftserverless.ListEndpointAccessInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListEndpointAccessOutput, error)
	ListRecoveryPoints(context.Context, *redshiftserverless.ListRecoveryPointsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListRecoveryPointsOutput, error)
	ListManagedWorkgroups(context.Context, *redshiftserverless.ListManagedWorkgroupsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListManagedWorkgroupsOutput, error)
	ListCustomDomainAssociations(context.Context, *redshiftserverless.ListCustomDomainAssociationsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListCustomDomainAssociationsOutput, error)
	ListReservations(context.Context, *redshiftserverless.ListReservationsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListReservationsOutput, error)
	ListScheduledActions(context.Context, *redshiftserverless.ListScheduledActionsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListScheduledActionsOutput, error)
	ListSnapshotCopyConfigurations(context.Context, *redshiftserverless.ListSnapshotCopyConfigurationsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListSnapshotCopyConfigurationsOutput, error)
	ListUsageLimits(context.Context, *redshiftserverless.ListUsageLimitsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListUsageLimitsOutput, error)
	ListTagsForResource(context.Context, *redshiftserverless.ListTagsForResourceInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListTagsForResourceOutput, error)
}

const (
	rssTagsOp = "redshiftserverless:ListTagsForResource"
	// rssPageCap is the maxResults cap (1-100) the model sets on the tagged
	// listings.
	rssPageCap = 100
)

// scanRedshiftServerless discovers Redshift Serverless namespaces, workgroups
// and their satellites.
func scanRedshiftServerless(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := redshiftserverless.NewFromConfig(acct.cfg, func(o *redshiftserverless.Options) { o.Region = region })
	return scanRSSPhases(ctx, client, acct, region, st, scanID)
}

// scanRSSPhases runs every listing as its own phase, one at a time, in the
// order below: a phase's error does not stop the later ones; a failed write
// and ctx's end do (runHostPhases). The namespace and workgroup phases hand
// the parents they stored, by name, to the scheduled-action and
// custom-domain phases. The namespace, snapshot and workgroup listings write
// a page at a time with each row's tags read (rssTags); the tag denials of
// the whole scan are one warning.
//
// Recovery points are not tag-read, although the Service Reference lists
// them for TagResource: ListTagsForResource takes the resource's ARN, and a
// listed RecoveryPoint carries no ARN member (only its id and its
// namespace's ARN). Their NativeID is a built ARN whose resource segment,
// "recovery-point/", is not the Service Reference's "recoverypoint/", so it
// is no ARN to send either. Snapshots differ in exactly this: each listed
// snapshot carries its SnapshotArn.
func scanRSSPhases(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := rssTags(client).sharingDenials()
	var namespaces, workgroups []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "redshift-serverless", 0, []hostPhase{
		{op: "redshiftserverless:ListNamespaces", run: func(p *phaseTotals) (failed bool) {
			namespaces, failed = scanRSSNamespaces(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "redshiftserverless:ListSnapshots", run: func(p *phaseTotals) bool {
			return scanRSSSnapshots(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "redshiftserverless:ListWorkgroups", run: func(p *phaseTotals) (failed bool) {
			workgroups, failed = scanRSSWorkgroups(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, "redshiftserverless:ListEndpointAccess", func() (int, int, error) {
			return scanRSSEndpointAccess(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "redshiftserverless:ListRecoveryPoints", func() (int, int, error) {
			return scanRSSRecoveryPoints(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "redshiftserverless:ListManagedWorkgroups", func() (int, int, error) {
			return scanRSSManagedWorkgroups(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "redshiftserverless:ListReservations", func() (int, int, error) {
			return scanRSSReservations(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "redshiftserverless:ListSnapshotCopyConfigurations", func() (int, int, error) {
			return scanRSSSnapshotCopyConfigurations(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "redshiftserverless:ListUsageLimits", func() (int, int, error) {
			return scanRSSUsageLimits(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "redshiftserverless:ListScheduledActions", func() (int, int, error) {
			return scanRSSScheduledActions(ctx, client, acct, region, st, scanID, namespaces)
		}),
		countedPhase(0, "redshiftserverless:ListCustomDomainAssociations", func() (int, int, error) {
			return scanRSSCustomDomainAssociations(ctx, client, acct, region, st, scanID, workgroups)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// rssTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The op returns every tag in one
// response (it takes no page token). ResourceNotFoundException is the one
// not-found code the op models.
func rssTags(client redshiftServerlessAPI) rowTagReader {
	return rowTagReader{
		op:       rssTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &redshiftserverless.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t rsstypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanRSSNamespaces stores the region's namespaces and returns those stored
// with a name, the key the scheduled actions name their namespace by.
func scanRSSNamespaces(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "redshiftserverless:ListNamespaces", "namespaces", "NamespaceName",
		func(ctx context.Context, token *string) ([]rsstypes.Namespace, *string, error) {
			out, err := client.ListNamespaces(ctx, &redshiftserverless.ListNamespacesInput{NextToken: token, MaxResults: sdkaws.Int32(rssPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Namespaces, out.NextToken, nil
		},
		func(n rsstypes.Namespace) (*store.Resource, string, string) {
			status := string(n.Status)
			return arnRow("namespaces", n.NamespaceArn, n.NamespaceName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessNamespace, NativeID: sv(n.NamespaceArn),
				Name: n.NamespaceName, Region: &region, Status: &status, CreatedAt: tp(n.CreationDate),
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		},
		func(n rsstypes.Namespace) string { return sv(n.NamespaceName) },
		tags, p)
}

// scanRSSSnapshots stores the region's snapshots. ListSnapshots takes an
// ownerAccount filter, a snapshot names its OwnerAccount, and GetSnapshot's
// ownerAccount names a snapshot shared from another account, so the listing
// may return a snapshot another account owns. Its tag read may then be
// refused or reported not found while the snapshot exists, so such a row is
// kept untagged (untaggedIfOtherAccount on the ARN's account). Whether
// ListSnapshots returns other accounts' snapshots when no filter is sent, and
// how ListTagsForResource answers for them, is unverified.
func scanRSSSnapshots(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "redshiftserverless:ListSnapshots",
		func(ctx context.Context, token *string) ([]rsstypes.Snapshot, *string, error) {
			out, err := client.ListSnapshots(ctx, &redshiftserverless.ListSnapshotsInput{NextToken: token, MaxResults: sdkaws.Int32(rssPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Snapshots, out.NextToken, nil
		},
		func(s rsstypes.Snapshot) (*store.Resource, string, string) {
			status := string(s.Status)
			return arnRow("snapshots", s.SnapshotArn, s.SnapshotName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessSnapshot, NativeID: sv(s.SnapshotArn),
				Name: s.SnapshotName, Region: &region, Status: &status, CreatedAt: tp(s.SnapshotCreateTime),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}, tags.untaggedIfOtherAccount(acct.ID), p)
}

// scanRSSWorkgroups stores the region's workgroups and returns those stored
// with a name, the key the custom domain associations name their workgroup
// by. ListWorkgroups takes an ownerAccount filter, which the model ties to
// cross-account endpoint access (CreateEndpointAccess and ListEndpointAccess
// take the same ownerAccount), so the listing may return a workgroup another
// account owns. Such a row's tag read is handled as scanRSSSnapshots handles
// another account's snapshot. Whether ListWorkgroups returns other accounts'
// workgroups when no filter is sent, and how ListTagsForResource answers for
// them, is unverified.
func scanRSSWorkgroups(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "redshiftserverless:ListWorkgroups", "workgroups", "WorkgroupName",
		func(ctx context.Context, token *string) ([]rsstypes.Workgroup, *string, error) {
			out, err := client.ListWorkgroups(ctx, &redshiftserverless.ListWorkgroupsInput{NextToken: token, MaxResults: sdkaws.Int32(rssPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Workgroups, out.NextToken, nil
		},
		func(w rsstypes.Workgroup) (*store.Resource, string, string) {
			status := string(w.Status)
			return arnRow("workgroups", w.WorkgroupArn, w.WorkgroupName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessWorkgroup, NativeID: sv(w.WorkgroupArn),
				Name: w.WorkgroupName, Region: &region, Status: &status, CreatedAt: tp(w.CreationDate),
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		},
		func(w rsstypes.Workgroup) string { return sv(w.WorkgroupName) },
		tags.untaggedIfOtherAccount(acct.ID), p)
}

func scanRSSEndpointAccess(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListEndpointAccessPaginator(client, &redshiftserverless.ListEndpointAccessInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "redshiftserverless:ListEndpointAccess", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("redshiftserverless:ListEndpointAccess: %w", err)
		}
		for _, e := range out.Endpoints {
			arn := sv(e.EndpointArn)
			if arn == "" {
				continue
			}
			status := sv(e.EndpointStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessEndpointAccess, NativeID: arn,
				Name: e.EndpointName, Region: &region, CreatedAt: tp(e.EndpointCreateTime),
				Status: &status, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshiftserverless endpoint access")
}

func scanRSSRecoveryPoints(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListRecoveryPointsPaginator(client, &redshiftserverless.ListRecoveryPointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "redshiftserverless:ListRecoveryPoints", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("redshiftserverless:ListRecoveryPoints: %w", err)
		}
		for _, p := range out.RecoveryPoints {
			id := sv(p.RecoveryPointId)
			nativeID := arnFor(acct.partition(), "redshift-serverless", region, acct.ID, "recovery-point/"+id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessRecoveryPoint, NativeID: nativeID,
				Name: &id, Region: &region, CreatedAt: tp(p.RecoveryPointCreateTime),
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshiftserverless recovery points")
}

// scanRSSManagedWorkgroups discovers AWS-managed (Amazon-operated) workgroups,
// e.g. those backing zero-ETL / SageMaker integrations. edge-less + provider-managed.
func scanRSSManagedWorkgroups(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListManagedWorkgroupsPaginator(client, &redshiftserverless.ListManagedWorkgroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "redshiftserverless:ListManagedWorkgroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("redshiftserverless:ListManagedWorkgroups: %w", err)
		}
		for _, w := range out.ManagedWorkgroups {
			id := sv(w.ManagedWorkgroupId)
			nativeID := sv(w.SourceArn)
			if nativeID == "" {
				nativeID = arnFor(acct.partition(), "redshift-serverless", region, acct.ID, "managed-workgroup/"+id)
			}
			status := string(w.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessManagedWorkgroup, NativeID: nativeID,
				Name: w.ManagedWorkgroupName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshiftserverless managed workgroups")
}

func scanRSSReservations(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListReservationsPaginator(client, &redshiftserverless.ListReservationsInput{})
	return pageScan(ctx, "redshiftserverless:ListReservations", acct, region, st, pager.HasMorePages,
		func(ctx context.Context) (*redshiftserverless.ListReservationsOutput, error) {
			return pager.NextPage(ctx)
		},
		func(out *redshiftserverless.ListReservationsOutput) []rsstypes.Reservation {
			return out.ReservationsList
		},
		func(r rsstypes.Reservation) *store.Resource {
			arn := sv(r.ReservationArn)
			if arn == "" {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessReservation, NativeID: arn,
				Name: r.ReservationId, Region: &region, Status: nonEmptyPtr(sv(r.Status)),
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			}
		})
}

func scanRSSSnapshotCopyConfigurations(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListSnapshotCopyConfigurationsPaginator(client, &redshiftserverless.ListSnapshotCopyConfigurationsInput{})
	return pageScan(ctx, "redshiftserverless:ListSnapshotCopyConfigurations", acct, region, st, pager.HasMorePages,
		func(ctx context.Context) (*redshiftserverless.ListSnapshotCopyConfigurationsOutput, error) {
			return pager.NextPage(ctx)
		},
		func(out *redshiftserverless.ListSnapshotCopyConfigurationsOutput) []rsstypes.SnapshotCopyConfiguration {
			return out.SnapshotCopyConfigurations
		},
		func(c rsstypes.SnapshotCopyConfiguration) *store.Resource {
			arn := sv(c.SnapshotCopyConfigurationArn)
			if arn == "" {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessSnapshotCopyConfiguration, NativeID: arn,
				Name: c.SnapshotCopyConfigurationId, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}
		})
}

// scanRSSUsageLimits lists the region's usage limits in one call: the API
// documents ResourceArn as an optional filter ("Lists all usage limits"), so
// it needs no per-workgroup/namespace fan-out.
func scanRSSUsageLimits(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListUsageLimitsPaginator(client, &redshiftserverless.ListUsageLimitsInput{})
	return pageScan(ctx, "redshiftserverless:ListUsageLimits", acct, region, st, pager.HasMorePages,
		func(ctx context.Context) (*redshiftserverless.ListUsageLimitsOutput, error) {
			return pager.NextPage(ctx)
		},
		func(out *redshiftserverless.ListUsageLimitsOutput) []rsstypes.UsageLimit { return out.UsageLimits },
		func(u rsstypes.UsageLimit) *store.Resource {
			arn := sv(u.UsageLimitArn)
			if arn == "" {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessUsageLimit, NativeID: arn,
				Name: u.UsageLimitId, Region: &region,
				AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			}
		})
}

// scanRSSScheduledActions lists every scheduled action in one call. An action
// carries no ARN and names its namespace only by name, while the namespace ARN
// is id-based, so it is keyed {namespaceArn}/scheduled-action/{name} using the
// namespaces this scan stored; one whose namespace was not stored cannot be
// keyed and is skipped. With no stored namespaces nothing could be keyed, so
// the call is not made. The row is identity-only: ScheduledActionAssociation
// carries just the two names, and the schedule, target action and role would
// need a GetScheduledAction per action, which is not called.
func scanRSSScheduledActions(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string, namespaces []childParent) (int, int, error) {
	const op = "redshiftserverless:ListScheduledActions"
	if len(namespaces) == 0 {
		return 0, 0, nil
	}
	nsARN := parentARNByName(namespaces)
	var children []containedChild
	pager := redshiftserverless.NewListScheduledActionsPaginator(client, &redshiftserverless.ListScheduledActionsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, op, acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("%s: %w", op, err)
		}
		for _, a := range out.ScheduledActions {
			name := sv(a.ScheduledActionName)
			parent, ok := nsARN[sv(a.NamespaceName)]
			if name == "" || !ok {
				continue
			}
			children = append(children, containedChild{parentID: store.ResourceID("aws", acct.ID, parent), row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessScheduledAction, NativeID: parent + "/scheduled-action/" + name,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}})
		}
	}
	return upsertContained(st, op, children)
}

// scanRSSCustomDomainAssociations lists every custom domain association in
// one call (the API has no workgroup filter). An association carries no ARN,
// so it is keyed {workgroupArn}/custom-domain-association/{domain} using the
// workgroups this scan stored; one whose workgroup was not stored cannot be
// keyed and is skipped. With no stored workgroups nothing could be keyed, so
// the call is not made.
func scanRSSCustomDomainAssociations(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string, workgroups []childParent) (int, int, error) {
	const op = "redshiftserverless:ListCustomDomainAssociations"
	if len(workgroups) == 0 {
		return 0, 0, nil
	}
	wgARN := parentARNByName(workgroups)
	var children []containedChild
	pager := redshiftserverless.NewListCustomDomainAssociationsPaginator(client, &redshiftserverless.ListCustomDomainAssociationsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, op, acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("%s: %w", op, err)
		}
		for _, a := range out.Associations {
			domain := sv(a.CustomDomainName)
			parent, ok := wgARN[sv(a.WorkgroupName)]
			if domain == "" || !ok {
				continue
			}
			children = append(children, containedChild{parentID: store.ResourceID("aws", acct.ID, parent), row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessCustomDomainAssociation, NativeID: parent + "/custom-domain-association/" + domain,
				Name: &domain, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}})
		}
	}
	return upsertContained(st, op, children)
}

func parentARNByName(parents []childParent) map[string]string {
	m := make(map[string]string, len(parents))
	for _, p := range parents {
		m[p.id] = p.arn
	}
	return m
}
