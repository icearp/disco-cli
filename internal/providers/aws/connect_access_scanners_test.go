package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	connectThrottled = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	connectHardErr   = &smithy.GenericAPIError{Code: "UnrecognizedFailureException", Message: "boom"}
)

// connectAccessCase drives one access phase that reads its parents from the
// store. Each parent answers two pages; respond returns page two when token is
// set.
type connectAccessCase struct {
	name       string
	parentType string
	parentKind string
	childType  string
	op         string
	// parent extracts the instance and parent id the phase sent.
	parent func(in any) (instanceID, parentID string, token *string)
	// pageSize extracts the MaxResults the phase sent; it must be pageCap.
	pageSize func(in any) *int32
	pageCap  int32
	respond  func(parentID string, token *string) any
	// empty is a one-page answer with nothing listed.
	empty any
	// wantIDs is the NativeIDs stored for one parent across both pages.
	wantIDs func(parentARN, parentID string) []string
	// wantEmptyIDs is the NativeIDs stored for one parent answering empty.
	wantEmptyIDs func(parentARN string) []string
	run          func(context.Context, connectAccessAPI, *account, *store.Store) (int, int, error)
	check        func(t *testing.T, r store.Resource)
}

func connectAccessCases() []connectAccessCase {
	page2 := ptrStr("p2")
	return []connectAccessCase{
		{
			name: "security profile permissions", parentType: TypeConnectSecurityProfile, parentKind: "security-profile",
			childType: TypeConnectSecurityProfilePermission, op: "ListSecurityProfilePermissions",
			parent: func(in any) (string, string, *string) {
				p := in.(*connect.ListSecurityProfilePermissionsInput)
				return sv(p.InstanceId), sv(p.SecurityProfileId), p.NextToken
			},
			pageSize: func(in any) *int32 { return in.(*connect.ListSecurityProfilePermissionsInput).MaxResults },
			pageCap:  1000,
			respond: func(_ string, token *string) any {
				if token == nil {
					return &connect.ListSecurityProfilePermissionsOutput{NextToken: page2, Permissions: []string{"Users.View", "BasicAgentAccess"}}
				}
				return &connect.ListSecurityProfilePermissionsOutput{Permissions: []string{"OutboundCallAccess"}}
			},
			empty:        &connect.ListSecurityProfilePermissionsOutput{},
			wantIDs:      func(arn, _ string) []string { return []string{arn + "/permission"} },
			wantEmptyIDs: func(arn string) []string { return []string{arn + "/permission"} },
			run: func(ctx context.Context, c connectAccessAPI, a *account, st *store.Store) (int, int, error) {
				return scanConnectSecurityProfilePermissions(ctx, c, a, testRegion, st, testScanID)
			},
			check: func(t *testing.T, r store.Resource) {
				var attrs struct{ Permissions []string }
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
					t.Fatalf("attributes %s: %v", r.AttributesJSON, err)
				}
				if want := []string{"BasicAgentAccess", "OutboundCallAccess", "Users.View"}; !slices.Equal(attrs.Permissions, want) {
					t.Errorf("Permissions = %v, want both pages sorted %v", attrs.Permissions, want)
				}
			},
		},
		{
			name: "routing profile queues", parentType: TypeConnectRoutingProfile, parentKind: "routing-profile",
			childType: TypeConnectRoutingProfileQueue, op: "ListRoutingProfileQueues",
			parent: func(in any) (string, string, *string) {
				p := in.(*connect.ListRoutingProfileQueuesInput)
				return sv(p.InstanceId), sv(p.RoutingProfileId), p.NextToken
			},
			pageSize: func(in any) *int32 { return in.(*connect.ListRoutingProfileQueuesInput).MaxResults },
			pageCap:  100,
			respond: func(id string, token *string) any {
				queueARN := connectTestInstanceARN + "/queue/q-" + id
				if token == nil {
					return &connect.ListRoutingProfileQueuesOutput{NextToken: page2, RoutingProfileQueueConfigSummaryList: []cttypes.RoutingProfileQueueConfigSummary{
						{QueueId: ptrStr("q-" + id), QueueArn: &queueARN, QueueName: ptrStr("support"), Channel: cttypes.ChannelVoice, Priority: ptrInt32(1), Delay: 30},
					}}
				}
				return &connect.ListRoutingProfileQueuesOutput{RoutingProfileQueueConfigSummaryList: []cttypes.RoutingProfileQueueConfigSummary{
					{QueueId: ptrStr("q-" + id), QueueArn: &queueARN, QueueName: ptrStr("support"), Channel: cttypes.ChannelChat, Priority: ptrInt32(2)},
				}}
			},
			empty: &connect.ListRoutingProfileQueuesOutput{},
			wantIDs: func(arn, id string) []string {
				return []string{arn + "/queue/q-" + id + "/channel/VOICE", arn + "/queue/q-" + id + "/channel/CHAT"}
			},
			wantEmptyIDs: func(string) []string { return nil },
			run: func(ctx context.Context, c connectAccessAPI, a *account, st *store.Store) (int, int, error) {
				return scanConnectRoutingProfileQueues(ctx, c, a, testRegion, st, testScanID)
			},
			check: func(t *testing.T, r store.Resource) {
				if sv(r.Name) != "support" {
					t.Errorf("Name = %q, want support", sv(r.Name))
				}
				var attrs struct {
					Priority *int32
					Delay    int32
					Channel  string
				}
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Priority == nil || attrs.Channel == "" {
					t.Errorf("attributes %s: want Priority and Channel (err %v)", r.AttributesJSON, err)
				}
			},
		},
	}
}

