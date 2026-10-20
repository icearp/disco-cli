package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

const connectTestInstanceARN = "arn:aws:connect:us-east-1:123456789012:instance/i-1"

var (
	connectDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: connect:List"}
	connectNotFound = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"}
	connectBadParam = &smithy.GenericAPIError{Code: "InvalidParameterException", Message: "bad"}
)

// connectStubClient answers each Connect call from handlers keyed by operation
// name; an op without a handler is denied (see opStubConfig).
func connectStubClient(handlers map[string]func(in any) (any, error)) *connect.Client {
	return connect.NewFromConfig(connectStubConfig(handlers))
}

func connectStubConfig(handlers map[string]func(in any) (any, error)) sdkaws.Config {
	return opStubConfig(handlers, connectDenied)
}

func connectInstances(ids ...string) []cttypes.InstanceSummary {
	out := make([]cttypes.InstanceSummary, len(ids))
	for i, id := range ids {
		out[i] = cttypes.InstanceSummary{Id: ptrStr(id), Arn: ptrStr("arn:aws:connect:us-east-1:123456789012:instance/" + id)}
	}
	return out
}

// connectChildCase drives one store-parent child phase. Each parent answers two
// pages; respond returns page two when token is set.
type connectChildCase struct {
	name       string
	parentType string
	parentKind string
	childType  string
	// parent extracts the instance and parent id the phase sent.
	parent  func(in any) (instanceID, parentID string, token *string)
	op      string
	respond func(parentID string, token *string) any
	// wantIDs is the NativeIDs stored for one parent across both pages.
	wantIDs func(parentARN, parentID string) []string
	run     func(context.Context, connectChildrenAPI, *account, *store.Store) (int, int, error)
	check   func(t *testing.T, r store.Resource)
}

