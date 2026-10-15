package aws

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rekognitiontypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
	"github.com/icearp/disco-cli/store"
)

const (
	rekTestProjA      = "arn:aws:rekognition:us-east-1:123456789012:project/alpha/1700000000"
	rekTestProjB      = "arn:aws:rekognition:us-east-1:123456789012:project/beta/1700000001"
	rekTestCollection = "arn:aws:rekognition:us-east-1:123456789012:collection/faces"
	rekTestPolA1      = rekTestProjA + "/policy/share-a1"
	rekTestPolA2      = rekTestProjA + "/policy/share-a2"
	rekTestPolB1      = rekTestProjB + "/policy/share-b1"
)

var (
	rekDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: rekognition:ListProjectPolicies")
	rekBadInput = apiErr("InvalidParameterException", "bad")
)

func rekPolicy(projectARN, name string) rekognitiontypes.ProjectPolicy {
	return rekognitiontypes.ProjectPolicy{
		ProjectArn:        sdkaws.String(projectARN),
		PolicyName:        sdkaws.String(name),
		PolicyRevisionId:  sdkaws.String("rev-" + name),
		PolicyDocument:    sdkaws.String(`{"Version":"2012-10-17","Statement":[]}`),
		CreationTimestamp: sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
	}
}

// rekHandlers answers every Rekognition op the scan calls: one collection, two
// projects, no versions or stream processors, and policies listed over two
// pages for project A (plus a nameless element to skip) and one for project B.
// Each ListProjectPolicies call is recorded in policyCalls.
func rekHandlers(policyCalls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListCollections": func(any) (any, error) {
			return &rekognition.ListCollectionsOutput{CollectionIds: []string{"faces"}}, nil
		},
		"DescribeProjects": func(any) (any, error) {
			return &rekognition.DescribeProjectsOutput{ProjectDescriptions: []rekognitiontypes.ProjectDescription{
				{ProjectArn: sdkaws.String(rekTestProjA), Status: rekognitiontypes.ProjectStatusCreated},
				{ProjectArn: sdkaws.String(rekTestProjB), Status: rekognitiontypes.ProjectStatusCreated},
			}}, nil
		},
		"DescribeProjectVersions": func(any) (any, error) {
			return &rekognition.DescribeProjectVersionsOutput{}, nil
		},
		"ListStreamProcessors": func(any) (any, error) {
			return &rekognition.ListStreamProcessorsOutput{}, nil
		},
		"ListProjectPolicies": func(in any) (any, error) {
			p := in.(*rekognition.ListProjectPoliciesInput)
			policyCalls.add(sdkaws.ToString(p.ProjectArn))
			if sdkaws.ToInt32(p.MaxResults) != rekProjectPolicyPageLimit {
				return nil, apiErr("ValidationException", "MaxResults must be at most 5")
			}
			switch sdkaws.ToString(p.ProjectArn) {
			case rekTestProjA:
				if p.NextToken == nil {
					return &rekognition.ListProjectPoliciesOutput{
						ProjectPolicies: []rekognitiontypes.ProjectPolicy{rekPolicy(rekTestProjA, "share-a1"), {ProjectArn: sdkaws.String(rekTestProjA)}},
						NextToken:       sdkaws.String("page2"),
					}, nil
				}
				return &rekognition.ListProjectPoliciesOutput{ProjectPolicies: []rekognitiontypes.ProjectPolicy{rekPolicy(rekTestProjA, "share-a2")}}, nil
			case rekTestProjB:
				return &rekognition.ListProjectPoliciesOutput{ProjectPolicies: []rekognitiontypes.ProjectPolicy{rekPolicy(rekTestProjB, "share-b1")}}, nil
			}
			return nil, apiErr("ResourceNotFoundException", "unknown project")
		},
	}
}

func scanRekognitionTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, rekDenied)
	total, _, err := scanRekognition(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanRekognition_StoresProjectPolicies(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanRekognitionTopLevel(t, st, rekHandlers(&calls))
	if err != nil {
		t.Fatalf("scanRekognition: %v", err)
	}
	// 1 collection + 2 projects + 3 policies
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{rekTestProjA, rekTestProjB}) {
		t.Errorf("ListProjectPolicies parents = %v, want both projects", got)
	}
	rows := rowsByNativeID(t, st, TypeRekognitionProjectPolicy)
	if got := nativeIDs(rows); !slices.Equal(got, []string{rekTestPolA1, rekTestPolA2, rekTestPolB1}) {
		t.Fatalf("policy rows = %v, want both pages of A and B's policy", got)
	}
	r := rows[rekTestPolA2]
	if r.Type != TypeRekognitionProjectPolicy || sv(r.Region) != testRegion || sv(r.Name) != "share-a2" {
		t.Errorf("row = type %q region %q name %q", r.Type, sv(r.Region), sv(r.Name))
	}
	if sv(r.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("CreatedAt = %q, want 2026-01-02T03:04:05Z", sv(r.CreatedAt))
	}
	var attrs struct {
		PolicyRevisionID string `json:"PolicyRevisionId"`
		PolicyDocument   string `json:"PolicyDocument"`
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs.PolicyRevisionID != "rev-share-a2" || attrs.PolicyDocument == "" {
		t.Errorf("attrs = %+v, want the SDK policy verbatim", attrs)
	}
	assertContainsNative(t, st, rekTestProjA, rekTestPolA1)
	assertContainsNative(t, st, rekTestProjA, rekTestPolA2)
	assertContainsNative(t, st, rekTestProjB, rekTestPolB1)
}

func TestScanRekognition_ProjectsWithoutPolicies(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := rekHandlers(&calls)
	h["ListProjectPolicies"] = func(in any) (any, error) {
		calls.add(sdkaws.ToString(in.(*rekognition.ListProjectPoliciesInput).ProjectArn))
		return &rekognition.ListProjectPoliciesOutput{}, nil
	}
	if _, err := scanRekognitionTopLevel(t, st, h); err != nil {
		t.Fatalf("scanRekognition: %v", err)
	}
	if n := len(rowsByNativeID(t, st, TypeRekognitionProjectPolicy)); n != 0 {
		t.Errorf("policy rows = %d, want 0", n)
	}
	if got := calls.distinct(); len(got) != 2 {
		t.Errorf("ListProjectPolicies parents = %v, want both projects", got)
	}
}

// A project deleted since listing, denied, or failing transiently after
// retries drops only its own policies: deleted is silent, a denial warns once
// however many projects hit it, and a transient error warns once. Any other
// error fails the phase (childFanOut stores none of its rows) and is returned;
// the older phases' rows stay.
func TestScanRekognition_FailedPolicyParent(t *testing.T) {
	throttled := apiErr("ThrottlingException", "slow down")
	for _, tc := range []struct {
		name      string
		fail      map[string]error
		wantCode  string // "" means the scan returns nil
		wantRows  []string
		wantWarns int
	}{
		{"project gone", map[string]error{rekTestProjA: apiErr("ResourceNotFoundException", "gone")}, "", []string{rekTestPolB1}, 0},
		{"denied everywhere", map[string]error{rekTestProjA: rekDenied, rekTestProjB: rekDenied}, "", nil, 1},
		{"throttled everywhere", map[string]error{rekTestProjA: throttled, rekTestProjB: throttled}, "", nil, 1},
		{"provisioned throughput exceeded", map[string]error{rekTestProjA: apiErr("ProvisionedThroughputExceededException", "rate exceeded")}, "", []string{rekTestPolB1}, 1},
		{"other error", map[string]error{rekTestProjA: rekBadInput}, "InvalidParameterException", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, rekProjectPoliciesOp)
			var calls parentCalls
			h := rekHandlers(&calls)
			next := h["ListProjectPolicies"]
			h["ListProjectPolicies"] = func(in any) (any, error) {
				if err, ok := tc.fail[sdkaws.ToString(in.(*rekognition.ListProjectPoliciesInput).ProjectArn)]; ok {
					return nil, err
				}
				return next(in)
			}
			_, err := scanRekognitionTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
				if isTransientNetworkError(err) {
					t.Errorf("err = %v reads as transient, want a hard error", err)
				}
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeRekognitionProjectPolicy)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("policy rows = %v, want %v", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if _, ok := rowsByNativeID(t, st, TypeRekognitionCollection)[rekTestCollection]; !ok {
				t.Error("collection missing; the policy phase must not stop the older phases")
			}
		})
	}
}

// A hard error in an older phase after the projects were stored still lets
// the policy phase run over them, and is returned.
func TestScanRekognition_OlderPhaseErrorStillRunsPolicies(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := rekHandlers(&calls)
	h["ListStreamProcessors"] = func(any) (any, error) { return nil, rekBadInput }
	total, err := scanRekognitionTopLevel(t, st, h)
	assertAPICode(t, err, "InvalidParameterException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeRekognitionProjectPolicy)); !slices.Equal(got, []string{rekTestPolA1, rekTestPolA2, rekTestPolB1}) {
		t.Errorf("policy rows = %v, want all three", got)
	}
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
}

// A transient error in an older phase is demoted to a warning so that the
// policy phase's hard error still reaches the dispatcher as a hard error.
func TestScanRekognition_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "rekognition")
	var calls parentCalls
	h := rekHandlers(&calls)
	h["ListStreamProcessors"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["ListProjectPolicies"] = func(any) (any, error) { return nil, rekBadInput }
	_, err := scanRekognitionTopLevel(t, st, h)
	assertAPICode(t, err, "InvalidParameterException")
	if isTransientNetworkError(err) {
		t.Errorf("err = %v reads as transient; the hard policy error must not be masked", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}
