package aws

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/icearp/disco-cli/store"
)

// stubLambdaPerFn serves the per-function version and provisioned
// concurrency lists from page sets keyed by function name; errFor fails
// every call for that function.
type stubLambdaPerFn struct {
	lambdaAPI
	mu       sync.Mutex
	calls    []string
	errFor   map[string]error
	versions map[string][][]lambdatypes.FunctionConfiguration
	pcs      map[string][][]lambdatypes.ProvisionedConcurrencyConfigListItem
}

func (s *stubLambdaPerFn) record(fn string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, fn)
	return s.errFor[fn]
}

func (s *stubLambdaPerFn) ListVersionsByFunction(_ context.Context, in *lambda.ListVersionsByFunctionInput, _ ...func(*lambda.Options)) (*lambda.ListVersionsByFunctionOutput, error) {
	if err := s.record(sv(in.FunctionName)); err != nil {
		return nil, err
	}
	items, next, err := stubPage(s.versions[sv(in.FunctionName)], in.Marker, nil)
	if err != nil {
		return nil, err
	}
	return &lambda.ListVersionsByFunctionOutput{Versions: items, NextMarker: next}, nil
}

func (s *stubLambdaPerFn) ListProvisionedConcurrencyConfigs(_ context.Context, in *lambda.ListProvisionedConcurrencyConfigsInput, _ ...func(*lambda.Options)) (*lambda.ListProvisionedConcurrencyConfigsOutput, error) {
	if err := s.record(sv(in.FunctionName)); err != nil {
		return nil, err
	}
	items, next, err := stubPage(s.pcs[sv(in.FunctionName)], in.Marker, nil)
	if err != nil {
		return nil, err
	}
	return &lambda.ListProvisionedConcurrencyConfigsOutput{ProvisionedConcurrencyConfigs: items, NextMarker: next}, nil
}

func lambdaFnARN(name string) string {
	return "arn:aws:lambda:" + testRegion + ":" + testAccountID + ":function:" + name
}

func lambdaPC(fn, qualifier string) lambdatypes.ProvisionedConcurrencyConfigListItem {
	return lambdatypes.ProvisionedConcurrencyConfigListItem{
		FunctionArn:                              sdkaws.String(lambdaFnARN(fn) + ":" + qualifier),
		RequestedProvisionedConcurrentExecutions: sdkaws.Int32(5),
		Status:                                   lambdatypes.ProvisionedConcurrencyStatusEnumReady,
	}
}

// lambdaPCFixture seeds functions f1 and f2 (rows plus stub pages): f1 has
// configs on alias "live" and version "3" across two pages plus one with no
// FunctionArn; f2 has one on version "1".
func lambdaPCFixture(t *testing.T, st *store.Store) (*stubLambdaPerFn, []lambdaFunctionSummary) {
	t.Helper()
	fns := []lambdaFunctionSummary{{name: "f1", arn: lambdaFnARN("f1")}, {name: "f2", arn: lambdaFnARN("f2")}}
	for _, fn := range fns {
		upsertTestResource(t, st, "aws", testAccountID, TypeLambdaFunction, fn.arn, testRegion, "{}")
	}
	stub := &stubLambdaPerFn{pcs: map[string][][]lambdatypes.ProvisionedConcurrencyConfigListItem{
		"f1": {{lambdaPC("f1", "live"), {Status: lambdatypes.ProvisionedConcurrencyStatusEnumReady}}, {lambdaPC("f1", "3")}},
		"f2": {{lambdaPC("f2", "1")}},
	}}
	return stub, fns
}