func connectChildCases() []connectChildCase {
	page2 := ptrStr("p2")
	return []connectChildCase{
		{
			name: "hours of operation overrides", parentType: TypeConnectHoursOfOperation, parentKind: "operating-hours",
			childType: TypeConnectHoursOfOperationOverride, op: "ListHoursOfOperationOverrides",
			parent: func(in any) (string, string, *string) {
				p := in.(*connect.ListHoursOfOperationOverridesInput)
				return sv(p.InstanceId), sv(p.HoursOfOperationId), p.NextToken
			},
			respond: func(id string, token *string) any {
				if token == nil {
					return &connect.ListHoursOfOperationOverridesOutput{NextToken: page2, HoursOfOperationOverrideList: []cttypes.HoursOfOperationOverride{
						{HoursOfOperationOverrideId: ptrStr(id + "-a"), Name: ptrStr("holiday"), OverrideType: cttypes.OverrideTypeClosed},
					}}
				}
				return &connect.ListHoursOfOperationOverridesOutput{HoursOfOperationOverrideList: []cttypes.HoursOfOperationOverride{
					{HoursOfOperationOverrideId: ptrStr(id + "-b"), Name: ptrStr("holiday")}, {Name: ptrStr("no id")},
				}}
			},
			wantIDs: func(arn, id string) []string {
				return []string{arn + "/override/" + id + "-a", arn + "/override/" + id + "-b"}
			},
			run: func(ctx context.Context, c connectChildrenAPI, a *account, st *store.Store) (int, int, error) {
				return scanConnectHoursOfOperationOverrides(ctx, c, a, testRegion, st, testScanID)
			},
			check: func(t *testing.T, r store.Resource) {
				if sv(r.Name) != "holiday" {
					t.Errorf("Name = %q, want holiday", sv(r.Name))
				}
			},
		},
		{
			name: "use cases", parentType: TypeConnectIntegrationAssociation, parentKind: "integration-association",
			childType: TypeConnectUseCase, op: "ListUseCases",
			parent: func(in any) (string, string, *string) {
				p := in.(*connect.ListUseCasesInput)
				return sv(p.InstanceId), sv(p.IntegrationAssociationId), p.NextToken
			},
			respond: func(id string, token *string) any {
				arn := "arn:aws:connect:us-east-1:123456789012:instance/i-1/use-case/" + id
				if token == nil {
					return &connect.ListUseCasesOutput{NextToken: page2, UseCaseSummaryList: []cttypes.UseCase{
						{UseCaseArn: ptrStr(arn + "-a"), UseCaseType: cttypes.UseCaseTypeRulesEvaluation},
					}}
				}
				return &connect.ListUseCasesOutput{UseCaseSummaryList: []cttypes.UseCase{
					{UseCaseArn: ptrStr(arn + "-b"), UseCaseType: cttypes.UseCaseTypeRulesEvaluation}, {UseCaseId: ptrStr("no arn")},
				}}
			},
			wantIDs: func(_, id string) []string {
				arn := "arn:aws:connect:us-east-1:123456789012:instance/i-1/use-case/" + id
				return []string{arn + "-a", arn + "-b"}
			},
			run: func(ctx context.Context, c connectChildrenAPI, a *account, st *store.Store) (int, int, error) {
				return scanConnectUseCases(ctx, c, a, testRegion, st, testScanID)
			},
			check: func(t *testing.T, r store.Resource) {
				if sv(r.Name) != string(cttypes.UseCaseTypeRulesEvaluation) {
					t.Errorf("Name = %q, want the use-case type", sv(r.Name))
				}
			},
		},
		{
			name: "evaluation form versions", parentType: TypeConnectEvaluationForm, parentKind: "evaluation-form",
			childType: TypeConnectEvaluationFormVersion, op: "ListEvaluationFormVersions",
			parent: func(in any) (string, string, *string) {
				p := in.(*connect.ListEvaluationFormVersionsInput)
				return sv(p.InstanceId), sv(p.EvaluationFormId), p.NextToken
			},
			respond: func(_ string, token *string) any {
				created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
				if token == nil {
					return &connect.ListEvaluationFormVersionsOutput{NextToken: page2, EvaluationFormVersionSummaryList: []cttypes.EvaluationFormVersionSummary{
						{EvaluationFormVersion: 1, Status: cttypes.EvaluationFormVersionStatusActive, CreatedTime: &created, Locked: true},
					}}
				}
				return &connect.ListEvaluationFormVersionsOutput{EvaluationFormVersionSummaryList: []cttypes.EvaluationFormVersionSummary{
					{EvaluationFormVersion: 2, Status: cttypes.EvaluationFormVersionStatusActive, CreatedTime: &created, Locked: true},
				}}
			},
			wantIDs: func(arn, _ string) []string { return []string{arn + "/version/1", arn + "/version/2"} },
			run: func(ctx context.Context, c connectChildrenAPI, a *account, st *store.Store) (int, int, error) {
				return scanConnectEvaluationFormVersions(ctx, c, a, testRegion, st, testScanID)
			},
			check: func(t *testing.T, r store.Resource) {
				if sv(r.Status) != "ACTIVE" || sv(r.CreatedAt) != "2026-01-02T03:04:05Z" {
					t.Errorf("Status, CreatedAt = %q, %q; want ACTIVE, 2026-01-02T03:04:05Z", sv(r.Status), sv(r.CreatedAt))
				}
				var attrs struct{ Locked bool }
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || !attrs.Locked {
					t.Errorf("attributes %s: want Locked true (err %v)", r.AttributesJSON, err)
				}
			},
		},
		{
			name: "workspace pages", parentType: TypeConnectWorkspace, parentKind: "workspace",
			childType: TypeConnectWorkspacePage, op: "ListWorkspacePages",
			parent: func(in any) (string, string, *string) {
				p := in.(*connect.ListWorkspacePagesInput)
				return sv(p.InstanceId), sv(p.WorkspaceId), p.NextToken
			},
			respond: func(_ string, token *string) any {
				if token == nil {
					return &connect.ListWorkspacePagesOutput{NextToken: page2, WorkspacePageList: []cttypes.WorkspacePage{{Page: ptrStr("HOME"), Slug: ptrStr("home")}}}
				}
				return &connect.ListWorkspacePagesOutput{WorkspacePageList: []cttypes.WorkspacePage{{Page: ptrStr("custom"), Slug: ptrStr("custom")}, {Slug: ptrStr("no page")}}}
			},
			wantIDs: func(arn, _ string) []string { return []string{arn + "/page/HOME", arn + "/page/custom"} },
			run: func(ctx context.Context, c connectChildrenAPI, a *account, st *store.Store) (int, int, error) {
				return scanConnectWorkspacePages(ctx, c, a, testRegion, st, testScanID)
			},
			check: func(t *testing.T, r store.Resource) {
				var attrs struct{ Slug string }
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Slug == "" || sv(r.Name) == "" {
					t.Errorf("Name %q attributes %s: want the page and its Slug (err %v)", sv(r.Name), r.AttributesJSON, err)
				}
			},
		},
	}
}