func (c connectAccessCase) parentARN(id string) string {
	return connectTestInstanceARN + "/" + c.parentKind + "/" + id
}

// seedParents reuses the children family's seeding: p-1 and p-2 are this
// scan's parents; the other-region, other-account and stale ones must not be
// queried.
func (c connectAccessCase) seedParents(t *testing.T, st *store.Store) {
	t.Helper()
	connectChildCase{parentType: c.parentType, parentKind: c.parentKind}.seedParents(t, st)
}

func TestConnectAccessPhases_PagesEveryParentOfThisScan(t *testing.T) {
	for _, c := range connectAccessCases() {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			c.seedParents(t, st)
			var calls parentCalls
			client := connectStubClient(map[string]func(any) (any, error){c.op: func(in any) (any, error) {
				inst, id, token := c.parent(in)
				calls.add(inst + "/" + id)
				if size := c.pageSize(in); size == nil || *size != c.pageCap {
					return nil, connectBadParam
				}
				return c.respond(id, token), nil
			}})
			total, inserted, err := c.run(context.Background(), client, newTestAccount(testAccountID), st)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := calls.distinct(); !slices.Equal(got, []string{"i-1/p-1", "i-1/p-2"}) {
				t.Errorf("parents queried = %v, want [i-1/p-1 i-1/p-2]", got)
			}
			rows := rowsByNativeID(t, st, c.childType)
			want := append(c.wantIDs(c.parentARN("p-1"), "p-1"), c.wantIDs(c.parentARN("p-2"), "p-2")...)
			if total != len(want) || inserted != len(want) || len(rows) != len(want) {
				t.Errorf("total=%d inserted=%d rows=%d, want %d", total, inserted, len(rows), len(want))
			}
			for _, id := range want {
				r, ok := rows[id]
				if !ok {
					t.Errorf("missing row %s; have %v", id, rows)
					continue
				}
				if r.Type != c.childType || sv(r.Region) != testRegion || r.ManagedByProvider {
					t.Errorf("%s: Type=%s Region=%s Managed=%v", id, r.Type, sv(r.Region), r.ManagedByProvider)
				}
				c.check(t, r)
			}
			p2 := c.wantIDs(c.parentARN("p-2"), "p-2")
			assertContainsNative(t, st, c.parentARN("p-2"), p2[len(p2)-1])
			assertContainsEdgesDeclared(t, st)
		})
	}
}

