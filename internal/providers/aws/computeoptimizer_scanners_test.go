package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/computeoptimizer"
	cotypes "github.com/aws/aws-sdk-go-v2/service/computeoptimizer/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	coDenied       = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: compute-optimizer:GetRecommendationPreferences"}
	coOptIn        = &smithy.GenericAPIError{Code: "OptInRequiredException", Message: "The account is not opted in to Compute Optimizer."}
	coInvalidParam = &smithy.GenericAPIError{Code: "InvalidParameterValueException", Message: "invalid resource type"}
	coOtherErr     = &smithy.GenericAPIError{Code: "UnrecognizedFailureException", Message: "boom"}

	coInstanceARN = "arn:aws:ec2:" + testRegion + ":" + testAccountID + ":instance/i-0abc"
	coGroupARN    = "arn:aws:autoscaling:" + testRegion + ":" + testAccountID + ":autoScalingGroup:11111111-2222-3333-4444-555555555555:autoScalingGroupName/web"
)

func coClient(handlers map[string]func(in any) (any, error)) *computeoptimizer.Client {
	return computeoptimizer.NewFromConfig(opStubConfig(handlers, coDenied))
}

func coPref(rt cotypes.ResourceType, scope cotypes.ScopeName, value string) cotypes.RecommendationPreferencesDetail {
	return cotypes.RecommendationPreferencesDetail{
		ResourceType:                  rt,
		Scope:                         &cotypes.Scope{Name: scope, Value: ptrStr(value)},
		EnhancedInfrastructureMetrics: cotypes.EnhancedInfrastructureMetricsActive,
		LookBackPeriod:                cotypes.LookBackPeriodPreferenceDays93,
	}
}

// coPrefsHandler answers GetRecommendationPreferences from byType, keyed by
// the queried resource type; a type without an entry returns no preferences.
func coPrefsHandler(byType map[cotypes.ResourceType]func(*computeoptimizer.GetRecommendationPreferencesInput) (any, error)) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*computeoptimizer.GetRecommendationPreferencesInput)
		if h, ok := byType[req.ResourceType]; ok {
			return h(req)
		}
		return &computeoptimizer.GetRecommendationPreferencesOutput{}, nil
	}
}

func coPrefsOut(prefs ...cotypes.RecommendationPreferencesDetail) (any, error) {
	return &computeoptimizer.GetRecommendationPreferencesOutput{RecommendationPreferencesDetails: prefs}, nil
}

