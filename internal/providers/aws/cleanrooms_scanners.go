package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/cleanrooms"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCleanRoomsAnalysisTemplate, Service: "cleanrooms"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsCollaboration, Service: "cleanrooms"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsConfiguredTable, Service: "cleanrooms"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsConfiguredTableAssociation, Service: "cleanrooms"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsConfiguredAudienceModelAssociation, Service: "cleanrooms"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsIDMappingTable, Service: "cleanrooms"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsIDNamespaceAssociation, Service: "cleanrooms"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsIntermediateTable, Service: "cleanrooms"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMember, Service: "cleanrooms"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMembership, Service: "cleanrooms"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsPrivacyBudgetTemplate, Service: "cleanrooms"})
	registerService(serviceEntry{
		name: "aws:cleanrooms",
		fn:   scanCleanRooms,
	})
}

type cleanRoomsAPI interface {
	ListCollaborations(context.Context, *cleanrooms.ListCollaborationsInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListCollaborationsOutput, error)
	ListConfiguredTables(context.Context, *cleanrooms.ListConfiguredTablesInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListConfiguredTablesOutput, error)
	ListMemberships(context.Context, *cleanrooms.ListMembershipsInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListMembershipsOutput, error)
	ListAnalysisTemplates(context.Context, *cleanrooms.ListAnalysisTemplatesInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListAnalysisTemplatesOutput, error)
	ListConfiguredTableAssociations(context.Context, *cleanrooms.ListConfiguredTableAssociationsInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListConfiguredTableAssociationsOutput, error)
	ListConfiguredAudienceModelAssociations(context.Context, *cleanrooms.ListConfiguredAudienceModelAssociationsInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListConfiguredAudienceModelAssociationsOutput, error)
	ListIdMappingTables(context.Context, *cleanrooms.ListIdMappingTablesInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListIdMappingTablesOutput, error)
	ListIdNamespaceAssociations(context.Context, *cleanrooms.ListIdNamespaceAssociationsInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListIdNamespaceAssociationsOutput, error)
	ListPrivacyBudgetTemplates(context.Context, *cleanrooms.ListPrivacyBudgetTemplatesInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListPrivacyBudgetTemplatesOutput, error)
	ListMembers(context.Context, *cleanrooms.ListMembersInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListMembersOutput, error)
	ListIntermediateTables(context.Context, *cleanrooms.ListIntermediateTablesInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListIntermediateTablesOutput, error)
}

func scanCleanRooms(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cleanrooms.NewFromConfig(acct.cfg, func(o *cleanrooms.Options) { o.Region = region })
	return scanCleanRoomsIn(ctx, client, acct, region, st, scanID)
}