func (c connectChildCase) parentARN(id string) string {
	return connectTestInstanceARN + "/" + c.parentKind + "/" + id
}

// seedParents stores p-1 and p-2 as this scan's parents in testRegion, plus
// one in another region, one in another account and one only an earlier scan
// saw; none of those three may be queried.
func (c connectChildCase) seedParents(t *testing.T, st *store.Store) {
	t.Helper()
	upsertTestResource(t, st, "aws", testAccountID, c.parentType, c.parentARN("p-1"), testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, c.parentType, c.parentARN("p-2"), testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, c.parentType, "arn:aws:connect:eu-west-1:123456789012:instance/i-9/"+c.parentKind+"/p-west", "eu-west-1", "{}")
	upsertTestResource(t, st, "aws", "999999999999", c.parentType, "arn:aws:connect:us-east-1:999999999999:instance/i-other/"+c.parentKind+"/p-other", testRegion, "{}")
	const oldScan = "11111111111111111111111111111111"
	insertTestScan(t, st, oldScan, "completed")
	if _, err := st.UpsertResources([]*store.Resource{{
		Provider: "aws", AccountID: testAccountID, Type: c.parentType, NativeID: c.parentARN("p-stale"),
		Region: ptrStr(testRegion), AttributesJSON: "{}", DiscoveredBy: oldScan,
	}}); err != nil {
		t.Fatalf("seed stale parent: %v", err)
	}
}

func TestConnectChildPhases_PagesEveryParentOfThisScan(t *testing.T) {
	for _, c := range connectChildCases() {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			c.seedParents(t, st)
			var calls parentCalls
			client := connectStubClient(map[string]func(any) (any, error){c.op: func(in any) (any, error) {
				inst, id, token := c.parent(in)
				calls.add(inst + "/" + id)
				return c.respond(id, token), nil
			}})
			total, _, err := c.run(context.Background(), client, newTestAccount(testAccountID), st)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := calls.distinct(); len(got) != 2 || got[0] != "i-1/p-1" || got[1] != "i-1/p-2" {
				t.Errorf("parents queried = %v, want [i-1/p-1 i-1/p-2]", got)
			}
			rows := rowsByNativeID(t, st, c.childType)
			want := append(c.wantIDs(c.parentARN("p-1"), "p-1"), c.wantIDs(c.parentARN("p-2"), "p-2")...)
			if total != len(want) || len(rows) != len(want) {
				t.Errorf("total=%d rows=%d, want %d", total, len(rows), len(want))
			}
			for _, id := range want {
				r, ok := rows[id]
				if !ok {
					t.Errorf("missing row %s; have %v", id, rows)
					continue
				}
				if r.Type != c.childType || sv(r.Region) != testRegion {
					t.Errorf("%s: Type=%s Region=%s", id, r.Type, sv(r.Region))
				}
				c.check(t, r)
			}
			assertContainsNative(t, st, c.parentARN("p-2"), c.wantIDs(c.parentARN("p-2"), "p-2")[1])
		})
	}
}

