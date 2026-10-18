package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/wellarchitected"
	wellarchitectedtypes "github.com/aws/aws-sdk-go-v2/service/wellarchitected/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWellArchitectedWorkload, Service: "wellarchitected"})
	// CreatedAt/UpdatedAt qualify as volatile under the region-collision
	// reason: an AWS-owned lens ARN carries no region
	// (arn:aws:wellarchitected::aws:lens/...), so every region reports the same
	// natural key with that region's own rollout stamps and the regions
	// version-split each other within a single scan.
	//
	// A customer-owned lens IS region-qualified, so it never collides -- and it
	// pays for this, losing two real fields to a rule it did not need, because
	// volatile rules are per type and both lens kinds share one. Accepted: an
	// AWS-owned lens is the overwhelming majority and its stamps describe AWS's
	// rollout rather than anything the customer did. scanWALenses already tells
	// the two apart per row (ManagedByProvider), so a per-row rule is the fix if
	// a customer ever needs those fields. See TestVolatileRulesAreJustified.
	registerType(restype.Descriptor{Type: TypeWellArchitectedLens, Service: "wellarchitected", Volatile: []string{"CreatedAt", "UpdatedAt"}})
	registerType(restype.Descriptor{Type: TypeWellArchitectedProfile, Service: "wellarchitected"})
	registerType(restype.Descriptor{Type: TypeWellArchitectedReviewTemplate, Service: "wellarchitected"})
	registerType(restype.Descriptor{Type: TypeWellArchitectedWorkloadShare, Service: "wellarchitected"})
	registerType(restype.Descriptor{Type: TypeWellArchitectedLensReview, Service: "wellarchitected"})
	registerType(restype.Descriptor{Type: TypeWellArchitectedLensShare, Service: "wellarchitected"})
	registerType(restype.Descriptor{Type: TypeWellArchitectedProfileShare, Service: "wellarchitected"})
	registerType(restype.Descriptor{Type: TypeWellArchitectedTemplateShare, Service: "wellarchitected"})
	registerType(restype.Descriptor{Type: TypeWellArchitectedAgentProfile, Service: "wellarchitected"})
	// Title, Description and the context's ApplicationOverview, Industry,
	// ArchitectureOverview and AdditionalContext are @sensitive free text, stored
	// on purpose: they carry no credential by contract, and redaction is for
	// credentials.
	registerType(restype.Descriptor{Type: TypeWellArchitectedAgentContext, Service: "wellarchitected"})
	registerType(restype.Descriptor{Type: TypeWellArchitectedAgentGoal, Service: "wellarchitected"})
	registerService(serviceEntry{
		name: "aws:wellarchitected",
		fn:   scanWellArchitected,
	})
}

type wellArchitectedAPI interface {
	ListWorkloads(context.Context, *wellarchitected.ListWorkloadsInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListWorkloadsOutput, error)
	ListLenses(context.Context, *wellarchitected.ListLensesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListLensesOutput, error)
	ListProfiles(context.Context, *wellarchitected.ListProfilesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListProfilesOutput, error)
	ListReviewTemplates(context.Context, *wellarchitected.ListReviewTemplatesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListReviewTemplatesOutput, error)
	ListWorkloadShares(context.Context, *wellarchitected.ListWorkloadSharesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListWorkloadSharesOutput, error)
	ListLensReviews(context.Context, *wellarchitected.ListLensReviewsInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListLensReviewsOutput, error)
	ListLensShares(context.Context, *wellarchitected.ListLensSharesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListLensSharesOutput, error)
	ListProfileShares(context.Context, *wellarchitected.ListProfileSharesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListProfileSharesOutput, error)
	ListTemplateShares(context.Context, *wellarchitected.ListTemplateSharesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListTemplateSharesOutput, error)
	ListAgentProfiles(context.Context, *wellarchitected.ListAgentProfilesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListAgentProfilesOutput, error)
	ListAgentContexts(context.Context, *wellarchitected.ListAgentContextsInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListAgentContextsOutput, error)
	ListAgentGoals(context.Context, *wellarchitected.ListAgentGoalsInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListAgentGoalsOutput, error)
}