func TestConnectAccessPhases_Errors(t *testing.T) {
	for _, c := range connectAccessCases() {
		t.Run(c.name+"/empty", func(t *testing.T) {
			st := newTestStore(t)
			c.seedParents(t, st)
			client := connectStubClient(map[string]func(any) (any, error){c.op: func(any) (any, error) { return c.empty, nil }})
			total, _, err := c.run(context.Background(), client, newTestAccount(testAccountID), st)
			want := append(c.wantEmptyIDs(c.parentARN("p-1")), c.wantEmptyIDs(c.parentARN("p-2"))...)
			if err != nil || total != len(want) {
				t.Fatalf("got (%d, %v), want (%d, nil)", total, err, len(want))
			}
			rows := rowsByNativeID(t, st, c.childType)
			for _, id := range want {
				if _, ok := rows[id]; !ok {
					t.Errorf("missing row %s for a parent that lists nothing", id)
				}
			}
		})
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
			warns := countOpWarnings(st, "connect:"+c.op)
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

// Permissions listed in either order store the same row.
func TestScanConnectSecurityProfilePermissions_OrderIsNoChange(t *testing.T) {
	var attrs []string
	for _, perms := range [][]string{{"b", "a", "c"}, {"c", "b", "a"}} {
		st := newTestStore(t)
		upsertTestResource(t, st, "aws", testAccountID, TypeConnectSecurityProfile, connectTestInstanceARN+"/security-profile/sp-1", testRegion, "{}")
		client := connectStubClient(map[string]func(any) (any, error){"ListSecurityProfilePermissions": func(any) (any, error) {
			return &connect.ListSecurityProfilePermissionsOutput{Permissions: perms}, nil
		}})
		if _, _, err := scanConnectSecurityProfilePermissions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		attrs = append(attrs, rowsByNativeID(t, st, TypeConnectSecurityProfilePermission)[connectTestInstanceARN+"/security-profile/sp-1/permission"].AttributesJSON)
	}
	if attrs[0] != attrs[1] || attrs[0] != `{"Permissions":["a","b","c"]}` {
		t.Errorf("attributes = %q, want both {\"Permissions\":[\"a\",\"b\",\"c\"]}", attrs)
	}
}

func TestScanConnectSecurityProfilePermissions_EmptyIsAnEmptyList(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectSecurityProfile, connectTestInstanceARN+"/security-profile/sp-1", testRegion, "{}")
	client := connectStubClient(map[string]func(any) (any, error){"ListSecurityProfilePermissions": func(any) (any, error) {
		return &connect.ListSecurityProfilePermissionsOutput{}, nil
	}})
	if _, _, err := scanConnectSecurityProfilePermissions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r := rowsByNativeID(t, st, TypeConnectSecurityProfilePermission)[connectTestInstanceARN+"/security-profile/sp-1/permission"]
	if r.AttributesJSON != `{"Permissions":[]}` {
		t.Errorf("attributes = %s, want an empty list, not null", r.AttributesJSON)
	}
}

const connectTestFunctionARN = "arn:aws:lambda:us-east-1:123456789012:function:"

// lambdaFunctionsHandler answers two pages per instance.
func lambdaFunctionsHandler(calls *parentCalls) func(any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*connect.ListLambdaFunctionsInput)
		inst := sv(p.InstanceId)
		calls.add(inst)
		if p.MaxResults == nil || *p.MaxResults != connectLambdaFunctionsPageCap {
			return nil, connectBadParam
		}
		if p.NextToken == nil {
			return &connect.ListLambdaFunctionsOutput{NextToken: ptrStr("p2"), LambdaFunctions: []string{connectTestFunctionARN + inst + "-a"}}, nil
		}
		return &connect.ListLambdaFunctionsOutput{LambdaFunctions: []string{connectTestFunctionARN + inst + "-b:live"}}, nil
	}
}

