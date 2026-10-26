package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
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
}

// scanOrganizations discovers the AWS Organizations structure — organization,
// roots, OUs, accounts, and SCPs. Only callable from the management account;
// AccessDenied from member accounts means "not the payer" and is silently
// skipped so member-account scans stay clean.
func scanOrganizations(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := organizations.NewFromConfig(acct.cfg)

	org, t, i, ferr := scanOrgRoot(ctx, client, acct, st, scanID)
	total += t
	inserted += i
	if ferr != nil || org == nil {
		return total, inserted, ferr
	}
	// Member account: org row persisted; management-only phases skipped.
	if mgmt := sv(org.MasterAccountId); mgmt != "" && mgmt != acct.ID {
		return total, inserted, nil
	}
	orgID := store.ResourceID("aws", acct.ID, sv(org.Arn))
	closurePairs := [][2]string{{orgID, orgID}}

	// Every phase runs whatever the others return, so one failure costs no
	// other phase its rows, and the closure links whatever the tree phases
	// stored. The OU walk starts from the roots listed before any failure.
	var errs phaseErrs
	add := func(op string, t, i int, err error) {
		total += t
		inserted += i
		errs.add(op, err)
	}
	rootIDs, ouARNByNativeID, t, i, ferr := scanOrgRoots(ctx, client, acct, st, scanID, orgID, &closurePairs)
	add("organizations:ListRoots", t, i, ferr)
	t, i, ferr = scanOrgOUTree(ctx, client, acct, st, scanID, rootIDs, ouARNByNativeID, &closurePairs)
	add("organizations:ListOrganizationalUnitsForParent", t, i, ferr)
	t, i, ferr = scanOrgAccounts(ctx, client, acct, st, scanID, ouARNByNativeID, &closurePairs)
	add("organizations:ListAccounts", t, i, ferr)
	t, i, ferr = scanOrgSCPs(ctx, client, acct, st, scanID)
	add("organizations:ListPolicies", t, i, ferr)
	if err := st.RecordHierarchyBatch(closurePairs); err != nil {
		errs.add("organizations hierarchy closure", fmt.Errorf("org hierarchy closure: %w", err))
	}
	t, i, ferr = scanOrganizationsResourcePolicy(ctx, client, acct, st, scanID)
	add("organizations:DescribeResourcePolicy", t, i, ferr)
	t, i = scanOrgResponsibilityTransfers(ctx, client, acct, st, scanID, &errs)
	total += t
	inserted += i
	return total, inserted, errs.join(st, acct, "")
}