func TestConnectChildPhases_ParentErrors(t *testing.T) {
	for _, c := range connectChildCases() {
		t.Run(c.name+"/not found parent skipped", func(t *testing.T) {
			st := newTestStore(t)
			c.seedParents(t, st)
			warns := countWarnings(st)
			client := connectStubClient(map[string]func(any) (any, error){c.op: func(in any) (any, error) {
				_, id, token := c.parent(in)
				if id == "p-1" {
					return nil, connectNotFound
				}
				return c.respond(id, token), nil
			}})
			total, _, err := c.run(context.Background(), client, newTestAccount(testAccountID), st)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if want := len(c.wantIDs(c.parentARN("p-2"), "p-2")); total != want || len(rowsByNativeID(t, st, c.childType)) != want {
				t.Errorf("total=%d, want p-2's %d rows only", total, want)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d, want 0 for a deleted parent", *warns)
			}
		})
		t.Run(c.name+"/denied warns once", func(t *testing.T) {
			st := newTestStore(t)
			c.seedParents(t, st)
			warns := countWarnings(st)
			total, _, err := c.run(context.Background(), connectStubClient(nil), newTestAccount(testAccountID), st)
			if err != nil || total != 0 {
				t.Fatalf("got (%d, %v), want (0, nil)", total, err)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d, want 1 for two denied parents", *warns)
			}
		})
		t.Run(c.name+"/other error propagates", func(t *testing.T) {
			st := newTestStore(t)
			c.seedParents(t, st)
			client := connectStubClient(map[string]func(any) (any, error){c.op: func(any) (any, error) { return nil, connectBadParam }})
			if _, _, err := c.run(context.Background(), client, newTestAccount(testAccountID), st); !isAPIErrorCode(err, "InvalidParameterException") {
				t.Errorf("err = %v, want InvalidParameterException", err)
			}
		})
		t.Run(c.name+"/unparsable parent is an error", func(t *testing.T) {
			st := newTestStore(t)
			upsertTestResource(t, st, "aws", testAccountID, c.parentType, connectTestInstanceARN+"/"+c.parentKind, testRegion, "{}")
			if _, _, err := c.run(context.Background(), connectStubClient(nil), newTestAccount(testAccountID), st); !errors.Is(err, errConnectParentARN) {
				t.Errorf("err = %v, want errConnectParentARN", err)
			}
		})
		t.Run(c.name+"/no parents", func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := c.run(context.Background(), connectStubClient(nil), newTestAccount(testAccountID), st)
			if err != nil || total != 0 {
				t.Errorf("got (%d, %v), want (0, nil)", total, err)
			}
		})
	}
}

func TestConnectChildARNIDs(t *testing.T) {
	for _, tc := range []struct{ arn, inst, id string }{
		{connectTestInstanceARN + "/operating-hours/h-1", "i-1", "h-1"},
		{connectTestInstanceARN, "", ""},
		{connectTestInstanceARN + "/view/v-1/version/2", "", ""},
		{"arn:aws:connect:us-east-1:123456789012:traffic-distribution-group/t-1", "", ""},
	} {
		inst, id := connectChildARNIDs(tc.arn)
		if inst != tc.inst || id != tc.id {
			t.Errorf("connectChildARNIDs(%s) = %q, %q; want %q, %q", tc.arn, inst, id, tc.inst, tc.id)
		}
	}
}

func testCaseHandlers(listCalls *parentCalls, describe func(id string) (any, error)) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListTestCases": func(in any) (any, error) {
			p := in.(*connect.ListTestCasesInput)
			inst := sv(p.InstanceId)
			listCalls.add(inst)
			arn := "arn:aws:connect:us-east-1:123456789012:instance/" + inst + "/test-case/"
			if p.NextToken == nil {
				return &connect.ListTestCasesOutput{NextToken: ptrStr("p2"), TestCaseSummaryList: []cttypes.TestCaseSummary{{Arn: ptrStr(arn + "tc-a"), Id: ptrStr(inst + ":tc-a")}}}, nil
			}
			return &connect.ListTestCasesOutput{TestCaseSummaryList: []cttypes.TestCaseSummary{{Arn: ptrStr(arn + "tc-b"), Id: ptrStr(inst + ":tc-b")}, {Id: ptrStr("no arn")}}}, nil
		},
		"DescribeTestCase": func(in any) (any, error) {
			return describe(sv(in.(*connect.DescribeTestCaseInput).TestCaseId))
		},
	}
}

