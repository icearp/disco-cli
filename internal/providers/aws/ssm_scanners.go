package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// ssmParameterARN builds a parameter's NativeID from its name. Names may lack
// a leading "/", and the ARN never doubles it: "/a/b" and "a" become
// parameter/a/b and parameter/a.
func ssmParameterARN(partition, region, acct, name string) string {
	return arnFor(partition, "ssm", region, acct, "parameter/"+strings.TrimPrefix(name, "/"))
}

// ssmMaintenanceWindowARN builds a maintenance window's NativeID from its id;
// the API returns no ARN, and the target and task resolvers rebuild it.
func ssmMaintenanceWindowARN(partition, region, acct, windowID string) string {
	return arnFor(partition, "ssm", region, acct, "maintenancewindow/"+windowID)
}

func init() {
	registerType(restype.Descriptor{Type: TypeSSMDocument, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMParameter, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMPatchBaseline, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMAssociation, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMMaintenanceWindow, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMMaintenanceWindowTarget, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMMaintenanceWindowTask, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMResourceDataSync, Service: "ssm", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeSSMManagedInstance, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMOpsMetadata, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMActivation, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMCloudConnector, Service: "ssm"})
	registerService(serviceEntry{
		name: "aws:ssm",
		fn:   scanSSM,
	})
}

// ssmAPI is the narrow set of SSM operations called by scanSSMAll.
type ssmAPI interface {
	DescribeParameters(context.Context, *ssm.DescribeParametersInput, ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error)
	ListDocuments(context.Context, *ssm.ListDocumentsInput, ...func(*ssm.Options)) (*ssm.ListDocumentsOutput, error)
	DescribeDocument(context.Context, *ssm.DescribeDocumentInput, ...func(*ssm.Options)) (*ssm.DescribeDocumentOutput, error)
	DescribePatchBaselines(context.Context, *ssm.DescribePatchBaselinesInput, ...func(*ssm.Options)) (*ssm.DescribePatchBaselinesOutput, error)
	ListAssociations(context.Context, *ssm.ListAssociationsInput, ...func(*ssm.Options)) (*ssm.ListAssociationsOutput, error)
	DescribeMaintenanceWindows(context.Context, *ssm.DescribeMaintenanceWindowsInput, ...func(*ssm.Options)) (*ssm.DescribeMaintenanceWindowsOutput, error)
	DescribeMaintenanceWindowTargets(context.Context, *ssm.DescribeMaintenanceWindowTargetsInput, ...func(*ssm.Options)) (*ssm.DescribeMaintenanceWindowTargetsOutput, error)
	DescribeMaintenanceWindowTasks(context.Context, *ssm.DescribeMaintenanceWindowTasksInput, ...func(*ssm.Options)) (*ssm.DescribeMaintenanceWindowTasksOutput, error)
	ListResourceDataSync(context.Context, *ssm.ListResourceDataSyncInput, ...func(*ssm.Options)) (*ssm.ListResourceDataSyncOutput, error)
	DescribeInstanceInformation(context.Context, *ssm.DescribeInstanceInformationInput, ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error)
	ListOpsMetadata(context.Context, *ssm.ListOpsMetadataInput, ...func(*ssm.Options)) (*ssm.ListOpsMetadataOutput, error)
	DescribeActivations(context.Context, *ssm.DescribeActivationsInput, ...func(*ssm.Options)) (*ssm.DescribeActivationsOutput, error)
	ListCloudConnectors(context.Context, *ssm.ListCloudConnectorsInput, ...func(*ssm.Options)) (*ssm.ListCloudConnectorsOutput, error)
	DescribeDocumentPermission(context.Context, *ssm.DescribeDocumentPermissionInput, ...func(*ssm.Options)) (*ssm.DescribeDocumentPermissionOutput, error)
}

// scanSSM discovers SSM parameters (metadata only, never values), customer-
// owned SSM documents, and customer-owned patch baselines, then the rest of
// SSM, then the sharing permission of every document it stored. The earlier
// phases are sequential, not independent: after an error in scanSSMAll,
// scanSSMExtended has not run. The permission phase runs whatever the earlier
// phases returned, so its failure costs them nothing. The earlier phases'
// transient error is demoted to a warning only beside a hard error: joined, it
// would be buried in the hard error instead of reported under its op; the
// counts of every row stored are summed.
//
// Values are never fetched: even with the store-level secret scrubber,
// plaintext would transit memory and risk landing in debug dumps. The KMS
// edge for SecureString parameters is recoverable from metadata alone.
func scanSSM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := ssm.NewFromConfig(acct.cfg, func(o *ssm.Options) { o.Region = region })
	total, inserted, docs, err := scanSSMPhases(ctx, client, acct, region, st, scanID)
	t, i, perr := scanSSMDocumentPermissions(ctx, client, acct, region, st, scanID, docs)
	// The permission phase needs no demotion: childFanOutCollectSkipCodes
	// already drops its transient per-document errors.
	return total + t, inserted + i, demoteBeside(st, "ssm", acct, region, err, perr)
}