// scanOrgResponsibilityTransfers captures billing responsibility transfers
// from both inbound and outbound lists (a transfer can appear in both),
// deduped by ARN/id. Only the BILLING transfer type is API-supported.
//
// A list that fails or ends early keeps the transfers read before it, and the
// other list still runs. Each failure is added to errs under its own op, so a
// demoted transient one is warned about under the list that failed.
func scanOrgResponsibilityTransfers(ctx context.Context, client organizationsAPI, acct *account, st *store.Store, scanID string, errs *phaseErrs) (int, int) {
	seen := map[string]bool{}
	var batch []*store.Resource
	add := func(transfers []types.ResponsibilityTransfer) {
		for _, tr := range transfers {
			arn := sv(tr.Arn)
			if arn == "" {
				if id := sv(tr.Id); id != "" {
					arn = arnFor(acct.partition(), "organizations", "", acct.ID, "responsibilitytransfer/"+id)
				}
			}
			if arn == "" || seen[arn] {
				continue
			}
			seen[arn] = true
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Region: regionGlobal, Type: TypeOrganizationsResponsibilityTransfer, NativeID: arn,
				Name: tr.Name, AttributesJSON: mustJSON(tr), DiscoveredBy: scanID,
			})
		}
	}

	// Inbound.
	var inToken *string
	for {
		out, err := client.ListInboundResponsibilityTransfers(ctx, &organizations.ListInboundResponsibilityTransfersInput{
			Type: types.ResponsibilityTransferTypeBilling, NextToken: inToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "organizations:ListInboundResponsibilityTransfers", acct.ID, "", err)
			} else {
				errs.add("organizations:ListInboundResponsibilityTransfers", fmt.Errorf("organizations:ListInboundResponsibilityTransfers: %w", err))
			}
			break
		}
		add(out.ResponsibilityTransfers)
		if last, err := lastPage(inToken, out.NextToken); last {
			if err != nil {
				errs.add("organizations:ListInboundResponsibilityTransfers", fmt.Errorf("organizations:ListInboundResponsibilityTransfers: %w", err))
			}
			break
		}
		inToken = out.NextToken
	}

	// Outbound.
	var outToken *string
	for {
		out, err := client.ListOutboundResponsibilityTransfers(ctx, &organizations.ListOutboundResponsibilityTransfersInput{
			Type: types.ResponsibilityTransferTypeBilling, NextToken: outToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "organizations:ListOutboundResponsibilityTransfers", acct.ID, "", err)
			} else {
				errs.add("organizations:ListOutboundResponsibilityTransfers", fmt.Errorf("organizations:ListOutboundResponsibilityTransfers: %w", err))
			}
			break
		}
		add(out.ResponsibilityTransfers)
		if last, err := lastPage(outToken, out.NextToken); last {
			if err != nil {
				errs.add("organizations:ListOutboundResponsibilityTransfers", fmt.Errorf("organizations:ListOutboundResponsibilityTransfers: %w", err))
			}
			break
		}
		outToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "organizations responsibility-transfers")
	errs.add("organizations responsibility-transfers upsert", err)
	return total, inserted
}

// scanOrgRoot describes the organization, upserts the org resource, and
// returns the SDK struct so the caller can route on MasterAccountId. Returns
// (nil, ...) if the account isn't in an org or the call is service-disabled /
// access-denied.
func scanOrgRoot(ctx context.Context, client *organizations.Client, acct *account, st *store.Store, scanID string) (*types.Organization, int, int, error) {
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

// scanOrgRoots upserts each org root once, typed aws:organizations:root, as
// the hierarchy parent of top-level OUs and accounts. Returns the stored roots'
// native ids + an ARN-by-id map populated for roots only; later phases extend
// it with deeper OUs. A root is created with the org and undeletable while it
// exists, but not provider-managed: EnablePolicyType and DisablePolicyType set
// which policy types apply at it.
func scanOrgRoots(ctx context.Context, client organizationsAPI, acct *account, st *store.Store, scanID, orgID string, closurePairs *[][2]string) ([]string, map[string]string, int, int, error) {
	var rootIDs []string
	ouARNByNativeID := map[string]string{}
	var (
		total, inserted int
		noARN           skipTally
	)
	defer func() {
		warnNotStored(st, "organizations:ListRoots", acct, "global", &noARN, "roots without Arn, the NativeID")
	}()
	pager := organizations.NewListRootsPaginator(client, &organizations.ListRootsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return rootIDs, ouARNByNativeID, total, inserted, skipIfAccessDenied(st, "organizations:ListRoots", acct.ID, "", err)
			}
			return rootIDs, ouARNByNativeID, total, inserted, fmt.Errorf("organizations:ListRoots: %w", err)
		}
		var (
			batch    []*store.Resource
			pageRoot []types.Root
		)
		for _, root := range page.Roots {
			arn := sv(root.Arn)
			id := sv(root.Id)
			// No ARN means no row, but the root's OUs and accounts carry
			// their own ARNs: keep its Id so the walk still lists them.
			if arn == "" {
				noARN.skipped(id, nil)
				if id != "" {
					rootIDs = append(rootIDs, id)
				}
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeOrganizationsRoot,
				NativeID:       arn,
				Name:           root.Name,
				AttributesJSON: mustJSON(root),
				DiscoveredBy:   scanID,
			})
			pageRoot = append(pageRoot, root)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return rootIDs, ouARNByNativeID, total, inserted, fmt.Errorf("upsert roots: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		// Only stored roots are linked and walked as parents, so a failed
		// write leaves no closure rows naming a missing root.
		for _, root := range pageRoot {
			arn, id := sv(root.Arn), sv(root.Id)
			rootIDs = append(rootIDs, id)
			ouARNByNativeID[id] = arn
			*closurePairs = append(*closurePairs, [2]string{store.ResourceID("aws", acct.ID, arn), orgID})
		}
	}
	return rootIDs, ouARNByNativeID, total, inserted, nil
}