func describedTestCase(id string) (any, error) {
	inst, tc, _ := strings.Cut(id, ":")
	return &connect.DescribeTestCaseOutput{TestCase: &cttypes.TestCase{
		Arn:  ptrStr("arn:aws:connect:us-east-1:123456789012:instance/" + inst + "/test-case/" + tc),
		Id:   ptrStr(tc),
		Name: ptrStr("smoke " + tc), Status: cttypes.TestCaseStatusPublished,
		Content: ptrStr(`{"Version":"2019-10-30"}`), Tags: map[string]string{"team": "qa"},
	}}, nil
}

func TestScanConnectTestCases_DescribesEveryListedCase(t *testing.T) {
	st := newTestStore(t)
	var lists parentCalls
	client := connectStubClient(testCaseHandlers(&lists, describedTestCase))
	total, _, err := scanConnectTestCases(context.Background(), client, connectInstances("i-1", "i-2"), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeConnectTestCase)
	if total != 4 || len(rows) != 4 {
		t.Fatalf("total=%d rows=%d, want 4", total, len(rows))
	}
	r := rows["arn:aws:connect:us-east-1:123456789012:instance/i-2/test-case/tc-b"]
	if r.Type != TypeConnectTestCase || sv(r.Region) != testRegion || sv(r.Name) != "smoke tc-b" || sv(r.Status) != "PUBLISHED" {
		t.Errorf("row = %+v", r)
	}
	if sv(r.TagsJSON) != `{"team":"qa"}` {
		t.Errorf("TagsJSON = %q", sv(r.TagsJSON))
	}
	var attrs struct{ Content string }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Content == "" {
		t.Errorf("attributes %s: want the test Content (err %v)", r.AttributesJSON, err)
	}
}

func TestScanConnectTestCases_Errors(t *testing.T) {
	ctx := context.Background()
	t.Run("deleted instance skipped", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		var lists parentCalls
		h := testCaseHandlers(&lists, describedTestCase)
		list := h["ListTestCases"]
		h["ListTestCases"] = func(in any) (any, error) {
			if sv(in.(*connect.ListTestCasesInput).InstanceId) == "i-1" {
				return nil, connectNotFound
			}
			return list(in)
		}
		total, _, err := scanConnectTestCases(ctx, connectStubClient(h), connectInstances("i-1", "i-2"), newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 2 || *warns != 0 {
			t.Errorf("got (%d, %v, %d warnings), want i-2's 2 rows, nil, 0 warnings", total, err, *warns)
		}
	})
	t.Run("list denied warns once", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		total, _, err := scanConnectTestCases(ctx, connectStubClient(nil), connectInstances("i-1", "i-2"), newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 0 || *warns != 1 {
			t.Errorf("got (%d, %v, %d warnings), want (0, nil, 1)", total, err, *warns)
		}
	})
	t.Run("list error propagates", func(t *testing.T) {
		st := newTestStore(t)
		h := map[string]func(any) (any, error){"ListTestCases": func(any) (any, error) { return nil, connectBadParam }}
		if _, _, err := scanConnectTestCases(ctx, connectStubClient(h), connectInstances("i-1"), newTestAccount(testAccountID), testRegion, st, testScanID); !isAPIErrorCode(err, "InvalidParameterException") {
			t.Errorf("err = %v, want InvalidParameterException", err)
		}
	})
	t.Run("describe not found and denied drop only that case", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		var lists parentCalls
		h := testCaseHandlers(&lists, func(id string) (any, error) {
			switch id {
			case "i-1:tc-a":
				return nil, connectNotFound
			case "i-1:tc-b", "i-2:tc-a":
				return nil, connectDenied
			}
			return describedTestCase(id)
		})
		total, _, err := scanConnectTestCases(ctx, connectStubClient(h), connectInstances("i-1", "i-2"), newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 1 || *warns != 1 {
			t.Errorf("got (%d, %v, %d warnings), want (1, nil, 1)", total, err, *warns)
		}
	})
	t.Run("describe error propagates", func(t *testing.T) {
		st := newTestStore(t)
		var lists parentCalls
		h := testCaseHandlers(&lists, func(string) (any, error) { return nil, connectBadParam })
		if _, _, err := scanConnectTestCases(ctx, connectStubClient(h), connectInstances("i-1"), newTestAccount(testAccountID), testRegion, st, testScanID); !isAPIErrorCode(err, "InvalidParameterException") {
			t.Errorf("err = %v, want InvalidParameterException", err)
		}
	})
	t.Run("no test cases", func(t *testing.T) {
		st := newTestStore(t)
		h := map[string]func(any) (any, error){"ListTestCases": func(any) (any, error) { return &connect.ListTestCasesOutput{}, nil }}
		total, _, err := scanConnectTestCases(ctx, connectStubClient(h), connectInstances("i-1"), newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 0 {
			t.Errorf("got (%d, %v), want (0, nil)", total, err)
		}
	})
}

func attachedFilesHandler(calls *parentCalls) func(any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*connect.ListAttachedFilesConfigurationsInput)
		calls.add(sv(p.InstanceId))
		if p.NextToken == nil {
			return &connect.ListAttachedFilesConfigurationsOutput{NextToken: ptrStr("p2"), AttachedFilesConfigurations: []cttypes.AttachedFilesConfigurationSummary{
				{AttachmentScope: cttypes.AttachmentScopeEmail, InstanceId: p.InstanceId, MaximumSizeLimitInBytes: aws64(20971520)},
			}}, nil
		}
		return &connect.ListAttachedFilesConfigurationsOutput{AttachedFilesConfigurations: []cttypes.AttachedFilesConfigurationSummary{
			{AttachmentScope: cttypes.AttachmentScopeChat, InstanceId: p.InstanceId}, {InstanceId: p.InstanceId},
		}}, nil
	}
}

