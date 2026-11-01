package aws

import (
	"cmp"
	"context"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator"
	bcmpctypes "github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isCostExplorerNotEnabled disambiguates the "User not enabled for cost
// explorer access" / linked-account state from a real IAM denial. Shared by
// BCM Pricing Calculator + Cost Explorer scanners — both gate on the same
// account-level Cost Explorer enablement.
func isCostExplorerNotEnabled(err error) bool {
	return isAccessDeniedWithMessage(err, "not enabled for cost explorer") ||
		isAccessDeniedWithMessage(err, "doesn't have access to cost category")
}

// isMigrationRequiredIAMDeny matches the canned AccessDeniedException AWS
// returns for accounts whose IAM policies still reference the legacy action
// names of a service that has migrated to a new IAM action surface. Body:
// "To use this feature, please obtain the required permissions. Migrate
// the policies in your account to use the new IAM actions." Pre-migration
// is environment policy, not a misconfig of this scanner — silent-skip.
func isMigrationRequiredIAMDeny(err error) bool {
	return isAccessDeniedWithMessage(err, "Migrate the policies in your account to use the new IAM actions")
}

// AWS BCM Pricing Calculator is account-global — for aws accounts the SDK
// endpoint resolves only via us-east-1 (mirrors aws:billing / aws:ce).
const bcmPricingCalculatorRegion = "us-east-1"

func init() {
	registerType(restype.Descriptor{Type: TypeBcmPricingCalculatorBillScenario, Service: "bcmpricingcalculator"})
	registerType(restype.Descriptor{Type: TypeBcmPricingCalculatorBillEstimate, Service: "bcmpricingcalculator"})
	registerType(restype.Descriptor{Type: TypeBcmPricingCalculatorWorkloadEstimate, Service: "bcmpricingcalculator"})
	registerType(restype.Descriptor{Type: TypeBcmPricingCalculatorBillScenarioCommitmentModification, Service: "bcmpricingcalculator"})
	registerType(restype.Descriptor{Type: TypeBcmPricingCalculatorBillScenarioUsageModification, Service: "bcmpricingcalculator"})
	registerType(restype.Descriptor{Type: TypeBcmPricingCalculatorWorkloadEstimateUsage, Service: "bcmpricingcalculator"})
	registerService(serviceEntry{
		name:   "aws:bcmpricingcalculator",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
			region, err := acct.homeRegion("bcmpricingcalculator")
			if err != nil {
				return 0, 0, err
			}
			client := bcmpricingcalculator.NewFromConfig(acct.cfg, func(o *bcmpricingcalculator.Options) { o.Region = region })
			return scanBcmPricingCalculator(ctx, client, acct, st, scanID)
		},
	})
}

// bcmPricingCalculatorAPI is the narrow surface scanBcmPricingCalculator uses.
type bcmPricingCalculatorAPI interface {
	ListBillScenarios(context.Context, *bcmpricingcalculator.ListBillScenariosInput, ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListBillScenariosOutput, error)
	ListBillEstimates(context.Context, *bcmpricingcalculator.ListBillEstimatesInput, ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListBillEstimatesOutput, error)
	ListWorkloadEstimates(context.Context, *bcmpricingcalculator.ListWorkloadEstimatesInput, ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListWorkloadEstimatesOutput, error)
	ListBillScenarioCommitmentModifications(context.Context, *bcmpricingcalculator.ListBillScenarioCommitmentModificationsInput, ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListBillScenarioCommitmentModificationsOutput, error)
	ListBillScenarioUsageModifications(context.Context, *bcmpricingcalculator.ListBillScenarioUsageModificationsInput, ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListBillScenarioUsageModificationsOutput, error)
	ListWorkloadEstimateUsage(context.Context, *bcmpricingcalculator.ListWorkloadEstimateUsageInput, ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListWorkloadEstimateUsageOutput, error)
	ListTagsForResource(context.Context, *bcmpricingcalculator.ListTagsForResourceInput, ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListTagsForResourceOutput, error)
}