func scanOrgOUTree(ctx context.Context, client *organizations.Client, acct *account, st *store.Store, scanID string, rootIDs []string, ouARNByNativeID map[string]string, closurePairs *[][2]string) (int, int, error) {
	var total, inserted int
	for _, rootNativeID := range rootIDs {
		ouTotal, ouInserted, err := walkOUs(ctx, client, acct, scanID, st, rootNativeID, ouARNByNativeID[rootNativeID], ouARNByNativeID, closurePairs)
		if err != nil {
			return total, inserted, err
		}
		total += ouTotal
		inserted += ouInserted
	}
	return total, inserted, nil
}

func scanOrgAccounts(ctx context.Context, client *organizations.Client, acct *account, st *store.Store, scanID string, ouARNByNativeID map[string]string, closurePairs *[][2]string) (int, int, error) {
	var (
		total, inserted int
		parentDenied    skipTally
		parentTransient skipTally
		parentGone      skipTally
		parentNotStored skipTally
		failed          phaseErrs
	)
	defer func() {
		if parentTransient.count > 0 {
			_ = skipIfTransient(st, "organizations:ListParents", acct.ID, "", fmt.Errorf(
				"%d account(s) stored without their parent, first %s: %w", parentTransient.count, parentTransient.first, parentTransient.err))
		}
		if parentDenied.count > 0 {
			_ = skipIfAccessDenied(st, "organizations:ListParents", acct.ID, "", fmt.Errorf(
				"%d account(s) stored without their parent, first %s: %w", parentDenied.count, parentDenied.first, parentDenied.err))
		}
		if parentGone.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: "organizations:ListParents", Scope: acct.ID,
				Message: fmt.Sprintf("%d account(s) stored without their parent: they left the organization after ListAccounts; first %s: %v",
					parentGone.count, parentGone.first, parentGone.err),
			})
		}
		if parentNotStored.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: "organizations:ListParents", Scope: acct.ID,
				Message: fmt.Sprintf("%d account(s) stored without their parent: %v; first %s",
					parentNotStored.count, errParentNotStored, parentNotStored.first),
			})
		}
	}()
	pager := organizations.NewListAccountsPaginator(client, &organizations.ListAccountsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "organizations:ListAccounts", acct.ID, "", err)
			}
			return total, inserted, fmt.Errorf("organizations:ListAccounts: %w", err)
		}
		var (
			batch     []*store.Resource
			pagePairs [][2]string
			// Tallied only once the page is stored, since the warnings say
			// these accounts were stored.
			pageUnlinked []struct {
				arn   string
				err   error
				tally *skipTally
			}
		)
		for _, a := range page.Accounts {
			arn := sv(a.Arn)
			parentARN, err := firstParentARN(ctx, client, sv(a.Id), ouARNByNativeID)
			// Whatever ListParents returned, the listed account is stored; a
			// failed read only leaves it unlinked.
			var tally *skipTally
			switch {
			case err == nil:
			// ChildNotFoundException, in the op's model: the account left the
			// organization after ListAccounts.
			case isAPIErrorCode(err, "ChildNotFoundException"):
				tally = &parentGone
			case errors.Is(err, errParentNotStored):
				tally = &parentNotStored
			case isTransientNetworkError(err):
				tally = &parentTransient
			case isAccessDenied(err):
				tally = &parentDenied
			default:
				failed.add("organizations:ListParents", err)
			}
			if tally != nil {
				pageUnlinked = append(pageUnlinked, struct {
					arn   string
					err   error
					tally *skipTally
				}{arn, err, tally})
			}
			if parentARN != "" {
				pid := store.ResourceID("aws", acct.ID, parentARN)
				accID := store.ResourceID("aws", acct.ID, arn)
				pagePairs = append(pagePairs, [2]string{accID, pid})
			}
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeOrganizationsAccount,
				NativeID:       arn,
				Name:           a.Name,
				Status:         &status,
				CreatedAt:      tp(a.JoinedTimestamp),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, errors.Join(failed.join(st, acct, ""), fmt.Errorf("upsert org accounts: %w", err))
			}
			total += len(batch)
			inserted += n
		}
		// Linked only once stored, so a failed write leaves no closure rows
		// naming a missing account.
		*closurePairs = append(*closurePairs, pagePairs...)
		for _, u := range pageUnlinked {
			u.tally.skipped(u.arn, u.err)
		}
	}
	return total, inserted, failed.join(st, acct, "")
}

