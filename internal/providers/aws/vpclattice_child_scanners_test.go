package aws

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/vpclattice"
	vltypes "github.com/aws/aws-sdk-go-v2/service/vpclattice/types"
	"github.com/icearp/disco-cli/store"
)

const vlTestLambda = "arn:aws:lambda:us-east-1:123456789012:function:fn"

var (
	vlTestNetA      = "arn:aws:vpc-lattice:us-east-1:123456789012:servicenetwork/sn-a"
	vlTestNetB      = "arn:aws:vpc-lattice:us-east-1:123456789012:servicenetwork/sn-b"
	vlTestNetShared = "arn:aws:vpc-lattice:us-east-1:210987654321:servicenetwork/sn-x"
	vlTestTGA       = "arn:aws:vpc-lattice:us-east-1:123456789012:targetgroup/tg-a"
	vlTestTGB       = "arn:aws:vpc-lattice:us-east-1:123456789012:targetgroup/tg-b"
	vlTestSNVA      = "arn:aws:vpc-lattice:us-east-1:123456789012:servicenetworkvpcassociation/snva-1"
	vlTestCreated   = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	vlTestAssocs = []string{
		vlTestNetA + "/vpc-endpoint-association/snve-1",
		vlTestNetA + "/vpc-endpoint-association/snve-2",
		vlTestNetB + "/vpc-endpoint-association/snve-3",
	}
	vlTestTargets = []string{
		vlTestTGA + "/target/80/i-1",
		vlTestTGA + "/target/8080/i-1",
		vlTestTGB + "/target/" + vlTestLambda,
	}

	vlDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: vpc-lattice:List")
	vlBadInput = apiErr("ValidationException", "bad")
	vlThrottle = apiErr("ThrottlingException", "slow down")
	vlGone     = apiErr("ResourceNotFoundException", "gone")
)

// vlChildCalls records each child list call by its parent identifier.
type vlChildCalls struct{ assocs, targets parentCalls }

// vlChildPages answers one child list call for parent: the first parent of
// each op (sn-a, tg-a) lists two pages, the second (sn-b, tg-b) one page and
// the shared network sn-x none; any other parent is an error, so a call for a
// parent that should not be listed fails the scan.
func vlChildPages[M any](parent string, next *string, first, second, other []M) ([]M, *string, error) {
	switch parent {
	case vlTestNetA, vlTestTGA:
		if next == nil {
			return first, sdkaws.String("page2"), nil
		}
		return second, nil, nil
	case vlTestNetB, vlTestTGB:
		return other, nil, nil
	case vlTestNetShared:
		return nil, nil, nil
	}
	return nil, nil, apiErr("ValidationException", "unknown parent "+parent)
}

