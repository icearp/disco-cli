package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeOrganization, Service: "organizations", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeOrganizationsAccount, Service: "organizations"})
	registerType(restype.Descriptor{Type: TypeOrganizationsOU, Service: "organizations"})
	registerType(restype.Descriptor{Type: TypeOrganizationsSCP, Service: "organizations"})
	registerType(restype.Descriptor{Type: TypeOrganizationsResourcePolicy, Service: "organizations"})
	registerType(restype.Descriptor{Type: TypeOrganizationsRoot, Service: "organizations"})
	registerType(restype.Descriptor{Type: TypeOrganizationsResponsibilityTransfer, Service: "organizations"})
	registerService(serviceEntry{
		name:   "aws:organizations",
		global: true,
		fn:     scanOrganizations,
	})
}

// organizationsAPI is the narrow set of Organizations operations called by
// the scanOrganizations sub-phases.
type organizationsAPI interface {
	DescribeOrganization(context.Context, *organizations.DescribeOrganizationInput, ...func(*organizations.Options)) (*organizations.DescribeOrganizationOutput, error)
	ListRoots(context.Context, *organizations.ListRootsInput, ...func(*organizations.Options)) (*organizations.ListRootsOutput, error)
	ListOrganizationalUnitsForParent(context.Context, *organizations.ListOrganizationalUnitsForParentInput, ...func(*organizations.Options)) (*organizations.ListOrganizationalUnitsForParentOutput, error)
	ListAccounts(context.Context, *organizations.ListAccountsInput, ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error)
	ListPolicies(context.Context, *organizations.ListPoliciesInput, ...func(*organizations.Options)) (*organizations.ListPoliciesOutput, error)
	DescribePolicy(context.Context, *organizations.DescribePolicyInput, ...func(*organizations.Options)) (*organizations.DescribePolicyOutput, error)
	ListParents(context.Context, *organizations.ListParentsInput, ...func(*organizations.Options)) (*organizations.ListParentsOutput, error)
	DescribeResourcePolicy(context.Context, *organizations.DescribeResourcePolicyInput, ...func(*organizations.Options)) (*organizations.DescribeResourcePolicyOutput, error)
	ListInboundResponsibilityTransfers(context.Context, *organizations.ListInboundResponsibilityTransfersInput, ...func(*organizations.Options)) (*organizations.ListInboundResponsibilityTransfersOutput, error)
	ListOutboundResponsibilityTransfers(context.Context, *organizations.ListOutboundResponsibilityTransfersInput, ...func(*organizations.Options)) (*organizations.ListOutboundResponsibilityTransfersOutput, error)
	ListTagsForResource(context.Context, *organizations.ListTagsForResourceInput, ...func(*organizations.Options)) (*organizations.ListTagsForResourceOutput, error)
}

const (
	orgTagsOp    = "organizations:ListTagsForResource"
	orgListOUsOp = "organizations:ListOrganizationalUnitsForParent"
	orgParentsOp = "organizations:ListParents"
	// orgTransferOp labels the transfer phase and its write, which cover
	// two listings; each listing's warnings and errors name its own op.
	orgTransferOp = "organizations responsibility-transfers"
	// orgPageCap is the MaxResults ceiling (1-20) the Organizations
	// listings model.
	orgPageCap = 20
)