// bcmPricingCalculatorNativeID synthesizes the canonical ARN for a pricing-
// calculator artifact (the SDK summaries carry only an Id). The service is
// account-global, so the ARN's region segment is empty:
// arn:{p}:bcm-pricing-calculator::{acct}:{kind}/{id}.
func bcmPricingCalculatorNativeID(partition, acct, kind, id string) string {
	return arnFor(partition, "bcm-pricing-calculator", "", acct, kind+"/"+id)
}

const (
	bcmPCBillScenariosOp     = "bcmpricingcalculator:ListBillScenarios"
	bcmPCBillEstimatesOp     = "bcmpricingcalculator:ListBillEstimates"
	bcmPCWorkloadEstimatesOp = "bcmpricingcalculator:ListWorkloadEstimates"
	bcmPCTagsOp              = "bcmpricingcalculator:ListTagsForResource"
	// bcmPCParentPageLimit is the model's MaxResults cap (1-25) for
	// ListBillScenarios, ListBillEstimates and ListWorkloadEstimates.
	bcmPCParentPageLimit = 25
)

// bcmPCTags reads a bill scenario's, bill estimate's or workload estimate's
// tags with ListTagsForResource, sent the row's NativeID: the listings return
// no ARN, so it is the ARN built in the Service Reference's format
// (bcmPricingCalculatorNativeID). The model marks those three resources
// taggable and each one's Create input takes Tags. The Service Reference's
// TagResource lists only bill scenarios and workload estimates, so how the op
// answers a bill-estimate ARN is unverified live. The child types stored here
// are not taggable resources in the model, so they are not read.
// ResourceNotFoundException, the op's modeled not-found, drops a resource
// deleted, or expired, after it was listed.
func bcmPCTags(client bcmPricingCalculatorAPI) rowTagReader {
	return rowTagReader{
		op:       bcmPCTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &bcmpricingcalculator.ListTagsForResourceInput{Arn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// bcmPCGate is the account-level refusal one of the bill-scenario,
// bill-estimate and workload-estimate listings met, under the op that met it:
// the account has not enabled Cost Explorer, which it can do itself
// (markServiceDisabled), or is a member account where only payer and regular
// accounts may list (markServiceNotEntitled). err is nil when none did.
type bcmPCGate struct {
	op  string
	err error
}

// bcmPCGatedPages is page with an account-level refusal recorded in gate and
// ending the listing without an error, keeping the pages read before it. The
// migration-required IAM deny on the first page reads as an empty listing,
// without a warning (isMigrationRequiredIAMDeny says why); after a page was
// read it is returned, and warned about as a denial (firstPageGapPages).
func bcmPCGatedPages[Item any](gate *bcmPCGate, op string, page func(ctx context.Context, token *string) ([]Item, *string, error)) func(ctx context.Context, token *string) ([]Item, *string, error) {
	page = firstPageGapPages(isMigrationRequiredIAMDeny, page)
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, token)
		// Cost Explorer's refusal is an AccessDeniedException, so it is
		// matched before the listing reads err as a denial.
		switch {
		case isCostExplorerNotEnabled(err):
			*gate = bcmPCGate{op, markServiceDisabled(err)}
		case isPayerAccountOnly(err):
			*gate = bcmPCGate{op, markServiceNotEntitled(err)}
		default:
			return items, next, err
		}
		return nil, nil, nil
	}
}

// scanBcmPricingCalculator discovers the account's pricing-calculator
// artifacts. Every listing runs as its own phase, one at a time: a phase's
// error does not stop the later ones; a failed write and ctx's end do, and
// so does an NXDOMAIN of the first listing with nothing stored
// (runHostPhases). The child phases get the bill scenarios and workload
// estimates stored this scan. Bill scenarios, bill estimates and workload
// estimates have their tags read per row (bcmPCTags), whose denials are one
// warning for the scan.
//
// An account-level refusal (bcmPCGate) skips the bill-scenario,
// bill-estimate and workload-estimate listings after the one that met it;
// the child phases still run. It is returned alone when nothing was stored
// and nothing else failed. Otherwise it is a warning, because the dispatcher
// discards a sentinel's counts and ranks it above a hard error, which would
// hide both the rows and the other failures.
func scanBcmPricingCalculator(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	region := bcmPricingCalculatorRegion
	tags := bcmPCTags(client).sharingDenials()
	var (
		gate                 bcmPCGate
		scenarios, workloads []childParent
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "bcmpricingcalculator", 0, []hostPhase{
		{op: bcmPCBillScenariosOp, run: func(p *phaseTotals) (failed bool) {
			scenarios, failed = scanBcmPricingCalculatorBillScenarios(ctx, client, acct, region, st, scanID, &gate, tags, p)
			return failed
		}},
		{op: bcmPCBillEstimatesOp, run: func(p *phaseTotals) bool {
			if gate.err != nil {
				return false
			}
			return scanBcmPricingCalculatorBillEstimates(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}},
		{op: bcmPCWorkloadEstimatesOp, run: func(p *phaseTotals) (failed bool) {
			if gate.err != nil {
				return false
			}
			workloads, failed = scanBcmPricingCalculatorWorkloadEstimates(ctx, client, acct, region, st, scanID, &gate, tags, p)
			return failed
		}},
		countedPhase(0, bcmPCCommitmentModOp, func() (int, int, error) {
			return scanBcmPCCommitmentModifications(ctx, client, acct, region, st, scanID, scenarios)
		}),
		countedPhase(0, bcmPCUsageModOp, func() (int, int, error) {
			return scanBcmPCUsageModifications(ctx, client, acct, region, st, scanID, scenarios)
		}),
		countedPhase(0, bcmPCWorkloadUsageOp, func() (int, int, error) {
			return scanBcmPCWorkloadEstimateUsage(ctx, client, acct, region, st, scanID, workloads)
		}),
	})
	tags.warnDenials(st, acct, region)
	if gate.err == nil {
		return total, inserted, err
	}
	if total == 0 && err == nil {
		return 0, 0, gate.err
	}
	warnBcmPCGated(st, acct.ID, region, gate)
	return total, inserted, err
}

// warnBcmPCGated records that gate.op's listing was refused for the account
// after rows were stored or another call failed, so it and the bill-scenario,
// bill-estimate and workload-estimate listings after it were not read in
// full.
func warnBcmPCGated(st *store.Store, accountID, region string, gate bcmPCGate) {
	st.ReportWarning(store.ScanWarning{
		Provider: "aws",
		Service:  gate.op,
		Scope:    accountID + "/" + region,
		Message:  "the account refused this listing after rows were stored or another call failed, so it and any later bill-scenario, bill-estimate or workload-estimate listing were not read in full: " + gate.err.Error(),
	})
}

// scanBcmPricingCalculatorBillScenarios stores the bill scenarios with their
// tags, a page at a time, and returns those stored as the parents of the
// scenario child phases. It reports whether a write failed, after which the
// scan must stop.
func scanBcmPricingCalculatorBillScenarios(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string, gate *bcmPCGate, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, bcmPCBillScenariosOp, "bill scenarios", "Id",
		bcmPCGatedPages(gate, bcmPCBillScenariosOp, func(ctx context.Context, token *string) ([]bcmpctypes.BillScenarioSummary, *string, error) {
			out, err := client.ListBillScenarios(ctx, &bcmpricingcalculator.ListBillScenariosInput{MaxResults: sdkaws.Int32(bcmPCParentPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(s bcmpctypes.BillScenarioSummary) (*store.Resource, string, string) {
			id := sv(s.Id)
			if id == "" {
				return nil, "bill scenarios have no Id", cmp.Or(sv(s.Name), noIdentifier)
			}
			name := sv(s.Name)
			status := string(s.Status)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeBcmPricingCalculatorBillScenario,
				NativeID: bcmPricingCalculatorNativeID(acct.partition(), acct.ID, "bill-scenario", id),
				Name:     &name, Region: regionGlobal, Status: &status, CreatedAt: tp(s.CreatedAt),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}, "", ""
		},
		func(s bcmpctypes.BillScenarioSummary) string { return sv(s.Id) },
		tags, p)
}

// scanBcmPricingCalculatorBillEstimates stores the bill estimates with their
// tags, a page at a time. It reports whether a write failed, after which the
// scan must stop.
func scanBcmPricingCalculatorBillEstimates(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string, gate *bcmPCGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, bcmPCBillEstimatesOp,
		bcmPCGatedPages(gate, bcmPCBillEstimatesOp, func(ctx context.Context, token *string) ([]bcmpctypes.BillEstimateSummary, *string, error) {
			out, err := client.ListBillEstimates(ctx, &bcmpricingcalculator.ListBillEstimatesInput{MaxResults: sdkaws.Int32(bcmPCParentPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(e bcmpctypes.BillEstimateSummary) (*store.Resource, string, string) {
			id := sv(e.Id)
			if id == "" {
				return nil, "bill estimates have no Id", cmp.Or(sv(e.Name), noIdentifier)
			}
			name := sv(e.Name)
			status := string(e.Status)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeBcmPricingCalculatorBillEstimate,
				NativeID: bcmPricingCalculatorNativeID(acct.partition(), acct.ID, "bill-estimate", id),
				Name:     &name, Region: regionGlobal, Status: &status, CreatedAt: tp(e.CreatedAt),
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			}, "", ""
		},
		tags, p)
}

// scanBcmPricingCalculatorWorkloadEstimates stores the workload estimates
// with their tags, a page at a time, and returns those stored as the parents
// of the usage phase. It reports whether a write failed, after which the scan
// must stop.
func scanBcmPricingCalculatorWorkloadEstimates(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string, gate *bcmPCGate, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, bcmPCWorkloadEstimatesOp, "workload estimates", "Id",
		bcmPCGatedPages(gate, bcmPCWorkloadEstimatesOp, func(ctx context.Context, token *string) ([]bcmpctypes.WorkloadEstimateSummary, *string, error) {
			out, err := client.ListWorkloadEstimates(ctx, &bcmpricingcalculator.ListWorkloadEstimatesInput{MaxResults: sdkaws.Int32(bcmPCParentPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(e bcmpctypes.WorkloadEstimateSummary) (*store.Resource, string, string) {
			id := sv(e.Id)
			if id == "" {
				return nil, "workload estimates have no Id", cmp.Or(sv(e.Name), noIdentifier)
			}
			name := sv(e.Name)
			status := string(e.Status)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeBcmPricingCalculatorWorkloadEstimate,
				NativeID: bcmPricingCalculatorNativeID(acct.partition(), acct.ID, "workload-estimate", id),
				Name:     &name, Region: regionGlobal, Status: &status, CreatedAt: tp(e.CreatedAt),
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			}, "", ""
		},
		func(e bcmpctypes.WorkloadEstimateSummary) string { return sv(e.Id) },
		tags, p)
}

const (
	bcmPCCommitmentModOp = "bcmpricingcalculator:ListBillScenarioCommitmentModifications"
	bcmPCUsageModOp      = "bcmpricingcalculator:ListBillScenarioUsageModifications"
	bcmPCWorkloadUsageOp = "bcmpricingcalculator:ListWorkloadEstimateUsage"
	// bcmPCScenarioChildPageLimit and bcmPCWorkloadUsagePageLimit are the
	// model's MaxResults caps (1-25 for both bill-scenario child ops, 1-300 for
	// ListWorkloadEstimateUsage).
	bcmPCScenarioChildPageLimit = 25
	bcmPCWorkloadUsagePageLimit = 300
)

// isBcmPCChildSkip matches the child-list errors skipped silently for one
// parent: ResourceNotFoundException (the scenario or estimate was deleted, or
// expired, after it was listed) and the migration-required IAM deny the parent
// phases skip silently on their first page (bcmPCGatedPages).
func isBcmPCChildSkip(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException") || isMigrationRequiredIAMDeny(err)
}

// bcmPCChildFanOut runs storeChildren for each parent concurrently
// (childFanOutStored). DataUnavailableException is modeled for the child ops
// but not yet observed live, so it skips that parent with one warning per op.
// A parent whose call still fails transiently after the SDK's retries is
// skipped, keeping its siblings' rows, and the skips are reported in one
// warning. Rows a parent stored before its error stay and are counted.
func bcmPCChildFanOut(ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent, storeChildren func(context.Context, childParent) (int, int, error)) (int, int, error) {
	var (
		transient   transientTally
		unavailable sync.Once
	)
	total, inserted, err := childFanOutStored(ctx, st, acct, region, op, parents, isBcmPCChildSkip,
		func(ctx context.Context, p childParent) (int, int, error) {
			t, i, err := storeChildren(ctx, p)
			switch {
			case isAPIErrorCode(err, "DataUnavailableException"):
				unavailable.Do(func() {
					_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf("children of %s not listed this scan (later parents refused the same way not reported): %w", p.arn, err))
				})
				return t, i, nil
			case isTransientNetworkError(err):
				transient.skipped(p.arn, err)
				return t, i, nil
			}
			return t, i, err
		})
	transient.warnSkipped(st, op, acct, region)
	return total, inserted, err
}

// upsertBcmPCChildren stores one page of a parent's children and links each
// under the parent.
func upsertBcmPCChildren(st *store.Store, acct *account, op string, parent childParent, rows []*store.Resource) (int, int, error) {
	parentID := store.ResourceID("aws", acct.ID, parent.arn)
	children := make([]containedChild, len(rows))
	for i, r := range rows {
		children[i] = containedChild{row: r, parentID: parentID}
	}
	return upsertContained(st, op, children)
}

// bcmPCCommitmentModAttrs shadows the modification's CommitmentAction union,
// which marshals as an unlabelled {"Value": ...}, with one key per known member
// so the stored row says whether a Reserved Instance or Savings Plan is added
// or negated. A member newer than the pinned SDK is kept under UnknownMember
// with its tag and its value, which is []byte and so stored base64-encoded.
type bcmPCCommitmentModAttrs struct {
	bcmpctypes.BillScenarioCommitmentModificationItem
	CommitmentAction bcmPCCommitmentActionAttrs
}

type bcmPCCommitmentActionAttrs struct {
	AddReservedInstanceAction    *bcmpctypes.AddReservedInstanceAction    `json:",omitempty"`
	AddSavingsPlanAction         *bcmpctypes.AddSavingsPlanAction         `json:",omitempty"`
	NegateReservedInstanceAction *bcmpctypes.NegateReservedInstanceAction `json:",omitempty"`
	NegateSavingsPlanAction      *bcmpctypes.NegateSavingsPlanAction      `json:",omitempty"`
	UnknownMember                *bcmpctypes.UnknownUnionMember           `json:",omitempty"`
}

func flattenBcmPCCommitmentMod(m bcmpctypes.BillScenarioCommitmentModificationItem) bcmPCCommitmentModAttrs {
	out := bcmPCCommitmentModAttrs{BillScenarioCommitmentModificationItem: m}
	switch a := m.CommitmentAction.(type) {
	case *bcmpctypes.BillScenarioCommitmentModificationActionMemberAddReservedInstanceAction:
		out.CommitmentAction.AddReservedInstanceAction = &a.Value
	case *bcmpctypes.BillScenarioCommitmentModificationActionMemberAddSavingsPlanAction:
		out.CommitmentAction.AddSavingsPlanAction = &a.Value
	case *bcmpctypes.BillScenarioCommitmentModificationActionMemberNegateReservedInstanceAction:
		out.CommitmentAction.NegateReservedInstanceAction = &a.Value
	case *bcmpctypes.BillScenarioCommitmentModificationActionMemberNegateSavingsPlanAction:
		out.CommitmentAction.NegateSavingsPlanAction = &a.Value
	case *bcmpctypes.UnknownUnionMember:
		out.CommitmentAction.UnknownMember = a
	}
	return out
}

// scanBcmPCCommitmentModifications lists the commitment modifications of each
// bill scenario. A modification has no ARN, so its NativeID is
// {scenarioARN}/commitment-modification/{Id}.
func scanBcmPCCommitmentModifications(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string, scenarios []childParent) (int, int, error) {
	return bcmPCChildFanOut(ctx, st, acct, region, bcmPCCommitmentModOp, scenarios,
		func(ctx context.Context, sc childParent) (total, inserted int, err error) {
			pager := bcmpricingcalculator.NewListBillScenarioCommitmentModificationsPaginator(client,
				&bcmpricingcalculator.ListBillScenarioCommitmentModificationsInput{BillScenarioId: &sc.id},
				func(o *bcmpricingcalculator.ListBillScenarioCommitmentModificationsPaginatorOptions) {
					o.Limit = bcmPCScenarioChildPageLimit
				})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return total, inserted, err
				}
				var rows []*store.Resource
				for _, m := range out.Items {
					id := sv(m.Id)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type:     TypeBcmPricingCalculatorBillScenarioCommitmentModification,
						NativeID: sc.arn + "/commitment-modification/" + id,
						Region:   regionGlobal, AttributesJSON: mustJSON(flattenBcmPCCommitmentMod(m)), DiscoveredBy: scanID,
					})
				}
				t, i, err := upsertBcmPCChildren(st, acct, bcmPCCommitmentModOp, sc, rows)
				total += t
				inserted += i
				if err != nil {
					return total, inserted, err
				}
			}
			return total, inserted, nil
		})
}

// scanBcmPCUsageModifications lists the usage modifications of each bill
// scenario. Each carries its hourly Quantities, so a parent's modifications
// are stored a page at a time rather than held until the end. A modification has no ARN, so its NativeID is
// {scenarioARN}/usage-modification/{Id}.
func scanBcmPCUsageModifications(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string, scenarios []childParent) (int, int, error) {
	return bcmPCChildFanOut(ctx, st, acct, region, bcmPCUsageModOp, scenarios,
		func(ctx context.Context, sc childParent) (total, inserted int, err error) {
			pager := bcmpricingcalculator.NewListBillScenarioUsageModificationsPaginator(client,
				&bcmpricingcalculator.ListBillScenarioUsageModificationsInput{BillScenarioId: &sc.id},
				func(o *bcmpricingcalculator.ListBillScenarioUsageModificationsPaginatorOptions) {
					o.Limit = bcmPCScenarioChildPageLimit
				})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return total, inserted, err
				}
				var rows []*store.Resource
				for _, m := range out.Items {
					id := sv(m.Id)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type:     TypeBcmPricingCalculatorBillScenarioUsageModification,
						NativeID: sc.arn + "/usage-modification/" + id,
						Region:   regionGlobal, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
					})
				}
				t, i, err := upsertBcmPCChildren(st, acct, bcmPCUsageModOp, sc, rows)
				total += t
				inserted += i
				if err != nil {
					return total, inserted, err
				}
			}
			return total, inserted, nil
		})
}

// scanBcmPCWorkloadEstimateUsage lists the usage lines of each workload
// estimate. A line has no ARN, so its NativeID is
// {workloadEstimateARN}/usage/{Id}.
func scanBcmPCWorkloadEstimateUsage(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string, workloads []childParent) (int, int, error) {
	return bcmPCChildFanOut(ctx, st, acct, region, bcmPCWorkloadUsageOp, workloads,
		func(ctx context.Context, we childParent) (total, inserted int, err error) {
			pager := bcmpricingcalculator.NewListWorkloadEstimateUsagePaginator(client,
				&bcmpricingcalculator.ListWorkloadEstimateUsageInput{WorkloadEstimateId: &we.id},
				func(o *bcmpricingcalculator.ListWorkloadEstimateUsagePaginatorOptions) {
					o.Limit = bcmPCWorkloadUsagePageLimit
				})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return total, inserted, err
				}
				var rows []*store.Resource
				for _, u := range out.Items {
					id := sv(u.Id)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type:     TypeBcmPricingCalculatorWorkloadEstimateUsage,
						NativeID: we.arn + "/usage/" + id,
						Region:   regionGlobal, Status: nonEmptyPtr(string(u.Status)),
						AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
					})
				}
				t, i, err := upsertBcmPCChildren(st, acct, bcmPCWorkloadUsageOp, we, rows)
				total += t
				inserted += i
				if err != nil {
					return total, inserted, err
				}
			}
			return total, inserted, nil
		})
}