// vlHandlers answers every op scanVpcLattice calls: service networks sn-a and
// sn-b owned by the account plus sn-x shared with it, target groups tg-a
// (INSTANCE) and tg-b (LAMBDA), one VPC association (the last older phase's
// row), and the children of each network and target group. The first
// page of each two-page parent also carries an element without an id, to skip.
func vlHandlers(calls *vlChildCalls) map[string]func(in any) (any, error) {
	checkLimit := func(maxResults *int32) error {
		if sdkaws.ToInt32(maxResults) != vlChildPageLimit {
			return apiErr("ValidationException", "maxResults must be between 1 and 100")
		}
		return nil
	}
	return map[string]func(in any) (any, error){
		"ListServices": func(any) (any, error) { return &vpclattice.ListServicesOutput{}, nil },
		"ListServiceNetworks": func(any) (any, error) {
			return &vpclattice.ListServiceNetworksOutput{Items: []vltypes.ServiceNetworkSummary{
				{Arn: sdkaws.String(vlTestNetA), Name: sdkaws.String("a")},
				{Arn: sdkaws.String(vlTestNetB), Name: sdkaws.String("b")},
				{Arn: sdkaws.String(vlTestNetShared), Name: sdkaws.String("x")},
			}}, nil
		},
		"ListResourceConfigurations": func(any) (any, error) { return &vpclattice.ListResourceConfigurationsOutput{}, nil },
		"ListTargetGroups": func(any) (any, error) {
			return &vpclattice.ListTargetGroupsOutput{Items: []vltypes.TargetGroupSummary{
				{Arn: sdkaws.String(vlTestTGA), Name: sdkaws.String("tg-a"), Type: vltypes.TargetGroupTypeInstance},
				{Arn: sdkaws.String(vlTestTGB), Name: sdkaws.String("tg-b"), Type: vltypes.TargetGroupTypeLambda},
			}}, nil
		},
		"ListAccessLogSubscriptions": func(any) (any, error) { return &vpclattice.ListAccessLogSubscriptionsOutput{}, nil },
		"GetAuthPolicy":              func(any) (any, error) { return &vpclattice.GetAuthPolicyOutput{}, nil },
		"GetResourcePolicy":          func(any) (any, error) { return &vpclattice.GetResourcePolicyOutput{}, nil },
		"ListDomainVerifications":    func(any) (any, error) { return &vpclattice.ListDomainVerificationsOutput{}, nil },
		"ListResourceGateways":       func(any) (any, error) { return &vpclattice.ListResourceGatewaysOutput{}, nil },
		"ListServiceNetworkResourceAssociations": func(any) (any, error) {
			return &vpclattice.ListServiceNetworkResourceAssociationsOutput{}, nil
		},
		"ListServiceNetworkServiceAssociations": func(any) (any, error) {
			return &vpclattice.ListServiceNetworkServiceAssociationsOutput{}, nil
		},
		"ListServiceNetworkVpcAssociations": func(in any) (any, error) {
			if sdkaws.ToString(in.(*vpclattice.ListServiceNetworkVpcAssociationsInput).ServiceNetworkIdentifier) != vlTestNetA {
				return &vpclattice.ListServiceNetworkVpcAssociationsOutput{}, nil
			}
			return &vpclattice.ListServiceNetworkVpcAssociationsOutput{Items: []vltypes.ServiceNetworkVpcAssociationSummary{
				{Arn: sdkaws.String(vlTestSNVA), Id: sdkaws.String("snva-1")},
			}}, nil
		},
		"ListServiceNetworkVpcEndpointAssociations": func(in any) (any, error) {
			p := in.(*vpclattice.ListServiceNetworkVpcEndpointAssociationsInput)
			net := sdkaws.ToString(p.ServiceNetworkIdentifier)
			calls.assocs.add(net)
			if err := checkLimit(p.MaxResults); err != nil {
				return nil, err
			}
			a := func(id, vpce string) vltypes.ServiceNetworkEndpointAssociation {
				return vltypes.ServiceNetworkEndpointAssociation{
					Id: sdkaws.String(id), ServiceNetworkArn: &net, VpcEndpointId: sdkaws.String(vpce),
					VpcId: sdkaws.String("vpc-1"), State: sdkaws.String("ACTIVE"), CreatedAt: &vlTestCreated,
				}
			}
			items, next, err := vlChildPages(net, p.NextToken,
				[]vltypes.ServiceNetworkEndpointAssociation{a("snve-1", "vpce-1"), {VpcEndpointId: sdkaws.String("vpce-9")}},
				[]vltypes.ServiceNetworkEndpointAssociation{a("snve-2", "vpce-2")},
				[]vltypes.ServiceNetworkEndpointAssociation{a("snve-3", "vpce-3")})
			if err != nil {
				return nil, err
			}
			return &vpclattice.ListServiceNetworkVpcEndpointAssociationsOutput{Items: items, NextToken: next}, nil
		},
		"ListTargets": func(in any) (any, error) {
			p := in.(*vpclattice.ListTargetsInput)
			tg := sdkaws.ToString(p.TargetGroupIdentifier)
			calls.targets.add(tg)
			if err := checkLimit(p.MaxResults); err != nil {
				return nil, err
			}
			items, next, err := vlChildPages(tg, p.NextToken,
				[]vltypes.TargetSummary{
					{Id: sdkaws.String("i-1"), Port: sdkaws.Int32(80), Status: vltypes.TargetStatusHealthy},
					{Port: sdkaws.Int32(80), Status: vltypes.TargetStatusHealthy},
				},
				[]vltypes.TargetSummary{{Id: sdkaws.String("i-1"), Port: sdkaws.Int32(8080), Status: vltypes.TargetStatusUnhealthy, ReasonCode: sdkaws.String("HealthCheckFailed")}},
				[]vltypes.TargetSummary{{Id: sdkaws.String(vlTestLambda), Status: vltypes.TargetStatusUnavailable}})
			if err != nil {
				return nil, err
			}
			return &vpclattice.ListTargetsOutput{Items: items, NextToken: next}, nil
		},
	}
}

func scanVpcLatticeTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, vlDenied)
	return scanVpcLattice(context.Background(), acct, testRegion, st, testScanID)
}

func TestScanVpcLattice_StoresChildren(t *testing.T) {
	st := newTestStore(t)
	var calls vlChildCalls
	total, inserted, err := scanVpcLatticeTopLevel(t, st, vlHandlers(&calls))
	if err != nil {
		t.Fatalf("scanVpcLattice: %v", err)
	}
	// 3 networks + 2 target groups + 1 VPC association + 3 endpoint associations + 3 targets
	if total != 12 || inserted != 12 {
		t.Errorf("total, inserted = %d, %d, want 12, 12", total, inserted)
	}
	if got, want := calls.assocs.distinct(), []string{vlTestNetA, vlTestNetB, vlTestNetShared}; !slices.Equal(got, want) {
		t.Errorf("ListServiceNetworkVpcEndpointAssociations parents = %v, want %v (shared-in networks included)", got, want)
	}
	if got, want := calls.targets.distinct(), []string{vlTestTGA, vlTestTGB}; !slices.Equal(got, want) {
		t.Errorf("ListTargets parents = %v, want %v", got, want)
	}

	assocs := rowsByNativeID(t, st, TypeVpcLatticeServiceNetworkVpcEndpointAssociation)
	if got := nativeIDs(assocs); !slices.Equal(got, vlTestAssocs) {
		t.Fatalf("association rows = %v, want %v", got, vlTestAssocs)
	}
	for i, parent := range []string{vlTestNetA, vlTestNetA, vlTestNetB} {
		assertContainsNative(t, st, parent, vlTestAssocs[i])
	}
	a := assocs[vlTestAssocs[0]]
	if a.Type != TypeVpcLatticeServiceNetworkVpcEndpointAssociation || sv(a.Region) != testRegion || sv(a.Name) != "snve-1" ||
		sv(a.Status) != "ACTIVE" || sv(a.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("association row = type %q region %q name %q status %q created %q", a.Type, sv(a.Region), sv(a.Name), sv(a.Status), sv(a.CreatedAt))
	}
	var aAttrs struct {
		VpcEndpointID string `json:"VpcEndpointId"`
		VpcID         string `json:"VpcId"`
	}
	if err := json.Unmarshal([]byte(a.AttributesJSON), &aAttrs); err != nil || aAttrs.VpcEndpointID != "vpce-1" || aAttrs.VpcID != "vpc-1" {
		t.Errorf("association attrs = %s (%v), want the SDK element verbatim", a.AttributesJSON, err)
	}

	targets := rowsByNativeID(t, st, TypeVpcLatticeTarget)
	if got := nativeIDs(targets); !slices.Equal(got, vlTestTargets) {
		t.Fatalf("target rows = %v, want %v", got, vlTestTargets)
	}
	for i, parent := range []string{vlTestTGA, vlTestTGA, vlTestTGB} {
		assertContainsNative(t, st, parent, vlTestTargets[i])
	}
	tr := targets[vlTestTargets[1]]
	if tr.Type != TypeVpcLatticeTarget || sv(tr.Region) != testRegion || sv(tr.Name) != "i-1" || sv(tr.Status) != "UNHEALTHY" {
		t.Errorf("target row = type %q region %q name %q status %q", tr.Type, sv(tr.Region), sv(tr.Name), sv(tr.Status))
	}
	var tAttrs struct {
		Port       int32
		ReasonCode string
	}
	if err := json.Unmarshal([]byte(tr.AttributesJSON), &tAttrs); err != nil || tAttrs.Port != 8080 || tAttrs.ReasonCode != "HealthCheckFailed" {
		t.Errorf("target attrs = %s (%v), want the SDK element verbatim", tr.AttributesJSON, err)
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanVpcLattice_NoChildren(t *testing.T) {
	st := newTestStore(t)
	var calls vlChildCalls
	h := vlHandlers(&calls)
	h["ListServiceNetworkVpcEndpointAssociations"] = func(any) (any, error) {
		return &vpclattice.ListServiceNetworkVpcEndpointAssociationsOutput{}, nil
	}
	h["ListTargets"] = func(any) (any, error) { return &vpclattice.ListTargetsOutput{}, nil }
	total, _, err := scanVpcLatticeTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanVpcLattice: %v", err)
	}
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	for _, typ := range []string{TypeVpcLatticeServiceNetworkVpcEndpointAssociation, TypeVpcLatticeTarget} {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
}

// A failed parent drops only its own children: a deleted target group is
// silent, a denial warns once however many parents hit it, and a transient
// error warns once. A target group refused with ValidationException is skipped
// with one warning; for a service network that code, like a not-found the op
// does not model and any other error, fails the phase (storing none of its
// rows) and is returned. The older phases' rows stay, the last one's included.
func TestScanVpcLattice_FailedChildParent(t *testing.T) {
	type failCase struct {
		name      string
		fail      map[string]error // keyed by parent ARN
		wantCode  string           // "" means the scan returns nil
		wantRows  []string
		wantWarns int
	}
	for _, op := range []struct {
		op, typ, first, second string
		rows                   []string
		notReadySkipped        bool
	}{
		{"ListServiceNetworkVpcEndpointAssociations", TypeVpcLatticeServiceNetworkVpcEndpointAssociation, vlTestNetA, vlTestNetB, vlTestAssocs, false},
		{"ListTargets", TypeVpcLatticeTarget, vlTestTGA, vlTestTGB, vlTestTargets, true},
	} {
		onlySecond := []string{op.rows[2]}
		cases := []failCase{
			{"denied everywhere", map[string]error{op.first: vlDenied, op.second: vlDenied}, "", nil, 1},
			{"throttled once", map[string]error{op.first: vlThrottle}, "", onlySecond, 1},
			{"server error once", map[string]error{op.first: apiErr("InternalServerException", "x")}, "", onlySecond, 1},
			{"unmodeled code", map[string]error{op.first: apiErr("ConflictException", "x")}, "ConflictException", nil, 0},
		}
		if op.notReadySkipped {
			cases = append(cases,
				failCase{"parent gone", map[string]error{op.first: vlGone}, "", onlySecond, 0},
				failCase{"validation everywhere", map[string]error{op.first: vlBadInput, op.second: vlBadInput}, "", nil, 1})
		} else {
			cases = append(cases,
				failCase{"not found unmodeled", map[string]error{op.first: vlGone}, "ResourceNotFoundException", nil, 0},
				failCase{"validation", map[string]error{op.first: vlBadInput}, "ValidationException", nil, 0})
		}
		for _, tc := range cases {
			t.Run(op.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "vpclattice:"+op.op)
				var calls vlChildCalls
				h := vlHandlers(&calls)
				next := h[op.op]
				h[op.op] = func(in any) (any, error) {
					var parent string
					switch p := in.(type) {
					case *vpclattice.ListServiceNetworkVpcEndpointAssociationsInput:
						parent = sdkaws.ToString(p.ServiceNetworkIdentifier)
					case *vpclattice.ListTargetsInput:
						parent = sdkaws.ToString(p.TargetGroupIdentifier)
					}
					if err, ok := tc.fail[parent]; ok {
						return nil, err
					}
					return next(in)
				}
				_, _, err := scanVpcLatticeTopLevel(t, st, h)
				if tc.wantCode == "" {
					if err != nil {
						t.Fatalf("err = %v, want nil", err)
					}
				} else {
					assertAPICode(t, err, tc.wantCode)
					if got := classifyServiceError(err); got != outcomeError {
						t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
					}
				}
				if got := nativeIDs(rowsByNativeID(t, st, op.typ)); !slices.Equal(got, tc.wantRows) {
					t.Errorf("%s rows = %v, want %v", op.typ, got, tc.wantRows)
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
				}
				for _, older := range []struct{ typ, id string }{
					{TypeVpcLatticeServiceNetwork, vlTestNetB},
					{TypeVpcLatticeTargetGroup, vlTestTGB},
					{TypeVpcLatticeServiceNetworkVpcAssociation, vlTestSNVA},
				} {
					if _, ok := rowsByNativeID(t, st, older.typ)[older.id]; !ok {
						t.Errorf("%s %s missing; a child phase must not stop the older phases", older.typ, older.id)
					}
				}
			})
		}
	}
}

