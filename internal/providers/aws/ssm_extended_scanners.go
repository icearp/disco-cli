package aws

import (
	"cmp"
	"context"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/icearp/disco-cli/store"
)

// The SSM resource types beyond parameters, documents and patch baselines;
// scanSSMAll runs each listing as a phase.
//
// AWS::SSM::ResourcePolicy is not enumerable: GetResourcePolicies requires a
// ResourceArn and SSM exposes no list API for resources that have policies
// attached. Skipped; it stays an uncovered row in `disco coverage services`.

// scanSSMActivations discovers hybrid-node activations, their tags from the
// listed element. The activation code is returned only by CreateActivation,
// so the listed element carries no secret. The API returns no ARN and the
// service authorization reference defines no activation ARN format, so the
// NativeID is synthesized from the ActivationId:
// arn:{p}:ssm:{region}:{acct}:activation/{ActivationId}.
func scanSSMActivations(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ssmDescribeActivationsOp,
		func(ctx context.Context, token *string) ([]types.Activation, *string, error) {
			out, err := client.DescribeActivations(ctx, &ssm.DescribeActivationsInput{MaxResults: sdkaws.Int32(ssmPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ActivationList, out.NextToken, nil
		},
		func(a types.Activation) (*store.Resource, string, string) {
			id := sv(a.ActivationId)
			if id == "" {
				return nil, "activations have no id", cmp.Or(sv(a.DefaultInstanceName), noIdentifier)
			}
			status := "active"
			if a.Expired {
				status = "expired"
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMActivation, NativeID: arnFor(acct.partition(), "ssm", region, acct.ID, "activation/"+id),
				Name: &id, Region: &region, Status: &status,
				CreatedAt:      tp(a.CreatedDate),
				AttributesJSON: mustJSON(a), TagsJSON: kvTagsJSON(a.Tags, ssmTagKV),
				DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanSSMCloudConnectors discovers cloud connectors, their tags read by
// connector id. The summary carries no ARN; the NativeID follows the service
// authorization reference format
// arn:{p}:ssm:{region}:{acct}:cloud-connector/{CloudConnectorId}. That
// ListTagsForResource takes the CloudConnector type by that id is unverified
// live: AddTagsToResource documents no id form for it, so an
// InvalidResourceId keeps the row untagged and is an error (ssmTagsOf).
func scanSSMCloudConnectors(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, ssmListCloudConnectorsOp,
		func(ctx context.Context, token *string) ([]types.CloudConnectorSummary, *string, error) {
			out, err := client.ListCloudConnectors(ctx, &ssm.ListCloudConnectorsInput{MaxResults: sdkaws.Int32(ssmCloudConnectorPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.CloudConnectors, out.NextToken, nil
		},
		func(c types.CloudConnectorSummary) (*store.Resource, string, string) {
			id := sv(c.CloudConnectorId)
			if id == "" {
				return nil, "cloud connectors have no id", cmp.Or(sv(c.DisplayName), noIdentifier)
			}
			label := cmp.Or(sv(c.DisplayName), id)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMCloudConnector, NativeID: arnFor(acct.partition(), "ssm", region, acct.ID, "cloud-connector/"+id),
				Name: &label, Region: &region,
				CreatedAt:      tp(c.CreatedAt),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		},
		ssmTagsOf(client, tags, types.ResourceTypeForTaggingCloudConnector, ssmTagID("cloud-connector")), p)
}

// scanSSMManagedInstances discovers SSM-managed nodes (DescribeInstanceInformation).
// The API returns no ARN, so the NativeID is synthesized from the instance ID:
// arn:{p}:ssm:{region}:{acct}:managed-instance/{InstanceId}, the Service
// Reference's managed-instance format. Only on-premises nodes (mi-…) have
// their tags read, by that id: the op's ManagedInstance type covers only
// those (AddTagsToResource). The listing also returns EC2 instances, whose
// tags are their aws:ec2:instance row's; they are stored untagged and their
// ids are never sent, since the op would answer InvalidResourceId, which
// drops a ManagedInstance row as deleted.
func scanSSMManagedInstances(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, ssmDescribeInstanceInformationOp,
		func(ctx context.Context, token *string) ([]types.InstanceInformation, *string, error) {
			out, err := client.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{MaxResults: sdkaws.Int32(ssmPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.InstanceInformationList, out.NextToken, nil
		},
		func(in types.InstanceInformation) (*store.Resource, string, string) {
			id := sv(in.InstanceId)
			if id == "" {
				return nil, "managed nodes have no instance id", cmp.Or(sv(in.ComputerName), noIdentifier)
			}
			label := cmp.Or(sv(in.ComputerName), id)
			status := string(in.PingStatus)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMManagedInstance, NativeID: arnFor(acct.partition(), "ssm", region, acct.ID, "managed-instance/"+id),
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(in), DiscoveredBy: scanID,
			}, "", ""
		},
		ssmOnPremisesTags(client, tags), p)
}

// ssmOnPremisesTags reads the tags of only the rows of on-premises nodes,
// whose instance ids start "mi-"; any other row gets no tags and nothing is
// sent. The gate tests the same id that is sent.
func ssmOnPremisesTags(client ssmAPI, tags rowTagReader) rowTagReader {
	id := ssmTagID("managed-instance")
	tags = ssmTagsOf(client, tags, types.ResourceTypeForTaggingManagedInstance, id)
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		if !strings.HasPrefix(id(r), "mi-") {
			return nil, nil
		}
		return read(ctx, r)
	}
	return tags
}

// scanSSMOpsMetadata discovers Application Manager OpsMetadata objects, their
// tags read by the id AddTagsToResource documents for them: what follows
// "opsmetadata/" in the ARN.
func scanSSMOpsMetadata(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, ssmListOpsMetadataOp,
		func(ctx context.Context, token *string) ([]types.OpsMetadata, *string, error) {
			out, err := client.ListOpsMetadata(ctx, &ssm.ListOpsMetadataInput{MaxResults: sdkaws.Int32(ssmPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.OpsMetadataList, out.NextToken, nil
		},
		func(o types.OpsMetadata) (*store.Resource, string, string) {
			label := sv(o.ResourceId)
			return arnRow("OpsMetadata objects", o.OpsMetadataArn, o.ResourceId, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMOpsMetadata, NativeID: sv(o.OpsMetadataArn),
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		},
		ssmTagsOf(client, tags, types.ResourceTypeForTaggingOpsmetadata, ssmTagID("opsmetadata")), p)
}

// scanSSMAssociations stores the account's State Manager associations, their
// tags read by association id. AddTagsToResource documents no id form for
// associations, so an InvalidResourceId keeps the row untagged and is an
// error (ssmTagsOf).
func scanSSMAssociations(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, ssmListAssociationsOp,
		func(ctx context.Context, token *string) ([]types.Association, *string, error) {
			out, err := client.ListAssociations(ctx, &ssm.ListAssociationsInput{MaxResults: sdkaws.Int32(ssmPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Associations, out.NextToken, nil
		},
		func(a types.Association) (*store.Resource, string, string) {
			id := sv(a.AssociationId)
			if id == "" {
				return nil, "associations have no id", cmp.Or(sv(a.AssociationName), noIdentifier)
			}
			label := cmp.Or(sv(a.AssociationName), id)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMAssociation, NativeID: arnFor(acct.partition(), "ssm", region, acct.ID, "association/"+id),
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		},
		ssmTagsOf(client, tags, types.ResourceTypeForTaggingAssociation, ssmTagID("association")), p)
}

// scanSSMMaintenanceWindows stores the maintenance windows, their tags read
// by window id, and returns those stored, keyed by window id, as the parents
// of the target and task phases, or storeWriteFailed (and none).
func scanSSMMaintenanceWindows(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, ssmDescribeMaintenanceWindowsOp, "maintenance windows", "WindowId",
		func(ctx context.Context, token *string) ([]types.MaintenanceWindowIdentity, *string, error) {
			out, err := client.DescribeMaintenanceWindows(ctx, &ssm.DescribeMaintenanceWindowsInput{MaxResults: sdkaws.Int32(ssmWindowPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WindowIdentities, out.NextToken, nil
		},
		func(w types.MaintenanceWindowIdentity) (*store.Resource, string, string) {
			id := sv(w.WindowId)
			if id == "" {
				return nil, "maintenance windows have no id", cmp.Or(sv(w.Name), noIdentifier)
			}
			label := cmp.Or(sv(w.Name), id)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMMaintenanceWindow, NativeID: ssmMaintenanceWindowARN(acct.partition(), region, acct.ID, id),
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			}, "", ""
		},
		func(w types.MaintenanceWindowIdentity) string { return sv(w.WindowId) },
		ssmTagsOf(client, tags, types.ResourceTypeForTaggingMaintenanceWindow, ssmTagID("maintenancewindow")), p)
}

// scanSSMMaintenanceWindowTargets lists the targets of each window stored
// this scan, the windows read concurrently (storeTokenEachParent).
func scanSSMMaintenanceWindowTargets(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, windows []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, ssmDescribeMaintenanceWindowTargetsOp, ssmWindowNotFound, childParentIDs(windows),
		func(ctx context.Context, wid string, token *string) ([]types.MaintenanceWindowTarget, *string, error) {
			out, err := client.DescribeMaintenanceWindowTargets(ctx, &ssm.DescribeMaintenanceWindowTargetsInput{
				WindowId: &wid, MaxResults: sdkaws.Int32(ssmWindowPageLimit), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Targets, out.NextToken, nil
		},
		func(wid string, t types.MaintenanceWindowTarget) (*store.Resource, string, string) {
			tid := sv(t.WindowTargetId)
			if tid == "" {
				return nil, "maintenance window targets have no id", cmp.Or(sv(t.Name), noIdentifier) + " of window " + wid
			}
			label := cmp.Or(sv(t.Name), tid)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMMaintenanceWindowTarget, NativeID: arnFor(acct.partition(), "ssm", region, acct.ID, "windowtarget/"+wid+"/"+tid),
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanSSMMaintenanceWindowTasks lists the tasks of each window stored this
// scan, the windows read concurrently (storeTokenEachParent).
func scanSSMMaintenanceWindowTasks(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, windows []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, ssmDescribeMaintenanceWindowTasksOp, ssmWindowNotFound, childParentIDs(windows),
		func(ctx context.Context, wid string, token *string) ([]types.MaintenanceWindowTask, *string, error) {
			out, err := client.DescribeMaintenanceWindowTasks(ctx, &ssm.DescribeMaintenanceWindowTasksInput{
				WindowId: &wid, MaxResults: sdkaws.Int32(ssmWindowPageLimit), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Tasks, out.NextToken, nil
		},
		func(wid string, t types.MaintenanceWindowTask) (*store.Resource, string, string) {
			tid := sv(t.WindowTaskId)
			if tid == "" {
				return nil, "maintenance window tasks have no id", cmp.Or(sv(t.Name), noIdentifier) + " of window " + wid
			}
			label := cmp.Or(sv(t.Name), tid)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMMaintenanceWindowTask, NativeID: arnFor(acct.partition(), "ssm", region, acct.ID, "windowtask/"+wid+"/"+tid),
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			}, "", ""
		})
}

func scanSSMResourceDataSync(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ssmListResourceDataSyncOp,
		func(ctx context.Context, token *string) ([]types.ResourceDataSyncItem, *string, error) {
			out, err := client.ListResourceDataSync(ctx, &ssm.ListResourceDataSyncInput{MaxResults: sdkaws.Int32(ssmPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ResourceDataSyncItems, out.NextToken, nil
		},
		func(s types.ResourceDataSyncItem) (*store.Resource, string, string) {
			name := sv(s.SyncName)
			if name == "" {
				return nil, "resource data syncs have no name", noIdentifier
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMResourceDataSync, NativeID: arnFor(acct.partition(), "ssm", region, acct.ID, "resource-data-sync/"+name),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}, "", ""
		})
}