// scanCleanRoomsIn runs every phase and joins their errors, so one failing
// phase does not hide the others' rows. The membership children phases fan
// out over the memberships the membership phase listed, the member phase over
// the listed collaborations.
func scanCleanRoomsIn(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		collaborations, memberships []childParent
		errs                        phaseErrs
	)
	perMembership := func(scan func(memberID string) (int, int, error)) (t, i int, err error) {
		for _, m := range memberships {
			mt, mi, perr := scan(m.id)
			t += mt
			i += mi
			if perr != nil {
				return t, i, perr
			}
		}
		return t, i, nil
	}
	for n, phase := range []struct {
		name string
		run  func() (int, int, error)
	}{
		{"cleanrooms:ListCollaborations", func() (int, int, error) {
			var (
				t, i int
				err  error
			)
			collaborations, t, i, err = scanCRCollaborations(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{"cleanrooms:ListConfiguredTables", func() (int, int, error) { return scanCRConfiguredTables(ctx, client, acct, region, st, scanID) }},
		{"cleanrooms:ListMemberships", func() (int, int, error) {
			var (
				t, i int
				err  error
			)
			memberships, t, i, err = scanCRMemberships(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{"cleanrooms:ListAnalysisTemplates", func() (int, int, error) {
			return perMembership(func(mid string) (int, int, error) {
				return scanCRAnalysisTemplates(ctx, client, acct, region, st, scanID, mid)
			})
		}},
		{"cleanrooms:ListConfiguredTableAssociations", func() (int, int, error) {
			return perMembership(func(mid string) (int, int, error) {
				return scanCRConfiguredTableAssociations(ctx, client, acct, region, st, scanID, mid)
			})
		}},
		{"cleanrooms:ListConfiguredAudienceModelAssociations", func() (int, int, error) {
			return perMembership(func(mid string) (int, int, error) {
				return scanCRConfiguredAudienceModelAssociations(ctx, client, acct, region, st, scanID, mid)
			})
		}},
		{"cleanrooms:ListIdMappingTables", func() (int, int, error) {
			return perMembership(func(mid string) (int, int, error) {
				return scanCRIdMappingTables(ctx, client, acct, region, st, scanID, mid)
			})
		}},
		{"cleanrooms:ListIdNamespaceAssociations", func() (int, int, error) {
			return perMembership(func(mid string) (int, int, error) {
				return scanCRIdNamespaceAssociations(ctx, client, acct, region, st, scanID, mid)
			})
		}},
		{"cleanrooms:ListPrivacyBudgetTemplates", func() (int, int, error) {
			return perMembership(func(mid string) (int, int, error) {
				return scanCRPrivacyBudgetTemplates(ctx, client, acct, region, st, scanID, mid)
			})
		}},
		{"cleanrooms:ListMembers", func() (int, int, error) {
			return scanCRMembers(ctx, client, acct, region, st, scanID, collaborations)
		}},
		{"cleanrooms:ListIntermediateTables", func() (int, int, error) {
			return scanCRIntermediateTables(ctx, client, acct, region, st, scanID, memberships)
		}},
	} {
		t, i, perr := phase.run()
		// Without an endpoint every phase would fail the same way; returned
		// alone, the dispatcher reports the region unavailable.
		if n == 0 && isEndpointUnreachable(perr) {
			return t, i, perr
		}
		total += t
		inserted += i
		errs.add(phase.name, lateNXDOMAIN(phase.name, perr))
	}
	return total, inserted, errs.join(st, acct, region)
}

func scanCRCollaborations(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := cleanrooms.NewListCollaborationsPaginator(client, &cleanrooms.ListCollaborationsInput{})
	var (
		batch   []*store.Resource
		parents []childParent
	)
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "cleanrooms:ListCollaborations", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("cleanrooms:ListCollaborations: %w", perr)
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
			if id := sv(c.Id); id != "" {
				parents = append(parents, childParent{id: id, arn: arn})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsCollaboration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "cleanrooms collaborations")
	if err != nil {
		return nil, 0, 0, err
	}
	return parents, t, i, nil
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

func scanCRMemberships(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := cleanrooms.NewListMembershipsPaginator(client, &cleanrooms.ListMembershipsInput{})
	var (
		batch   []*store.Resource
		parents []childParent
	)
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
				parents = append(parents, childParent{id: id, arn: arn})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsMembership, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "cleanrooms memberships")
	if err != nil {
		return nil, 0, 0, err
	}
	return parents, t, i, nil
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

func scanCRConfiguredAudienceModelAssociations(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID, memberID string) (int, int, error) {
	mid := memberID
	pager := cleanrooms.NewListConfiguredAudienceModelAssociationsPaginator(client, &cleanrooms.ListConfiguredAudienceModelAssociationsInput{MembershipIdentifier: &mid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "cleanrooms:ListConfiguredAudienceModelAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("cleanrooms:ListConfiguredAudienceModelAssociations: %w", perr)
		}
		for _, a := range out.ConfiguredAudienceModelAssociationSummaries {
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
				Type: TypeCleanRoomsConfiguredAudienceModelAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a),
				CreatedAt: tp(a.CreateTime), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cleanrooms configured-audience-model-associations")
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

// isCRParentGone matches a collaboration or membership deleted between its
// list call and the child list call (both child ops model ResourceNotFound).
func isCRParentGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// crPageCap is the documented maxResults ceiling of the cleanrooms list ops.
const crPageCap = 100

// scanCRMembers lists each collaboration's roster under the collaboration,
// NativeID {collaborationARN}/member/{accountId}: a member carries no ARN of
// its own, and an account joins a collaboration at most once. Members that
// LEFT or were REMOVED stay listed by the API and are kept with that status.
func scanCRMembers(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, collaborations []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "cleanrooms:ListMembers", collaborations, isCRParentGone,
		func(gctx context.Context, c childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := cleanrooms.NewListMembersPaginator(client, &cleanrooms.ListMembersInput{CollaborationIdentifier: &c.id},
				func(o *cleanrooms.ListMembersPaginatorOptions) { o.Limit = crPageCap })
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, m := range out.MemberSummaries {
					accountID := sv(m.AccountId)
					if accountID == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeCleanRoomsMember, NativeID: c.arn + "/member/" + accountID,
						Name: m.DisplayName, Status: nonEmptyPtr(string(m.Status)), Region: &region,
						CreatedAt: tp(m.CreateTime), AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanCRIntermediateTables lists the intermediate tables this account owns in
// each membership, under the membership.
func scanCRIntermediateTables(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, memberships []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "cleanrooms:ListIntermediateTables", memberships, isCRParentGone,
		func(gctx context.Context, m childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := cleanrooms.NewListIntermediateTablesPaginator(client, &cleanrooms.ListIntermediateTablesInput{MembershipIdentifier: &m.id},
				func(o *cleanrooms.ListIntermediateTablesPaginatorOptions) { o.Limit = crPageCap })
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, it := range out.IntermediateTableSummaries {
					arn := sv(it.Arn)
					if arn == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeCleanRoomsIntermediateTable, NativeID: arn,
						Name: it.Name, Status: nonEmptyPtr(string(it.Status)), Region: &region,
						CreatedAt: tp(it.CreateTime), AttributesJSON: mustJSON(it), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}
