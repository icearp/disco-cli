package aws

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

// efpPhase is one per-parent listing scanEC2Late runs over parents its
// categories stored. Parent 1 answers two pages: the first a tagged element,
// the second an untagged one and one without its key. Parent 2 answers one
// page holding one element.
type efpPhase struct {
	name, op, sdkOp, typ string
	parentType           string
	// parentARN is the NativeID a parent's own scanner stores.
	parentARN func(id string) string
	// goneStatus is a parent Status the phase must not query ("" for none).
	goneStatus string
	pageSize   int32
	// request is the parent, MaxResults and NextToken a call sent.
	request                      func(in any) (parent string, size int32, token *string)
	page1, page2, parent2, empty any
	run                          func(context.Context, ec2API, *account, string, *store.Store, string) (int, int, error)
	rows                         map[string]mexRow
	// noKey is the key the warning about the element without its key names.
	noKey string
	// parent2Row is the row parent 2 stores; page1Row the one parent 1's
	// first page stores.
	parent2Row, page1Row string
	// topLevel answers the parent listing of a scanEC2 run with parent 1.
	topLevel map[string]func(any) (any, error)
}

const (
	efpParent1 = "p-efp-0001"
	efpParent2 = "p-efp-0002"
	efpParent3 = "p-efp-0003"
)

var efpPhases = []efpPhase{efpIPAMAllocations, efpEndpointPermissions}

var efpIPAMAllocations = efpPhase{
	name: "ipam pool allocations", op: "ec2:GetIpamPoolAllocations", sdkOp: "GetIpamPoolAllocations",
	typ: TypeEC2IPAMAllocation, parentType: TypeEC2IPAMPool,
	parentARN:  func(id string) string { return "arn:aws:ec2::" + testAccountID + ":ipam-pool/" + id },
	goneStatus: "delete-complete",
	pageSize:   100000,
	request: func(in any) (string, int32, *string) {
		p := in.(*ec2.GetIpamPoolAllocationsInput)
		return sv(p.IpamPoolId), sv32(p.MaxResults), p.NextToken
	},
	page1: &ec2.GetIpamPoolAllocationsOutput{NextToken: ptrStr(elxPageNext), IpamPoolAllocations: []ec2types.IpamPoolAllocation{{
		IpamPoolAllocationId: ptrStr("ipam-pool-alloc-1"), Cidr: ptrStr("10.0.0.0/24"), Description: ptrStr("app"),
		ResourceType: ec2types.IpamPoolAllocationResourceTypeCustom,
		Tags:         []ec2types.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}, {Key: ptrStr("owner"), Value: ptrStr("net")}},
	}}},
	page2: &ec2.GetIpamPoolAllocationsOutput{IpamPoolAllocations: []ec2types.IpamPoolAllocation{
		{IpamPoolAllocationId: ptrStr("ipam-pool-alloc-2"), Cidr: ptrStr("10.0.1.0/24")},
		{Cidr: ptrStr("10.0.2.0/24")},
	}},
	parent2: &ec2.GetIpamPoolAllocationsOutput{IpamPoolAllocations: []ec2types.IpamPoolAllocation{
		{IpamPoolAllocationId: ptrStr("ipam-pool-alloc-3"), Cidr: ptrStr("10.1.0.0/24")},
	}},
	empty: &ec2.GetIpamPoolAllocationsOutput{},
	run:   scanIPAMAllocations,
	rows: map[string]mexRow{
		elxARN("ipam-allocation", efpParent1+"/ipam-pool-alloc-1"): {
			name:  "app",
			attrs: `{"Cidr":"10.0.0.0/24","Description":"app","IpamPoolAllocationId":"ipam-pool-alloc-1","ResourceId":null,"ResourceOwner":null,"ResourceRegion":null,"ResourceType":"custom","Tags":[{"Key":"env","Value":"prod"},{"Key":"owner","Value":"net"}]}`,
			tags:  map[string]string{"env": "prod", "owner": "net"},
		},
		elxARN("ipam-allocation", efpParent1+"/ipam-pool-alloc-2"): {
			attrs: `{"Cidr":"10.0.1.0/24","Description":null,"IpamPoolAllocationId":"ipam-pool-alloc-2","ResourceId":null,"ResourceOwner":null,"ResourceRegion":null,"ResourceType":"","Tags":null}`,
		},
		elxARN("ipam-allocation", efpParent2+"/ipam-pool-alloc-3"): {
			attrs: `{"Cidr":"10.1.0.0/24","Description":null,"IpamPoolAllocationId":"ipam-pool-alloc-3","ResourceId":null,"ResourceOwner":null,"ResourceRegion":null,"ResourceType":"","Tags":null}`,
		},
	},
	noKey:      "one in pool " + efpParent1,
	page1Row:   elxARN("ipam-allocation", efpParent1+"/ipam-pool-alloc-1"),
	parent2Row: elxARN("ipam-allocation", efpParent2+"/ipam-pool-alloc-3"),
	topLevel: map[string]func(any) (any, error){
		"DescribeIpamPools": func(any) (any, error) {
			return &ec2.DescribeIpamPoolsOutput{IpamPools: []ec2types.IpamPool{{
				IpamPoolId: ptrStr(efpParent1), IpamPoolArn: ptrStr("arn:aws:ec2::" + testAccountID + ":ipam-pool/" + efpParent1),
				State: ec2types.IpamPoolStateCreateComplete,
			}}}, nil
		},
	},
}