func TestScanConnectLambdaFunctions_OneRowPerInstanceAndFunction(t *testing.T) {
	st := newTestStore(t)
	seedConnectInstances(t, st, "i-1", "i-2")
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectInstance, "arn:aws:connect:eu-west-1:123456789012:instance/i-west", "eu-west-1", "{}")
	var calls parentCalls
	client := connectStubClient(map[string]func(any) (any, error){"ListLambdaFunctions": lambdaFunctionsHandler(&calls)})
	total, inserted, err := scanConnectLambdaFunctions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"i-1", "i-2"}) {
		t.Errorf("instances queried = %v, want [i-1 i-2]", got)
	}
	rows := rowsByNativeID(t, st, TypeConnectLambdaFunction)
	if total != 4 || inserted != 4 || len(rows) != 4 {
		t.Fatalf("total=%d inserted=%d rows=%d, want 4", total, inserted, len(rows))
	}
	fnARN := connectTestFunctionARN + "i-2-b:live"
	id := "arn:aws:connect:us-east-1:123456789012:instance/i-2/lambda-function/" + fnARN
	r, ok := rows[id]
	if !ok {
		t.Fatalf("missing %s; have %v", id, rows)
	}
	if r.Type != TypeConnectLambdaFunction || sv(r.Region) != testRegion || sv(r.Name) != "i-2-b:live" || r.ManagedByProvider {
		t.Errorf("row = %+v", r)
	}
	if r.AttributesJSON != `{"FunctionArn":"`+fnARN+`"}` {
		t.Errorf("attributes = %s", r.AttributesJSON)
	}
	assertContainsNative(t, st, "arn:aws:connect:us-east-1:123456789012:instance/i-2", id)
	assertContainsEdgesDeclared(t, st)
}

