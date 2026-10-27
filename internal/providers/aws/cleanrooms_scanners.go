package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cleanrooms"
	"github.com/aws/aws-sdk-go-v2/service/cleanrooms/types"
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
	ListTagsForResource(context.Context, *cleanrooms.ListTagsForResourceInput, ...func(*cleanrooms.Options)) (*cleanrooms.ListTagsForResourceOutput, error)
}

const (
	// crPageCap is the model's maxResults cap (1 to 100) of every Clean
	// Rooms listing scanned.
	crPageCap = 100

	crListTagsOp           = "cleanrooms:ListTagsForResource"
	crListCollaborationsOp = "cleanrooms:ListCollaborations"
)

// crNotFoundCodes is the only not-found code ListTagsForResource and the
// per-membership and per-collaboration listings model: the resource, or the
// parent listed, was deleted after it was listed.
var crNotFoundCodes = []string{"ResourceNotFoundException"}

func scanCleanRooms(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cleanrooms.NewFromConfig(acct.cfg, func(o *cleanrooms.Options) { o.Region = region })
	return scanCleanRoomsIn(ctx, client, acct, region, st, scanID)
}

// scanCleanRoomsIn runs the phases one at a time. Each phase adds its counts
// and errors to p and reports whether a store write failed. A phase's error
// does not stop the later ones, except that a collaboration listing that
// stored nothing and could not reach its endpoint stops the scan. A failed
// write also stops it, and so does ctx's end, reported as one error. The
// per-membership phases read the memberships the membership phase stored,
// the member phase the stored collaborations.
func scanCleanRoomsIn(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		p                           phaseTotals
		tags                        = crTags(client, acct.ID)
		collaborations, memberships []childParent
	)
	phases := []func(p *phaseTotals) (storeWriteFailed bool){
		func(p *phaseTotals) (stop bool) {
			collaborations, stop = scanCRCollaborations(ctx, client, acct, region, st, scanID, tags, p)
			return stop
		},
		func(p *phaseTotals) bool {
			return scanCRConfiguredTables(ctx, client, acct, region, st, scanID, tags, p)
		},
		func(p *phaseTotals) (stop bool) {
			memberships, stop = scanCRMemberships(ctx, client, acct, region, st, scanID, tags, p)
			return stop
		},
		func(p *phaseTotals) bool {
			return scanCRAnalysisTemplates(ctx, client, acct, region, st, scanID, memberships, tags, p)
		},
		func(p *phaseTotals) bool {
			return scanCRConfiguredTableAssociations(ctx, client, acct, region, st, scanID, memberships, tags, p)
		},
		func(p *phaseTotals) bool {
			return scanCRConfiguredAudienceModelAssociations(ctx, client, acct, region, st, scanID, memberships, tags, p)
		},
		func(p *phaseTotals) bool {
			return scanCRIdMappingTables(ctx, client, acct, region, st, scanID, memberships, tags, p)
		},
		func(p *phaseTotals) bool {
			return scanCRIdNamespaceAssociations(ctx, client, acct, region, st, scanID, memberships, tags, p)
		},
		func(p *phaseTotals) bool {
			return scanCRPrivacyBudgetTemplates(ctx, client, acct, region, st, scanID, memberships, tags, p)
		},
		func(p *phaseTotals) bool {
			t, i, err := scanCRMembers(ctx, client, acct, region, st, scanID, collaborations)
			return p.addPhase("cleanrooms:ListMembers", t, i, err)
		},
		func(p *phaseTotals) bool {
			return scanCRIntermediateTables(ctx, client, acct, region, st, scanID, memberships, tags, p)
		},
	}
	notRun := 0
	for n, run := range phases {
		if ctx.Err() != nil {
			notRun = len(phases) - n
			break
		}
		total, before := p.total, len(p.errs)
		if run(&p) {
			break
		}
		// Without an endpoint every later phase would fail the same way, so
		// none is run. Only the listing's error counts, and only when it
		// stored nothing: stored rows show the endpoint answered.
		if n == 0 && p.total == total && crListingUnreachable(p.errs[before:]) {
			break
		}
	}
	p.foldStop(ctx, "cleanrooms", notRun)
	return p.resultLateNXDOMAIN(st, acct, region)
}