// A hard error in the first child phase still lets the second one run, and
// is returned.
func TestScanVpcLattice_ChildPhaseErrorStillRunsOtherChildPhase(t *testing.T) {
	st := newTestStore(t)
	var calls vlChildCalls
	h := vlHandlers(&calls)
	h["ListServiceNetworkVpcEndpointAssociations"] = func(any) (any, error) { return nil, apiErr("ConflictException", "x") }
	total, _, err := scanVpcLatticeTopLevel(t, st, h)
	assertAPICode(t, err, "ConflictException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeVpcLatticeTarget)); !slices.Equal(got, vlTestTargets) {
		t.Errorf("target rows = %v, want %v", got, vlTestTargets)
	}
	// 6 older rows + 3 targets
	if total != 9 {
		t.Errorf("total = %d, want 9", total)
	}
}

// A hard error in the last older phase still lets the child phases run over
// the service networks and target groups already stored, and is returned.
func TestScanVpcLattice_OlderPhaseErrorStillRunsChildren(t *testing.T) {
	st := newTestStore(t)
	var calls vlChildCalls
	h := vlHandlers(&calls)
	h["ListServiceNetworkVpcAssociations"] = func(any) (any, error) { return nil, vlBadInput }
	total, _, err := scanVpcLatticeTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	for typ, want := range map[string][]string{
		TypeVpcLatticeServiceNetworkVpcEndpointAssociation: vlTestAssocs,
		TypeVpcLatticeTarget: vlTestTargets,
	} {
		if got := nativeIDs(rowsByNativeID(t, st, typ)); !slices.Equal(got, want) {
			t.Errorf("%s rows = %v, want %v", typ, got, want)
		}
	}
	// 3 networks + 2 target groups + 6 children
	if total != 11 {
		t.Errorf("total = %d, want 11", total)
	}
}