func TestScanConnectLambdaFunctions_Errors(t *testing.T) {
	ctx := context.Background()
	t.Run("empty", func(t *testing.T) {
		st := newTestStore(t)
		seedConnectInstances(t, st, "i-1")
		client := connectStubClient(map[string]func(any) (any, error){"ListLambdaFunctions": func(any) (any, error) { return &connect.ListLambdaFunctionsOutput{}, nil }})
		total, _, err := scanConnectLambdaFunctions(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 0 {
			t.Errorf("got (%d, %v), want (0, nil)", total, err)
		}
	})
	t.Run("deleted instance skipped", func(t *testing.T) {
		st := newTestStore(t)
		seedConnectInstances(t, st, "i-1", "i-2")
		warns := countWarnings(st)
		var calls parentCalls
		ok := lambdaFunctionsHandler(&calls)
		client := connectStubClient(map[string]func(any) (any, error){"ListLambdaFunctions": func(in any) (any, error) {
			if sv(in.(*connect.ListLambdaFunctionsInput).InstanceId) == "i-1" {
				return nil, connectNotFound
			}
			return ok(in)
		}})
		total, _, err := scanConnectLambdaFunctions(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 2 || *warns != 0 {
			t.Errorf("got (%d, %v, %d warnings), want (2, nil, 0)", total, err, *warns)
		}
	})
	t.Run("denied warns once", func(t *testing.T) {
		st := newTestStore(t)
		seedConnectInstances(t, st, "i-1", "i-2")
		warns := countOpWarnings(st, connectListLambdaFunctionsOp)
		total, _, err := scanConnectLambdaFunctions(ctx, connectStubClient(nil), newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 0 || *warns != 1 {
			t.Errorf("got (%d, %v, %d warnings), want (0, nil, 1)", total, err, *warns)
		}
	})
	t.Run("other error propagates", func(t *testing.T) {
		st := newTestStore(t)
		seedConnectInstances(t, st, "i-1")
		client := connectStubClient(map[string]func(any) (any, error){"ListLambdaFunctions": func(any) (any, error) { return nil, connectBadParam }})
		if _, _, err := scanConnectLambdaFunctions(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID); !isAPIErrorCode(err, "InvalidParameterException") {
			t.Errorf("err = %v, want InvalidParameterException", err)
		}
	})
	t.Run("unparsable instance is an error", func(t *testing.T) {
		st := newTestStore(t)
		upsertTestResource(t, st, "aws", testAccountID, TypeConnectInstance, "arn:aws:connect:us-east-1:123456789012:instance/", testRegion, "{}")
		if _, _, err := scanConnectLambdaFunctions(ctx, connectStubClient(nil), newTestAccount(testAccountID), testRegion, st, testScanID); !errors.Is(err, errConnectParentARN) {
			t.Errorf("err = %v, want errConnectParentARN", err)
		}
	})
}

func TestLambdaFunctionNameFromARN(t *testing.T) {
	for _, tc := range []struct {
		arn  string
		want *string
	}{
		{connectTestFunctionARN + "fn", ptrStr("fn")},
		{connectTestFunctionARN + "fn:live", ptrStr("fn:live")},
		{connectTestFunctionARN, nil},
		{"not-an-arn", nil},
	} {
		if got := lambdaFunctionNameFromARN(tc.arn); sv(got) != sv(tc.want) || (got == nil) != (tc.want == nil) {
			t.Errorf("lambdaFunctionNameFromARN(%q) = %v, want %v", tc.arn, got, tc.want)
		}
	}
}

// connectAccessFamilyHandlers extends connectFamilyHandlers with one security
// profile and one routing profile on instance i-1.
func connectAccessFamilyHandlers() map[string]func(any) (any, error) {
	h := connectFamilyHandlers()
	maps.Copy(h, map[string]func(any) (any, error){
		"ListSecurityProfiles": func(any) (any, error) {
			return &connect.ListSecurityProfilesOutput{SecurityProfileSummaryList: []cttypes.SecurityProfileSummary{{Id: ptrStr("sp-1")}}}, nil
		},
		"DescribeSecurityProfile": func(any) (any, error) {
			return &connect.DescribeSecurityProfileOutput{SecurityProfile: &cttypes.SecurityProfile{Arn: ptrStr(connectTestInstanceARN + "/security-profile/sp-1"), SecurityProfileName: ptrStr("Agent")}}, nil
		},
		"ListRoutingProfiles": func(any) (any, error) {
			return &connect.ListRoutingProfilesOutput{RoutingProfileSummaryList: []cttypes.RoutingProfileSummary{{Id: ptrStr("rp-1")}}}, nil
		},
		"DescribeRoutingProfile": func(any) (any, error) {
			return &connect.DescribeRoutingProfileOutput{RoutingProfile: &cttypes.RoutingProfile{RoutingProfileArn: ptrStr(connectTestInstanceARN + "/routing-profile/rp-1"), Name: ptrStr("Basic")}}, nil
		},
	})
	return h
}

// connectAccessOps answers every access op with one row.
var connectAccessOps = map[string]func(any) (any, error){
	"ListSecurityProfilePermissions": func(any) (any, error) {
		return &connect.ListSecurityProfilePermissionsOutput{Permissions: []string{"BasicAgentAccess"}}, nil
	},
	"ListLambdaFunctions": func(any) (any, error) {
		return &connect.ListLambdaFunctionsOutput{LambdaFunctions: []string{connectTestFunctionARN + "fn"}}, nil
	},
	"ListRoutingProfileQueues": func(any) (any, error) {
		return &connect.ListRoutingProfileQueuesOutput{RoutingProfileQueueConfigSummaryList: []cttypes.RoutingProfileQueueConfigSummary{
			{QueueId: ptrStr("q-1"), Channel: cttypes.ChannelVoice, Priority: ptrInt32(1)},
		}}, nil
	},
}

var connectAccessRowIDs = map[string]string{
	TypeConnectSecurityProfilePermission: connectTestInstanceARN + "/security-profile/sp-1/permission",
	TypeConnectLambdaFunction:            connectTestInstanceARN + "/lambda-function/" + connectTestFunctionARN + "fn",
	TypeConnectRoutingProfileQueue:       connectTestInstanceARN + "/routing-profile/rp-1/queue/q-1/channel/VOICE",
}

// connectScanWith runs the top-level scan against handlers.
func connectScanWith(t *testing.T, st *store.Store, h map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = connectStubConfig(h)
	total, _, err := scanConnect(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanConnect_AccessReadsParentsTheFamiliesStored(t *testing.T) {
	st := newTestStore(t)
	h := connectAccessFamilyHandlers()
	maps.Copy(h, connectChildOps)
	maps.Copy(h, connectAccessOps)
	if _, err := connectScanWith(t, st, h); err != nil {
		t.Fatalf("scanConnect: %v", err)
	}
	for typ, id := range connectAccessRowIDs {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s: no row %s", typ, id)
		}
	}
}

// A failing access phase must cost no family or child row and no sibling
// access row.
func TestScanConnect_AccessPhaseErrorAfterEveryOtherPhase(t *testing.T) {
	earlierRows := map[string]string{
		TypeConnectSecurityProfile:          connectTestInstanceARN + "/security-profile/sp-1",
		TypeConnectRoutingProfile:           connectTestInstanceARN + "/routing-profile/rp-1",
		TypeConnectHoursOfOperationOverride: connectChildRowIDs[TypeConnectHoursOfOperationOverride],
		TypeConnectWorkspacePage:            connectChildRowIDs[TypeConnectWorkspacePage],
	}
	for failing := range connectAccessOps {
		t.Run(failing, func(t *testing.T) {
			st := newTestStore(t)
			h := connectAccessFamilyHandlers()
			maps.Copy(h, connectChildOps)
			maps.Copy(h, connectAccessOps)
			h[failing] = func(any) (any, error) { return nil, connectBadParam }
			if _, err := connectScanWith(t, st, h); !isAPIErrorCode(err, "InvalidParameterException") {
				t.Fatalf("err = %v, want InvalidParameterException", err)
			}
			for typ, id := range earlierRows {
				if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
					t.Errorf("%s: earlier row %s lost", typ, id)
				}
			}
			stored := 0
			for typ, id := range connectAccessRowIDs {
				if _, ok := rowsByNativeID(t, st, typ)[id]; ok {
					stored++
				}
			}
			if stored != len(connectAccessRowIDs)-1 {
				t.Errorf("access rows stored = %d, want the %d siblings of the failing phase", stored, len(connectAccessRowIDs)-1)
			}
		})
	}
}

// A child phase's hard error must not skip the access phases.
func TestScanConnect_ChildErrorDoesNotSkipAccess(t *testing.T) {
	st := newTestStore(t)
	h := connectAccessFamilyHandlers()
	maps.Copy(h, connectChildOps)
	maps.Copy(h, connectAccessOps)
	h["ListWorkspacePages"] = func(any) (any, error) { return nil, connectHardErr }
	if _, err := connectScanWith(t, st, h); !errors.Is(err, connectHardErr) {
		t.Fatalf("err = %v, want the ListWorkspacePages error", err)
	}
	for typ, id := range connectAccessRowIDs {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s: no row %s", typ, id)
		}
	}
}

// A throttled earlier phase is demoted to one warning so that an access
// phase's hard error still classifies the service as failed.
func TestScanConnect_EarlierTransientDoesNotMaskAccessError(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "connect")
	h := connectAccessFamilyHandlers()
	maps.Copy(h, connectChildOps)
	maps.Copy(h, connectAccessOps)
	h["ListWorkspacePages"] = func(any) (any, error) { return nil, connectThrottled }
	h["ListRoutingProfileQueues"] = func(any) (any, error) { return nil, connectHardErr }
	_, err := connectScanWith(t, st, h)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if !errors.Is(err, connectHardErr) {
		t.Errorf("err = %v, want the ListRoutingProfileQueues error", err)
	}
	if *warned != 1 {
		t.Errorf("connect transient warnings = %d, want 1", *warned)
	}
}

// Each access phase's transient is demoted on its own, so a sibling's hard
// error still classifies the service as failed.
func TestScanConnectAccess_TransientDoesNotMaskSiblingError(t *testing.T) {
	for throttled := range connectAccessOps {
		t.Run(throttled, func(t *testing.T) {
			st := newTestStore(t)
			seedConnectInstances(t, st, "i-1")
			upsertTestResource(t, st, "aws", testAccountID, TypeConnectSecurityProfile, connectTestInstanceARN+"/security-profile/sp-1", testRegion, "{}")
			upsertTestResource(t, st, "aws", testAccountID, TypeConnectRoutingProfile, connectTestInstanceARN+"/routing-profile/rp-1", testRegion, "{}")
			warned := countOpWarnings(st, "connect:"+throttled)
			h := maps.Clone(connectAccessOps)
			h[throttled] = func(any) (any, error) { return nil, connectThrottled }
			hardOp := "ListLambdaFunctions"
			if throttled == hardOp {
				hardOp = "ListRoutingProfileQueues"
			}
			h[hardOp] = func(any) (any, error) { return nil, connectHardErr }
			_, _, err := scanConnectAccess(context.Background(), connectStubClient(h), newTestAccount(testAccountID), testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if !errors.Is(err, connectHardErr) {
				t.Errorf("err = %v, want the %s error", err, hardOp)
			}
			if *warned != 1 {
				t.Errorf("%s transient warnings = %d, want 1", throttled, *warned)
			}
		})
	}
}

// Entries missing the model-required QueueId or Channel are not stored and
// are reported in one warning; their siblings are stored.
func TestScanConnectRoutingProfileQueues_IncompleteEntriesWarnOnce(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"rp-1", "rp-2"} {
		upsertTestResource(t, st, "aws", testAccountID, TypeConnectRoutingProfile, connectTestInstanceARN+"/routing-profile/"+id, testRegion, "{}")
	}
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	client := connectStubClient(map[string]func(any) (any, error){"ListRoutingProfileQueues": func(any) (any, error) {
		return &connect.ListRoutingProfileQueuesOutput{RoutingProfileQueueConfigSummaryList: []cttypes.RoutingProfileQueueConfigSummary{
			{QueueId: ptrStr("q-1"), Channel: cttypes.ChannelVoice, Priority: ptrInt32(1)},
			{QueueName: ptrStr("no id"), Channel: cttypes.ChannelVoice},
			{QueueId: ptrStr("q-2")},
		}}, nil
	}})
	total, _, err := scanConnectRoutingProfileQueues(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("got (%d, %v), want (2, nil): one complete entry per profile", total, err)
	}
	// The count is the warning's payload and has no structured field, so the
	// message's leading number is read.
	if len(warnings) != 1 || warnings[0].Service != connectListRoutingProfileQueuesOp || !strings.HasPrefix(warnings[0].Message, "4 ") {
		t.Errorf("warnings = %+v, want one %s warning counting 4 incomplete entries", warnings, connectListRoutingProfileQueuesOp)
	}
	if _, ok := rowsByNativeID(t, st, TypeConnectRoutingProfileQueue)[connectTestInstanceARN+"/routing-profile/rp-2/queue/q-1/channel/VOICE"]; !ok {
		t.Error("complete sibling entry not stored")
	}
}

func TestScanConnectRoutingProfileQueues_CompleteEntriesDoNotWarn(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectRoutingProfile, connectTestInstanceARN+"/routing-profile/rp-1", testRegion, "{}")
	warns := countOpWarnings(st, connectListRoutingProfileQueuesOp)
	client := connectStubClient(map[string]func(any) (any, error){"ListRoutingProfileQueues": connectAccessOps["ListRoutingProfileQueues"]})
	if _, _, err := scanConnectRoutingProfileQueues(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d, want 0", *warns)
	}
}

