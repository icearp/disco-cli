package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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
	ListTagsForResource(context.Context, *wellarchitected.ListTagsForResourceInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListTagsForResourceOutput, error)
	ListWorkloadShares(context.Context, *wellarchitected.ListWorkloadSharesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListWorkloadSharesOutput, error)
	ListLensReviews(context.Context, *wellarchitected.ListLensReviewsInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListLensReviewsOutput, error)
	ListLensShares(context.Context, *wellarchitected.ListLensSharesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListLensSharesOutput, error)
	ListProfileShares(context.Context, *wellarchitected.ListProfileSharesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListProfileSharesOutput, error)
	ListTemplateShares(context.Context, *wellarchitected.ListTemplateSharesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListTemplateSharesOutput, error)
	ListAgentProfiles(context.Context, *wellarchitected.ListAgentProfilesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListAgentProfilesOutput, error)
	ListAgentContexts(context.Context, *wellarchitected.ListAgentContextsInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListAgentContextsOutput, error)
	ListAgentGoals(context.Context, *wellarchitected.ListAgentGoalsInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListAgentGoalsOutput, error)
}

const (
	waListWorkloadsOp       = "wellarchitected:ListWorkloads"
	waListLensesOp          = "wellarchitected:ListLenses"
	waListProfilesOp        = "wellarchitected:ListProfiles"
	waListReviewTemplatesOp = "wellarchitected:ListReviewTemplates"
	waTagsOp                = "wellarchitected:ListTagsForResource"
	// waToolPageCap is the MaxResults maximum the model documents for
	// ListWorkloads, ListLenses, ListProfiles and ListReviewTemplates.
	waToolPageCap = 50
)

// waParents are the parents the list phases hand to the child phases: only
// rows they stored. Shares are listed only on what this account owns: a
// workload, profile or template shared in from another account, and every
// AWS-official or shared-in lens, has no shares this account can list.
type waParents struct {
	workloads, ownedWorkloads, ownedLenses, ownedProfiles, ownedTemplates []childParent
}

