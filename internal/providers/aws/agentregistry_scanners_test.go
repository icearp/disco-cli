package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/agentregistrycontrol"
	artypes "github.com/aws/aws-sdk-go-v2/service/agentregistrycontrol/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	arDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: agent-registry:List"}
	arThrottled = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	arHardErr   = &smithy.GenericAPIError{Code: "UnrecognizedFailureException", Message: "boom"}
	arCreated   = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

func arClient(handlers map[string]func(in any) (any, error)) *agentregistrycontrol.Client {
	return agentregistrycontrol.NewFromConfig(opStubConfig(handlers, arDenied))
}

func arRegistryARN(id string) string {
	return "arn:aws:agent-registry:" + testRegion + ":" + testAccountID + ":registry/" + id
}

func arRecordARN(registryID, id string) string {
	return arRegistryARN(registryID) + "/record/" + id
}

func arRegistry(id string) artypes.RegistrySummary {
	return artypes.RegistrySummary{
		RegistryId: ptrStr(id), RegistryArn: ptrStr(arRegistryARN(id)), Name: ptrStr("reg " + id),
		Status: artypes.RegistryStatusReady, Description: ptrStr("catalog " + id),
		CreatedAt: &arCreated, UpdatedAt: &arCreated,
	}
}

func arRecord(registryID, id string) artypes.RegistryRecordSummary {
	return artypes.RegistryRecordSummary{
		RegistryArn: ptrStr(arRegistryARN(registryID)), RecordArn: ptrStr(arRecordARN(registryID, id)),
		RecordId: ptrStr(id), Name: ptrStr("rec " + id), RecordType: artypes.RecordTypeMcp,
		RecordVersion: ptrStr("1"), Status: artypes.RegistryRecordStatusApproved,
		CreatedAt: &arCreated, UpdatedAt: &arCreated,
	}
}

// arSeedRegistries stores the registry rows the records are linked under.
func arSeedRegistries(t *testing.T, st *store.Store, ids ...string) []childParent {
	t.Helper()
	regs := make([]childParent, len(ids))
	for i, id := range ids {
		regs[i] = childParent{id: id, arn: arRegistryARN(id)}
		upsertTestResource(t, st, "aws", testAccountID, TypeAgentRegistryRegistry, regs[i].arn, testRegion, "{}")
	}
	return regs
}

func TestScanAgentRegistryRegistries_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	var (
		mu       sync.Mutex
		maxSizes []int32
	)
	handlers := map[string]func(any) (any, error){
		"ListRegistries": func(in any) (any, error) {
			req := in.(*agentregistrycontrol.ListRegistriesInput)
			mu.Lock()
			maxSizes = append(maxSizes, *req.MaxResults)
			mu.Unlock()
			if req.NextToken == nil {
				return &agentregistrycontrol.ListRegistriesOutput{
					Registries: []artypes.RegistrySummary{arRegistry("r1"), {Name: ptrStr("no id")}}, NextToken: ptrStr("p2"),
				}, nil
			}
			return &agentregistrycontrol.ListRegistriesOutput{Registries: []artypes.RegistrySummary{arRegistry("r2")}}, nil
		},
	}
	regs, total, _, err := scanAgentRegistryRegistries(context.Background(), arClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	want := []childParent{{id: "r1", arn: arRegistryARN("r1")}, {id: "r2", arn: arRegistryARN("r2")}}
	if !slices.Equal(regs, want) {
		t.Errorf("registries = %v, want %v", regs, want)
	}
	if !slices.Equal(maxSizes, []int32{100, 100}) {
		t.Errorf("MaxResults = %v, want [100 100]", maxSizes)
	}
	rows := rowsByNativeID(t, st, TypeAgentRegistryRegistry)
	if len(rows) != 2 {
		t.Errorf("registry rows = %d, want 2", len(rows))
	}
	got, ok := rows[arRegistryARN("r2")]
	if !ok {
		t.Fatalf("registry %s missing", arRegistryARN("r2"))
	}
	if got.Type != TypeAgentRegistryRegistry || sv(got.Region) != testRegion || sv(got.Name) != "reg r2" ||
		sv(got.Status) != "READY" || sv(got.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("row = type %q region %q name %q status %q created %q", got.Type, sv(got.Region), sv(got.Name), sv(got.Status), sv(got.CreatedAt))
	}
	var attrs struct {
		RegistryID  string `json:"RegistryId"`
		Description string
	}
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.RegistryID != "r2" || attrs.Description != "catalog r2" {
		t.Errorf("attrs = %+v (%v), want RegistryId r2, Description catalog r2", attrs, err)
	}
}