// seedConnectAccessParent stores the security profile as already stored this
// scan: runScanners cancels the sibling families on a family's error, so
// whether the users family stores it first is a race.
func seedConnectAccessParent(t *testing.T, st *store.Store) {
	t.Helper()
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectSecurityProfile, connectTestInstanceARN+"/security-profile/sp-1", testRegion, "{}")
}

// A family's hard error skips the children but not the access family, which
// still lists the children of the parents stored before that error.
func TestScanConnect_FamilyErrorDoesNotSkipAccess(t *testing.T) {
	st := newTestStore(t)
	seedConnectAccessParent(t, st)
	h := connectAccessFamilyHandlers()
	maps.Copy(h, connectChildOps)
	maps.Copy(h, connectAccessOps)
	h["ListHoursOfOperations"] = func(any) (any, error) { return nil, connectHardErr }
	if _, err := connectScanWith(t, st, h); !errors.Is(err, connectHardErr) {
		t.Fatalf("err = %v, want the ListHoursOfOperations error", err)
	}
	id := connectAccessRowIDs[TypeConnectSecurityProfilePermission]
	if _, ok := rowsByNativeID(t, st, TypeConnectSecurityProfilePermission)[id]; !ok {
		t.Errorf("no row %s for the profile stored before the routing family failed", id)
	}
}

