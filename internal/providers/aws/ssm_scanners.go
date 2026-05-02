package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:ssm",
		fn:   scanSSM,
		emits: []coverage.TypeDecl{
			{Service: "ssm", DiscoType: TypeSSMDocument},
			{Service: "ssm", DiscoType: TypeSSMParameter},
			{Service: "ssm", DiscoType: TypeSSMPatchBaseline},
			{Service: "ssm", DiscoType: TypeSSMAssociation},
			{Service: "ssm", DiscoType: TypeSSMMaintenanceWindow},
			{Service: "ssm", DiscoType: TypeSSMMaintenanceWindowTarget},
			{Service: "ssm", DiscoType: TypeSSMMaintenanceWindowTask},
			{Service: "ssm", DiscoType: TypeSSMResourceDataSync},
		},
	})
}

// ssmAPI is the narrow set of SSM operations called by scanSSMAll.
type ssmAPI interface {
	DescribeParameters(context.Context, *ssm.DescribeParametersInput, ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error)
	ListDocuments(context.Context, *ssm.ListDocumentsInput, ...func(*ssm.Options)) (*ssm.ListDocumentsOutput, error)
	DescribePatchBaselines(context.Context, *ssm.DescribePatchBaselinesInput, ...func(*ssm.Options)) (*ssm.DescribePatchBaselinesOutput, error)
	ListAssociations(context.Context, *ssm.ListAssociationsInput, ...func(*ssm.Options)) (*ssm.ListAssociationsOutput, error)
	DescribeMaintenanceWindows(context.Context, *ssm.DescribeMaintenanceWindowsInput, ...func(*ssm.Options)) (*ssm.DescribeMaintenanceWindowsOutput, error)
	DescribeMaintenanceWindowTargets(context.Context, *ssm.DescribeMaintenanceWindowTargetsInput, ...func(*ssm.Options)) (*ssm.DescribeMaintenanceWindowTargetsOutput, error)
	DescribeMaintenanceWindowTasks(context.Context, *ssm.DescribeMaintenanceWindowTasksInput, ...func(*ssm.Options)) (*ssm.DescribeMaintenanceWindowTasksOutput, error)
	ListResourceDataSync(context.Context, *ssm.ListResourceDataSyncInput, ...func(*ssm.Options)) (*ssm.ListResourceDataSyncOutput, error)
}

// scanSSM discovers SSM parameters (metadata only — never values), customer-
// owned SSM documents, and customer-owned patch baselines.
//
// Parameter values are intentionally never fetched: even with the store-level
// secret scrubber, plaintext values would transit memory and could land in
// debug dumps. The KMS edge off SecureString parameters is recoverable from
// metadata alone.
func scanSSM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ssm.NewFromConfig(acct.cfg, func(o *ssm.Options) { o.Region = region })
	t, i, ferr := scanSSMAll(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	t, i, ferr = scanSSMExtended(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanSSMAll holds the testable scan body.
func scanSSMAll(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// Parameters (metadata only).
	var paramBatch []*store.Resource
	paramPager := ssm.NewDescribeParametersPaginator(client, &ssm.DescribeParametersInput{})
	for paramPager.HasMorePages() {
		page, err := paramPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm:DescribeParameters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm:DescribeParameters: %w", err)
		}
		for _, p := range page.Parameters {
			name := sv(p.Name)
			if name == "" {
				continue
			}
			// Parameter names may or may not start with '/'; ARN format uses the
			// name verbatim after 'parameter'. Leading slash joins naturally.
			joined := name
			if joined[0] != '/' {
				joined = "/" + joined
			}
			arn := fmt.Sprintf("arn:aws:ssm:%s:%s:parameter%s", region, acct.ID, joined)
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
			return 0, 0, fmt.Errorf("upsert SSM parameters: %w", err)
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
			return 0, 0, fmt.Errorf("ssm:ListDocuments: %w", err)
		}
		for _, d := range page.DocumentIdentifiers {
			name := sv(d.Name)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ssm:%s:%s:document/%s", region, acct.ID, name)
			dtype := string(d.DocumentType)
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
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			}
			docBatch = append(docBatch, r)
		}
	}
	if len(docBatch) > 0 {
		n, err := st.UpsertResources(docBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert SSM documents: %w", err)
		}
		total += len(docBatch)
		inserted += n
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
			return 0, 0, fmt.Errorf("ssm:DescribePatchBaselines: %w", err)
		}
		for _, b := range page.BaselineIdentities {
			id := sv(b.BaselineId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ssm:%s:%s:patchbaseline/%s", region, acct.ID, id)
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
			return 0, 0, fmt.Errorf("upsert SSM patch baselines: %w", err)
		}
		total += len(baseBatch)
		inserted += n
	}

	return total, inserted, nil
}