// scanOrgSCPs lists SCP summaries, fans out DescribePolicy to fetch each
// policy's Content, and upserts the rows. SCPs have no hierarchy parent —
// resolver wires SCP → root/OU/account targets.
func scanOrgSCPs(ctx context.Context, client *organizations.Client, acct *account, st *store.Store, scanID string) (int, int, error) {
	var summaries []types.PolicySummary
	pager := organizations.NewListPoliciesPaginator(client, &organizations.ListPoliciesInput{
		Filter: types.PolicyTypeServiceControlPolicy,
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "organizations:ListPolicies", acct.ID, "", err)
			}
			return 0, 0, fmt.Errorf("organizations:ListPolicies: %w", err)
		}
		summaries = append(summaries, page.Policies...)
	}
	if len(summaries) == 0 {
		return 0, 0, nil
	}
	policies, err := describeSCPs(ctx, client, summaries, acct, st)
	if err != nil {
		return 0, 0, err
	}
	var batch []*store.Resource
	for _, p := range policies {
		if p == nil || p.PolicySummary == nil {
			continue
		}
		arn := sv(p.PolicySummary.Arn)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Region:         regionGlobal,
			Type:           TypeOrganizationsSCP,
			NativeID:       arn,
			Name:           p.PolicySummary.Name,
			AttributesJSON: mustJSON(p),
			DiscoveredBy:   scanID,
			// p-FullAWSAccess is the AWS-default SCP attached to every
			// root/OU/account on org creation.
			ManagedByProvider: sv(p.PolicySummary.Id) == "p-FullAWSAccess",
		})
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert SCPs: %w", err)
	}
	return len(batch), n, nil
}

// scanOrganizationsResourcePolicy captures the organization-level resource
// policy (singleton). Synth ARN: arn:{p}:organizations::{a}:resourcepolicy.
func scanOrganizationsResourcePolicy(ctx context.Context, client organizationsAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeResourcePolicy(ctx, &organizations.DescribeResourcePolicyInput{})
	switch {
	case err == nil:
	case isAPIErrorCode(err, "ResourcePolicyNotFoundException", "AWSOrganizationsNotInUseException"):
		return 0, 0, nil
	case isAccessDenied(err):
		return 0, 0, skipIfAccessDenied(st, "organizations:DescribeResourcePolicy", acct.ID, "", err)
	default:
		return 0, 0, fmt.Errorf("organizations:DescribeResourcePolicy: %w", err)
	}
	if out.ResourcePolicy == nil || out.ResourcePolicy.ResourcePolicySummary == nil {
		return 0, 0, nil
	}
	arn := sv(out.ResourcePolicy.ResourcePolicySummary.Arn)
	if arn == "" {
		arn = arnFor(acct.partition(), "organizations", "", acct.ID, "resourcepolicy")
	}
	label := acct.ID
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeOrganizationsResourcePolicy, NativeID: arn,
		Name: &label, Region: regionGlobal,
		AttributesJSON: mustJSON(out.ResourcePolicy), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "organizations resource-policy")
}