// A throttled family is demoted to one warning so that an access phase's hard
// error still classifies the service as failed.
func TestScanConnect_FamilyTransientDoesNotMaskAccessError(t *testing.T) {
	st := newTestStore(t)
	seedConnectAccessParent(t, st)
	warned := countOpWarnings(st, "connect")
	h := connectAccessFamilyHandlers()
	maps.Copy(h, connectChildOps)
	maps.Copy(h, connectAccessOps)
	h["ListHoursOfOperations"] = func(any) (any, error) { return nil, connectThrottled }
	h["ListSecurityProfilePermissions"] = func(any) (any, error) { return nil, connectHardErr }
	_, err := connectScanWith(t, st, h)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if !errors.Is(err, connectHardErr) {
		t.Errorf("err = %v, want the ListSecurityProfilePermissions error", err)
	}
	if *warned != 1 {
		t.Errorf("connect transient warnings = %d, want 1", *warned)
	}
}

// rendezvous wraps h so that each call first signals started, then waits for
// other: two wrapped calls only both return if they run at the same time.
func rendezvous(h func(any) (any, error), started, other chan struct{}, once *sync.Once) func(any) (any, error) {
	return func(in any) (any, error) {
		once.Do(func() { close(started) })
		select {
		case <-other:
			return h(in)
		case <-time.After(10 * time.Second):
			return nil, connectHardErr
		}
	}
}