// efpHandler answers parent 1 with page1 then page2 (or fail2), parent 2
// with its one page (or fail), and fails any call without MaxResults at
// the page cap or for another parent.
func efpHandler(ph efpPhase, calls *parentCalls, fail, fail2 error) func(any) (any, error) {
	return func(in any) (any, error) {
		parent, size, token := ph.request(in)
		calls.add(parent)
		if size != ph.pageSize {
			return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
		}
		switch {
		case parent == efpParent1 && token == nil:
			return ph.page1, nil
		case parent == efpParent1 && sv(token) == elxPageNext && fail2 != nil:
			return nil, fail2
		case parent == efpParent1 && sv(token) == elxPageNext:
			return ph.page2, nil
		case parent == efpParent2 && token == nil && fail != nil:
			return nil, fail
		case parent == efpParent2 && token == nil:
			return ph.parent2, nil
		}
		return nil, apiErr("InvalidParameterValue", "unexpected call for "+parent+" token "+sv(token))
	}
}

// efpSeed stores parents 1 and 2 live and, when the phase has a gone status,
// parent 3 gone; plus one in another Region and one stored by another scan.
func efpSeed(t *testing.T, st *store.Store, ph efpPhase) {
	t.Helper()
	tgxSeedParent(t, st, ph.parentType, ph.parentARN(efpParent1), testRegion, "available", testScanID)
	tgxSeedParent(t, st, ph.parentType, ph.parentARN(efpParent2), testRegion, "pending", testScanID)
	if ph.goneStatus != "" {
		tgxSeedParent(t, st, ph.parentType, ph.parentARN(efpParent3), testRegion, ph.goneStatus, testScanID)
	}
	tgxSeedParent(t, st, ph.parentType, ph.parentARN(efpParent1+"-eu"), "eu-west-1", "available", testScanID)
	insertTestScan(t, st, elxOtherScan, "completed")
	tgxSeedParent(t, st, ph.parentType, ph.parentARN(efpParent1+"-old"), testRegion, "available", elxOtherScan)
}

// Each phase reads every page of each live parent this scan stored in the
// Region (no gone, other-Region or other-scan parent) and stores each row
// with its NativeID, Name, the element verbatim and its tags (none when
// untagged); the element without its key is warned about once, by that key.
func TestScanEC2LateTaggedFanOut_StoresRows(t *testing.T) {
	for _, ph := range efpPhases {
		t.Run(ph.name, func(t *testing.T) {
			st := newTestStore(t)
			efpSeed(t, st, ph)
			warns := recordWarnings(st)
			var calls parentCalls
			total, inserted, err := elxRun(ph.run, st, map[string]func(any) (any, error){ph.sdkOp: efpHandler(ph, &calls, nil, nil)})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if total != 3 || inserted != 3 {
				t.Errorf("total, inserted = %d, %d; want 3, 3", total, inserted)
			}
			if got := calls.distinct(); !slices.Equal(got, []string{efpParent1, efpParent2}) {
				t.Errorf("parents queried = %v, want [%s %s]", got, efpParent1, efpParent2)
			}
			mexAssertNotStoredWarning(t, *warns, ph.op, ph.noKey)
			mexAssertRows(t, st, ph.typ, ph.rows)
		})
	}
}

// With no stored parent nothing is called; a parent with nothing to list
// stores nothing.
func TestScanEC2LateTaggedFanOut_Empty(t *testing.T) {
	for _, ph := range efpPhases {
		t.Run(ph.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := 0
			h := map[string]func(any) (any, error){ph.sdkOp: func(any) (any, error) { calls++; return ph.empty, nil }}
			if total, _, err := elxRun(ph.run, st, h); err != nil || total != 0 || calls != 0 {
				t.Fatalf("no parents: total, calls, err = %d, %d, %v; want 0, 0, nil", total, calls, err)
			}
			efpSeed(t, st, ph)
			if total, _, err := elxRun(ph.run, st, h); err != nil || total != 0 || calls != 2 {
				t.Fatalf("empty parents: total, calls, err = %d, %d, %v; want 0, 2, nil", total, calls, err)
			}
		})
	}
}

