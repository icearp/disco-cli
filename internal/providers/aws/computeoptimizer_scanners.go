package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/computeoptimizer"
	cotypes "github.com/aws/aws-sdk-go-v2/service/computeoptimizer/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeComputeOptimizerRecommendationPreference, Service: "compute-optimizer"})
	registerService(serviceEntry{name: "aws:compute-optimizer", fn: scanComputeOptimizer})
}

const (
	computeOptimizerGetPreferencesOp = "compute-optimizer:GetRecommendationPreferences"
	// computeOptimizerPreferencesPageCap is the model's maxResults ceiling.
	computeOptimizerPreferencesPageCap = 1000
)

// computeOptimizerPreferenceTypes are the resource types the model documents
// preferences for (PutRecommendationPreferences lookBackPeriod and
// preferredResources name these six). GetRecommendationPreferences takes one
// required ResourceType; the rest of the enum (LambdaFunction, License, Idle,
// NotApplicable) has no documented preference and is not queried.
var computeOptimizerPreferenceTypes = []cotypes.ResourceType{
	cotypes.ResourceTypeEc2Instance,
	cotypes.ResourceTypeAutoScalingGroup,
	cotypes.ResourceTypeEbsVolume,
	cotypes.ResourceTypeEcsService,
	cotypes.ResourceTypeRdsDbInstance,
	cotypes.ResourceTypeAuroraDbClusterStorage,
}

// computeOptimizerAPI is the narrow Compute Optimizer surface the scanner
// uses; *computeoptimizer.Client satisfies it.
type computeOptimizerAPI interface {
	GetRecommendationPreferences(context.Context, *computeoptimizer.GetRecommendationPreferencesInput, ...func(*computeoptimizer.Options)) (*computeoptimizer.GetRecommendationPreferencesOutput, error)
}

func scanComputeOptimizer(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := computeoptimizer.NewFromConfig(acct.cfg, func(o *computeoptimizer.Options) { o.Region = region })
	return scanComputeOptimizerPreferences(ctx, client, acct, region, st, scanID)
}

// computeOptimizerPreferenceNativeID synthesizes the NativeID of one
// preference: the API issues no ARN, and a preference is identified by its
// resource type and scope. The scope value goes last because a ResourceArn
// scope's value is an ARN.
func computeOptimizerPreferenceNativeID(partition, region, accountID string, resourceType cotypes.ResourceType, scopeName cotypes.ScopeName, scopeValue string) string {
	return arnFor(partition, "compute-optimizer", region, accountID,
		"recommendation-preferences/"+string(resourceType)+"/"+string(scopeName)+"/"+scopeValue)
}

// scanComputeOptimizerPreferences lists the recommendation preferences of
// each resource type in computeOptimizerPreferenceTypes. No Scope is sent, so
// the op chooses which scopes it returns (not verified live); each row is keyed
// by the queried resource type and the scope the API reports for it.
//
// Two errors skip only the resource type they hit, each with a warning naming
// it, so the other types are still listed:
//   - an access denial, since the action takes the compute-optimizer:ResourceType
//     condition key and a policy can deny some types only;
//   - InvalidParameterValueException, since a region may not yet serve
//     preferences for one of the newer types (not observed live).
//
// An account not opted in to Compute Optimizer (OptInRequiredException, which
// the account can clear itself) reports the service disabled when no row was
// stored; after rows were stored it is returned as an error, as is any other
// error.
func scanComputeOptimizerPreferences(ctx context.Context, client computeOptimizerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var total, inserted int
	for _, rt := range computeOptimizerPreferenceTypes {
		rows, lerr := listComputeOptimizerPreferences(ctx, client, acct, region, scanID, rt)
		t, i, uerr := upsertBatch(st, rows, "compute-optimizer recommendation-preferences")
		total, inserted = total+t, inserted+i
		if uerr != nil {
			return total, inserted, uerr
		}
		switch {
		case lerr == nil:
			continue
		case isAPIErrorCode(lerr, "OptInRequiredException") && total == 0:
			return 0, 0, markServiceDisabled(lerr)
		case isAccessDenied(lerr):
			_ = skipIfAccessDenied(st, computeOptimizerGetPreferencesOp, acct.ID, region, fmt.Errorf("ResourceType %s: %w", rt, lerr))
		case isAPIErrorCode(lerr, "InvalidParameterValueException"):
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: computeOptimizerGetPreferencesOp, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("ResourceType %s rejected; this region may not support preferences for it (unverified): %s", rt, lerr),
			})
		default:
			return total, inserted, fmt.Errorf("%s %s: %w", computeOptimizerGetPreferencesOp, rt, lerr)
		}
	}
	return total, inserted, nil
}

// listComputeOptimizerPreferences returns the rows of one resource type's
// preferences, including those of the pages read before an error.
func listComputeOptimizerPreferences(ctx context.Context, client computeOptimizerAPI, acct *account, region, scanID string, rt cotypes.ResourceType) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := computeoptimizer.NewGetRecommendationPreferencesPaginator(client,
		&computeoptimizer.GetRecommendationPreferencesInput{ResourceType: rt},
		func(o *computeoptimizer.GetRecommendationPreferencesPaginatorOptions) {
			o.Limit = computeOptimizerPreferencesPageCap
		})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return rows, err
		}
		for _, p := range out.RecommendationPreferencesDetails {
			if p.Scope == nil || p.Scope.Name == "" || sv(p.Scope.Value) == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:           TypeComputeOptimizerRecommendationPreference,
				NativeID:       computeOptimizerPreferenceNativeID(acct.partition(), region, acct.ID, rt, p.Scope.Name, sv(p.Scope.Value)),
				Region:         &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}