func aws64(v int64) *int64 { return &v }

func TestScanConnectAttachedFilesConfigurations_OneRowPerScope(t *testing.T) {
	st := newTestStore(t)
	seedConnectInstances(t, st, "i-1", "i-2")
	warns := countWarnings(st)
	var calls parentCalls
	client := connectStubClient(map[string]func(any) (any, error){"ListAttachedFilesConfigurations": attachedFilesHandler(&calls)})
	total, _, err := scanConnectAttachedFilesConfigurations(context.Background(), client, connectInstances("i-1", "i-2"), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); len(got) != 2 {
		t.Errorf("instances queried = %v, want both", got)
	}
	rows := rowsByNativeID(t, st, TypeConnectAttachedFilesConfiguration)
	if total != 4 || len(rows) != 4 {
		t.Fatalf("total=%d rows=%d, want 4", total, len(rows))
	}
	id := connectTestInstanceARN + "/attached-files-configuration/EMAIL"
	r, ok := rows[id]
	if !ok {
		t.Fatalf("missing %s; have %v", id, rows)
	}
	if r.Type != TypeConnectAttachedFilesConfiguration || sv(r.Region) != testRegion || sv(r.Name) != "EMAIL" || r.ManagedByProvider {
		t.Errorf("row = %+v", r)
	}
	var attrs struct{ MaximumSizeLimitInBytes int64 }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.MaximumSizeLimitInBytes != 20971520 {
		t.Errorf("attributes %s (err %v)", r.AttributesJSON, err)
	}
	assertContainsNative(t, st, connectTestInstanceARN, id)
	if *warns != 0 {
		t.Errorf("warnings = %d, want 0", *warns)
	}
}

// seedConnectInstances stores the instance rows childFanOut links children under.
func seedConnectInstances(t *testing.T, st *store.Store, ids ...string) {
	t.Helper()
	for _, inst := range connectInstances(ids...) {
		upsertTestResource(t, st, "aws", testAccountID, TypeConnectInstance, *inst.Arn, testRegion, "{}")
	}
}