// Per-parent errors: parent 2's failure keeps every row parent 1 stored. A
// denial warns once and is not returned; a vanished parent, a transient
// failure and any other error are returned. A failure on parent 1's second
// page keeps its first page and parent 2's row.
func TestScanEC2LateTaggedFanOut_PerParentErrors(t *testing.T) {
	// EC2 models no not-found code, so a vanished parent is returned, never
	// skipped silently.
	vanished := apiErr("InvalidParameterValue.NotFound", "gone")
	cases := []struct {
		name        string
		fail, fail2 error
		wantErr     error
		// rows and warns: stored rows and warnings for the op.
		rows, warns int
	}{
		{"denied", elxDenied, nil, nil, 2, 2},
		{"vanished", vanished, nil, vanished, 2, 1},
		{"transient", io.EOF, nil, io.EOF, 2, 1},
		{"other", errElxTest, nil, errElxTest, 2, 1},
		{"second page fails", nil, errElxTest, errElxTest, 2, 0},
	}
	for _, ph := range efpPhases {
		for _, c := range cases {
			t.Run(ph.name+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				efpSeed(t, st, ph)
				warnings := countOpWarnings(st, ph.op)
				var calls parentCalls
				total, _, err := elxRun(ph.run, st, map[string]func(any) (any, error){ph.sdkOp: efpHandler(ph, &calls, c.fail, c.fail2)})
				switch {
				case c.wantErr == nil && err != nil:
					t.Fatalf("err = %v, want nil", err)
				case c.wantErr != nil && !errors.Is(err, c.wantErr):
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				rows := rowsByNativeID(t, st, ph.typ)
				if len(rows) != c.rows || total != c.rows {
					t.Errorf("rows, total = %v, %d; want %d", nativeIDs(rows), total, c.rows)
				}
				want := ph.parent2Row
				if c.fail != nil {
					want = ph.page1Row
				}
				if _, ok := rows[want]; !ok {
					t.Errorf("row %s missing, want it kept", want)
				}
				if *warnings != c.warns {
					t.Errorf("warnings = %d, want %d", *warnings, c.warns)
				}
			})
		}
	}
}

// A repeated NextToken ends the parent's listing with errRepeatedNextToken,
// its first page kept.
func TestScanEC2LateTaggedFanOut_RepeatedToken(t *testing.T) {
	for _, ph := range efpPhases {
		t.Run(ph.name, func(t *testing.T) {
			st := newTestStore(t)
			tgxSeedParent(t, st, ph.parentType, ph.parentARN(efpParent1), testRegion, "available", testScanID)
			calls := 0
			_, _, err := elxRun(ph.run, st, map[string]func(any) (any, error){ph.sdkOp: func(any) (any, error) {
				calls++
				if calls > 3 {
					return nil, errElxTest
				}
				return ph.page1, nil
			}})
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v, want errRepeatedNextToken", err)
			}
			if calls != 2 {
				t.Errorf("calls = %d, want 2", calls)
			}
			if _, ok := rowsByNativeID(t, st, ph.typ)[ph.page1Row]; !ok {
				t.Errorf("first page row %s missing", ph.page1Row)
			}
		})
	}
}

// efpTopLevel answers the older categories' image listing, each phase's
// parent listing and each phase's own op; scanEC2 runs every other EC2 op
// against the denial.
func efpTopLevel() map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"DescribeImages": func(any) (any, error) {
			return &ec2.DescribeImagesOutput{Images: []ec2types.Image{{ImageId: ptrStr(elxImg1), State: ec2types.ImageStateAvailable}}}, nil
		},
	}
	var calls parentCalls
	for _, ph := range efpPhases {
		for op, fn := range ph.topLevel {
			h[op] = fn
		}
		h[ph.sdkOp] = efpHandler(ph, &calls, nil, nil)
	}
	return h
}

// scanEC2 stores each phase's rows under the parents its categories stored.
func TestScanEC2_StoresLateTaggedFanOutRows(t *testing.T) {
	st := newTestStore(t)
	if err := elxScanEC2(st, efpTopLevel()); err != nil {
		t.Fatalf("scanEC2: %v", err)
	}
	for _, ph := range efpPhases {
		if _, ok := rowsByNativeID(t, st, ph.typ)[ph.page1Row]; !ok {
			t.Errorf("%s row %s missing", ph.name, ph.page1Row)
		}
	}
}

