package aws

import (
	"context"
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
}

// waParents are the parents the list phases hand to the child phases. Shares
// are listed only on what this account owns: a workload, profile or template
// shared in from another account, and every AWS-official or shared-in lens,
// has no shares this account can list.
type waParents struct {
	workloads, ownedWorkloads, ownedLenses, ownedProfiles, ownedTemplates []childParent
}

// scanWellArchitected discovers Well-Architected workloads, lenses, profiles
// and review templates, then the lens reviews of each workload and the shares
// of each owned workload, custom lens, profile and review template. Phases run
// in order and a phase error stops the scan with the rows counted so far; the
// child phases run last so their errors cannot cost the parent rows.
//
// Not scanned: agent profiles (the pinned SDK has no agent ops), answers,
// check details/summaries, lens-review improvements and consolidated reports
// (computed from the review), milestones (point-in-time snapshots), profile
// notifications and share invitations (transient).
func scanWellArchitected(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := wellarchitected.NewFromConfig(acct.cfg, func(o *wellarchitected.Options) { o.Region = region })

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
