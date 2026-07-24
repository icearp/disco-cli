package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// scanSSMExtended discovers five additional SSM resource types: Association,
// MaintenanceWindow, MaintenanceWindowTarget, MaintenanceWindowTask, and
// ResourceDataSync. Targets and tasks fan out per maintenance window.
//
// AWS::SSM::ResourcePolicy is not enumerable: GetResourcePolicies requires a
// ResourceArn and SSM exposes no list API for resources that have policies
// attached. Skipped — see docs/aws-missing-services.md.
func scanSSMExtended(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t, i, ferr := scanSSMAssociations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	windowIDs, t, i, ferr := scanSSMMaintenanceWindows(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, wid := range windowIDs {
		t, i, ferr = scanSSMMaintenanceWindowTargets(ctx, client, acct, region, st, scanID, wid)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanSSMMaintenanceWindowTasks(ctx, client, acct, region, st, scanID, wid)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	t, i, ferr = scanSSMResourceDataSync(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSSMManagedInstances(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSSMOpsMetadata(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanSSMManagedInstances discovers SSM-managed nodes (DescribeInstanceInformation).
// The API returns no ARN, so the NativeID is synthesized from the instance ID:
// arn:aws:ssm:{region}:{acct}:managed-instance/{InstanceId}.
func scanSSMManagedInstances(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ssm.NewDescribeInstanceInformationPaginator(client, &ssm.DescribeInstanceInformationInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm:DescribeInstanceInformation", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm:DescribeInstanceInformation: %w", err)
		}
		for _, in := range out.InstanceInformationList {
			id := sv(in.InstanceId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ssm:%s:%s:managed-instance/%s", region, acct.ID, id)
			label := id
			if cn := sv(in.ComputerName); cn != "" {
				label = cn
			}
			status := string(in.PingStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMManagedInstance, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(in), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ssm managed-instances")
}

// scanSSMOpsMetadata discovers Application Manager OpsMetadata objects.
func scanSSMOpsMetadata(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ssm.NewListOpsMetadataPaginator(client, &ssm.ListOpsMetadataInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm:ListOpsMetadata", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm:ListOpsMetadata: %w", err)
		}
		for _, o := range out.OpsMetadataList {
			arn := sv(o.OpsMetadataArn)
			if arn == "" {
				continue
			}
			label := sv(o.ResourceId)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMOpsMetadata, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ssm opsmetadata")
}

func scanSSMAssociations(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ssm.NewListAssociationsPaginator(client, &ssm.ListAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm:ListAssociations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm:ListAssociations: %w", err)
		}
		for _, a := range out.Associations {
			id := sv(a.AssociationId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ssm:%s:%s:association/%s", region, acct.ID, id)
			label := sv(a.AssociationName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMAssociation, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ssm associations")
}

func scanSSMMaintenanceWindows(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := ssm.NewDescribeMaintenanceWindowsPaginator(client, &ssm.DescribeMaintenanceWindowsInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "ssm:DescribeMaintenanceWindows", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("ssm:DescribeMaintenanceWindows: %w", err)
		}
		for _, w := range out.WindowIdentities {
			id := sv(w.WindowId)
			if id == "" {
				continue
			}
			ids = append(ids, id)
			arn := fmt.Sprintf("arn:aws:ssm:%s:%s:maintenancewindow/%s", region, acct.ID, id)
			label := sv(w.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMMaintenanceWindow, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "ssm maintenance-windows")
	return ids, t, i, err
}

func scanSSMMaintenanceWindowTargets(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, windowID string) (int, int, error) {
	wid := windowID
	pager := ssm.NewDescribeMaintenanceWindowTargetsPaginator(client, &ssm.DescribeMaintenanceWindowTargetsInput{WindowId: &wid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm:DescribeMaintenanceWindowTargets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm:DescribeMaintenanceWindowTargets: %w", err)
		}
		for _, t := range out.Targets {
			tid := sv(t.WindowTargetId)
			if tid == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ssm:%s:%s:windowtarget/%s/%s", region, acct.ID, wid, tid)
			label := sv(t.Name)
			if label == "" {
				label = tid
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMMaintenanceWindowTarget, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ssm maintenance-window-targets")
}

func scanSSMMaintenanceWindowTasks(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, windowID string) (int, int, error) {
	wid := windowID
	pager := ssm.NewDescribeMaintenanceWindowTasksPaginator(client, &ssm.DescribeMaintenanceWindowTasksInput{WindowId: &wid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm:DescribeMaintenanceWindowTasks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm:DescribeMaintenanceWindowTasks: %w", err)
		}
		for _, t := range out.Tasks {
			tid := sv(t.WindowTaskId)
			if tid == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ssm:%s:%s:windowtask/%s/%s", region, acct.ID, wid, tid)
			label := sv(t.Name)
			if label == "" {
				label = tid
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMMaintenanceWindowTask, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ssm maintenance-window-tasks")
}

func scanSSMResourceDataSync(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ssm.NewListResourceDataSyncPaginator(client, &ssm.ListResourceDataSyncInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm:ListResourceDataSync", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm:ListResourceDataSync: %w", err)
		}
		for _, s := range out.ResourceDataSyncItems {
			name := sv(s.SyncName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ssm:%s:%s:resource-data-sync/%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMResourceDataSync, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ssm resource-data-syncs")
}