// A transient error in an older phase is demoted to a warning so that a
// child phase's hard error still reaches the dispatcher as a hard error.
func TestScanVpcLattice_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "vpclattice")
	var calls vlChildCalls
	h := vlHandlers(&calls)
	h["ListServiceNetworkVpcAssociations"] = func(any) (any, error) { return nil, vlThrottle }
	h["ListTargets"] = func(any) (any, error) { return nil, apiErr("ConflictException", "x") }
	_, _, err := scanVpcLatticeTopLevel(t, st, h)
	assertAPICode(t, err, "ConflictException")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError; the hard child error must not be masked", err, got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

// A parent listing that fails on its second page stores none of its rows, so
// none of them are listed for children; the failure is returned.
func TestScanVpcLattice_NoChildrenOfUnstoredParents(t *testing.T) {
	for _, tc := range []struct {
		listOp string
		page2  func(next map[string]func(in any) (any, error)) func(in any) (any, error)
		calls  func(*vlChildCalls) *parentCalls
	}{
		{
			"ListServiceNetworks",
			func(h map[string]func(in any) (any, error)) func(in any) (any, error) {
				return func(in any) (any, error) {
					if in.(*vpclattice.ListServiceNetworksInput).NextToken == nil {
						out, _ := h["ListServiceNetworks"](in)
						o := out.(*vpclattice.ListServiceNetworksOutput)
						o.NextToken = sdkaws.String("page2")
						return o, nil
					}
					return nil, vlBadInput
				}
			},
			func(c *vlChildCalls) *parentCalls { return &c.assocs },
		},
		{
			"ListTargetGroups",
			func(h map[string]func(in any) (any, error)) func(in any) (any, error) {
				return func(in any) (any, error) {
					if in.(*vpclattice.ListTargetGroupsInput).NextToken == nil {
						out, _ := h["ListTargetGroups"](in)
						o := out.(*vpclattice.ListTargetGroupsOutput)
						o.NextToken = sdkaws.String("page2")
						return o, nil
					}
					return nil, vlBadInput
				}
			},
			func(c *vlChildCalls) *parentCalls { return &c.targets },
		},
	} {
		t.Run(tc.listOp, func(t *testing.T) {
			st := newTestStore(t)
			var calls vlChildCalls
			h := vlHandlers(&calls)
			h[tc.listOp] = tc.page2(vlHandlers(&vlChildCalls{}))
			_, _, err := scanVpcLatticeTopLevel(t, st, h)
			assertAPICode(t, err, "ValidationException")
			if got := tc.calls(&calls).distinct(); len(got) != 0 {
				t.Errorf("children listed for unstored parents %v", got)
			}
		})
	}
}