func TestScanAgentRegistryRegistries_Errors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{"empty", nil, false, 0},
		{"access denied warns", arDenied, false, 1},
		{"other error propagates", arHardErr, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, agentRegistryListRegistriesOp)
			handlers := map[string]func(any) (any, error){
				"ListRegistries": func(any) (any, error) {
					if tc.err != nil {
						return nil, tc.err
					}
					return &agentregistrycontrol.ListRegistriesOutput{}, nil
				},
			}
			regs, total, _, err := scanAgentRegistryRegistries(context.Background(), arClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantErr != (err != nil) || (tc.wantErr && !errors.Is(err, tc.err)) {
				t.Errorf("err = %v, want error %v", err, tc.wantErr)
			}
			if total != 0 || len(regs) != 0 {
				t.Errorf("total, registries = %d, %v; want 0, none", total, regs)
			}
			if *warned != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warned, tc.wantWarns)
			}
		})
	}
}

// A failure on a later page keeps the earlier pages' rows and returns their
// registries, so the records phase still lists them.
func TestScanAgentRegistryRegistries_LaterPageErrorKeepsStoredRegistries(t *testing.T) {
	st := newTestStore(t)
	handlers := map[string]func(any) (any, error){
		"ListRegistries": func(in any) (any, error) {
			if in.(*agentregistrycontrol.ListRegistriesInput).NextToken == nil {
				return &agentregistrycontrol.ListRegistriesOutput{Registries: []artypes.RegistrySummary{arRegistry("r1")}, NextToken: ptrStr("p2")}, nil
			}
			return nil, arHardErr
		},
	}
	regs, total, _, err := scanAgentRegistryRegistries(context.Background(), arClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, arHardErr) {
		t.Errorf("err = %v, want the page-2 error", err)
	}
	if total != 1 || !slices.Equal(regs, []childParent{{id: "r1", arn: arRegistryARN("r1")}}) {
		t.Errorf("total, registries = %d, %v; want 1, [r1]", total, regs)
	}
}

func TestScanAgentRegistryRecords_StoresEachRegistrysRecords(t *testing.T) {
	st := newTestStore(t)
	regs := arSeedRegistries(t, st, "r1", "r2", "r3")
	calls := &parentCalls{}
	var (
		mu       sync.Mutex
		maxSizes []int32
	)
	handlers := map[string]func(any) (any, error){
		"ListRegistryRecords": func(in any) (any, error) {
			req := in.(*agentregistrycontrol.ListRegistryRecordsInput)
			calls.add(*req.RegistryId)
			mu.Lock()
			maxSizes = append(maxSizes, *req.MaxResults)
			mu.Unlock()
			switch {
			case *req.RegistryId == "r3":
				return &agentregistrycontrol.ListRegistryRecordsOutput{}, nil
			case *req.RegistryId == "r2":
				return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: []artypes.RegistryRecordSummary{arRecord("r2", "c3"), {Name: ptrStr("no arn")}}}, nil
			case req.NextToken == nil:
				return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: []artypes.RegistryRecordSummary{arRecord("r1", "c1")}, NextToken: ptrStr("p2")}, nil
			default:
				return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: []artypes.RegistryRecordSummary{arRecord("r1", "c2")}}, nil
			}
		},
	}
	total, _, err := scanAgentRegistryRecords(context.Background(), arClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, regs)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if got, want := calls.distinct(), []string{"r1", "r2", "r3"}; !slices.Equal(got, want) {
		t.Errorf("registries queried %v, want %v", got, want)
	}
	for _, n := range maxSizes {
		if n != 100 {
			t.Errorf("MaxResults = %d, want 100", n)
		}
	}
	rows := rowsByNativeID(t, st, TypeAgentRegistryRegistryRecord)
	if len(rows) != 3 {
		t.Errorf("record rows = %d, want 3", len(rows))
	}
	for _, c := range []struct{ reg, id string }{{"r1", "c1"}, {"r1", "c2"}, {"r2", "c3"}} {
		native := arRecordARN(c.reg, c.id)
		if _, ok := rows[native]; !ok {
			t.Errorf("record %s missing", native)
		}
		assertContainsNative(t, st, arRegistryARN(c.reg), native)
	}
	got := rows[arRecordARN("r2", "c3")]
	if got.Type != TypeAgentRegistryRegistryRecord || sv(got.Region) != testRegion || sv(got.Name) != "rec c3" ||
		sv(got.Status) != "APPROVED" || sv(got.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("row = type %q region %q name %q status %q created %q", got.Type, sv(got.Region), sv(got.Name), sv(got.Status), sv(got.CreatedAt))
	}
	var attrs struct{ RecordType, RecordVersion string }
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.RecordType != "MCP" || attrs.RecordVersion != "1" {
		t.Errorf("attrs = %+v (%v), want RecordType MCP, RecordVersion 1", attrs, err)
	}
}

