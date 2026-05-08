package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/cleanrooms"
)

func init() {
	registerService(serviceEntry{
		name: "aws:cleanrooms",
		fn:   scanCleanRooms,
		emits: []coverage.TypeDecl{
			{Service: "cleanrooms", DiscoType: TypeCleanRoomsAnalysisTemplate},
			{Service: "cleanrooms", DiscoType: TypeCleanRoomsCollaboration, Leaf: true},
			{Service: "cleanrooms", DiscoType: TypeCleanRoomsConfiguredTable, Leaf: true},
			{Service: "cleanrooms", DiscoType: TypeCleanRoomsConfiguredTableAssociation},
			{Service: "cleanrooms", DiscoType: TypeCleanRoomsIDMappingTable},
			{Service: "cleanrooms", DiscoType: TypeCleanRoomsIDNamespaceAssociation},
			{Service: "cleanrooms", DiscoType: TypeCleanRoomsMembership},
			{Service: "cleanrooms", DiscoType: TypeCleanRoomsPrivacyBudgetTemplate},
		},
	})
}

type cleanRoomsAPI interface {
	ListCollaborations(context.Context, *cleanrooms.ListCollaborationsInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListCollaborationsOutput, error)
	ListConfiguredTables(context.Context, *cleanrooms.ListConfiguredTablesInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListConfiguredTablesOutput, error)
	ListMemberships(context.Context, *cleanrooms.ListMembershipsInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListMembershipsOutput, error)
	ListAnalysisTemplates(context.Context, *cleanrooms.ListAnalysisTemplatesInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListAnalysisTemplatesOutput, error)
	ListConfiguredTableAssociations(context.Context, *cleanrooms.ListConfiguredTableAssociationsInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListConfiguredTableAssociationsOutput, error)
	ListIdMappingTables(context.Context, *cleanrooms.ListIdMappingTablesInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListIdMappingTablesOutput, error)
	ListIdNamespaceAssociations(context.Context, *cleanrooms.ListIdNamespaceAssociationsInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListIdNamespaceAssociationsOutput, error)
	ListPrivacyBudgetTemplates(context.Context, *cleanrooms.ListPrivacyBudgetTemplatesInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListPrivacyBudgetTemplatesOutput, error)
}

func scanCleanRooms(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cleanrooms.NewFromConfig(acct.cfg, func(o *cleanrooms.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanCRCollaborations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanCRConfiguredTables(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	memberIDs, t, i, ferr := scanCRMemberships(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, mid := range memberIDs {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanCRAnalysisTemplates(ctx, client, acct, region, st, scanID, mid) },
			func() (int, int, error) {
				return scanCRConfiguredTableAssociations(ctx, client, acct, region, st, scanID, mid)
			},
			func() (int, int, error) {
				return scanCRIdMappingTables(ctx, client, acct, region, st, scanID, mid)
			},
			func() (int, int, error) {
				return scanCRIdNamespaceAssociations(ctx, client, acct, region, st, scanID, mid)
			},
			func() (int, int, error) {
				return scanCRPrivacyBudgetTemplates(ctx, client, acct, region, st, scanID, mid)
			},
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

func scanCRCollaborations(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := cleanrooms.NewListCollaborationsPaginator(client, &cleanrooms.ListCollaborationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "cleanrooms:ListCollaborations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("cleanrooms:ListCollaborations: %w", perr)
		}
		for _, c := range out.CollaborationList {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = sv(c.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsCollaboration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cleanrooms collaborations")
}

func scanCRConfiguredTables(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := cleanrooms.NewListConfiguredTablesPaginator(client, &cleanrooms.ListConfiguredTablesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "cleanrooms:ListConfiguredTables", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("cleanrooms:ListConfiguredTables: %w", perr)
		}
		for _, c := range out.ConfiguredTableSummaries {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = sv(c.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsConfiguredTable, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cleanrooms configured-tables")
}

func scanCRMemberships(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := cleanrooms.NewListMembershipsPaginator(client, &cleanrooms.ListMembershipsInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "cleanrooms:ListMemberships", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("cleanrooms:ListMemberships: %w", perr)
		}
		for _, m := range out.MembershipSummaries {
			arn := sv(m.Arn)
			if arn == "" {
				continue
			}
			id := sv(m.Id)
			label := sv(m.CollaborationName)
			if label == "" {
				label = id
			}
			if id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsMembership, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "cleanrooms memberships")
	return ids, t, i, err
}

func scanCRAnalysisTemplates(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID, memberID string) (int, int, error) {
	mid := memberID
	pager := cleanrooms.NewListAnalysisTemplatesPaginator(client, &cleanrooms.ListAnalysisTemplatesInput{MembershipIdentifier: &mid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "cleanrooms:ListAnalysisTemplates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("cleanrooms:ListAnalysisTemplates: %w", perr)
		}
		for _, a := range out.AnalysisTemplateSummaries {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = sv(a.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsAnalysisTemplate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cleanrooms analysis-templates")
}

func scanCRConfiguredTableAssociations(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID, memberID string) (int, int, error) {
	mid := memberID
	pager := cleanrooms.NewListConfiguredTableAssociationsPaginator(client, &cleanrooms.ListConfiguredTableAssociationsInput{MembershipIdentifier: &mid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "cleanrooms:ListConfiguredTableAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("cleanrooms:ListConfiguredTableAssociations: %w", perr)
		}
		for _, a := range out.ConfiguredTableAssociationSummaries {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = sv(a.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsConfiguredTableAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cleanrooms configured-table-associations")
}

func scanCRIdMappingTables(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID, memberID string) (int, int, error) {
	mid := memberID
	pager := cleanrooms.NewListIdMappingTablesPaginator(client, &cleanrooms.ListIdMappingTablesInput{MembershipIdentifier: &mid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "cleanrooms:ListIdMappingTables", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("cleanrooms:ListIdMappingTables: %w", perr)
		}
		for _, t := range out.IdMappingTableSummaries {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			label := sv(t.Name)
			if label == "" {
				label = sv(t.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsIDMappingTable, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cleanrooms id-mapping-tables")
}

func scanCRIdNamespaceAssociations(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID, memberID string) (int, int, error) {
	mid := memberID
	pager := cleanrooms.NewListIdNamespaceAssociationsPaginator(client, &cleanrooms.ListIdNamespaceAssociationsInput{MembershipIdentifier: &mid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "cleanrooms:ListIdNamespaceAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("cleanrooms:ListIdNamespaceAssociations: %w", perr)
		}
		for _, a := range out.IdNamespaceAssociationSummaries {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = sv(a.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsIDNamespaceAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cleanrooms id-namespace-associations")
}

func scanCRPrivacyBudgetTemplates(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID, memberID string) (int, int, error) {
	mid := memberID
	pager := cleanrooms.NewListPrivacyBudgetTemplatesPaginator(client, &cleanrooms.ListPrivacyBudgetTemplatesInput{MembershipIdentifier: &mid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "cleanrooms:ListPrivacyBudgetTemplates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("cleanrooms:ListPrivacyBudgetTemplates: %w", perr)
		}
		for _, p := range out.PrivacyBudgetTemplateSummaries {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			label := sv(p.Id)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsPrivacyBudgetTemplate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cleanrooms privacy-budget-templates")
}