// The children and the access family run concurrently: a child call and an
// access call each wait for the other, so either order run one after the
// other times out.
func TestScanConnect_ChildrenAndAccessRunConcurrently(t *testing.T) {
	st := newTestStore(t)
	h := connectAccessFamilyHandlers()
	maps.Copy(h, connectChildOps)
	maps.Copy(h, connectAccessOps)
	childStarted, accessStarted := make(chan struct{}), make(chan struct{})
	var childOnce, accessOnce sync.Once
	h["ListWorkspacePages"] = rendezvous(h["ListWorkspacePages"], childStarted, accessStarted, &childOnce)
	h["ListSecurityProfilePermissions"] = rendezvous(h["ListSecurityProfilePermissions"], accessStarted, childStarted, &accessOnce)
	if _, err := connectScanWith(t, st, h); err != nil {
		t.Fatalf("scanConnect: %v (a child or access call waited for the other in vain)", err)
	}
}

// A family's hard error does not skip the children: they list the children
// of the parents stored this scan, and the service still returns the error.
func TestScanConnect_FamilyErrorDoesNotSkipChildren(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectWorkspace, connectTestInstanceARN+"/workspace/w-1", testRegion, "{}")
	h := connectAccessFamilyHandlers()
	maps.Copy(h, connectChildOps)
	maps.Copy(h, connectAccessOps)
	h["ListHoursOfOperations"] = func(any) (any, error) { return nil, connectHardErr }
	var calls parentCalls
	pages := h["ListWorkspacePages"]
	h["ListWorkspacePages"] = func(in any) (any, error) {
		calls.add(sv(in.(*connect.ListWorkspacePagesInput).WorkspaceId))
		return pages(in)
	}
	if _, err := connectScanWith(t, st, h); !errors.Is(err, connectHardErr) {
		t.Fatalf("err = %v, want the ListHoursOfOperations error", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"w-1"}) {
		t.Errorf("workspaces queried = %v, want [w-1]", got)
	}
	if _, ok := rowsByNativeID(t, st, TypeConnectWorkspacePage)[connectChildRowIDs[TypeConnectWorkspacePage]]; !ok {
		t.Errorf("no workspace page row after the family error")
	}
}