// Each phase runs after every older category: its hard failure leaves an
// older category's slow call uncancelled, does not stop the other phase,
// and reaches the caller.
func TestScanEC2_LateTaggedFanOutAfterOlderCategories(t *testing.T) {
	for _, failed := range efpPhases {
		t.Run(failed.name+" fails", func(t *testing.T) {
			st := newTestStore(t)
			h := efpTopLevel()
			h[failed.sdkOp] = func(any) (any, error) { return nil, errElxTest }
			ctxHandlers := ignoreCtx(h)
			ctxHandlers["DescribeImages"] = func(ctx context.Context, in any) (any, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(200 * time.Millisecond):
					return h["DescribeImages"](in)
				}
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfigCtx(ctxHandlers, elxDenied)
			_, _, err := scanEC2(context.Background(), acct, testRegion, st, testScanID)
			if !errors.Is(err, errElxTest) {
				t.Fatalf("err = %v, want the %s error", err, failed.sdkOp)
			}
			if _, ok := rowsByNativeID(t, st, TypeEC2Image)[elxARN("image", elxImg1)]; !ok {
				t.Errorf("image row missing: the %s failure cancelled an older category", failed.sdkOp)
			}
			for _, other := range efpPhases {
				if other.sdkOp == failed.sdkOp {
					continue
				}
				if _, ok := rowsByNativeID(t, st, other.typ)[other.page1Row]; !ok {
					t.Errorf("%s row missing after the %s failure", other.name, failed.sdkOp)
				}
			}
		})
	}
}

// mexIPAMPools is the mexPhase of scanIPAMPools (shared tests in
// ec2_misc_extra_scanners_test.go). A pool's NativeID is its IpamPoolArn.
var mexIPAMPools = mexPhase{
	name: "ipam pools", op: "ec2:DescribeIpamPools", sdkOp: "DescribeIpamPools",
	typ: TypeEC2IPAMPool,
	page1: &ec2.DescribeIpamPoolsOutput{NextToken: ptrStr(elxPageNext), IpamPools: []ec2types.IpamPool{{
		IpamPoolId: ptrStr("ipam-pool-1"), IpamPoolArn: ptrStr(efpIPAMAllocations.parentARN("ipam-pool-1")),
		Description: ptrStr("prod pool"), State: ec2types.IpamPoolStateCreateComplete,
		Tags: []ec2types.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}},
	}}},
	page2: &ec2.DescribeIpamPoolsOutput{IpamPools: []ec2types.IpamPool{
		{IpamPoolId: ptrStr("ipam-pool-2"), IpamPoolArn: ptrStr(efpIPAMAllocations.parentARN("ipam-pool-2"))},
		{IpamPoolId: ptrStr("ipam-pool-x")},
	}},
	empty: &ec2.DescribeIpamPoolsOutput{},
	request: func(in any) (int32, *string) {
		p := in.(*ec2.DescribeIpamPoolsInput)
		return sv32(p.MaxResults), p.NextToken
	},
	run: scanIPAMPools,
	rows: map[string]mexRow{
		efpIPAMAllocations.parentARN("ipam-pool-1"): {name: "prod pool", status: "create-complete", attrs: `{"AddressFamily":"","AllocationDefaultNetmaskLength":null,"AllocationMaxNetmaskLength":null,"AllocationMinNetmaskLength":null,"AllocationResourceTags":null,"AutoImport":null,"AwsService":"","Description":"prod pool","IpamArn":null,"IpamPoolArn":"arn:aws:ec2::123456789012:ipam-pool/ipam-pool-1","IpamPoolId":"ipam-pool-1","IpamRegion":null,"IpamScopeArn":null,"IpamScopeType":"","Locale":null,"OwnerId":null,"PoolDepth":null,"PublicIpSource":"","PubliclyAdvertisable":null,"SourceIpamPoolId":null,"SourceResource":null,"State":"create-complete","StateMessage":null,"Tags":[{"Key":"env","Value":"prod"}]}`, tags: map[string]string{"env": "prod"}},
		efpIPAMAllocations.parentARN("ipam-pool-2"): {attrs: `{"AddressFamily":"","AllocationDefaultNetmaskLength":null,"AllocationMaxNetmaskLength":null,"AllocationMinNetmaskLength":null,"AllocationResourceTags":null,"AutoImport":null,"AwsService":"","Description":null,"IpamArn":null,"IpamPoolArn":"arn:aws:ec2::123456789012:ipam-pool/ipam-pool-2","IpamPoolId":"ipam-pool-2","IpamRegion":null,"IpamScopeArn":null,"IpamScopeType":"","Locale":null,"OwnerId":null,"PoolDepth":null,"PublicIpSource":"","PubliclyAdvertisable":null,"SourceIpamPoolId":null,"SourceResource":null,"State":"","StateMessage":null,"Tags":null}`},
	},
	noKey:      "ipam-pool-x",
	page1Row:   efpIPAMAllocations.parentARN("ipam-pool-1"),
	gapSkipped: true,
}