func TestScanConnectAttachedFilesConfigurations_Errors(t *testing.T) {
	ctx := context.Background()
	t.Run("deleted instance skipped", func(t *testing.T) {
		st := newTestStore(t)
		seedConnectInstances(t, st, "i-1", "i-2")
		warns := countWarnings(st)
		var calls parentCalls
		ok := attachedFilesHandler(&calls)
		client := connectStubClient(map[string]func(any) (any, error){"ListAttachedFilesConfigurations": func(in any) (any, error) {
			if sv(in.(*connect.ListAttachedFilesConfigurationsInput).InstanceId) == "i-1" {
				return nil, connectNotFound
			}
			return ok(in)
		}})
		total, _, err := scanConnectAttachedFilesConfigurations(ctx, client, connectInstances("i-1", "i-2"), newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 2 || *warns != 0 {
			t.Errorf("got (%d, %v, %d warnings), want (2, nil, 0)", total, err, *warns)
		}
	})
	t.Run("denied warns once", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		total, _, err := scanConnectAttachedFilesConfigurations(ctx, connectStubClient(nil), connectInstances("i-1", "i-2"), newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 0 || *warns != 1 {
			t.Errorf("got (%d, %v, %d warnings), want (0, nil, 1)", total, err, *warns)
		}
	})
	t.Run("other error propagates", func(t *testing.T) {
		st := newTestStore(t)
		client := connectStubClient(map[string]func(any) (any, error){"ListAttachedFilesConfigurations": func(any) (any, error) { return nil, connectBadParam }})
		if _, _, err := scanConnectAttachedFilesConfigurations(ctx, client, connectInstances("i-1"), newTestAccount(testAccountID), testRegion, st, testScanID); !isAPIErrorCode(err, "InvalidParameterException") {
			t.Errorf("err = %v, want InvalidParameterException", err)
		}
	})
}

// connectFamilyHandlers answers the existing families' parent ops for one
// instance with one hours-of-operation, integration association, evaluation
// form and workspace; every other existing op is denied.
func connectFamilyHandlers() map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListInstances": func(any) (any, error) {
			return &connect.ListInstancesOutput{InstanceSummaryList: connectInstances("i-1")}, nil
		},
		"DescribeInstance": func(any) (any, error) {
			return &connect.DescribeInstanceOutput{Instance: &cttypes.Instance{Id: ptrStr("i-1"), Arn: ptrStr(connectTestInstanceARN)}}, nil
		},
		"ListHoursOfOperations": func(any) (any, error) {
			return &connect.ListHoursOfOperationsOutput{HoursOfOperationSummaryList: []cttypes.HoursOfOperationSummary{{Id: ptrStr("h-1")}}}, nil
		},
		"DescribeHoursOfOperation": func(any) (any, error) {
			return &connect.DescribeHoursOfOperationOutput{HoursOfOperation: &cttypes.HoursOfOperation{HoursOfOperationArn: ptrStr(connectTestInstanceARN + "/operating-hours/h-1")}}, nil
		},
		"ListIntegrationAssociations": func(any) (any, error) {
			return &connect.ListIntegrationAssociationsOutput{IntegrationAssociationSummaryList: []cttypes.IntegrationAssociationSummary{
				{IntegrationAssociationArn: ptrStr(connectTestInstanceARN + "/integration-association/ia-1")},
			}}, nil
		},
		"ListEvaluationForms": func(any) (any, error) {
			return &connect.ListEvaluationFormsOutput{EvaluationFormSummaryList: []cttypes.EvaluationFormSummary{{EvaluationFormId: ptrStr("f-1")}}}, nil
		},
		"DescribeEvaluationForm": func(any) (any, error) {
			return &connect.DescribeEvaluationFormOutput{EvaluationForm: &cttypes.EvaluationForm{EvaluationFormArn: ptrStr(connectTestInstanceARN + "/evaluation-form/f-1")}}, nil
		},
		"ListWorkspaces": func(any) (any, error) {
			return &connect.ListWorkspacesOutput{WorkspaceSummaryList: []cttypes.WorkspaceSummary{{Id: ptrStr("w-1")}}}, nil
		},
		"DescribeWorkspace": func(any) (any, error) {
			return &connect.DescribeWorkspaceOutput{Workspace: &cttypes.Workspace{Arn: ptrStr(connectTestInstanceARN + "/workspace/w-1")}}, nil
		},
	}
}