// One registry fails with code; the other two keep their records.
func TestScanAgentRegistryRecords_PerRegistryErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{"deleted after listing is silent", &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"}, false, 0},
		{"conflict warns once", &smithy.GenericAPIError{Code: "ConflictException", Message: "not ready"}, false, 1},
		{"validation warns once", &smithy.GenericAPIError{Code: "ValidationException", Message: "not ready"}, false, 1},
		{"denied warns once", arDenied, false, 1},
		{"transient warns once", arThrottled, false, 1},
		{"other error propagates", arHardErr, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			regs := arSeedRegistries(t, st, "r1", "r2", "r3", "r4")
			warned := countOpWarnings(st, agentRegistryListRecordsOp)
			handlers := map[string]func(any) (any, error){
				"ListRegistryRecords": func(in any) (any, error) {
					id := *in.(*agentregistrycontrol.ListRegistryRecordsInput).RegistryId
					if id == "r2" || id == "r4" {
						return nil, tc.err
					}
					return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: []artypes.RegistryRecordSummary{arRecord(id, "c-"+id)}}, nil
				},
			}
			_, _, err := scanAgentRegistryRecords(context.Background(), arClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, regs)
			if tc.wantErr {
				if !errors.Is(err, tc.err) {
					t.Errorf("err = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if *warned != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warned, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeAgentRegistryRegistryRecord)
			if len(rows) != 2 {
				t.Errorf("record rows = %d, want 2 (r1, r3)", len(rows))
			}
			for _, id := range []string{"r1", "r3"} {
				if _, ok := rows[arRecordARN(id, "c-"+id)]; !ok {
					t.Errorf("record of %s missing", id)
				}
			}
		})
	}
}

func arTopLevelHandlers(override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListRegistries": func(any) (any, error) {
			return &agentregistrycontrol.ListRegistriesOutput{Registries: []artypes.RegistrySummary{arRegistry("r1"), arRegistry("r2")}}, nil
		},
		"ListRegistryRecords": func(in any) (any, error) {
			id := *in.(*agentregistrycontrol.ListRegistryRecordsInput).RegistryId
			return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: []artypes.RegistryRecordSummary{arRecord(id, "c-"+id)}}, nil
		},
	}
	for op, fn := range override {
		h[op] = fn
	}
	return h
}

// The records phase queries exactly the registries the first phase listed.
func TestScanAgentRegistryAll_ListsRecordsOfListedRegistries(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanAgentRegistryAll(context.Background(), arClient(arTopLevelHandlers(nil)), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4 (2 registries + 2 records)", total)
	}
	for _, id := range []string{"r1", "r2"} {
		assertContainsNative(t, st, arRegistryARN(id), arRecordARN(id, "c-"+id))
	}
	assertContainsEdgesDeclared(t, st)
}

// A throttled registries page is demoted to a warning so that the records
// phase's hard error still classifies the service as failed.
func TestScanAgentRegistryAll_TransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, agentRegistryListRegistriesOp)
	handlers := arTopLevelHandlers(map[string]func(any) (any, error){
		"ListRegistries": func(in any) (any, error) {
			if in.(*agentregistrycontrol.ListRegistriesInput).NextToken == nil {
				return &agentregistrycontrol.ListRegistriesOutput{Registries: []artypes.RegistrySummary{arRegistry("r1")}, NextToken: ptrStr("p2")}, nil
			}
			return nil, arThrottled
		},
		"ListRegistryRecords": func(any) (any, error) { return nil, arHardErr },
	})
	total, _, err := scanAgentRegistryAll(context.Background(), arClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if !errors.Is(err, arHardErr) {
		t.Errorf("err = %v, want the ListRegistryRecords error", err)
	}
	if *warned != 1 {
		t.Errorf("ListRegistries transient warnings = %d, want 1", *warned)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (the registry stored before the throttle)", total)
	}
}

// In a region with no agent-registry endpoint the NXDOMAIN reaches the
// dispatcher unchanged, which reports the region unavailable.
func TestScanAgentRegistryAll_UnreachableEndpointIsUnavailable(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	handlers := arTopLevelHandlers(map[string]func(any) (any, error){
		"ListRegistries": func(any) (any, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "agent-registry-control.us-east-1.api.aws", IsNotFound: true}
		},
	})
	total, _, err := scanAgentRegistryAll(context.Background(), arClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeUnavailable {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeUnavailable", err, got)
	}
	if total != 0 || len(warnings) != 0 {
		t.Errorf("total, warnings = %d, %v; want 0, none", total, warnings)
	}
}
