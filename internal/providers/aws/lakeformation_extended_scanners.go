package aws

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/lakeformation"
)

// scanLakeFormationExtended discovers four additional LakeFormation resource
// types: data-cells filter, data-lake settings (singleton), principal
// permissions, LF tag.
//
// AWS::LakeFormation::Permissions and AWS::LakeFormation::TagAssociation are
// skip-logged: Permissions duplicates PrincipalPermissions data via the same
// ListPermissions API; TagAssociation has no enumerable list endpoint
// (GetResourceLFTags requires a per-resource Glue ARN).
func scanLakeFormationExtended(ctx context.Context, client lakeformationAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanLFDataCellsFilter(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLFDataLakeSettings(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLFPrincipalPermissions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLFTags(ctx, client, acct, region, st, scanID) },
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

func scanLFDataCellsFilter(ctx context.Context, client lakeformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := lakeformation.NewListDataCellsFilterPaginator(client, &lakeformation.ListDataCellsFilterInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lakeformation:ListDataCellsFilter", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lakeformation:ListDataCellsFilter: %w", err)
		}
		for _, f := range out.DataCellsFilters {
			db, table, name := sv(f.DatabaseName), sv(f.TableName), sv(f.Name)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:lakeformation:%s:%s:data-cells-filter/%s/%s/%s", region, acct.ID, db, table, name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLakeFormationDataCellsFilter, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "lakeformation data-cells-filters")
}

func scanLFDataLakeSettings(ctx context.Context, client lakeformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetDataLakeSettings(ctx, &lakeformation.GetDataLakeSettingsInput{})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "EntityNotFoundException") {
			return 0, 0, skipIfAccessDenied(st, "lakeformation:GetDataLakeSettings", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("lakeformation:GetDataLakeSettings: %w", err)
	}
	if out.DataLakeSettings == nil {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:lakeformation:%s:%s:data-lake-settings", region, acct.ID)
	label := "data-lake-settings"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeLakeFormationDataLakeSettings, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out.DataLakeSettings), DiscoveredBy: scanID,
		// Per-(acct, region) AWS-managed singleton config row.
	}
	return upsertBatch(st, []*store.Resource{r}, "lakeformation data-lake-settings")
}

// scanLFPrincipalPermissions enumerates all (principal, resource) permission
// grants. Each grant has no AWS-issued ARN — synthesize a stable hash over
// the principal+resource+permissions tuple.
func scanLFPrincipalPermissions(ctx context.Context, client lakeformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := lakeformation.NewListPermissionsPaginator(client, &lakeformation.ListPermissionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lakeformation:ListPermissions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lakeformation:ListPermissions: %w", err)
		}
		for _, p := range out.PrincipalResourcePermissions {
			body := mustJSON(p)
			h := sha1.Sum([]byte(body))
			id := hex.EncodeToString(h[:])[:16]
			arn := fmt.Sprintf("arn:aws:lakeformation:%s:%s:principal-permissions/%s", region, acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLakeFormationPrincipalPermissions, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: body, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "lakeformation principal-permissions")
}

func scanLFTags(ctx context.Context, client lakeformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := lakeformation.NewListLFTagsPaginator(client, &lakeformation.ListLFTagsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lakeformation:ListLFTags", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lakeformation:ListLFTags: %w", err)
		}
		for _, t := range out.LFTags {
			key := sv(t.TagKey)
			if key == "" {
				continue
			}
			catalogID := sv(t.CatalogId)
			if catalogID == "" {
				catalogID = acct.ID
			}
			arn := fmt.Sprintf("arn:aws:lakeformation:%s:%s:lf-tag/%s", region, catalogID, key)
			label := key
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLakeFormationTag, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "lakeformation tags")
}