// waParents are the parents the list phases hand to the child phases. Shares
// are listed only on what this account owns: a workload, profile or template
// shared in from another account, and every AWS-official or shared-in lens,
// has no shares this account can list.
type waParents struct {
	workloads, ownedWorkloads, ownedLenses, ownedProfiles, ownedTemplates []childParent
}

// scanWellArchitected discovers the Well-Architected Tool resources, then the
// Well-Architected Agent's profiles and the contexts and goals of each. Every
// phase runs whatever the phases before it returned, so one phase's error
// cannot cost another's rows, and the errors are joined. The tool and profile
// errors are demoted when transient: a transient member would make the
// dispatcher read the whole join as transient and drop the other side's hard
// error and every side's counts. The context and goal phases need no
// demotion: they skip a transient parent themselves, so their errors are
// never transient.
//
// Not scanned: answers, check details/summaries, lens-review improvements and
// consolidated reports (computed from the review), milestones (point-in-time
// snapshots), profile notifications and share invitations (transient), and the
// agent's recommendations and recommendation generations.
func scanWellArchitected(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := wellarchitected.NewFromConfig(acct.cfg, func(o *wellarchitected.Options) { o.Region = region })
	total, inserted, err := scanWAToolPhases(ctx, client, acct, region, st, scanID)
	pt, pi, profiles, perr := scanWAAgentProfiles(ctx, client, acct, region, st, scanID)
	ct, ci, cerr := scanWAAgentContexts(ctx, client, profiles, acct, region, st, scanID)
	gt, gi, gerr := scanWAAgentGoals(ctx, client, profiles, acct, region, st, scanID)
	return total + pt + ct + gt, inserted + pi + ci + gi, errors.Join(
		demoteTransient(st, "wellarchitected", acct, region, err),
		demoteTransient(st, "wellarchitected:ListAgentProfiles", acct, region, perr),
		cerr, gerr,
	)
}

// scanWAToolPhases discovers workloads, lenses, profiles and review templates,
// then the lens reviews of each workload and the shares of each owned
// workload, custom lens, profile and review template. Phases run in order and
// a phase error stops them with the rows counted so far; the child phases run
// last so their errors cannot cost the parent rows.
func scanWAToolPhases(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var p waParents
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanWAWorkloads(ctx, client, acct, region, st, scanID, &p) },
		func() (int, int, error) { return scanWALenses(ctx, client, acct, region, st, scanID, &p) },
		func() (int, int, error) { return scanWAProfiles(ctx, client, acct, region, st, scanID, &p) },
		func() (int, int, error) { return scanWAReviewTemplates(ctx, client, acct, region, st, scanID, &p) },
		func() (int, int, error) { return scanWALensReviews(ctx, client, p.workloads, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanWAWorkloadShares(ctx, client, p.ownedWorkloads, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanWALensShares(ctx, client, p.ownedLenses, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanWAProfileShares(ctx, client, p.ownedProfiles, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanWATemplateShares(ctx, client, p.ownedTemplates, acct, region, st, scanID)
		},
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

func scanWAWorkloads(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string, p *waParents) (int, int, error) {
	pager := wellarchitected.NewListWorkloadsPaginator(client, &wellarchitected.ListWorkloadsInput{})
	var (
		batch      []*store.Resource
		all, owned []childParent
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wellarchitected:ListWorkloads", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wellarchitected:ListWorkloads: %w", err)
		}
		for _, w := range out.WorkloadSummaries {
			arn := sv(w.WorkloadArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedWorkload, NativeID: arn,
				Name: w.WorkloadName, Region: &region,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
			// The child list ops key on WorkloadId, not the ARN.
			id := sv(w.WorkloadId)
			if id == "" {
				continue
			}
			all = append(all, childParent{id: id, arn: arn})
			if sv(w.Owner) == acct.ID {
				owned = append(owned, childParent{id: id, arn: arn})
			}
		}
	}
	total, inserted, err := upsertBatch(st, batch, "wellarchitected workloads")
	if err != nil {
		return 0, 0, err
	}
	p.workloads, p.ownedWorkloads = all, owned
	return total, inserted, nil
}

func scanWALenses(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string, p *waParents) (int, int, error) {
	pager := wellarchitected.NewListLensesPaginator(client, &wellarchitected.ListLensesInput{})
	var (
		batch []*store.Resource
		owned []childParent
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wellarchitected:ListLenses", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wellarchitected:ListLenses: %w", err)
		}
		for _, l := range out.LensSummaries {
			arn := sv(l.LensArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedLens, NativeID: arn,
				Name: l.LensName, Region: &region,
				ManagedByProvider: sv(l.Owner) == "AWS",
				AttributesJSON:    mustJSON(l), DiscoveredBy: scanID,
			})
			// Only a custom lens this account created can be shared; ListLensShares
			// takes its ARN as the alias.
			if l.LensType == wellarchitectedtypes.LensTypeCustomSelf {
				owned = append(owned, childParent{id: arn, arn: arn})
			}
		}
	}
	total, inserted, err := upsertBatch(st, batch, "wellarchitected lenses")
	if err != nil {
		return 0, 0, err
	}
	p.ownedLenses = owned
	return total, inserted, nil
}