// scanOrganizations discovers the AWS Organizations structure: the
// organization, its roots, OUs, accounts and SCPs, its resource policy and
// its billing responsibility transfers. Only the management account lists
// them; a member account stores the organization row alone, and an account
// outside any organization or denied DescribeOrganization stores nothing
// (scanOrgRoot).
//
// The listings run as phases, one at a time (runHostPhases): a phase's error
// does not stop the later ones; a failed write and ctx's end do. The OU walk
// starts from the roots listed before any failure, and the closure links
// whatever the tree phases stored. Every type but the organization has its
// tags read per row (orgScan.tagsFor), and the tag denials of the whole scan
// are one warning.
func scanOrganizations(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	client := organizations.NewFromConfig(acct.cfg)
	org, orgTotal, orgInserted, err := scanOrgRoot(ctx, client, acct, st, scanID)
	if err != nil || org == nil {
		return orgTotal, orgInserted, err
	}
	// Member account: org row persisted; management-only phases skipped.
	if mgmt := sv(org.MasterAccountId); mgmt != "" && mgmt != acct.ID {
		return orgTotal, orgInserted, nil
	}
	// runHostPhases would count the organization row's phase as not run.
	if err := ctx.Err(); err != nil {
		return orgTotal, orgInserted, fmt.Errorf("organizations: %w", err)
	}
	s := newOrgScan(client, acct, st, scanID, sv(org.Arn))
	total, inserted, err := runHostPhases(ctx, st, acct, s.region, "organizations", 0, []hostPhase{
		// Counted as the first phase: the host answered DescribeOrganization,
		// so an NXDOMAIN from a later phase is a failure of this scan, not a
		// missing endpoint (runHostPhases).
		countedPhase(0, "organizations:DescribeOrganization", func() (int, int, error) { return orgTotal, orgInserted, nil }),
		{op: "organizations:ListRoots", run: func(p *phaseTotals) bool { return scanOrgRoots(ctx, s, p) }},
		{op: orgListOUsOp, run: func(p *phaseTotals) bool { return scanOrgOUTree(ctx, s, p) }},
		{op: "organizations:ListAccounts", run: func(p *phaseTotals) bool { return scanOrgAccounts(ctx, s, p) }},
		countedPhase(0, "organizations hierarchy closure", func() (int, int, error) {
			if err := st.RecordHierarchyBatch(s.tree.pairs); err != nil {
				return 0, 0, fmt.Errorf("org hierarchy closure: %w", err)
			}
			return 0, 0, nil
		}),
		{op: "organizations:ListPolicies", run: func(p *phaseTotals) bool { return scanOrgSCPs(ctx, s, p) }},
		{op: "organizations:DescribeResourcePolicy", run: func(p *phaseTotals) bool { return scanOrganizationsResourcePolicy(ctx, s, p) }},
		{op: orgTransferOp, run: func(p *phaseTotals) bool { return scanOrgResponsibilityTransfers(ctx, s, p) }},
	})
	s.tags.warnDenials(st, acct, s.region)
	// A row tallied here may be one a failed write lost, so nothing is
	// warned about then.
	if !errors.Is(err, store.ErrStoreWrite) {
		if s.noID.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: orgTagsOp, Scope: acct.ID + "/" + s.region,
				Message: fmt.Sprintf("%d row(s) stored without tags: they were listed without an Id, which reading their tags requires; first %s", s.noID.count, s.noID.first),
			})
		}
		warnNoChildID(st, orgListOUsOp, acct, s.region, &s.tree.noID, "roots or organizational units", "Id")
	}
	return total, inserted, err
}

// orgScan is what the phases of one management-account scan share.
type orgScan struct {
	client organizationsAPI
	acct   *account
	st     *store.Store
	scanID string
	region string
	// tags reads every phase's rows (tagsFor); its denials are one warning
	// for the whole scan.
	tags rowTagReader
	// noID tallies the rows built from an element without an Id: they are
	// stored without tags.
	noID skipTally
	tree orgTree
}

// newOrgScan is the state of one management-account scan of the
// organization orgARN.
func newOrgScan(client organizationsAPI, acct *account, st *store.Store, scanID, orgARN string) *orgScan {
	orgID := store.ResourceID("aws", acct.ID, orgARN)
	return &orgScan{
		client: client, acct: acct, st: st, scanID: scanID, region: sv(regionGlobal),
		tags: rowTagReader{op: orgTagsOp, notFound: []string{"TargetNotFoundException"}}.sharingDenials(),
		tree: orgTree{orgID: orgID, arnByID: map[string]string{}, pairs: [][2]string{{orgID, orgID}}},
	}
}

