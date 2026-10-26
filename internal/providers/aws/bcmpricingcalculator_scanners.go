package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

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
}

// bcmPricingCalculatorNativeID synthesizes the canonical ARN for a pricing-
// calculator artifact (the SDK summaries carry only an Id). The service is
// account-global, so the ARN's region segment is empty:
// arn:aws:bcm-pricing-calculator::{acct}:{kind}/{id}.
func bcmPricingCalculatorNativeID(acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:bcm-pricing-calculator::%s:%s/%s", acct, kind, id)
}

// bcmPCListErr classifies a BCM Pricing Calculator List* page error into the
// value a phase returns: markServiceDisabled when the account hasn't enabled
// Cost Explorer (self-enableable → (account: disabled)); markServiceNotEntitled
// for the payer-only topology gate (member account can't self-enable →
// (account: not entitled)); nil for the migration-required IAM deny and the
// soft access-denied skip (orchestrator continues to next phase); or a
// wrapped fatal error otherwise.
func bcmPCListErr(st *store.Store, op, acctID, region string, perr error) error {
	switch {
	case isCostExplorerNotEnabled(perr):
		return markServiceDisabled(perr)
	case isPayerAccountOnly(perr):
		return markServiceNotEntitled(perr)
	case isMigrationRequiredIAMDeny(perr):
		return nil
	case isAccessDenied(perr):
		return skipIfAccessDenied(st, op, acctID, region, perr)
	default:
		return fmt.Errorf("%s: %w", op, perr)
	}
}

// scanBcmPricingCalculator discovers the account's pricing-calculator
// artifacts.
//
// The bill-scenario, bill-estimate and workload-estimate phases stop at their
// first hard error. The three child phases run whatever those returned, over
// the scenarios and workload estimates stored before a failure, so their
// failure costs no other phase its rows and an older phase's failure does not
// skip them. The older phases' transient error is demoted to a warning only
// beside a hard error: joined, it would be buried in the hard error instead of
// reported under its op; the child phases never return one (bcmPCChildFanOut
// tallies them). The errors are joined and the counts of all phases summed.
//
// An older phase's disabled / not-entitled sentinel is returned alone when
// nothing was stored. After rows were stored it becomes a warning instead,
// because the dispatcher discards a sentinel's counts and ranks it above a
// hard error, which would hide both the rows and a child phase's failure.
func scanBcmPricingCalculator(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	region := bcmPricingCalculatorRegion
	total, inserted, scenarios, workloads, err := scanBcmPCOlderPhases(ctx, client, acct, region, st, scanID)
	if errors.Is(err, errServiceDisabled) || errors.Is(err, errServiceNotEntitled) {
		if total == 0 {
			return 0, 0, err
		}
		warnBcmPCOlderPhasesGated(st, acct.ID, region, err)
		err = nil
	}
	var errs phaseErrs
	errs.add("bcmpricingcalculator", err)
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanBcmPCCommitmentModifications(ctx, client, acct, region, st, scanID, scenarios)
		},
		func() (int, int, error) {
			return scanBcmPCUsageModifications(ctx, client, acct, region, st, scanID, scenarios)
		},
		func() (int, int, error) {
			return scanBcmPCWorkloadEstimateUsage(ctx, client, acct, region, st, scanID, workloads)
		},
	} {
		t, i, perr := phase()
		total += t
		inserted += i
		errs.add("bcmpricingcalculator", perr)
	}
	return total, inserted, errs.join(st, acct, region)
}

// warnBcmPCOlderPhasesGated records that an older phase was refused as
// disabled or not entitled after an earlier one stored rows, so that phase and
// the older phases after it were not scanned.
func warnBcmPCOlderPhasesGated(st *store.Store, accountID, region string, err error) {
	st.ReportWarning(store.ScanWarning{
		Provider: "aws",
		Service:  "bcmpricingcalculator",
		Scope:    accountID + "/" + region,
		Message:  "a bill-estimate or workload-estimate listing was refused after earlier phases stored rows; that phase and the phases after it were not scanned: " + err.Error(),
	})
}

