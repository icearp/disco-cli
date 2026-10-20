package aws

import (
	"context"
	"fmt"

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
}

// scanRedshiftServerless discovers Redshift Serverless namespaces, workgroups
// and their satellites. The namespace and workgroup phases hand their
// name→ARN lists on to the children keyed by those names; the newer phases
// run after every established one so their errors cannot cost those rows.
func scanRedshiftServerless(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := redshiftserverless.NewFromConfig(acct.cfg, func(o *redshiftserverless.Options) { o.Region = region })
	return scanRSSPhases(ctx, client, acct, region, st, scanID)
}

func scanRSSPhases(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var namespaces, workgroups []childParent
	for _, phase := range []func() (int, int, error){
		func() (t, i int, err error) {
			t, i, namespaces, err = scanRSSNamespaces(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanRSSSnapshots(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			t, i, workgroups, err = scanRSSWorkgroups(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanRSSEndpointAccess(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRSSRecoveryPoints(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRSSManagedWorkgroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRSSReservations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanRSSSnapshotCopyConfigurations(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanRSSUsageLimits(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanRSSScheduledActions(ctx, client, acct, region, st, scanID, namespaces)
		},
		func() (int, int, error) {
			return scanRSSCustomDomainAssociations(ctx, client, acct, region, st, scanID, workgroups)
		},
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanRSSNamespaces(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	pager := redshiftserverless.NewListNamespacesPaginator(client, &redshiftserverless.ListNamespacesInput{})
	var (
		batch   []*store.Resource
		parents []childParent
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "redshiftserverless:ListNamespaces", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("redshiftserverless:ListNamespaces: %w", err)
		}
		for _, n := range out.Namespaces {
			arn := sv(n.NamespaceArn)
			if arn == "" {
				continue
			}
			// A nameless parent would key "" and adopt every child whose parent
			// name is missing.
			if name := sv(n.NamespaceName); name != "" {
				parents = append(parents, childParent{id: name, arn: arn})
			}
			status := string(n.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessNamespace, NativeID: arn,
				Name: n.NamespaceName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "redshiftserverless namespaces")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, parents, nil
}

func scanRSSSnapshots(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListSnapshotsPaginator(client, &redshiftserverless.ListSnapshotsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "redshiftserverless:ListSnapshots", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("redshiftserverless:ListSnapshots: %w", err)
		}
		for _, s := range out.Snapshots {
			arn := sv(s.SnapshotArn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessSnapshot, NativeID: arn,
				Name: s.SnapshotName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshiftserverless snapshots")
}

func scanRSSWorkgroups(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	pager := redshiftserverless.NewListWorkgroupsPaginator(client, &redshiftserverless.ListWorkgroupsInput{})
	var (
		batch   []*store.Resource
		parents []childParent
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "redshiftserverless:ListWorkgroups", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("redshiftserverless:ListWorkgroups: %w", err)
		}
		for _, w := range out.Workgroups {
			arn := sv(w.WorkgroupArn)
			if arn == "" {
				continue
			}
			if name := sv(w.WorkgroupName); name != "" {
				parents = append(parents, childParent{id: name, arn: arn})
			}
			status := string(w.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessWorkgroup, NativeID: arn,
				Name: w.WorkgroupName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "redshiftserverless workgroups")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, parents, nil
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
			nativeID := fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:recovery-point/%s", region, acct.ID, id)
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
				nativeID = fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:managed-workgroup/%s", region, acct.ID, id)
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