func scanWAProfiles(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string, p *waParents) (int, int, error) {
	pager := wellarchitected.NewListProfilesPaginator(client, &wellarchitected.ListProfilesInput{})
	var (
		batch []*store.Resource
		owned []childParent
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wellarchitected:ListProfiles", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wellarchitected:ListProfiles: %w", err)
		}
		for _, pr := range out.ProfileSummaries {
			arn := sv(pr.ProfileArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedProfile, NativeID: arn,
				Name: pr.ProfileName, Region: &region,
				AttributesJSON: mustJSON(pr), DiscoveredBy: scanID,
			})
			if sv(pr.Owner) == acct.ID {
				owned = append(owned, childParent{id: arn, arn: arn})
			}
		}
	}
	total, inserted, err := upsertBatch(st, batch, "wellarchitected profiles")
	if err != nil {
		return 0, 0, err
	}
	p.ownedProfiles = owned
	return total, inserted, nil
}

func scanWAReviewTemplates(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string, p *waParents) (int, int, error) {
	pager := wellarchitected.NewListReviewTemplatesPaginator(client, &wellarchitected.ListReviewTemplatesInput{})
	var (
		batch []*store.Resource
		owned []childParent
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wellarchitected:ListReviewTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wellarchitected:ListReviewTemplates: %w", err)
		}
		for _, r := range out.ReviewTemplates {
			arn := sv(r.TemplateArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedReviewTemplate, NativeID: arn,
				Name: r.TemplateName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
			if sv(r.Owner) == acct.ID {
				owned = append(owned, childParent{id: arn, arn: arn})
			}
		}
	}
	total, inserted, err := upsertBatch(st, batch, "wellarchitected review-templates")
	if err != nil {
		return 0, 0, err
	}
	p.ownedTemplates = owned
	return total, inserted, nil
}