func TestScanLambdaProvisionedConcurrencyConfigs_PagesEveryFunction(t *testing.T) {
	st := newTestStore(t)
	stub, fns := lambdaPCFixture(t, st)

	total, _, err := scanLambdaProvisionedConcurrencyConfigs(context.Background(), stub, newTestAccount(testAccountID), fns, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 {
		t.Errorf("total=%d, want 3", total)
	}
	rows := lmvRows(t, st, TypeLambdaProvisionedConcurrencyConfig)
	for _, qualified := range []string{lambdaFnARN("f1") + ":live", lambdaFnARN("f1") + ":3", lambdaFnARN("f2") + ":1"} {
		native := qualified + "/provisioned-concurrency"
		r, ok := rows[native]
		if !ok {
			t.Errorf("missing row %s (have %v)", native, rows)
			continue
		}
		if r.Type != TypeLambdaProvisionedConcurrencyConfig || sv(r.Region) != testRegion || sv(r.Name) != qualified || sv(r.Status) != "READY" {
			t.Errorf("%s: type=%s region=%s name=%s status=%s", native, r.Type, sv(r.Region), sv(r.Name), sv(r.Status))
		}
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(rows[lambdaFnARN("f1")+":live/provisioned-concurrency"].AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["RequestedProvisionedConcurrentExecutions"] != float64(5) {
		t.Errorf("attrs=%v, want RequestedProvisionedConcurrentExecutions 5", attrs)
	}

	f1 := store.ResourceID("aws", testAccountID, lambdaFnARN("f1"))
	rels, err := st.RelationshipsFrom(f1, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, f1, store.ResourceID("aws", testAccountID, lambdaFnARN("f1")+":3/provisioned-concurrency"), store.RelContains)
	for _, r := range rels {
		if r.ToID == store.ResourceID("aws", testAccountID, lambdaFnARN("f2")+":1/provisioned-concurrency") {
			t.Error("f1 contains f2's config")
		}
	}
}

func TestScanLambdaProvisionedConcurrencyConfigs_PerFunctionErrors(t *testing.T) {
	deny := apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/x is not authorized to perform: lambda:ListProvisionedConcurrencyConfigs")
	for _, tc := range []struct {
		name         string
		errFor       map[string]error
		wantRows     int
		wantWarnings int
		wantErr      string
	}{
		{name: "function gone between list and call", errFor: map[string]error{"f1": apiErr("ResourceNotFoundException", "gone")}, wantRows: 1},
		{name: "denied on every function warns once", errFor: map[string]error{"f1": deny, "f2": deny}, wantWarnings: 1},
		{name: "other error", errFor: map[string]error{"f1": apiErr("InvalidParameterValueException", "bad")}, wantErr: "InvalidParameterValueException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := 0
			st.OnWarn = func(store.ScanWarning) { warnings++ }
			stub, fns := lambdaPCFixture(t, st)
			stub.errFor = tc.errFor

			_, _, err := scanLambdaProvisionedConcurrencyConfigs(context.Background(), stub, newTestAccount(testAccountID), fns, testRegion, st, testScanID)
			if tc.wantErr != "" {
				if !isAPIErrorCode(err, tc.wantErr) {
					t.Fatalf("err=%v, want %s propagated", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err=%v, want nil", err)
			}
			if warnings != tc.wantWarnings {
				t.Errorf("warnings=%d, want %d", warnings, tc.wantWarnings)
			}
			if rows := lmvRows(t, st, TypeLambdaProvisionedConcurrencyConfig); len(rows) != tc.wantRows {
				t.Errorf("stored %d rows, want %d", len(rows), tc.wantRows)
			}
		})
	}
}

func TestScanLambdaProvisionedConcurrencyConfigs_Empty(t *testing.T) {
	st := newTestStore(t)
	stub := &stubLambdaPerFn{}
	fns := []lambdaFunctionSummary{{name: "f1", arn: lambdaFnARN("f1")}}
	total, _, err := scanLambdaProvisionedConcurrencyConfigs(context.Background(), stub, newTestAccount(testAccountID), fns, testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("total=%d err=%v, want 0/nil", total, err)
	}
	if rows := lmvRows(t, st, TypeLambdaProvisionedConcurrencyConfig); len(rows) != 0 {
		t.Errorf("stored %d rows, want 0", len(rows))
	}
}
