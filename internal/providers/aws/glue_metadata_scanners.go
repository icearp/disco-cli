package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/glue"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGlueResourcePolicy, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueAssetType, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueGlossary, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueGlossaryTerm, Service: "glue"})
}

// scanGlueMetadata discovers the Data Catalog resource policy and the business
// metadata (asset types, glossaries and their terms).
//
// GetResourcePolicies is not used: its GluePolicy elements carry no resource
// ARN, so a policy set through RAM on a database or table cannot be told apart
// from the catalog's own. Assets are not scanned: SearchAssets is the only way
// to enumerate them, it needs search text or a filter clause whose attribute
// names the SDK does not document, and a relevance-ranked search promises no
// complete listing.
func scanGlueMetadata(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t, i, err := scanGlueResourcePolicy(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	total += t
	inserted += i

	t, i, err = scanGlueAssetTypes(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	total += t
	inserted += i

	glossaries, t, i, err := scanGlueGlossaries(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	total += t
	inserted += i

	t, i, err = scanGlueGlossaryTerms(ctx, client, glossaries, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	return total + t, inserted + i, nil
}

// scanGlueResourcePolicy stores the Data Catalog resource policy, one per
// region. GetResourcePolicy without a ResourceArn returns it; the account
// creates it with PutResourcePolicy and can delete it, so it is not
// provider-managed.
func scanGlueResourcePolicy(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetResourcePolicy(ctx, &glue.GetResourcePolicyInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "glue:GetResourcePolicy", acct.ID, region, err)
		}
		// The op's modeled code when no catalog policy has been put.
		if isAPIErrorCode(err, "EntityNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("glue:GetResourcePolicy: %w", err)
	}
	if sv(out.PolicyInJson) == "" {
		return 0, 0, nil
	}
	name := "data-catalog-resource-policy"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type:     TypeGlueResourcePolicy,
		NativeID: arnFor(acct.partition(), "glue", region, acct.ID, "resource-policy"),
		Name:     &name, Region: &region, CreatedAt: tp(out.CreateTime),
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "glue resource-policy")
}

// scanGlueAssetTypes lists the asset types. They carry no ARN; NativeID is
// built like the other ARN-less Glue types.
func scanGlueAssetTypes(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := glue.NewListAssetTypesPaginator(client, &glue.ListAssetTypesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "glue:ListAssetTypes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("glue:ListAssetTypes: %w", err)
		}
		for _, a := range out.Items {
			id := sv(a.Id)
			if id == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeGlueAssetType,
				NativeID: glueResourceARN(region, acct.ID, "assetType", id),
				Name:     a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "glue asset-types")
}

// scanGlueGlossaries lists the business glossaries and returns them as the
// parents of scanGlueGlossaryTerms.
func scanGlueGlossaries(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	var (
		batch   []*store.Resource
		parents []childParent
	)
	pager := glue.NewListGlossariesPaginator(client, &glue.ListGlossariesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "glue:ListGlossaries", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("glue:ListGlossaries: %w", err)
		}
		for _, g := range out.Items {
			id := sv(g.Id)
			if id == "" {
				continue
			}
			arn := glueResourceARN(region, acct.ID, "glossary", id)
			parents = append(parents, childParent{id: id, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlueGlossary, NativeID: arn,
				Name: g.Name, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "glue glossaries")
	if err != nil {
		return nil, 0, 0, err
	}
	return parents, total, inserted, nil
}

// scanGlueGlossaryTerms lists each glossary's terms under the glossary,
// NativeID {glossaryARN}/term/{id}.
//
// No error is skipped silently: ListGlossaryTerms models no not-found code,
// and what it returns for a glossary deleted after ListGlossaries is not
// verified live, so such an error fails this phase.
func scanGlueGlossaryTerms(ctx context.Context, client glueAPI, glossaries []childParent, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "glue:ListGlossaryTerms", glossaries,
		func(error) bool { return false },
		func(gctx context.Context, g childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := glue.NewListGlossaryTermsPaginator(client, &glue.ListGlossaryTermsInput{GlossaryIdentifier: &g.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, term := range out.Items {
					id := sv(term.Id)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeGlueGlossaryTerm, NativeID: g.arn + "/term/" + id,
						Name: term.Name, Region: &region,
						AttributesJSON: mustJSON(term), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}