// scanSSMPhases runs scanSSMAll then scanSSMExtended, stopping at the first
// error, and returns the counts of the rows stored before it and the
// documents scanSSMAll stored.
func scanSSMPhases(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	total, inserted, docs, err := scanSSMAll(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, docs, err
	}
	t, i, err := scanSSMExtended(ctx, client, acct, region, st, scanID)
	return total + t, inserted + i, docs, err
}

// scanSSMAll holds the testable scan body. It returns the documents it
// stored, also when a later phase fails. Every one is owned by the account:
// the listing filters on Owner=Self.
func scanSSMAll(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, docs []childParent, err error) {
	// Parameters (metadata only).
	var paramBatch []*store.Resource
	paramPager := ssm.NewDescribeParametersPaginator(client, &ssm.DescribeParametersInput{})
	for paramPager.HasMorePages() {
		page, err := paramPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, nil, skipIfAccessDenied(st, "ssm:DescribeParameters", acct.ID, region, err)
			}
			return total, inserted, nil, fmt.Errorf("ssm:DescribeParameters: %w", err)
		}
		for _, p := range page.Parameters {
			name := sv(p.Name)
			if name == "" {
				continue
			}
			arn := ssmParameterARN(acct.partition(), region, acct.ID, name)
			ptype := string(p.Type)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSSMParameter,
				NativeID:       arn,
				Name:           p.Name,
				Region:         &region,
				Status:         &ptype,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			}
			paramBatch = append(paramBatch, r)
		}
	}
	if len(paramBatch) > 0 {
		n, err := st.UpsertResources(paramBatch)
		if err != nil {
			return total, inserted, nil, fmt.Errorf("upsert SSM parameters: %w", err)
		}
		total += len(paramBatch)
		inserted += n
	}

	// Documents — Owner=Self to avoid tens of thousands of AWS-owned docs.
	var docBatch []*store.Resource
	docPager := ssm.NewListDocumentsPaginator(client, &ssm.ListDocumentsInput{
		Filters: []types.DocumentKeyValuesFilter{{Key: sp("Owner"), Values: []string{"Self"}}},
	})
	for docPager.HasMorePages() {
		page, err := docPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				break
			}
			return total, inserted, nil, fmt.Errorf("ssm:ListDocuments: %w", err)
		}
		for _, d := range page.DocumentIdentifiers {
			name := sv(d.Name)
			if name == "" {
				continue
			}
			arn := arnFor(acct.partition(), "ssm", region, acct.ID, "document/"+name)
			dtype := string(d.DocumentType)
			// Enrich with DescribeDocument: List returns a skeleton
			// DocumentIdentifier; Requires (cross-doc refs) is Describe-only.
			var attrsJSON string
			if dout, derr := client.DescribeDocument(ctx, &ssm.DescribeDocumentInput{Name: d.Name}); derr == nil && dout.Document != nil {
				attrsJSON = mustJSON(dout.Document)
			} else {
				attrsJSON = mustJSON(d)
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSSMDocument,
				NativeID:       arn,
				Name:           d.Name,
				Region:         &region,
				Status:         &dtype,
				CreatedAt:      tp(d.CreatedDate),
				AttributesJSON: attrsJSON,
				DiscoveredBy:   scanID,
			}
			docBatch = append(docBatch, r)
		}
	}
	if len(docBatch) > 0 {
		n, err := st.UpsertResources(docBatch)
		if err != nil {
			return total, inserted, nil, fmt.Errorf("upsert SSM documents: %w", err)
		}
		total += len(docBatch)
		inserted += n
		for _, d := range docBatch {
			docs = append(docs, childParent{id: *d.Name, arn: d.NativeID})
		}
	}

	// Patch baselines — Owner=Self.
	var baseBatch []*store.Resource
	basePager := ssm.NewDescribePatchBaselinesPaginator(client, &ssm.DescribePatchBaselinesInput{
		Filters: []types.PatchOrchestratorFilter{{Key: sp("OWNER"), Values: []string{"Self"}}},
	})
	for basePager.HasMorePages() {
		page, err := basePager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				break
			}
			return total, inserted, docs, fmt.Errorf("ssm:DescribePatchBaselines: %w", err)
		}
		for _, b := range page.BaselineIdentities {
			id := sv(b.BaselineId)
			if id == "" {
				continue
			}
			arn := arnFor(acct.partition(), "ssm", region, acct.ID, "patchbaseline/"+id)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSSMPatchBaseline,
				NativeID:       arn,
				Name:           b.BaselineName,
				Region:         &region,
				AttributesJSON: mustJSON(b),
				DiscoveredBy:   scanID,
			}
			baseBatch = append(baseBatch, r)
		}
	}
	if len(baseBatch) > 0 {
		n, err := st.UpsertResources(baseBatch)
		if err != nil {
			return total, inserted, docs, fmt.Errorf("upsert SSM patch baselines: %w", err)
		}
		total += len(baseBatch)
		inserted += n
	}

	return total, inserted, docs, nil
}