// orgTree is what the tree phases (roots, OUs, accounts) hand on.
type orgTree struct {
	// orgID is the organization row's resource id, the roots' parent.
	orgID string
	// arnByID maps the Id of every root and OU stored this scan to its ARN,
	// so that an account's parent, which ListParents names by Id, is linked
	// only when stored.
	arnByID map[string]string
	// next holds the roots and OUs whose child OUs are still to be listed:
	// those stored, and the roots listed without an ARN, whose OUs and
	// accounts carry their own.
	next []childParent
	// pairs is the closure of every row the tree phases stored.
	pairs [][2]string
	// noID tallies the roots and OUs stored without an Id, whose children
	// are not listed.
	noID skipTally
}

// stored records a root or OU row once it is written: its closure pair
// under parentID (a resource id), and, given its element's Id, its place as
// a parent of the later listings.
func (t *orgTree) stored(r *store.Resource, id, parentID string) {
	t.pairs = append(t.pairs, [2]string{store.ResourceID("aws", r.AccountID, r.NativeID), parentID})
	if id == "" {
		// The OU walk stores pages in no fixed order.
		t.noID.skippedLeast(r.NativeID, nil)
		return
	}
	t.arnByID[id] = r.NativeID
	t.next = append(t.next, childParent{id: id, arn: r.NativeID})
}

// orgRowIDs maps a phase's rows to the Ids of the elements they were built
// from, which ListTagsForResource and DescribePolicy take instead of the ARN,
// and an OU's row to the Id of the parent it was listed under. A row is
// added while its page is built and taken once the page is written, so only
// the pages in flight stay reachable. Safe for concurrent use.
type orgRowIDs struct {
	mu   sync.Mutex
	rows map[*store.Resource]orgRowID
}

type orgRowID struct{ id, parent string }

func (ids *orgRowIDs) get(r *store.Resource) orgRowID {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	return ids.rows[r]
}

func (ids *orgRowIDs) take(r *store.Resource) orgRowID {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	got := ids.rows[r]
	delete(ids.rows, r)
	return got
}

// addID records in ids the Id of the element r was built from, listed under
// parent ("" for a top-level listing), tallies r in s.noID when id is empty,
// and returns r.
func (s *orgScan) addID(ids *orgRowIDs, r *store.Resource, id, parent string) *store.Resource {
	// Rows are built concurrently in the OU walk, so the warning names the
	// smallest NativeID, not whichever was built first.
	if id == "" {
		s.noID.skippedLeast(r.NativeID, nil)
	}
	ids.mu.Lock()
	defer ids.mu.Unlock()
	if ids.rows == nil {
		ids.rows = map[*store.Resource]orgRowID{}
	}
	ids.rows[r] = orgRowID{id: id, parent: parent}
	return r
}

// tagsFor is s.tags reading a row's tags with ListTagsForResource, sent the
// Id ids holds for it (the op takes an account, root, OU, policy, resource
// policy or responsibility transfer Id, never an ARN), following every page.
// TargetNotFoundException, the op's modeled not-found, drops a resource
// deleted after it was listed. Not read, keeping the TagsJSON their builder
// set: a row without an Id (tallied in s.noID), and an AWS-managed policy
// (ManagedByProvider), which is the Service Reference's awspolicy resource,
// absent from the resources ListTagsForResource and TagResource list.
func (s *orgScan) tagsFor(ids *orgRowIDs) rowTagReader {
	tags := s.tags
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		id := ids.get(r).id
		if id == "" || r.ManagedByProvider {
			return r.TagsJSON, nil
		}
		got, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]types.Tag, *string, error) {
			out, err := s.client.ListTagsForResource(ctx, &organizations.ListTagsForResourceInput{ResourceId: &id, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Tags, out.NextToken, nil
		})
		if err != nil {
			return nil, err
		}
		return kvTagsJSON(got, func(t types.Tag) (*string, *string) { return t.Key, t.Value }), nil
	}
	return tags
}