// scanBcmPCOlderPhases runs the phases that predate the child phases, stopping
// at the first hard error. It also returns the bill scenarios and workload
// estimates stored.
func scanBcmPCOlderPhases(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, scenarios, workloads []childParent, err error) {
	scenarios, total, inserted, err = scanBcmPricingCalculatorBillScenarios(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, nil, nil, err
	}
	t, i, err := scanBcmPricingCalculatorBillEstimates(ctx, client, acct, region, st, scanID)
	total += t
	inserted += i
	if err != nil {
		return total, inserted, scenarios, nil, err
	}
	workloads, t, i, err = scanBcmPricingCalculatorWorkloadEstimates(ctx, client, acct, region, st, scanID)
	return total + t, inserted + i, scenarios, workloads, err
}

func scanBcmPricingCalculatorBillScenarios(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := bcmpricingcalculator.NewListBillScenariosPaginator(client, &bcmpricingcalculator.ListBillScenariosInput{})
	var (
		rows    []*store.Resource
		parents []childParent
	)
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return nil, 0, 0, bcmPCListErr(st, "bcmpricingcalculator:ListBillScenarios", acct.ID, region, perr)
		}
		for _, s := range out.Items {
			id := sv(s.Id)
			if id == "" {
				continue
			}
			name := sv(s.Name)
			status := string(s.Status)
			arn := bcmPricingCalculatorNativeID(acct.ID, "bill-scenario", id)
			parents = append(parents, childParent{id: id, arn: arn})
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeBcmPricingCalculatorBillScenario,
				NativeID: arn,
				Name:     &name, Region: regionGlobal, Status: &status, CreatedAt: tp(s.CreatedAt),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, rows, "bcmpricingcalculator bill-scenarios")
	if err != nil {
		return nil, 0, 0, err
	}
	return parents, total, inserted, nil
}

func scanBcmPricingCalculatorBillEstimates(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bcmpricingcalculator.NewListBillEstimatesPaginator(client, &bcmpricingcalculator.ListBillEstimatesInput{})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return 0, 0, bcmPCListErr(st, "bcmpricingcalculator:ListBillEstimates", acct.ID, region, perr)
		}
		for _, e := range out.Items {
			id := sv(e.Id)
			if id == "" {
				continue
			}
			name := sv(e.Name)
			status := string(e.Status)
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeBcmPricingCalculatorBillEstimate,
				NativeID: bcmPricingCalculatorNativeID(acct.ID, "bill-estimate", id),
				Name:     &name, Region: regionGlobal, Status: &status, CreatedAt: tp(e.CreatedAt),
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, rows, "bcmpricingcalculator bill-estimates")
}

func scanBcmPricingCalculatorWorkloadEstimates(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := bcmpricingcalculator.NewListWorkloadEstimatesPaginator(client, &bcmpricingcalculator.ListWorkloadEstimatesInput{})
	var (
		rows    []*store.Resource
		parents []childParent
	)
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return nil, 0, 0, bcmPCListErr(st, "bcmpricingcalculator:ListWorkloadEstimates", acct.ID, region, perr)
		}
		for _, e := range out.Items {
			id := sv(e.Id)
			if id == "" {
				continue
			}
			name := sv(e.Name)
			status := string(e.Status)
			arn := bcmPricingCalculatorNativeID(acct.ID, "workload-estimate", id)
			parents = append(parents, childParent{id: id, arn: arn})
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeBcmPricingCalculatorWorkloadEstimate,
				NativeID: arn,
				Name:     &name, Region: regionGlobal, Status: &status, CreatedAt: tp(e.CreatedAt),
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, rows, "bcmpricingcalculator workload-estimates")
	if err != nil {
		return nil, 0, 0, err
	}
	return parents, total, inserted, nil
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
// phases also skip silently (bcmPCListErr).
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