// scanWellArchitected discovers the Well-Architected Tool's workloads, lenses,
// profiles and review templates with their tags (waTags), then the lens
// reviews of each workload and the shares of each owned workload, custom
// lens, profile and review template, then the Well-Architected Agent's
// profiles and the contexts and goals of each. Every listing runs as its own
// phase, one at a time and in that order: a child phase reads the parents its
// parent phase stored. A phase's error does not stop the later ones; a failed
// write and ctx's end do, and so does a first phase that stores nothing and
// fails only with NXDOMAIN, since the region does not serve the service
// (runHostPhases). A transient error alone marks the scan partial; beside a
// hard error it is demoted to a warning under its op. The tag reads' denials
// are one warning for the whole scan. The context and goal phases skip a
// transient parent themselves, so their errors are never transient.
//
// Not scanned: answers, check details/summaries, lens-review improvements and
// consolidated reports (computed from the review), milestones (point-in-time
// snapshots), profile notifications and share invitations (transient), and the
// agent's recommendations and recommendation generations.
func scanWellArchitected(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := wellarchitected.NewFromConfig(acct.cfg, func(o *wellarchitected.Options) { o.Region = region })
	tags := waTags(client).sharingDenials()
	var (
		p        waParents
		profiles []childParent
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "wellarchitected", 0, []hostPhase{
		{op: waListWorkloadsOp, run: func(pt *phaseTotals) bool {
			return scanWAWorkloads(ctx, client, acct, region, st, scanID, tags, pt, &p)
		}},
		{op: waListLensesOp, run: func(pt *phaseTotals) bool {
			return scanWALenses(ctx, client, acct, region, st, scanID, tags, pt, &p)
		}},
		{op: waListProfilesOp, run: func(pt *phaseTotals) bool {
			return scanWAProfiles(ctx, client, acct, region, st, scanID, tags, pt, &p)
		}},
		{op: waListReviewTemplatesOp, run: func(pt *phaseTotals) bool {
			return scanWAReviewTemplates(ctx, client, acct, region, st, scanID, tags, pt, &p)
		}},
		countedPhase(0, "wellarchitected:ListLensReviews", func() (int, int, error) {
			return scanWALensReviews(ctx, client, p.workloads, acct, region, st, scanID)
		}),
		countedPhase(0, "wellarchitected:ListWorkloadShares", func() (int, int, error) {
			return scanWAWorkloadShares(ctx, client, p.ownedWorkloads, acct, region, st, scanID)
		}),
		countedPhase(0, "wellarchitected:ListLensShares", func() (int, int, error) {
			return scanWALensShares(ctx, client, p.ownedLenses, acct, region, st, scanID)
		}),
		countedPhase(0, "wellarchitected:ListProfileShares", func() (int, int, error) {
			return scanWAProfileShares(ctx, client, p.ownedProfiles, acct, region, st, scanID)
		}),
		countedPhase(0, "wellarchitected:ListTemplateShares", func() (int, int, error) {
			return scanWATemplateShares(ctx, client, p.ownedTemplates, acct, region, st, scanID)
		}),
		{op: "wellarchitected:ListAgentProfiles", run: func(pt *phaseTotals) bool {
			t, i, listed, perr := scanWAAgentProfiles(ctx, client, acct, region, st, scanID)
			profiles = listed
			return pt.addPhase("wellarchitected:ListAgentProfiles", t, i, perr)
		}},
		countedPhase(0, "wellarchitected:ListAgentContexts", func() (int, int, error) {
			return scanWAAgentContexts(ctx, client, profiles, acct, region, st, scanID)
		}),
		countedPhase(0, "wellarchitected:ListAgentGoals", func() (int, int, error) {
			return scanWAAgentGoals(ctx, client, profiles, acct, region, st, scanID)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// waTags reads the tags of a workload, custom lens, profile or review
// template with ListTagsForResource, sent the row's NativeID: the ARN its
// listing returned. The op documents its WorkloadArn input as any of those
// four ARNs, the Service Reference lists their resources on it and on
// TagResource, and CreateWorkload, ImportLens, CreateProfile and
// CreateReviewTemplate take Tags. The op returns every tag at once.
// ResourceNotFoundException, the one client error its model lists, drops a
// resource deleted after it was listed.
//
// Agent profiles are not read: their list element carries Tags, stored as
// listed. Agent contexts and goals, lens reviews and shares are no Service
// Reference resource, and their create ops take no tags.
func waTags(client wellArchitectedAPI) rowTagReader {
	return rowTagReader{
		op:       waTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &wellarchitected.ListTagsForResourceInput{WorkloadArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// Each list phase below keeps, by ARN, the elements of the page being
// stored: its row builder records them, its tag reads and its stored step
// read them, and storeTokenPagesTagsWith runs those one after another for
// each page, so no read overlaps a write. The stored step then clears them,
// so one page is held at a time. Each element names its owner (Owner, an
// account id): a resource shared in from another account is its owner's, so
// a denied or not-found tag read of it keeps the row untagged and silent
// (untaggedIfOwnerElsewhere).

// scanWAWorkloads stores the workloads a page at a time with their tags. The
// workloads stored become the parents of the lens-review phase, and those
// this account owns the parents of the share phase; one stored without the
// WorkloadId those ops key on is warned about. It reports whether a write
// failed, after which the scan must stop.
func scanWAWorkloads(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals, parents *waParents) (storeWriteFailed bool) {
	listed := map[string]wellarchitectedtypes.WorkloadSummary{}
	var noID skipTally
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, waListWorkloadsOp,
		func(ctx context.Context, token *string) ([]wellarchitectedtypes.WorkloadSummary, *string, error) {
			out, err := client.ListWorkloads(ctx, &wellarchitected.ListWorkloadsInput{MaxResults: sdkaws.Int32(waToolPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WorkloadSummaries, out.NextToken, nil
		},
		func(w wellarchitectedtypes.WorkloadSummary) (*store.Resource, string, string) {
			listed[sv(w.WorkloadArn)] = w
			return arnRow("workloads", w.WorkloadArn, w.WorkloadName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedWorkload, NativeID: sv(w.WorkloadArn),
				Name: w.WorkloadName, Region: &region,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		},
		tokenPageSteps{stored: func(rows []*store.Resource) {
			for _, r := range rows {
				w := listed[r.NativeID]
				id := sv(w.WorkloadId)
				if id == "" {
					noID.skipped(r.NativeID, nil)
					continue
				}
				parents.workloads = append(parents.workloads, childParent{id: id, arn: r.NativeID})
				if sv(w.Owner) == acct.ID {
					parents.ownedWorkloads = append(parents.ownedWorkloads, childParent{id: id, arn: r.NativeID})
				}
			}
			clear(listed)
		}},
		tags.untaggedIfOwnerElsewhere(acct.ID, func(r *store.Resource) string { return sv(listed[r.NativeID].Owner) }), p)
	// Like the listing's own warnings (storeTokenPagesTagsWith), this one is
	// sent only when every write succeeded.
	if !failed {
		warnNoChildID(st, waListWorkloadsOp, acct, region, &noID, "workloads", "WorkloadId")
	}
	return failed
}

// scanWALenses stores the lenses a page at a time. A lens with Owner "AWS" is
// ManagedByProvider: an AWS-official lens is assumed to carry that Owner,
// which is not verified against the live service (the model types Owner as a
// 12-digit account id). Every lens but an AWS-official one has its tags read
// (waLensTags). The custom lenses this account created and stored become the
// parents of the share phase, ListLensShares taking the ARN as the alias. It
// reports whether a write failed, after which the scan must stop.
func scanWALenses(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals, parents *waParents) (storeWriteFailed bool) {
	listed := map[string]wellarchitectedtypes.LensSummary{}
	return storeTokenPagesTagsWith(ctx, st, acct, region, waListLensesOp,
		func(ctx context.Context, token *string) ([]wellarchitectedtypes.LensSummary, *string, error) {
			out, err := client.ListLenses(ctx, &wellarchitected.ListLensesInput{MaxResults: sdkaws.Int32(waToolPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.LensSummaries, out.NextToken, nil
		},
		func(l wellarchitectedtypes.LensSummary) (*store.Resource, string, string) {
			listed[sv(l.LensArn)] = l
			return arnRow("lenses", l.LensArn, l.LensName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedLens, NativeID: sv(l.LensArn),
				Name: l.LensName, Region: &region,
				ManagedByProvider: sv(l.Owner) == "AWS",
				AttributesJSON:    mustJSON(l), DiscoveredBy: scanID,
			})
		},
		tokenPageSteps{stored: func(rows []*store.Resource) {
			for _, r := range rows {
				if listed[r.NativeID].LensType == wellarchitectedtypes.LensTypeCustomSelf {
					parents.ownedLenses = append(parents.ownedLenses, childParent{id: r.NativeID, arn: r.NativeID})
				}
			}
			clear(listed)
		}},
		waLensTags(tags, acct.ID, listed), p)
}

// waLensTags is tags for the lenses of the page being stored, listed by ARN.
// An AWS-official lens (LensType AWS_OFFICIAL) keeps no tags and costs no
// call: the op documents custom lens ARNs only, and an official lens is not
// the customer's to tag. Every other lens is read, one with no LensType
// included, since the model does not require LensType and skipping such a
// lens would leave a custom lens untagged without a word. A shared-in custom
// lens is its owner's (untaggedIfOwnerElsewhere).
func waLensTags(tags rowTagReader, acctID string, listed map[string]wellarchitectedtypes.LensSummary) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		if listed[r.NativeID].LensType == wellarchitectedtypes.LensTypeAwsOfficial {
			return nil, nil
		}
		return read(ctx, r)
	}
	return tags.untaggedIfOwnerElsewhere(acctID, func(r *store.Resource) string { return sv(listed[r.NativeID].Owner) })
}

// scanWAProfiles stores the profiles a page at a time with their tags; those
// this account owns and stored become the parents of the share phase. It
// reports whether a write failed, after which the scan must stop.
func scanWAProfiles(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals, parents *waParents) (storeWriteFailed bool) {
	listed := map[string]wellarchitectedtypes.ProfileSummary{}
	return storeTokenPagesTagsWith(ctx, st, acct, region, waListProfilesOp,
		func(ctx context.Context, token *string) ([]wellarchitectedtypes.ProfileSummary, *string, error) {
			out, err := client.ListProfiles(ctx, &wellarchitected.ListProfilesInput{MaxResults: sdkaws.Int32(waToolPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ProfileSummaries, out.NextToken, nil
		},
		func(pr wellarchitectedtypes.ProfileSummary) (*store.Resource, string, string) {
			listed[sv(pr.ProfileArn)] = pr
			return arnRow("profiles", pr.ProfileArn, pr.ProfileName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedProfile, NativeID: sv(pr.ProfileArn),
				Name: pr.ProfileName, Region: &region,
				AttributesJSON: mustJSON(pr), DiscoveredBy: scanID,
			})
		},
		tokenPageSteps{stored: func(rows []*store.Resource) {
			for _, r := range rows {
				if sv(listed[r.NativeID].Owner) == acct.ID {
					parents.ownedProfiles = append(parents.ownedProfiles, childParent{id: r.NativeID, arn: r.NativeID})
				}
			}
			clear(listed)
		}},
		tags.untaggedIfOwnerElsewhere(acct.ID, func(r *store.Resource) string { return sv(listed[r.NativeID].Owner) }), p)
}

// scanWAReviewTemplates stores the review templates a page at a time with
// their tags; those this account owns and stored become the parents of the
// share phase. It reports whether a write failed, after which the scan must
// stop.
func scanWAReviewTemplates(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals, parents *waParents) (storeWriteFailed bool) {
	listed := map[string]wellarchitectedtypes.ReviewTemplateSummary{}
	return storeTokenPagesTagsWith(ctx, st, acct, region, waListReviewTemplatesOp,
		func(ctx context.Context, token *string) ([]wellarchitectedtypes.ReviewTemplateSummary, *string, error) {
			out, err := client.ListReviewTemplates(ctx, &wellarchitected.ListReviewTemplatesInput{MaxResults: sdkaws.Int32(waToolPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ReviewTemplates, out.NextToken, nil
		},
		func(r wellarchitectedtypes.ReviewTemplateSummary) (*store.Resource, string, string) {
			listed[sv(r.TemplateArn)] = r
			return arnRow("review templates", r.TemplateArn, r.TemplateName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedReviewTemplate, NativeID: sv(r.TemplateArn),
				Name: r.TemplateName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		},
		tokenPageSteps{stored: func(rows []*store.Resource) {
			for _, r := range rows {
				if sv(listed[r.NativeID].Owner) == acct.ID {
					parents.ownedTemplates = append(parents.ownedTemplates, childParent{id: r.NativeID, arn: r.NativeID})
				}
			}
			clear(listed)
		}},
		tags.untaggedIfOwnerElsewhere(acct.ID, func(r *store.Resource) string { return sv(listed[r.NativeID].Owner) }), p)
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