// scanOrgResponsibilityTransfers captures billing responsibility transfers
// from both inbound and outbound lists (a transfer can appear in both),
// deduped by ARN. Only the BILLING transfer type is API-supported.
//
// A list that fails or ends early keeps the transfers read before it, and the
// other list still runs. A denied list is warned about under its own op once
// the transfers are stored; any other failure is returned under its op.
//
// A transfer listed without an Arn, its NativeID, is not stored, and is
// warned about under the list that returned it. No ARN is built for it: the
// Service Reference's format names an organization Id the transfer does not
// carry, and which participant's account it names is unverified.
//
// A transfer's ARN may name the other organization's management account
// (which list returns such ARNs is unverified). A transfer whose ARN names
// another account is kept without tags, and without a warning, when the tag
// read is denied or finds no such resource (untaggedIfOtherAccount): this
// account may not read it.
func scanOrgResponsibilityTransfers(ctx context.Context, s *orgScan, p *phaseTotals) (storeWriteFailed bool) {
	type transferList struct {
		op    string
		page  func(ctx context.Context, token *string) ([]types.ResponsibilityTransfer, *string, error)
		skips notStoredSkips
		// denied is the list's denial, warned about once the transfers are
		// stored.
		denied error
	}
	var (
		ids  orgRowIDs
		rows []*store.Resource
		seen = map[string]bool{}
	)
	lists := []*transferList{
		{op: "organizations:ListInboundResponsibilityTransfers", page: func(ctx context.Context, token *string) ([]types.ResponsibilityTransfer, *string, error) {
			out, err := s.client.ListInboundResponsibilityTransfers(ctx, &organizations.ListInboundResponsibilityTransfersInput{
				Type: types.ResponsibilityTransferTypeBilling, NextToken: token, MaxResults: sdkaws.Int32(orgPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ResponsibilityTransfers, out.NextToken, nil
		}},
		{op: "organizations:ListOutboundResponsibilityTransfers", page: func(ctx context.Context, token *string) ([]types.ResponsibilityTransfer, *string, error) {
			out, err := s.client.ListOutboundResponsibilityTransfers(ctx, &organizations.ListOutboundResponsibilityTransfersInput{
				Type: types.ResponsibilityTransferTypeBilling, NextToken: token, MaxResults: sdkaws.Int32(orgPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ResponsibilityTransfers, out.NextToken, nil
		}},
	}
	for _, l := range lists {
		err := eachTokenPage(ctx, l.page, func(transfers []types.ResponsibilityTransfer) error {
			for _, tr := range transfers {
				r, why, key := arnRow("responsibility transfers", tr.Arn, sdkaws.String(cmp.Or(sv(tr.Id), sv(tr.Name))), &store.Resource{
					Provider: "aws", AccountID: s.acct.ID, AccountName: &s.acct.Name,
					Region: regionGlobal, Type: TypeOrganizationsResponsibilityTransfer, NativeID: sv(tr.Arn),
					Name: tr.Name, AttributesJSON: mustJSON(tr), DiscoveredBy: s.scanID,
				})
				switch {
				case r == nil:
					l.skips.skipped(why, key)
				case seen[r.NativeID]:
					// Listed by both lists; stored once.
				default:
					seen[r.NativeID] = true
					rows = append(rows, s.addID(&ids, r, sv(tr.Id), ""))
				}
			}
			return nil
		})
		switch {
		case err == nil:
		case isAccessDenied(err):
			l.denied = err
		default:
			p.errs.add(l.op, fmt.Errorf("%s: %w", l.op, err))
		}
	}
	return storeRowsTags(ctx, s.st, s.acct, s.region, orgTransferOp, rows, nil, func() {
		for _, l := range lists {
			l.skips.warn(s.st, l.op, s.acct, s.region)
			if l.denied != nil {
				_ = skipIfAccessDenied(s.st, l.op, s.acct.ID, s.region, l.denied)
			}
		}
	}, s.tagsFor(&ids).untaggedIfOtherAccount(s.acct.ID), p)
}

// scanOrgRoot describes the organization, upserts the org resource, and
// returns the SDK struct so the caller can route on MasterAccountId. Returns
// (nil, ...) if the account isn't in an org or the call is service-disabled /
// access-denied.
func scanOrgRoot(ctx context.Context, client organizationsAPI, acct *account, st *store.Store, scanID string) (*types.Organization, int, int, error) {
	descOrg, err := client.DescribeOrganization(ctx, &organizations.DescribeOrganizationInput{})
	if err != nil {
		// Standalone account — never joined an Organization. Default state,
		// not a fault. Surface "(account: disabled)" with no warning.
		if isAPIErrorCode(err, "AWSOrganizationsNotInUseException") {
			return nil, 0, 0, markServiceDisabled(err)
		}
		if isAccessDenied(err) {
			return nil, 0, 0, skipIfAccessDenied(st, "organizations:DescribeOrganization", acct.ID, "", err)
		}
		return nil, 0, 0, fmt.Errorf("organizations:DescribeOrganization: %w", err)
	}
	org := descOrg.Organization
	if org == nil {
		return nil, 0, 0, nil
	}
	orgRes := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Region:         regionGlobal,
		Type:           TypeOrganization,
		NativeID:       sv(org.Arn),
		Name:           org.Id,
		AttributesJSON: mustJSON(org),
		DiscoveredBy:   scanID,
	}
	n, err := st.UpsertResources([]*store.Resource{orgRes})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("upsert organization: %w", err)
	}
	return org, 1, n, nil
}

// scanOrgRoots stores each org root once, typed aws:organizations:root, a
// page at a time, as the hierarchy parent of top-level OUs and accounts, and
// hands the stored roots to the OU walk (orgTree.stored). A root is created
// with the org and undeletable while it exists, but not provider-managed:
// EnablePolicyType and DisablePolicyType set which policy types apply at it.
func scanOrgRoots(ctx context.Context, s *orgScan, p *phaseTotals) (storeWriteFailed bool) {
	var ids orgRowIDs
	return storeTokenPagesTagsWith(ctx, s.st, s.acct, s.region, "organizations:ListRoots",
		func(ctx context.Context, token *string) ([]types.Root, *string, error) {
			out, err := s.client.ListRoots(ctx, &organizations.ListRootsInput{NextToken: token, MaxResults: sdkaws.Int32(orgPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Roots, out.NextToken, nil
		},
		func(root types.Root) (*store.Resource, string, string) {
			if sv(root.Arn) == "" {
				// No ARN means no row, but the root's OUs and accounts carry
				// their own ARNs: keep its Id so the walk still lists them.
				if id := sv(root.Id); id != "" {
					s.tree.next = append(s.tree.next, childParent{id: id})
				}
				return nil, "roots have no Arn to identify them", cmp.Or(sv(root.Id), noIdentifier)
			}
			return s.addID(&ids, &store.Resource{
				Provider:       "aws",
				AccountID:      s.acct.ID,
				AccountName:    &s.acct.Name,
				Region:         regionGlobal,
				Type:           TypeOrganizationsRoot,
				NativeID:       sv(root.Arn),
				Name:           root.Name,
				AttributesJSON: mustJSON(root),
				DiscoveredBy:   s.scanID,
			}, sv(root.Id), ""), "", ""
		},
		// Only stored roots are linked and walked as parents, so a failed
		// write leaves no closure rows naming a missing root.
		tokenPageSteps{stored: func(rows []*store.Resource) {
			for _, r := range rows {
				s.tree.stored(r, ids.take(r).id, s.tree.orgID)
			}
		}},
		s.tagsFor(&ids), p)
}

// scanOrgOUTree walks the OU tree one level at a time from the roots: each
// level lists the child OUs of every parent of the level before it,
// concurrently, and writes them a page at a time
// (storeEachParentPagesTagsWith). One parent's failure costs no other its
// OUs; a parent deleted since it was listed (ParentNotFoundException, the
// op's modeled not-found) is skipped. Only stored OUs are linked and walked,
// so a failed write leaves no closure rows naming a missing OU. The denied
// parents of every level are one warning, sent once the walk's writes all
// succeeded.
func scanOrgOUTree(ctx context.Context, s *orgScan, p *phaseTotals) (storeWriteFailed bool) {
	var denied skipTally
	// Runs on the parents' concurrent reads, before the helper's own
	// per-level denial check.
	deniedParent := func(parent string, err error) bool {
		if !isAccessDenied(err) {
			return false
		}
		denied.skippedLeast(parent, err)
		return true
	}
	parents := s.tree.next
	for len(parents) > 0 {
		s.tree.next = nil
		parentID := make(map[string]string, len(parents))
		for _, pr := range parents {
			parentID[pr.id] = store.ResourceID("aws", s.acct.ID, pr.arn)
		}
		var ids orgRowIDs
		if storeEachParentPagesTagsWith(ctx, s.st, s.acct, s.region, orgListOUsOp, []string{"ParentNotFoundException"}, parents,
			func(ctx context.Context, parent string, token *string) ([]types.OrganizationalUnit, *string, error) {
				out, err := s.client.ListOrganizationalUnitsForParent(ctx, &organizations.ListOrganizationalUnitsForParentInput{
					ParentId: &parent, NextToken: token, MaxResults: sdkaws.Int32(orgPageCap),
				})
				if err != nil {
					return nil, nil, err
				}
				return out.OrganizationalUnits, out.NextToken, nil
			},
			func(parent string, ou types.OrganizationalUnit) (*store.Resource, string, string) {
				r, why, key := arnRow("organizational units", ou.Arn, ou.Name, &store.Resource{
					Provider:       "aws",
					AccountID:      s.acct.ID,
					AccountName:    &s.acct.Name,
					Region:         regionGlobal,
					Type:           TypeOrganizationsOU,
					NativeID:       sv(ou.Arn),
					Name:           ou.Name,
					AttributesJSON: mustJSON(ou),
					DiscoveredBy:   s.scanID,
				})
				if r != nil {
					s.addID(&ids, r, sv(ou.Id), parent)
				}
				return r, why, key
			},
			deniedParent,
			func(rows []*store.Resource) {
				for _, r := range rows {
					got := ids.take(r)
					s.tree.stored(r, got.id, parentID[got.parent])
				}
			},
			s.tagsFor(&ids), p) {
			return true
		}
		// Pages of different parents are stored in no fixed order; sorted,
		// the next level's warnings do not depend on it.
		parents = slices.SortedFunc(slices.Values(s.tree.next), func(a, b childParent) int { return strings.Compare(a.id, b.id) })
	}
	denied.warnDeniedIn(s.st, orgListOUsOp, s.acct, s.region, "parent(s)")
	return false
}

// orgAccountParent is what ListParents said about one account: its parent's
// ARN, or the tally of why it has none, with the error.
type orgAccountParent struct {
	arn   string
	err   error
	tally *skipTally
}

// scanOrgAccounts stores the organization's accounts a page at a time, each
// linked under the parent ListParents names when that parent was stored this
// scan. Whatever ListParents returns, the listed account is stored; a failed
// read only leaves it unlinked, warned about once per cause, except an error
// of no known cause, which is returned. The warnings count only accounts
// stored.
func scanOrgAccounts(ctx context.Context, s *orgScan, p *phaseTotals) (storeWriteFailed bool) {
	var (
		ids orgRowIDs
		// Filled while a page's rows are built and emptied once it is
		// written, both on this goroutine.
		parents                                  = map[*store.Resource]orgAccountParent{}
		denied, transient, gone, notStored, noID skipTally
		hard                                     []error
	)
	parentOf := func(id string) orgAccountParent {
		if id == "" {
			return orgAccountParent{tally: &noID}
		}
		arn, err := firstParentARN(ctx, s.client, id, s.tree.arnByID)
		got := orgAccountParent{arn: arn, err: err}
		switch {
		case err == nil:
		// ChildNotFoundException, in the op's model: the account left the
		// organization after ListAccounts.
		case isAPIErrorCode(err, "ChildNotFoundException"):
			got.tally = &gone
		case errors.Is(err, errParentNotStored):
			got.tally = &notStored
		case isTransientNetworkError(err):
			got.tally = &transient
		case isAccessDenied(err):
			got.tally = &denied
		default:
			hard = append(hard, err)
		}
		return got
	}
	storeWriteFailed = storeTokenPagesTagsWith(ctx, s.st, s.acct, s.region, "organizations:ListAccounts",
		func(ctx context.Context, token *string) ([]types.Account, *string, error) {
			out, err := s.client.ListAccounts(ctx, &organizations.ListAccountsInput{NextToken: token, MaxResults: sdkaws.Int32(orgPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Accounts, out.NextToken, nil
		},
		func(a types.Account) (*store.Resource, string, string) {
			status := string(a.Status)
			r, why, key := arnRow("accounts", a.Arn, a.Name, &store.Resource{
				Provider:       "aws",
				AccountID:      s.acct.ID,
				AccountName:    &s.acct.Name,
				Region:         regionGlobal,
				Type:           TypeOrganizationsAccount,
				NativeID:       sv(a.Arn),
				Name:           a.Name,
				Status:         &status,
				CreatedAt:      tp(a.JoinedTimestamp),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   s.scanID,
			})
			if r != nil {
				s.addID(&ids, r, sv(a.Id), "")
				parents[r] = parentOf(sv(a.Id))
			}
			return r, why, key
		},
		// Linked only once stored, so a failed write leaves no closure rows
		// naming a missing account.
		tokenPageSteps{stored: func(rows []*store.Resource) {
			for _, r := range rows {
				ids.take(r)
				switch got := parents[r]; {
				case got.arn != "":
					s.tree.pairs = append(s.tree.pairs, [2]string{store.ResourceID("aws", s.acct.ID, r.NativeID), store.ResourceID("aws", s.acct.ID, got.arn)})
				case got.tally != nil:
					got.tally.skipped(r.NativeID, got.err)
				}
			}
			clear(parents)
		}},
		s.tagsFor(&ids), p)
	p.errs.add(orgParentsOp, errors.Join(hard...))
	if storeWriteFailed {
		return true
	}
	if transient.count > 0 {
		_ = skipIfTransient(s.st, orgParentsOp, s.acct.ID, s.region, fmt.Errorf(
			"%d account(s) stored without their parent, first %s: %w", transient.count, transient.first, transient.err))
	}
	if denied.count > 0 {
		_ = skipIfAccessDenied(s.st, orgParentsOp, s.acct.ID, s.region, fmt.Errorf(
			"%d account(s) stored without their parent, first %s: %w", denied.count, denied.first, denied.err))
	}
	for _, w := range []struct {
		t   *skipTally
		why string
	}{
		{&gone, fmt.Sprintf("they left the organization after ListAccounts; first %s: %v", gone.first, gone.err)},
		{&notStored, fmt.Sprintf("%v; first %s", errParentNotStored, notStored.first)},
		{&noID, "listed without an Id; first " + noID.first},
	} {
		if w.t.count > 0 {
			s.st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: orgParentsOp, Scope: s.acct.ID + "/" + s.region,
				Message: fmt.Sprintf("%d account(s) stored without their parent: %s", w.t.count, w.why),
			})
		}
	}
	return false
}

// scanOrgSCPs stores the SCPs a page at a time, each as its DescribePolicy
// body (Content + PolicySummary). A policy DescribePolicy no longer finds
// (PolicyNotFoundException, its modeled not-found) was deleted since the
// listing and is not stored; any other failure keeps the listed summary as
// the row: a denial is warned about once, any other error returned
// (tokenPageSteps.details). SCPs have no hierarchy parent — the resolver
// wires SCP → root/OU/account targets.
func scanOrgSCPs(ctx context.Context, s *orgScan, p *phaseTotals) (storeWriteFailed bool) {
	var ids orgRowIDs
	return storeTokenPagesTagsWith(ctx, s.st, s.acct, s.region, "organizations:ListPolicies",
		func(ctx context.Context, token *string) ([]types.PolicySummary, *string, error) {
			out, err := s.client.ListPolicies(ctx, &organizations.ListPoliciesInput{
				Filter: types.PolicyTypeServiceControlPolicy, NextToken: token, MaxResults: sdkaws.Int32(orgPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Policies, out.NextToken, nil
		},
		func(ps types.PolicySummary) (*store.Resource, string, string) {
			r, why, key := arnRow("policies", ps.Arn, ps.Name, &store.Resource{
				Provider:       "aws",
				AccountID:      s.acct.ID,
				AccountName:    &s.acct.Name,
				Region:         regionGlobal,
				Type:           TypeOrganizationsSCP,
				NativeID:       sv(ps.Arn),
				Name:           ps.Name,
				AttributesJSON: mustJSON(ps),
				DiscoveredBy:   s.scanID,
				// AwsManaged marks an AWS-owned policy (p-FullAWSAccess, the
				// default SCP attached to every root/OU/account on org
				// creation), which cannot be edited.
				ManagedByProvider: ps.AwsManaged,
			})
			if r != nil {
				s.addID(&ids, r, sv(ps.Id), "")
			}
			return r, why, key
		},
		tokenPageSteps{
			details: rowDetailReader{
				op: "organizations:DescribePolicy", what: "policies", notFound: []string{"PolicyNotFoundException"},
				get: func(ctx context.Context, r *store.Resource) (any, error) {
					id := ids.get(r).id
					if id == "" {
						return nil, nil
					}
					out, err := s.client.DescribePolicy(ctx, &organizations.DescribePolicyInput{PolicyId: &id})
					if err != nil || out.Policy == nil {
						return nil, err
					}
					return out.Policy, nil
				},
			},
			stored: func(rows []*store.Resource) {
				for _, r := range rows {
					ids.take(r)
				}
			},
		},
		s.tagsFor(&ids), p)
}

// scanOrganizationsResourcePolicy captures the organization-level resource
// policy (singleton), stored under its ARN; a policy described without one is
// not stored and is warned about once, as for roots, OUs, accounts, policies
// and transfers.
func scanOrganizationsResourcePolicy(ctx context.Context, s *orgScan, p *phaseTotals) (storeWriteFailed bool) {
	const op = "organizations:DescribeResourcePolicy"
	out, err := s.client.DescribeResourcePolicy(ctx, &organizations.DescribeResourcePolicyInput{})
	if isAPIErrorCode(err, "ResourcePolicyNotFoundException", "AWSOrganizationsNotInUseException") {
		return false
	}
	var (
		ids   orgRowIDs
		rows  []*store.Resource
		skips notStoredSkips
	)
	if err == nil && out.ResourcePolicy != nil && out.ResourcePolicy.ResourcePolicySummary != nil {
		summary := out.ResourcePolicy.ResourcePolicySummary
		label := s.acct.ID
		r, why, key := arnRow("resource policies", summary.Arn, summary.Id, &store.Resource{
			Provider: "aws", AccountID: s.acct.ID, AccountName: &s.acct.Name,
			Type: TypeOrganizationsResourcePolicy, NativeID: sv(summary.Arn),
			Name: &label, Region: regionGlobal,
			AttributesJSON: mustJSON(out.ResourcePolicy), DiscoveredBy: s.scanID,
		})
		if r == nil {
			skips.skipped(why, key)
		} else {
			rows = append(rows, s.addID(&ids, r, sv(summary.Id), ""))
		}
	}
	return storeRowsTags(ctx, s.st, s.acct, s.region, op, rows, err, func() {
		skips.warn(s.st, op, s.acct, s.region)
	}, s.tagsFor(&ids), p)
}

// firstParentARN resolves an account's first parent via ListParents, mapping
// the native parent id to its ARN via arnByID (populated during the OU walk).
// Returns errParentNotStored if no listed parent was stored this scan.
func firstParentARN(ctx context.Context, client organizationsAPI, accountID string, arnByID map[string]string) (string, error) {
	out, err := client.ListParents(ctx, &organizations.ListParentsInput{ChildId: &accountID})
	if err != nil {
		return "", fmt.Errorf("organizations:ListParents %s: %w", accountID, err)
	}
	for _, p := range out.Parents {
		if arn, ok := arnByID[sv(p.Id)]; ok {
			return arn, nil
		}
	}
	return "", fmt.Errorf("organizations:ListParents %s: %w", accountID, errParentNotStored)
}

// errParentNotStored marks an account whose listed parent this scan did not
// store (a root without an Arn, or an OU whose listing or write failed), so
// it is stored unlinked.
var errParentNotStored = errors.New("its parent root or OU was not stored this scan")