// connectChildOps answers every child op with one row.
var connectChildOps = map[string]func(any) (any, error){
	"ListTestCases": func(any) (any, error) {
		return &connect.ListTestCasesOutput{TestCaseSummaryList: []cttypes.TestCaseSummary{{Arn: ptrStr(connectTestInstanceARN + "/test-case/tc-1"), Id: ptrStr("i-1:tc-1")}}}, nil
	},
	"DescribeTestCase": func(in any) (any, error) {
		return describedTestCase(sv(in.(*connect.DescribeTestCaseInput).TestCaseId))
	},
	"ListAttachedFilesConfigurations": func(any) (any, error) {
		return &connect.ListAttachedFilesConfigurationsOutput{AttachedFilesConfigurations: []cttypes.AttachedFilesConfigurationSummary{{AttachmentScope: cttypes.AttachmentScopeEmail}}}, nil
	},
	"ListHoursOfOperationOverrides": func(any) (any, error) {
		return &connect.ListHoursOfOperationOverridesOutput{HoursOfOperationOverrideList: []cttypes.HoursOfOperationOverride{{HoursOfOperationOverrideId: ptrStr("o-1")}}}, nil
	},
	"ListUseCases": func(any) (any, error) {
		return &connect.ListUseCasesOutput{UseCaseSummaryList: []cttypes.UseCase{{UseCaseArn: ptrStr(connectTestInstanceARN + "/use-case/u-1")}}}, nil
	},
	"ListEvaluationFormVersions": func(any) (any, error) {
		return &connect.ListEvaluationFormVersionsOutput{EvaluationFormVersionSummaryList: []cttypes.EvaluationFormVersionSummary{{EvaluationFormVersion: 1}}}, nil
	},
	"ListWorkspacePages": func(any) (any, error) {
		return &connect.ListWorkspacePagesOutput{WorkspacePageList: []cttypes.WorkspacePage{{Page: ptrStr("HOME")}}}, nil
	},
}

var connectChildRowIDs = map[string]string{
	TypeConnectTestCase:                   connectTestInstanceARN + "/test-case/tc-1",
	TypeConnectAttachedFilesConfiguration: connectTestInstanceARN + "/attached-files-configuration/EMAIL",
	TypeConnectHoursOfOperationOverride:   connectTestInstanceARN + "/operating-hours/h-1/override/o-1",
	TypeConnectUseCase:                    connectTestInstanceARN + "/use-case/u-1",
	TypeConnectEvaluationFormVersion:      connectTestInstanceARN + "/evaluation-form/f-1/version/1",
	TypeConnectWorkspacePage:              connectTestInstanceARN + "/workspace/w-1/page/HOME",
}

func TestScanConnect_ChildrenReadParentsTheFamiliesStored(t *testing.T) {
	st := newTestStore(t)
	h := connectFamilyHandlers()
	maps.Copy(h, connectChildOps)
	acct := newTestAccount(testAccountID)
	acct.cfg = connectStubConfig(h)
	if _, _, err := scanConnect(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanConnect: %v", err)
	}
	for typ, id := range connectChildRowIDs {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s: no row %s", typ, id)
		}
	}
}

// A failing child phase must not cost the rows of the families that ran before it.
func TestScanConnect_ChildPhaseErrorAfterFamilies(t *testing.T) {
	familyRows := map[string]string{
		TypeConnectHoursOfOperation:       connectTestInstanceARN + "/operating-hours/h-1",
		TypeConnectIntegrationAssociation: connectTestInstanceARN + "/integration-association/ia-1",
		TypeConnectEvaluationForm:         connectTestInstanceARN + "/evaluation-form/f-1",
		TypeConnectWorkspace:              connectTestInstanceARN + "/workspace/w-1",
	}
	for failing := range connectChildOps {
		t.Run(failing, func(t *testing.T) {
			st := newTestStore(t)
			h := connectFamilyHandlers()
			maps.Copy(h, connectChildOps)
			h[failing] = func(any) (any, error) { return nil, connectBadParam }
			acct := newTestAccount(testAccountID)
			acct.cfg = connectStubConfig(h)
			if _, _, err := scanConnect(context.Background(), acct, testRegion, st, testScanID); !isAPIErrorCode(err, "InvalidParameterException") {
				t.Fatalf("err = %v, want InvalidParameterException", err)
			}
			for typ, id := range familyRows {
				if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
					t.Errorf("%s: family row %s lost", typ, id)
				}
			}
		})
	}
}