func TestScanComputeOptimizerPreferences_StoresEveryPageAndType(t *testing.T) {
	st := newTestStore(t)
	var (
		mu       sync.Mutex
		queried  []string
		maxSizes []int32
	)
	byType := map[cotypes.ResourceType]func(*computeoptimizer.GetRecommendationPreferencesInput) (any, error){
		cotypes.ResourceTypeEc2Instance: func(req *computeoptimizer.GetRecommendationPreferencesInput) (any, error) {
			if req.NextToken == nil {
				return &computeoptimizer.GetRecommendationPreferencesOutput{
					RecommendationPreferencesDetails: []cotypes.RecommendationPreferencesDetail{
						coPref(cotypes.ResourceTypeEc2Instance, cotypes.ScopeNameAccountId, testAccountID),
						{ResourceType: cotypes.ResourceTypeEc2Instance},
						{ResourceType: cotypes.ResourceTypeEc2Instance, Scope: &cotypes.Scope{Name: cotypes.ScopeNameAccountId}},
					},
					NextToken: ptrStr("p2"),
				}, nil
			}
			return coPrefsOut(coPref(cotypes.ResourceTypeEc2Instance, cotypes.ScopeNameResourceArn, coInstanceARN))
		},
		cotypes.ResourceTypeAutoScalingGroup: func(*computeoptimizer.GetRecommendationPreferencesInput) (any, error) {
			return coPrefsOut(coPref(cotypes.ResourceTypeAutoScalingGroup, cotypes.ScopeNameResourceArn, coGroupARN))
		},
		// ResourceType is model-optional in the response: the row is keyed by
		// the queried type, so a preference that omits it is still stored.
		cotypes.ResourceTypeRdsDbInstance: func(*computeoptimizer.GetRecommendationPreferencesInput) (any, error) {
			return coPrefsOut(coPref("", cotypes.ScopeNameOrganization, "ALL_ACCOUNTS"))
		},
	}
	record := coPrefsHandler(byType)
	handlers := map[string]func(any) (any, error){
		"GetRecommendationPreferences": func(in any) (any, error) {
			req := in.(*computeoptimizer.GetRecommendationPreferencesInput)
			mu.Lock()
			queried = append(queried, string(req.ResourceType))
			maxSizes = append(maxSizes, *req.MaxResults)
			mu.Unlock()
			return record(in)
		},
	}

	total, _, err := scanComputeOptimizerPreferences(context.Background(), coClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
	slices.Sort(queried)
	wantQueried := []string{"AuroraDBClusterStorage", "AutoScalingGroup", "EbsVolume", "EcsService", "Ec2Instance", "Ec2Instance", "RdsDBInstance"}
	slices.Sort(wantQueried)
	if !slices.Equal(queried, wantQueried) {
		t.Errorf("queried resource types = %v, want %v", queried, wantQueried)
	}
	for _, n := range maxSizes {
		if n != 1000 {
			t.Errorf("MaxResults = %v, want 1000 on every call", maxSizes)
			break
		}
	}

	rows := rowsByNativeID(t, st, TypeComputeOptimizerRecommendationPreference)
	prefix := "arn:aws:compute-optimizer:us-east-1:123456789012:recommendation-preferences/"
	want := []string{
		prefix + "Ec2Instance/AccountId/123456789012",
		prefix + "Ec2Instance/ResourceArn/" + coInstanceARN,
		prefix + "AutoScalingGroup/ResourceArn/" + coGroupARN,
		prefix + "RdsDBInstance/Organization/ALL_ACCOUNTS",
	}
	if len(rows) != len(want) {
		t.Errorf("rows = %d, want %d", len(rows), len(want))
	}
	for _, id := range want {
		got, ok := rows[id]
		if !ok {
			t.Errorf("row %s missing", id)
			continue
		}
		if got.Type != TypeComputeOptimizerRecommendationPreference || sv(got.Region) != testRegion || got.ManagedByProvider {
			t.Errorf("row %s = type %q region %q managed %v", id, got.Type, sv(got.Region), got.ManagedByProvider)
		}
	}
	var attrs struct {
		EnhancedInfrastructureMetrics string
		LookBackPeriod                string
		Scope                         struct{ Name, Value string }
	}
	if err := json.Unmarshal([]byte(rows[want[1]].AttributesJSON), &attrs); err != nil ||
		attrs.EnhancedInfrastructureMetrics != "Active" || attrs.LookBackPeriod != "DAYS_93" ||
		attrs.Scope.Name != "ResourceArn" || attrs.Scope.Value != coInstanceARN {
		t.Errorf("attrs = %+v (%v), want Active, DAYS_93, ResourceArn %s", attrs, err, coInstanceARN)
	}
}

func TestScanComputeOptimizerPreferences_Errors(t *testing.T) {
	ec2, asg, ebs, ecs := cotypes.ResourceTypeEc2Instance, cotypes.ResourceTypeAutoScalingGroup, cotypes.ResourceTypeEbsVolume, cotypes.ResourceTypeEcsService
	for _, tc := range []struct {
		name string
		// withRows makes every type without an error return one account
		// preference; errs fails the listed types' queries; pageErr fails the
		// second Ec2Instance page after its first page listed a row.
		withRows             bool
		errs                 map[cotypes.ResourceType]error
		pageErr              error
		wantRows             int
		wantWarnTypes        []cotypes.ResourceType
		wantErr, wantDisable bool
	}{
		{name: "empty"},
		{name: "access denied warns", errs: map[cotypes.ResourceType]error{ec2: coDenied}, wantWarnTypes: []cotypes.ResourceType{ec2}},
		{
			name: "access denied on one type keeps the others", withRows: true, errs: map[cotypes.ResourceType]error{ebs: coDenied},
			wantRows: 5, wantWarnTypes: []cotypes.ResourceType{ebs},
		},
		{
			name: "access denied on two types warns once per type", withRows: true, errs: map[cotypes.ResourceType]error{ec2: coDenied, ebs: coDenied},
			wantRows: 4, wantWarnTypes: []cotypes.ResourceType{ec2, ebs},
		},
		{
			name: "invalid parameter on one type warns and keeps the others", withRows: true, errs: map[cotypes.ResourceType]error{ecs: coInvalidParam},
			wantRows: 5, wantWarnTypes: []cotypes.ResourceType{ecs},
		},
		{name: "not opted in reports disabled", errs: map[cotypes.ResourceType]error{ec2: coOptIn}, wantErr: true, wantDisable: true},
		{name: "not opted in after rows is an error", withRows: true, errs: map[cotypes.ResourceType]error{asg: coOptIn}, wantRows: 1, wantErr: true},
		{name: "other error propagates", errs: map[cotypes.ResourceType]error{ec2: coOtherErr}, wantErr: true},
		{name: "other error after rows keeps them", withRows: true, errs: map[cotypes.ResourceType]error{asg: coOtherErr}, wantRows: 1, wantErr: true},
		{name: "other error on a later page keeps earlier pages", pageErr: coOtherErr, wantRows: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var warnings []string
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == computeOptimizerGetPreferencesOp {
					warnings = append(warnings, w.Message)
				}
			}
			byType := map[cotypes.ResourceType]func(*computeoptimizer.GetRecommendationPreferencesInput) (any, error){}
			for _, rt := range computeOptimizerPreferenceTypes {
				if err, ok := tc.errs[rt]; ok {
					byType[rt] = func(*computeoptimizer.GetRecommendationPreferencesInput) (any, error) { return nil, err }
				} else if tc.withRows {
					byType[rt] = func(*computeoptimizer.GetRecommendationPreferencesInput) (any, error) {
						return coPrefsOut(coPref(rt, cotypes.ScopeNameAccountId, testAccountID))
					}
				}
			}
			if tc.pageErr != nil {
				byType[ec2] = func(req *computeoptimizer.GetRecommendationPreferencesInput) (any, error) {
					if req.NextToken == nil {
						return &computeoptimizer.GetRecommendationPreferencesOutput{
							RecommendationPreferencesDetails: []cotypes.RecommendationPreferencesDetail{coPref(ec2, cotypes.ScopeNameAccountId, testAccountID)},
							NextToken:                        ptrStr("p2"),
						}, nil
					}
					return nil, tc.pageErr
				}
			}
			handlers := map[string]func(any) (any, error){"GetRecommendationPreferences": coPrefsHandler(byType)}

			total, _, err := scanComputeOptimizerPreferences(context.Background(), coClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got := errors.Is(err, errServiceDisabled); got != tc.wantDisable {
				t.Errorf("errors.Is(err, errServiceDisabled) = %v, want %v (err %v)", got, tc.wantDisable, err)
			}
			if tc.wantErr && !tc.wantDisable && !isAPIErrorCode(err, "OptInRequiredException", "UnrecognizedFailureException") {
				t.Errorf("err = %v, want the API error wrapped", err)
			}
			if len(warnings) != len(tc.wantWarnTypes) {
				t.Errorf("warnings = %q, want one per type %v", warnings, tc.wantWarnTypes)
			}
			for _, rt := range tc.wantWarnTypes {
				if !slices.ContainsFunc(warnings, func(m string) bool { return strings.Contains(m, "ResourceType "+string(rt)) }) {
					t.Errorf("warnings %q name no ResourceType %s", warnings, rt)
				}
			}
			if got := len(rowsByNativeID(t, st, TypeComputeOptimizerRecommendationPreference)); got != tc.wantRows {
				t.Errorf("rows = %d, want %d", got, tc.wantRows)
			}
			if !tc.wantDisable && total != tc.wantRows {
				t.Errorf("total = %d, want %d", total, tc.wantRows)
			}
		})
	}
}

// TestScanComputeOptimizer_TopLevelStoresPreferences pins the wiring from the
// registered service function to the preference rows.
func TestScanComputeOptimizer_TopLevelStoresPreferences(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"GetRecommendationPreferences": coPrefsHandler(map[cotypes.ResourceType]func(*computeoptimizer.GetRecommendationPreferencesInput) (any, error){
			cotypes.ResourceTypeEbsVolume: func(*computeoptimizer.GetRecommendationPreferencesInput) (any, error) {
				return coPrefsOut(coPref(cotypes.ResourceTypeEbsVolume, cotypes.ScopeNameAccountId, testAccountID))
			},
		}),
	}, coDenied)

	if _, _, err := scanComputeOptimizer(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("err = %v", err)
	}
	id := "arn:aws:compute-optimizer:us-east-1:123456789012:recommendation-preferences/EbsVolume/AccountId/123456789012"
	if _, ok := rowsByNativeID(t, st, TypeComputeOptimizerRecommendationPreference)[id]; !ok {
		t.Errorf("row %s missing", id)
	}
}