// crListingUnreachable reports whether the collaboration listing's error
// among errs says its endpoint could not be reached.
func crListingUnreachable(errs phaseErrs) bool {
	for _, f := range errs {
		if f.op == crListCollaborationsOp && isEndpointUnreachable(f.err) {
			return true
		}
	}
	return false
}

// crRow is the row of a listed Clean Rooms item stored under its ARN, item
// verbatim as the attributes.
func crRow(acct *account, region, scanID, typ, arn, name string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: &name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// crOtherAccount reports whether arn names an account other than acctID: a
// collaboration this account was invited to, or a resource of another
// member.
func crOtherAccount(arn, acctID string) bool {
	a, ok := parseARN(arn)
	return ok && a.AccountID != acctID
}

// crTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its list element carries; the op answers with every tag
// at once (no page token). Its model lists ResourceNotFoundException and
// ValidationException, and no denial code. The model caps resourceArn at 100
// characters and patterns it to the commercial partition; a membership
// child's ARN (two UUIDs) exceeds that cap. Those children are read because
// the Service Reference lists them under ListTagsForResource (the ID
// mapping table and ID namespace association only under TagResource, though
// their Create ops take tags); whether the op accepts their ARNs is not
// verified live. A row another account owns is read too: a denial of it is
// expected, the tags being its owner's, and keeps the row untagged without
// a warning. So does a not-found answer for such a row: what the op answers
// for another account's resource is not verified, and dropping the row
// would lose a resource the listing just returned.
func crTags(client cleanRoomsAPI, acctID string) rowTagReader {
	return rowTagReader{
		op:       crListTagsOp,
		notFound: crNotFoundCodes,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &cleanrooms.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil && crOtherAccount(r.NativeID, acctID) && (isAccessDenied(err) || isAPIErrorCode(err, crNotFoundCodes...)) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanCRCollaborations stores the collaborations this account created, is
// a member of or was invited to, with their tags, and returns the stored
// ones, the parents of the member phase.
func scanCRCollaborations(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (collaborations []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, crListCollaborationsOp, "collaborations", "Id",
		func(ctx context.Context, token *string) ([]types.CollaborationSummary, *string, error) {
			out, err := client.ListCollaborations(ctx, &cleanrooms.ListCollaborationsInput{NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.CollaborationList, out.NextToken, nil
		},
		func(c types.CollaborationSummary) (*store.Resource, string, string) {
			if sv(c.Arn) == "" {
				return nil, "collaborations have an empty Arn", cmp.Or(sv(c.Name), sv(c.Id), "(a collaboration with no name)")
			}
			return crRow(acct, region, scanID, TypeCleanRoomsCollaboration, sv(c.Arn), cmp.Or(sv(c.Name), sv(c.Id)), c), "", ""
		},
		func(c types.CollaborationSummary) string { return sv(c.Id) },
		tags, p)
}

// scanCRConfiguredTables stores the region's configured tables with their
// tags.
func scanCRConfiguredTables(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "cleanrooms:ListConfiguredTables",
		func(ctx context.Context, token *string) ([]types.ConfiguredTableSummary, *string, error) {
			out, err := client.ListConfiguredTables(ctx, &cleanrooms.ListConfiguredTablesInput{NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfiguredTableSummaries, out.NextToken, nil
		},
		func(c types.ConfiguredTableSummary) (*store.Resource, string, string) {
			if sv(c.Arn) == "" {
				return nil, "configured tables have an empty Arn", cmp.Or(sv(c.Name), sv(c.Id), "(a configured table with no name)")
			}
			return crRow(acct, region, scanID, TypeCleanRoomsConfiguredTable, sv(c.Arn), cmp.Or(sv(c.Name), sv(c.Id)), c), "", ""
		},
		tags, p)
}

// scanCRMemberships stores this account's memberships with their tags and
// returns the stored ones, the parents of the per-membership phases.
func scanCRMemberships(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (memberships []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "cleanrooms:ListMemberships", "memberships", "Id",
		func(ctx context.Context, token *string) ([]types.MembershipSummary, *string, error) {
			out, err := client.ListMemberships(ctx, &cleanrooms.ListMembershipsInput{NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.MembershipSummaries, out.NextToken, nil
		},
		func(m types.MembershipSummary) (*store.Resource, string, string) {
			if sv(m.Arn) == "" {
				return nil, "memberships have an empty Arn", cmp.Or(sv(m.CollaborationName), sv(m.Id), "(a membership with no name)")
			}
			return crRow(acct, region, scanID, TypeCleanRoomsMembership, sv(m.Arn), cmp.Or(sv(m.CollaborationName), sv(m.Id)), m), "", ""
		},
		func(m types.MembershipSummary) string { return sv(m.Id) },
		tags, p)
}

// scanCRAnalysisTemplates stores the analysis templates of each stored
// membership with their tags. A membership the listing no longer finds is
// skipped.
func scanCRAnalysisTemplates(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, memberships []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "cleanrooms:ListAnalysisTemplates", crNotFoundCodes, memberships,
		func(ctx context.Context, m string, token *string) ([]types.AnalysisTemplateSummary, *string, error) {
			out, err := client.ListAnalysisTemplates(ctx, &cleanrooms.ListAnalysisTemplatesInput{MembershipIdentifier: &m, NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AnalysisTemplateSummaries, out.NextToken, nil
		},
		func(m string, a types.AnalysisTemplateSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "analysis templates have an empty Arn", cmp.Or(sv(a.Name), sv(a.Id), "(an analysis template with no name)") + " of membership " + m
			}
			return crRow(acct, region, scanID, TypeCleanRoomsAnalysisTemplate, sv(a.Arn), cmp.Or(sv(a.Name), sv(a.Id)), a), "", ""
		}, tags, p)
	return stop
}

// scanCRConfiguredTableAssociations stores the configured table associations
// of each stored membership with their tags. A membership the listing no
// longer finds is skipped.
func scanCRConfiguredTableAssociations(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, memberships []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "cleanrooms:ListConfiguredTableAssociations", crNotFoundCodes, memberships,
		func(ctx context.Context, m string, token *string) ([]types.ConfiguredTableAssociationSummary, *string, error) {
			out, err := client.ListConfiguredTableAssociations(ctx, &cleanrooms.ListConfiguredTableAssociationsInput{MembershipIdentifier: &m, NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfiguredTableAssociationSummaries, out.NextToken, nil
		},
		func(m string, a types.ConfiguredTableAssociationSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "configured table associations have an empty Arn", cmp.Or(sv(a.Name), sv(a.Id), "(a configured table association with no name)") + " of membership " + m
			}
			return crRow(acct, region, scanID, TypeCleanRoomsConfiguredTableAssociation, sv(a.Arn), cmp.Or(sv(a.Name), sv(a.Id)), a), "", ""
		}, tags, p)
	return stop
}

// scanCRConfiguredAudienceModelAssociations stores the configured audience
// model associations of each stored membership with their tags. A
// membership the listing no longer finds is skipped.
func scanCRConfiguredAudienceModelAssociations(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, memberships []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "cleanrooms:ListConfiguredAudienceModelAssociations", crNotFoundCodes, memberships,
		func(ctx context.Context, m string, token *string) ([]types.ConfiguredAudienceModelAssociationSummary, *string, error) {
			out, err := client.ListConfiguredAudienceModelAssociations(ctx, &cleanrooms.ListConfiguredAudienceModelAssociationsInput{MembershipIdentifier: &m, NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfiguredAudienceModelAssociationSummaries, out.NextToken, nil
		},
		func(m string, a types.ConfiguredAudienceModelAssociationSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "configured audience model associations have an empty Arn", cmp.Or(sv(a.Name), sv(a.Id), "(a configured audience model association with no name)") + " of membership " + m
			}
			r := crRow(acct, region, scanID, TypeCleanRoomsConfiguredAudienceModelAssociation, sv(a.Arn), cmp.Or(sv(a.Name), sv(a.Id)), a)
			r.CreatedAt = tp(a.CreateTime)
			return r, "", ""
		}, tags, p)
	return stop
}

// scanCRIdMappingTables stores the ID mapping tables of each stored
// membership with their tags. A membership the listing no longer finds is
// skipped.
func scanCRIdMappingTables(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, memberships []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "cleanrooms:ListIdMappingTables", crNotFoundCodes, memberships,
		func(ctx context.Context, m string, token *string) ([]types.IdMappingTableSummary, *string, error) {
			out, err := client.ListIdMappingTables(ctx, &cleanrooms.ListIdMappingTablesInput{MembershipIdentifier: &m, NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.IdMappingTableSummaries, out.NextToken, nil
		},
		func(m string, t types.IdMappingTableSummary) (*store.Resource, string, string) {
			if sv(t.Arn) == "" {
				return nil, "ID mapping tables have an empty Arn", cmp.Or(sv(t.Name), sv(t.Id), "(an ID mapping table with no name)") + " of membership " + m
			}
			return crRow(acct, region, scanID, TypeCleanRoomsIDMappingTable, sv(t.Arn), cmp.Or(sv(t.Name), sv(t.Id)), t), "", ""
		}, tags, p)
	return stop
}

// scanCRIdNamespaceAssociations stores the ID namespace associations of
// each stored membership with their tags. A membership the listing no
// longer finds is skipped.
func scanCRIdNamespaceAssociations(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, memberships []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "cleanrooms:ListIdNamespaceAssociations", crNotFoundCodes, memberships,
		func(ctx context.Context, m string, token *string) ([]types.IdNamespaceAssociationSummary, *string, error) {
			out, err := client.ListIdNamespaceAssociations(ctx, &cleanrooms.ListIdNamespaceAssociationsInput{MembershipIdentifier: &m, NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.IdNamespaceAssociationSummaries, out.NextToken, nil
		},
		func(m string, a types.IdNamespaceAssociationSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "ID namespace associations have an empty Arn", cmp.Or(sv(a.Name), sv(a.Id), "(an ID namespace association with no name)") + " of membership " + m
			}
			return crRow(acct, region, scanID, TypeCleanRoomsIDNamespaceAssociation, sv(a.Arn), cmp.Or(sv(a.Name), sv(a.Id)), a), "", ""
		}, tags, p)
	return stop
}

// scanCRPrivacyBudgetTemplates stores the privacy budget templates of each
// stored membership with their tags. A membership the listing no longer
// finds is skipped. A template has no name, so its Id names the row.
func scanCRPrivacyBudgetTemplates(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, memberships []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "cleanrooms:ListPrivacyBudgetTemplates", crNotFoundCodes, memberships,
		func(ctx context.Context, m string, token *string) ([]types.PrivacyBudgetTemplateSummary, *string, error) {
			out, err := client.ListPrivacyBudgetTemplates(ctx, &cleanrooms.ListPrivacyBudgetTemplatesInput{MembershipIdentifier: &m, NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.PrivacyBudgetTemplateSummaries, out.NextToken, nil
		},
		func(m string, t types.PrivacyBudgetTemplateSummary) (*store.Resource, string, string) {
			if sv(t.Arn) == "" {
				return nil, "privacy budget templates have an empty Arn", cmp.Or(sv(t.Id), "(a privacy budget template with no id)") + " of membership " + m
			}
			return crRow(acct, region, scanID, TypeCleanRoomsPrivacyBudgetTemplate, sv(t.Arn), cmp.Or(sv(t.Id), sv(t.Arn)), t), "", ""
		}, tags, p)
	return stop
}

// scanCRMembers lists each stored collaboration's roster under the
// collaboration, NativeID {collaborationARN}/member/{accountId}: a member
// carries no ARN of its own, and an account joins a collaboration at most
// once. With no ARN a member is not taggable, so no tags are read. Members
// that LEFT or were REMOVED stay listed by the API and are kept with that
// status. A collaboration the listing no longer finds is skipped; a denied
// listing of another account's collaboration is expected and not warned
// about (crOtherAccount).
func scanCRMembers(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, collaborations []childParent) (int, int, error) {
	return storeChildrenEachParent(ctx, st, acct, region, "cleanrooms:ListMembers", crNotFoundCodes, collaborations,
		func(collaboration string, err error) bool {
			return isAccessDenied(err) && crOtherAccount(collaboration, acct.ID)
		}, nil,
		func(ctx context.Context, c childParent, token *string) ([]types.MemberSummary, *string, error) {
			out, err := client.ListMembers(ctx, &cleanrooms.ListMembersInput{CollaborationIdentifier: &c.id, NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.MemberSummaries, out.NextToken, nil
		},
		func(c childParent, m types.MemberSummary) (*store.Resource, string, string) {
			accountID := sv(m.AccountId)
			if accountID == "" {
				return nil, "members have an empty AccountId", cmp.Or(sv(m.DisplayName), "(a member with no name)") + " of collaboration " + c.arn
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsMember, NativeID: c.arn + "/member/" + accountID,
				Name: m.DisplayName, Status: nonEmptyPtr(string(m.Status)), Region: &region,
				CreatedAt: tp(m.CreateTime), AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanCRIntermediateTables stores the intermediate tables this account owns
// in each stored membership, with their tags, under the membership. A
// membership the listing no longer finds is skipped.
//
// The hierarchy is recorded after storeEachParentTags, which has no parentOf
// hook; the warnings it sent first concern rows it already wrote.
func scanCRIntermediateTables(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, scanID string, memberships []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "cleanrooms:ListIntermediateTables"
	membershipARN := make(map[string]string, len(memberships))
	for _, m := range memberships {
		membershipARN[m.id] = m.arn
	}
	parentOf := map[*store.Resource]string{}
	stored, stop := storeEachParentTags(ctx, st, acct, region, op, crNotFoundCodes, memberships,
		func(ctx context.Context, m string, token *string) ([]types.IntermediateTableSummary, *string, error) {
			out, err := client.ListIntermediateTables(ctx, &cleanrooms.ListIntermediateTablesInput{MembershipIdentifier: &m, NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.IntermediateTableSummaries, out.NextToken, nil
		},
		func(m string, it types.IntermediateTableSummary) (*store.Resource, string, string) {
			if sv(it.Arn) == "" {
				return nil, "intermediate tables have an empty Arn", cmp.Or(sv(it.Name), sv(it.Id), "(an intermediate table with no name)") + " of membership " + m
			}
			r := crRow(acct, region, scanID, TypeCleanRoomsIntermediateTable, sv(it.Arn), cmp.Or(sv(it.Name), sv(it.Id)), it)
			r.Status = nonEmptyPtr(string(it.Status))
			r.CreatedAt = tp(it.CreateTime)
			parentOf[r] = membershipARN[m]
			return r, "", ""
		}, tags, p)
	if stop {
		return true
	}
	pairs := make([][2]string, 0, len(stored))
	for _, r := range stored {
		pairs = append(pairs, [2]string{r.ID, store.ResourceID("aws", acct.ID, parentOf[r])})
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return p.addPhase(op, 0, 0, fmt.Errorf("closure %s: %w", op, err))
	}
	return false
}