// isWAParentGone matches the not-found every child list op models, returned
// when the parent was deleted between its list phase and the child call.
func isWAParentGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// scanWALensReviews lists the lens reviews of each workload: one per lens
// applied to it, created by AssociateLenses and removed by DisassociateLenses.
// It is a row rather than workload attributes because it has that lifecycle
// of its own and per-lens risk counts the workload summary only totals. The
// API gives no ARN; NativeID is {workloadARN}/lens-review/{key}, key being
// waLensReviewKey of the LensAlias GetLensReview takes.
func scanWALensReviews(ctx context.Context, client wellArchitectedAPI, workloads []childParent, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "wellarchitected:ListLensReviews", workloads, isWAParentGone,
		func(gctx context.Context, w childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := wellarchitected.NewListLensReviewsPaginator(client, &wellarchitected.ListLensReviewsInput{WorkloadId: &w.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, lr := range out.LensReviewSummaries {
					key := waLensReviewKey(sv(lr.LensAlias))
					if key == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeWellArchitectedLensReview, NativeID: w.arn + "/lens-review/" + key,
						Name: lr.LensName, Region: &region, Status: nonEmptyPtr(string(lr.LensStatus)),
						AttributesJSON: mustJSON(lr), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// waLensReviewKey returns one key per lens. For an AWS-official lens the SDK
// documents LensAlias as either the bare alias (serverless) or an ARN with no
// account (arn:aws:wellarchitected:us-east-1::lens/serverless); both become the
// bare alias, so one review keeps one NativeID across scans. A custom lens's
// alias is its ARN, which carries the owner account, and is kept whole.
func waLensReviewKey(alias string) string {
	if !strings.HasPrefix(alias, "arn:") {
		return alias
	}
	if _, name, ok := strings.Cut(alias, "::lens/"); ok {
		return name
	}
	return alias
}

// waShareRow builds one share row under its parent, or nil for a share with no
// ShareId. Shares carry no ARN; NativeID is {parentARN}/share/{ShareId}. Name
// is the grantee (an account, organization or OU id).
func waShareRow(acct *account, region, scanID, typ string, parent childParent, shareID, sharedWith *string, status wellarchitectedtypes.ShareStatus, share any) *store.Resource {
	id := sv(shareID)
	if id == "" {
		return nil
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: parent.arn + "/share/" + id,
		Name: sharedWith, Region: &region, Status: nonEmptyPtr(string(status)),
		AttributesJSON: mustJSON(share), DiscoveredBy: scanID,
	}
}

func scanWAWorkloadShares(ctx context.Context, client wellArchitectedAPI, workloads []childParent, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "wellarchitected:ListWorkloadShares", workloads, isWAParentGone,
		func(gctx context.Context, w childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := wellarchitected.NewListWorkloadSharesPaginator(client, &wellarchitected.ListWorkloadSharesInput{WorkloadId: &w.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, s := range out.WorkloadShareSummaries {
					if r := waShareRow(acct, region, scanID, TypeWellArchitectedWorkloadShare, w, s.ShareId, s.SharedWith, s.Status, s); r != nil {
						rows = append(rows, r)
					}
				}
			}
			return rows, nil
		})
}

// scanWALensShares lists the shares of each custom lens this account owns.
// AWS-official and shared-in lenses are never passed in, so a
// ValidationException here is unexpected and fails the phase.
func scanWALensShares(ctx context.Context, client wellArchitectedAPI, lenses []childParent, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "wellarchitected:ListLensShares", lenses, isWAParentGone,
		func(gctx context.Context, l childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := wellarchitected.NewListLensSharesPaginator(client, &wellarchitected.ListLensSharesInput{LensAlias: &l.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, s := range out.LensShareSummaries {
					if r := waShareRow(acct, region, scanID, TypeWellArchitectedLensShare, l, s.ShareId, s.SharedWith, s.Status, s); r != nil {
						rows = append(rows, r)
					}
				}
			}
			return rows, nil
		})
}

func scanWAProfileShares(ctx context.Context, client wellArchitectedAPI, profiles []childParent, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "wellarchitected:ListProfileShares", profiles, isWAParentGone,
		func(gctx context.Context, pr childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := wellarchitected.NewListProfileSharesPaginator(client, &wellarchitected.ListProfileSharesInput{ProfileArn: &pr.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, s := range out.ProfileShareSummaries {
					if r := waShareRow(acct, region, scanID, TypeWellArchitectedProfileShare, pr, s.ShareId, s.SharedWith, s.Status, s); r != nil {
						rows = append(rows, r)
					}
				}
			}
			return rows, nil
		})
}

func scanWATemplateShares(ctx context.Context, client wellArchitectedAPI, templates []childParent, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "wellarchitected:ListTemplateShares", templates, isWAParentGone,
		func(gctx context.Context, tm childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := wellarchitected.NewListTemplateSharesPaginator(client, &wellarchitected.ListTemplateSharesInput{TemplateArn: &tm.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, s := range out.TemplateShareSummaries {
					if r := waShareRow(acct, region, scanID, TypeWellArchitectedTemplateShare, tm, s.ShareId, s.SharedWith, s.Status, s); r != nil {
						rows = append(rows, r)
					}
				}
			}
			return rows, nil
		})
}

// waAgentPageCap is the maxResults maximum the model documents for
// ListAgentProfiles, ListAgentContexts and ListAgentGoals.
const waAgentPageCap = 1000

// isWAAgentOpGap reports the first-page errors of ListAgentProfiles that mean
// the region's endpoint does not serve it.
func isWAAgentOpGap(err error) bool {
	return isOpNotDeployed(err) || isWAUnknownOperation(err)
}

// isWAAgentChildOpGap is isWAAgentOpGap for the list ops that name a profile
// in their path. It leaves out isOpNotDeployed's bare-404 arm, which is safe
// only on a call that names no resource: here a 404 may be the profile, so an
// untyped one is returned rather than read as a gap.
func isWAAgentChildOpGap(err error) bool {
	return isAPIErrorCode(err, "UnknownOperationException") || isServiceNotAvailableInRegion(err) || isWAUnknownOperation(err)
}

// scanWAAgentProfiles lists the Well-Architected Agent's profiles: the
// customer-created scope (execution role, pillars, accounts and regions to
// analyse) the agent generates recommendations for. The agent is newer than
// the rest of the service and a region's endpoint may not serve the op, so a
// first-page isWAAgentOpGap is a silent gap. Rows read before a later failure
// are kept. It returns the profiles it stored, the parents of the context and
// goal phases; none when the store write failed.
func scanWAAgentProfiles(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	const op = "wellarchitected:ListAgentProfiles"
	pager := wellarchitected.NewListAgentProfilesPaginator(client, &wellarchitected.ListAgentProfilesInput{},
		func(o *wellarchitected.ListAgentProfilesPaginatorOptions) {
			o.Limit = waAgentPageCap
			o.StopOnDuplicateToken = true
		})
	var (
		batch    []*store.Resource
		profiles []childParent
		listErr  error
	)
	for firstPage := true; pager.HasMorePages(); firstPage = false {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if firstPage && isWAAgentOpGap(err) {
				return 0, 0, nil, nil
			}
			listErr = err
			break
		}
		for _, pr := range out.Items {
			arn := sv(pr.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedAgentProfile, NativeID: arn,
				Name: pr.Name, Region: &region, CreatedAt: tp(pr.CreatedAt),
				TagsJSON:       awsTagsJSON(pr.Tags),
				AttributesJSON: mustJSON(pr), DiscoveredBy: scanID,
			})
			profiles = append(profiles, childParent{id: arn, arn: arn})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "wellarchitected agent-profiles")
	if err != nil {
		if listErr != nil {
			err = errors.Join(err, fmt.Errorf("%s: %w", op, listErr))
		}
		return 0, 0, nil, err
	}
	switch {
	case listErr == nil:
		return total, inserted, profiles, nil
	case isAccessDenied(listErr):
		return total, inserted, profiles, skipIfAccessDenied(st, op, acct.ID, region, listErr)
	default:
		return total, inserted, profiles, fmt.Errorf("%s: %w", op, listErr)
	}
}

// scanWAAgentContexts lists the contexts of each agent profile: the
// customer-created description of an application (its accounts, regions,
// services, resource types and criticality) used during recommendation
// generation. List returns the same ContextSummary GetAgentContext does,
// so no describe call is made. The API gives no ARN and the service reference
// defines none; NativeID is {profileARN}/context/{Id}.
//
// ListAgentContexts does not model ResourceNotFoundException, but
// GetAgentContext and DeleteAgentContext, which take the same profileArn
// path, do; a profile deleted after its listing is therefore skipped silently
// on it (isWAParentGone). The live response is unverified. A first-page
// isWAAgentChildOpGap skips the profile silently; an untyped 404 is returned.
func scanWAAgentContexts(ctx context.Context, client wellArchitectedAPI, profiles []childParent, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return childFanOutSkipTransient(ctx, st, acct, region, "wellarchitected:ListAgentContexts", profiles, isWAParentGone, byParentARN,
		func(gctx context.Context, pr childParent) ([]*store.Resource, error) {
			pager := wellarchitected.NewListAgentContextsPaginator(client, &wellarchitected.ListAgentContextsInput{ProfileArn: &pr.id},
				func(o *wellarchitected.ListAgentContextsPaginatorOptions) {
					o.Limit = waAgentPageCap
					o.StopOnDuplicateToken = true
				})
			var rows []*store.Resource
			for firstPage := true; pager.HasMorePages(); firstPage = false {
				out, err := pager.NextPage(gctx)
				if err != nil {
					if firstPage && isWAAgentChildOpGap(err) {
						return nil, nil
					}
					return nil, err
				}
				for _, c := range out.Items {
					id := sv(c.Id)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeWellArchitectedAgentContext, NativeID: pr.arn + "/context/" + id,
						Name: c.Title, Region: &region, CreatedAt: tp(c.CreatedAt),
						AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanWAAgentGoals lists the optimization goals of each agent profile: the
// customer-created targets, each tied to pillars, for the optimization
// process. List returns the same GoalSummary GetAgentGoal does, so
// no describe call is made. The API gives no ARN and the service reference
// defines none; NativeID is {profileARN}/goal/{Id}. Errors are handled as in
// scanWAAgentContexts, ListAgentGoals modelling the same codes.
func scanWAAgentGoals(ctx context.Context, client wellArchitectedAPI, profiles []childParent, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return childFanOutSkipTransient(ctx, st, acct, region, "wellarchitected:ListAgentGoals", profiles, isWAParentGone, byParentARN,
		func(gctx context.Context, pr childParent) ([]*store.Resource, error) {
			pager := wellarchitected.NewListAgentGoalsPaginator(client, &wellarchitected.ListAgentGoalsInput{ProfileArn: &pr.id},
				func(o *wellarchitected.ListAgentGoalsPaginatorOptions) {
					o.Limit = waAgentPageCap
					o.StopOnDuplicateToken = true
				})
			var rows []*store.Resource
			for firstPage := true; pager.HasMorePages(); firstPage = false {
				out, err := pager.NextPage(gctx)
				if err != nil {
					if firstPage && isWAAgentChildOpGap(err) {
						return nil, nil
					}
					return nil, err
				}
				for _, g := range out.Items {
					id := sv(g.Id)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeWellArchitectedAgentGoal, NativeID: pr.arn + "/goal/" + id,
						Name: g.Title, Region: &region, CreatedAt: tp(g.CreatedAt),
						AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// isWAUnknownOperation reports the ValidationException reason the model
// defines for an operation the endpoint does not know. It is the shape an
// account or region without the Well-Architected Agent is expected to get;
// the live response is unverified.
func isWAUnknownOperation(err error) bool {
	var ve *wellarchitectedtypes.ValidationException
	return errors.As(err, &ve) && ve.Reason == wellarchitectedtypes.ValidationExceptionReasonUnknownOperation
}