// Target groups whose upsert failed are not handed to the target phase: no
// ListTargets call is made, no target row is stored, and the store failure is
// returned.
func TestScanVpcLattice_NoTargetsWhenTargetGroupUpsertFails(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	path := filepath.Join(t.TempDir(), "closing.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	insertTestScan(t, st, testScanID, "running")
	var calls vlChildCalls
	h := vlHandlers(&calls)
	listTGs := h["ListTargetGroups"]
	h["ListTargetGroups"] = func(in any) (any, error) {
		// Closing here fails the target-group upsert that follows this page.
		if err := st.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		return listTGs(in)
	}
	_, _, err = scanVpcLatticeTopLevel(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want store.ErrStoreWrite", err)
	}
	if got := calls.targets.distinct(); len(got) != 0 {
		t.Errorf("ListTargets called for %v, want no call for unstored target groups", got)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if n := len(rowsByNativeID(t, reopened, TypeVpcLatticeTarget)); n != 0 {
		t.Errorf("target rows = %d, want 0", n)
	}
}

func TestVlTargetNativeID_PortBeforeID(t *testing.T) {
	for _, tc := range []struct {
		id   string
		port *int32
		want string
	}{
		{"i-1", sdkaws.Int32(80), vlTestTGA + "/target/80/i-1"},
		{vlTestALB, sdkaws.Int32(443), vlTestTGA + "/target/443/" + vlTestALB},
		{vlTestLambda, nil, vlTestTGA + "/target/" + vlTestLambda},
	} {
		got := vlTargetNativeID(vlTestTGA, tc.id, tc.port)
		if got != tc.want {
			t.Errorf("vlTargetNativeID(%q, %v) = %q, want %q", tc.id, tc.port, got, tc.want)
		}
		if tg, ok := vlTargetGroupOfTarget(got); !ok || tg != vlTestTGA {
			t.Errorf("vlTargetGroupOfTarget(%q) = %q, %v, want %q", got, tg, ok, vlTestTGA)
		}
	}
}