// walkOUs recursively walks children of parentNativeID, upserting each OU
// and accumulating closure pairs. parentARN is the parent's (root or OU) ARN,
// used to compute its stable ResourceID.
func walkOUs(
	ctx context.Context,
	client organizationsAPI,
	acct *account,
	scanID string,
	st *store.Store,
	parentNativeID, parentARN string,
	arnByID map[string]string,
	closurePairs *[][2]string,
) (total, inserted int, err error) {
	pager := organizations.NewListOrganizationalUnitsForParentPaginator(client, &organizations.ListOrganizationalUnitsForParentInput{
		ParentId: &parentNativeID,
	})
	var children []types.OrganizationalUnit
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "organizations:ListOrganizationalUnitsForParent", acct.ID, "", err)
			}
			return total, inserted, fmt.Errorf("organizations:ListOrganizationalUnitsForParent %s: %w", parentNativeID, err)
		}
		children = append(children, page.OrganizationalUnits...)
	}
	parentResID := store.ResourceID("aws", acct.ID, parentARN)

	var (
		batch []*store.Resource
		pairs [][2]string
	)
	for _, ou := range children {
		arn := sv(ou.Arn)
		r := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Region:         regionGlobal,
			Type:           TypeOrganizationsOU,
			NativeID:       arn,
			Name:           ou.Name,
			AttributesJSON: mustJSON(ou),
			DiscoveredBy:   scanID,
		}
		batch = append(batch, r)
		childResID := store.ResourceID("aws", acct.ID, arn)
		pairs = append(pairs, [2]string{childResID, parentResID})
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert OUs: %w", err)
		}
		total += len(batch)
		inserted += n
	}
	// Only stored OUs are linked and resolve their accounts' parents, so a
	// failed write leaves no closure rows naming a missing OU.
	*closurePairs = append(*closurePairs, pairs...)
	for _, ou := range children {
		arnByID[sv(ou.Id)] = sv(ou.Arn)
	}
	for _, ou := range children {
		t2, i2, err := walkOUs(ctx, client, acct, scanID, st, sv(ou.Id), sv(ou.Arn), arnByID, closurePairs)
		if err != nil {
			return total, inserted, err
		}
		total += t2
		inserted += i2
	}
	return total, inserted, nil
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

// describeSCPs fans out DescribePolicy across the listed SCP summaries to
// fetch each policy's full body (Content + PolicySummary). Per-policy
// AccessDenied is tolerated via skipIfAccessDenied (warn + continue) so one
// permission gap doesn't drop sibling SCPs. Concurrency bounded by fanoutMed.
func describeSCPs(ctx context.Context, client organizationsAPI, summaries []types.PolicySummary, acct *account, st *store.Store) ([]*types.Policy, error) {
	out := make([]*types.Policy, len(summaries))
	sem := semaphore.NewWeighted(fanoutMed)
	g, gctx := errgroup.WithContext(ctx)
	for i, s := range summaries {
		if s.Id == nil {
			continue
		}
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			resp, err := client.DescribePolicy(gctx, &organizations.DescribePolicyInput{PolicyId: s.Id})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "organizations:DescribePolicy", acct.ID, sv(s.Id), err)
					return nil
				}
				return fmt.Errorf("organizations:DescribePolicy %s: %w", sv(s.Id), err)
			}
			if resp != nil && resp.Policy != nil {
				out[i] = resp.Policy
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}
