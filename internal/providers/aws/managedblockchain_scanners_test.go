package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/managedblockchain"
	mbtypes "github.com/aws/aws-sdk-go-v2/service/managedblockchain/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// mbTestTotal is every row of the fixture: two accessors, two networks,
	// three members, three nodes and two proposals.
	mbTestTotal = 2 + 2 + 3 + 3 + 2
	// mbTestOther owns the member of the Fabric network that is not this
	// account's.
	mbTestOther = "999999999999"
)

var (
	mbTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: managedblockchain:List")
	mbTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	mbTestBad      = apiErr("InvalidRequestException", "bad")
	mbTestGone     = apiErr("ResourceNotFoundException", "gone")
	mbTestNotReady = apiErr("ResourceNotReadyException", "not ready")
)

// mbTestARN is the ARN of res in partition and region, in acct ("" for a
// network or a proposal, whose ARNs name no account).
func mbTestARN(partition, region, acct, res string) string {
	return "arn:" + partition + ":managedblockchain:" + region + ":" + acct + ":" + res
}

// mbARN is mbTestARN in the aws partition and the test region.
func mbARN(acct, res string) string { return mbTestARN("aws", testRegion, acct, res) }

// mbFixture is what each listing returns, page by page: the members and
// proposals keyed by network id, the nodes by network id + "/" + member id
// (no member for a network-wide listing); and the tags of each ARN.
type mbFixture struct {
	accessors [][]mbtypes.AccessorSummary
	networks  [][]mbtypes.NetworkSummary
	members   map[string][][]mbtypes.MemberSummary
	nodes     map[string][][]mbtypes.NodeSummary
	proposals map[string][][]mbtypes.ProposalSummary
	tags      map[string]map[string]string
}

// newMBFixture is the fixture in partition and region for account acct: a
// Fabric network with this account's member m-own, another account's member
// m-other and this account's deleted member m-gone; m-own's two nodes; a
// proposal by each of m-own and m-other; an Ethereum network with one node;
// and two accessors. Every listing is two pages but the Ethereum nodes and
// m-gone's nodes. Every ARN is tagged with its type except the "-bare"
// rows, the tags of those not read (m-other, m-gone) included.
func newMBFixture(partition, region, acct string) mbFixture {
	arn := func(owner, res string) *string { return sdkaws.String(mbTestARN(partition, region, owner, res)) }
	s := sdkaws.String
	fx := mbFixture{
		accessors: [][]mbtypes.AccessorSummary{
			{{Arn: arn(acct, "accessors/ac-tagged"), Id: s("ac-tagged"), Status: mbtypes.AccessorStatusAvailable, Type: mbtypes.AccessorTypeBillingToken}},
			{{Arn: arn(acct, "accessors/ac-bare"), Id: s("ac-bare"), Status: mbtypes.AccessorStatusAvailable, Type: mbtypes.AccessorTypeBillingToken}},
		},
		networks: [][]mbtypes.NetworkSummary{
			{{Arn: arn("", "networks/n-fabric"), Id: s("n-fabric"), Name: s("fabric"), Framework: mbtypes.FrameworkHyperledgerFabric, Status: mbtypes.NetworkStatusAvailable}},
			{{Arn: arn("", "networks/n-eth"), Id: s("n-eth"), Name: s("eth"), Framework: mbtypes.FrameworkEthereum, Status: mbtypes.NetworkStatusAvailable}},
		},
		members: map[string][][]mbtypes.MemberSummary{
			"n-fabric": {
				{{Arn: arn(acct, "members/m-own"), Id: s("m-own"), Name: s("own"), IsOwned: sdkaws.Bool(true), Status: mbtypes.MemberStatusAvailable}},
				{
					{Arn: arn(mbTestOther, "members/m-other"), Id: s("m-other"), Name: s("other"), IsOwned: sdkaws.Bool(false), Status: mbtypes.MemberStatusAvailable},
					{Arn: arn(acct, "members/m-gone"), Id: s("m-gone"), Name: s("gone"), IsOwned: sdkaws.Bool(true), Status: mbtypes.MemberStatusDeleted},
				},
			},
		},
		nodes: map[string][][]mbtypes.NodeSummary{
			"n-fabric/m-own": {
				{{Arn: arn(acct, "nodes/nd-fabric-tagged"), Id: s("nd-fabric-tagged"), Status: mbtypes.NodeStatusAvailable}},
				{{Arn: arn(acct, "nodes/nd-fabric-bare"), Id: s("nd-fabric-bare"), Status: mbtypes.NodeStatusAvailable}},
			},
			"n-eth/": {{{Arn: arn(acct, "nodes/nd-eth"), Id: s("nd-eth"), Status: mbtypes.NodeStatusAvailable}}},
		},
		proposals: map[string][][]mbtypes.ProposalSummary{
			"n-fabric": {
				{{Arn: arn("", "proposals/p-own"), ProposalId: s("p-own"), ProposedByMemberId: s("m-own"), Status: mbtypes.ProposalStatusInProgress}},
				{{Arn: arn("", "proposals/p-other"), ProposalId: s("p-other"), ProposedByMemberId: s("m-other"), Status: mbtypes.ProposalStatusApproved}},
			},
		},
		tags: map[string]map[string]string{},
	}
	for res, typ := range map[string]string{
		acct + ":accessors/ac-tagged":    TypeManagedBlockchainAccessor,
		":networks/n-fabric":             TypeManagedBlockchainNetwork,
		":networks/n-eth":                TypeManagedBlockchainNetwork,
		acct + ":members/m-own":          TypeManagedBlockchainMember,
		mbTestOther + ":members/m-other": TypeManagedBlockchainMember,
		acct + ":members/m-gone":         TypeManagedBlockchainMember,
		acct + ":nodes/nd-fabric-tagged": TypeManagedBlockchainNode,
		acct + ":nodes/nd-eth":           TypeManagedBlockchainNode,
		":proposals/p-own":               TypeManagedBlockchainProposal,
		":proposals/p-other":             TypeManagedBlockchainProposal,
	} {
		owner, r, _ := strings.Cut(res, ":")
		fx.tags[mbTestARN(partition, region, owner, r)] = map[string]string{"type": typ}
	}
	return fx
}

// mbCalls records the inputs of a scan's calls and fails the tag reads of
// the ARNs in tagErrs.
type mbCalls struct {
	mu      sync.Mutex
	inputs  map[string][]any
	tagErrs map[string]error
}

func newMBCalls() *mbCalls {
	return &mbCalls{inputs: map[string][]any{}, tagErrs: map[string]error{}}
}

func mbRecord(c *mbCalls, op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs[op] = append(c.inputs[op], in)
}

func mbInputs(c *mbCalls, op string) []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.inputs[op])
}

// mbTagARNs is the ARNs ListTagsForResource was sent, sorted.
func mbTagARNs(c *mbCalls) []string {
	var arns []string
	for _, in := range mbInputs(c, "ListTagsForResource") {
		arns = append(arns, sv(in.(*managedblockchain.ListTagsForResourceInput).ResourceArn))
	}
	slices.Sort(arns)
	return arns
}

// mbFirstPageParents is the parents op's first pages were called for,
// sorted: NetworkId, plus "/" + MemberId for ListNodes.
func mbFirstPageParents(c *mbCalls, op string) []string {
	var got []string
	for _, in := range mbInputs(c, op) {
		if bacField[*string](in, "NextToken") != nil {
			continue
		}
		key := sv(bacField[*string](in, "NetworkId"))
		if op == "ListNodes" {
			key += "/" + sv(bacField[*string](in, "MemberId"))
		}
		got = append(got, key)
	}
	slices.Sort(got)
	return got
}

// mbPage is page token ("" or "p<n>") of pages and the token of the next.
func mbPage[T any](pages [][]T, token *string) ([]T, *string) {
	i := 0
	if token != nil {
		i, _ = strconv.Atoi(strings.TrimPrefix(*token, "p"))
	}
	if i >= len(pages) {
		return nil, nil
	}
	var next *string
	if i+1 < len(pages) {
		next = sdkaws.String("p" + strconv.Itoa(i+1))
	}
	return pages[i], next
}

// mbHandlers answers every op the scan calls from fx, recording the calls
// in c.
func mbHandlers(c *mbCalls, fx mbFixture) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListAccessors": func(in any) (any, error) {
			mbRecord(c, "ListAccessors", in)
			items, next := mbPage(fx.accessors, in.(*managedblockchain.ListAccessorsInput).NextToken)
			return &managedblockchain.ListAccessorsOutput{Accessors: items, NextToken: next}, nil
		},
		"ListNetworks": func(in any) (any, error) {
			mbRecord(c, "ListNetworks", in)
			items, next := mbPage(fx.networks, in.(*managedblockchain.ListNetworksInput).NextToken)
			return &managedblockchain.ListNetworksOutput{Networks: items, NextToken: next}, nil
		},
		"ListMembers": func(in any) (any, error) {
			mbRecord(c, "ListMembers", in)
			req := in.(*managedblockchain.ListMembersInput)
			items, next := mbPage(fx.members[sv(req.NetworkId)], req.NextToken)
			return &managedblockchain.ListMembersOutput{Members: items, NextToken: next}, nil
		},
		"ListNodes": func(in any) (any, error) {
			mbRecord(c, "ListNodes", in)
			req := in.(*managedblockchain.ListNodesInput)
			items, next := mbPage(fx.nodes[sv(req.NetworkId)+"/"+sv(req.MemberId)], req.NextToken)
			return &managedblockchain.ListNodesOutput{Nodes: items, NextToken: next}, nil
		},
		"ListProposals": func(in any) (any, error) {
			mbRecord(c, "ListProposals", in)
			req := in.(*managedblockchain.ListProposalsInput)
			items, next := mbPage(fx.proposals[sv(req.NetworkId)], req.NextToken)
			return &managedblockchain.ListProposalsOutput{Proposals: items, NextToken: next}, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			mbRecord(c, "ListTagsForResource", in)
			arn := sv(in.(*managedblockchain.ListTagsForResourceInput).ResourceArn)
			c.mu.Lock()
			err := c.tagErrs[arn]
			c.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return &managedblockchain.ListTagsForResourceOutput{Tags: fx.tags[arn]}, nil
		},
	}
}

// mbScan runs the scan's real entry point for acct in its first region
// against handlers; every op they do not answer is denied.
func mbScan(ctx context.Context, st *store.Store, acct *account, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct.cfg = opStubConfig(handlers, mbTestDenied)
	return scanManagedBlockchain(ctx, acct, acct.Regions[0], st, testScanID)
}

func mbScanDefault(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	return mbScan(ctx, st, newTestAccount(testAccountID), handlers)
}

// mbTestRow is one row the fixture stores: its type, NativeID, Name and
// Status, the Arn and id its attributes carry, and the tags stored (nil for
// none).
type mbTestRow struct {
	typ, nativeID, name, status, arn, id string
	tags                                 map[string]string
}

func mbTestRows() []mbTestRow {
	tagged := func(typ string) map[string]string { return map[string]string{"type": typ} }
	fabric := mbARN("", "networks/n-fabric")
	return []mbTestRow{
		{TypeManagedBlockchainAccessor, mbARN(testAccountID, "accessors/ac-tagged"), "ac-tagged", "AVAILABLE", mbARN(testAccountID, "accessors/ac-tagged"), "ac-tagged", tagged(TypeManagedBlockchainAccessor)},
		{TypeManagedBlockchainAccessor, mbARN(testAccountID, "accessors/ac-bare"), "ac-bare", "AVAILABLE", mbARN(testAccountID, "accessors/ac-bare"), "ac-bare", nil},
		{TypeManagedBlockchainNetwork, fabric, "fabric", "AVAILABLE", fabric, "n-fabric", tagged(TypeManagedBlockchainNetwork)},
		{TypeManagedBlockchainNetwork, mbARN("", "networks/n-eth"), "eth", "AVAILABLE", mbARN("", "networks/n-eth"), "n-eth", tagged(TypeManagedBlockchainNetwork)},
		{TypeManagedBlockchainMember, mbARN(testAccountID, "members/m-own"), "own", "AVAILABLE", mbARN(testAccountID, "members/m-own"), "m-own", tagged(TypeManagedBlockchainMember)},
		// Another account's: not read.
		{TypeManagedBlockchainMember, mbARN(mbTestOther, "members/m-other"), "other", "AVAILABLE", mbARN(mbTestOther, "members/m-other"), "m-other", nil},
		// Deleted: not read.
		{TypeManagedBlockchainMember, mbARN(testAccountID, "members/m-gone"), "gone", "DELETED", mbARN(testAccountID, "members/m-gone"), "m-gone", nil},
		{TypeManagedBlockchainNode, mbARN(testAccountID, "nodes/nd-fabric-tagged"), "nd-fabric-tagged", "AVAILABLE", mbARN(testAccountID, "nodes/nd-fabric-tagged"), "nd-fabric-tagged", tagged(TypeManagedBlockchainNode)},
		{TypeManagedBlockchainNode, mbARN(testAccountID, "nodes/nd-fabric-bare"), "nd-fabric-bare", "AVAILABLE", mbARN(testAccountID, "nodes/nd-fabric-bare"), "nd-fabric-bare", nil},
		{TypeManagedBlockchainNode, mbARN(testAccountID, "nodes/nd-eth"), "nd-eth", "AVAILABLE", mbARN(testAccountID, "nodes/nd-eth"), "nd-eth", tagged(TypeManagedBlockchainNode)},
		{TypeManagedBlockchainProposal, fabric + "/proposal/p-own", "p-own", "IN_PROGRESS", mbARN("", "proposals/p-own"), "p-own", tagged(TypeManagedBlockchainProposal)},
		{TypeManagedBlockchainProposal, fabric + "/proposal/p-other", "p-other", "APPROVED", mbARN("", "proposals/p-other"), "p-other", tagged(TypeManagedBlockchainProposal)},
	}
}

// mbReadARNs is every ARN the fixture's tag reads are sent: every network
// and proposal, a proposal at its Arn, and the other rows of this account
// that are not deleted.
func mbReadARNs() []string {
	return []string{
		mbARN("", "networks/n-eth"),
		mbARN("", "networks/n-fabric"),
		mbARN("", "proposals/p-other"),
		mbARN("", "proposals/p-own"),
		mbARN(testAccountID, "accessors/ac-bare"),
		mbARN(testAccountID, "accessors/ac-tagged"),
		mbARN(testAccountID, "members/m-own"),
		mbARN(testAccountID, "nodes/nd-eth"),
		mbARN(testAccountID, "nodes/nd-fabric-bare"),
		mbARN(testAccountID, "nodes/nd-fabric-tagged"),
	}
}

func mbWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, each row under its NativeID, named,
// with its Status, its attributes the listed element, and the tags its tag
// read returns. The tags of another account's member and a deleted member
// are not read; those of every network and proposal are. Members and proposals are
// listed only for the Fabric network; nodes per member this account owns on
// it and network-wide on the Ethereum one. Every listing is sent its
// model's MaxResults cap. No error, no warning.
func TestScanManagedBlockchain_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMBCalls()

	total, inserted, err := mbScanDefault(context.Background(), st, mbHandlers(calls, newMBFixture("aws", testRegion, testAccountID)))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mbTestTotal || inserted != mbTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, mbTestTotal, mbTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	perType := map[string]int{}
	for _, want := range mbTestRows() {
		perType[want.typ]++
		r, ok := rowsByNativeID(t, st, want.typ)[want.nativeID]
		if !ok {
			t.Errorf("%s %s missing", want.typ, want.nativeID)
			continue
		}
		if sv(r.Name) != want.name || sv(r.Status) != want.status || sv(r.Region) != testRegion || r.CreatedAt != nil || r.ManagedByProvider {
			t.Errorf("%s Name, Status, Region, CreatedAt, Managed = %q, %q, %q, %v, %v; want %q, %q, %q, nil, false",
				want.nativeID, sv(r.Name), sv(r.Status), sv(r.Region), r.CreatedAt, r.ManagedByProvider, want.name, want.status, testRegion)
		}
		var attrs struct {
			Arn        string
			ID         string `json:"Id"`
			ProposalID string `json:"ProposalId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Arn != want.arn || attrs.ID+attrs.ProposalID != want.id {
			t.Errorf("%s attrs = %s (%v); want the listed element", want.nativeID, r.AttributesJSON, err)
		}
		if want.tags != nil {
			assertTagsMap(t, r.TagsJSON, want.tags)
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", want.nativeID, *r.TagsJSON)
		}
	}
	for typ, n := range perType {
		if got := len(rowsByNativeID(t, st, typ)); got != n {
			t.Errorf("%s rows = %d; want %d", typ, got, n)
		}
	}
	if got, want := mbTagARNs(calls), slices.Sorted(slices.Values(mbReadARNs())); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
	for op, want := range map[string][]string{
		"ListMembers":   {"n-fabric"},
		"ListProposals": {"n-fabric"},
		"ListNodes":     {"n-eth/", "n-fabric/m-gone", "n-fabric/m-own"},
	} {
		if got := mbFirstPageParents(calls, op); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v; want %v", op, got, want)
		}
	}
	for op, want := range map[string]int32{"ListAccessors": 50, "ListNetworks": 10, "ListMembers": 20, "ListNodes": 20, "ListProposals": 100} {
		ins := mbInputs(calls, op)
		if len(ins) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, in := range ins {
			if n := bacField[*int32](in, "MaxResults"); n == nil || *n != want {
				t.Errorf("%s MaxResults = %v; want %d", op, n, want)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanManagedBlockchain_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	fx := newMBFixture("aws", testRegion, testAccountID)
	if _, _, err := mbScanDefault(context.Background(), st, mbHandlers(newMBCalls(), fx)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := mbScanDefault(context.Background(), st, mbHandlers(newMBCalls(), fx))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != mbTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, mbTestTotal)
	}
}

// No accessor and no network: nothing stored, no per-network listing or tag
// read, no error, no warning.
func TestScanManagedBlockchain_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMBCalls()

	total, inserted, err := mbScanDefault(context.Background(), st, mbHandlers(calls, mbFixture{}))
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListMembers", "ListNodes", "ListProposals", "ListTagsForResource"} {
		if n := len(mbInputs(calls, op)); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently (a network with its
// children, which are not listed), but an Ethereum network, which AWS runs,
// is kept untagged with its node. ResourceNotReadyException keeps the row
// untagged and warns under the tag op. A denial keeps the row untagged and
// warns once under the tag op with the scan's scope; a throttle keeps it and
// is transient; any other error keeps it and is an error.
func TestScanManagedBlockchain_TagReadFailures(t *testing.T) {
	node := mbARN(testAccountID, "nodes/nd-eth")
	eth := mbARN("", "networks/n-eth")
	fabric := mbARN("", "networks/n-fabric")
	proposal := fabric + "/proposal/p-own"
	// The Fabric network, its three members, m-own's two nodes and its two
	// proposals.
	fabricAndChildren := 1 + 3 + 2 + 2
	for _, c := range []struct {
		name          string
		tagARN        string
		typ, nativeID string
		err           error
		outcome       serviceOutcome // -1: no error
		code          string
		wantTotal     int
		wantKept      bool
		warn          bool
	}{
		{"node/not found", node, TypeManagedBlockchainNode, node, mbTestGone, -1, "", mbTestTotal - 1, false, false},
		{"node/denied", node, TypeManagedBlockchainNode, node, mbTestDenied, -1, "", mbTestTotal, true, true},
		{"node/transient", node, TypeManagedBlockchainNode, node, mbTestThrottle, outcomeTransient, "ThrottlingException", mbTestTotal, true, false},
		{"node/other error", node, TypeManagedBlockchainNode, node, mbTestBad, outcomeError, "InvalidRequestException", mbTestTotal, true, false},
		{"node/not ready", node, TypeManagedBlockchainNode, node, mbTestNotReady, -1, "", mbTestTotal, true, true},
		{"ethereum network/not found", eth, TypeManagedBlockchainNetwork, eth, mbTestGone, -1, "", mbTestTotal, true, false},
		{"proposal/not found", mbARN("", "proposals/p-own"), TypeManagedBlockchainProposal, proposal, mbTestGone, -1, "", mbTestTotal - 1, false, false},
		{"network/not found", fabric, TypeManagedBlockchainNetwork, fabric, mbTestGone, -1, "", mbTestTotal - fabricAndChildren, false, false},
		{"network/denied", fabric, TypeManagedBlockchainNetwork, fabric, mbTestDenied, -1, "", mbTestTotal, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newMBCalls()
			calls.tagErrs[c.tagARN] = c.err

			total, _, err := mbScanDefault(context.Background(), st, mbHandlers(calls, newMBFixture("aws", testRegion, testAccountID)))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.nativeID]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{mbTagsOp}
			}
			if got := mbWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads in three phases send one warning for the whole scan; the
// rows are kept.
func TestScanManagedBlockchain_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMBCalls()
	for _, arn := range []string{mbARN(testAccountID, "accessors/ac-bare"), mbARN(testAccountID, "members/m-own"), mbARN("", "proposals/p-own")} {
		calls.tagErrs[arn] = mbTestDenied
	}

	total, _, err := mbScanDefault(context.Background(), st, mbHandlers(calls, newMBFixture("aws", testRegion, testAccountID)))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mbTestTotal {
		t.Errorf("total = %d; want %d", total, mbTestTotal)
	}
	if got := mbWarnOps(warns()); !slices.Equal(got, []string{mbTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, mbTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanManagedBlockchain_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, mbTagsOp)
	calls := newMBCalls()
	calls.tagErrs[mbARN(testAccountID, "accessors/ac-tagged")] = mbTestThrottle
	calls.tagErrs[mbARN(testAccountID, "nodes/nd-fabric-bare")] = mbTestBad

	_, _, err := mbScanDefault(context.Background(), st, mbHandlers(calls, newMBFixture("aws", testRegion, testAccountID)))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", mbTagsOp, *warns)
	}
}

// ResourceNotReadyException on the tag reads of three phases keeps the rows
// untagged, with no error, and warns once under the tag op naming the
// smallest NativeID, not the first refused.
func TestScanManagedBlockchain_TagsNotReadyWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMBCalls()
	fabric := mbARN("", "networks/n-fabric")
	node := mbARN(testAccountID, "nodes/nd-eth")
	calls.tagErrs[fabric] = mbTestNotReady
	calls.tagErrs[node] = mbTestNotReady
	calls.tagErrs[mbARN("", "proposals/p-own")] = mbTestNotReady

	total, _, err := mbScanDefault(context.Background(), st, mbHandlers(calls, newMBFixture("aws", testRegion, testAccountID)))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mbTestTotal {
		t.Errorf("total = %d; want %d", total, mbTestTotal)
	}
	for typ, id := range map[string]string{
		TypeManagedBlockchainNetwork:  fabric,
		TypeManagedBlockchainNode:     node,
		TypeManagedBlockchainProposal: fabric + "/proposal/p-own",
	} {
		if r, ok := rowsByNativeID(t, st, typ)[id]; !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v; want stored untagged", id, ok)
		}
	}
	w := warns()
	if len(w) != 1 || w[0].Service != mbTagsOp || w[0].Scope != testAccountID+"/"+testRegion {
		t.Fatalf("warnings = %v; want one under %s", w, mbTagsOp)
	}
	// The node's ARN, which names the account, sorts before the networks'.
	if !strings.Contains(w[0].Message, "first "+node+":") {
		t.Errorf("warning %q; want it to name %s first", w[0].Message, node)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them, or a network listed without an Arn) or about rows
// written before it whose tag read was refused as not ready, no later phase
// runs, not even for the network without an Arn, which is not written, and
// the earlier phases' rows stay counted. An earlier phase's own warning is
// still sent. A failed node write sends no warning of the Fabric networks
// whose nodes could not be listed, though their member listing was denied.
func TestScanManagedBlockchain_FailedWriteStopsScan(t *testing.T) {
	for _, c := range []struct {
		refused, denied string
		membersDenied   bool
		notCalled       []string
		total           int
		warnOps         []string
	}{
		{TypeManagedBlockchainNetwork, mbARN("", "networks/n-fabric"), false, []string{"ListMembers", "ListNodes", "ListProposals"}, 2, nil},
		{TypeManagedBlockchainMember, mbARN(testAccountID, "members/m-own"), false, []string{"ListNodes", "ListProposals"}, 2 + 2, []string{"managedblockchain:ListNetworks"}},
		{TypeManagedBlockchainNode, mbARN(testAccountID, "nodes/nd-eth"), true, []string{"ListProposals"}, 2 + 2, []string{"managedblockchain:ListNetworks", "managedblockchain:ListMembers"}},
	} {
		t.Run(c.refused, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.refused)
			calls := newMBCalls()
			calls.tagErrs[mbARN(testAccountID, "accessors/ac-tagged")] = mbTestNotReady
			calls.tagErrs[c.denied] = mbTestDenied
			fx := newMBFixture("aws", testRegion, testAccountID)
			fx.networks[1] = append(fx.networks[1], mbtypes.NetworkSummary{Id: sdkaws.String("n-noarn"), Framework: mbtypes.FrameworkHyperledgerFabric})
			handlers := mbHandlers(calls, fx)
			if c.membersDenied {
				handlers["ListMembers"] = func(any) (any, error) { return nil, mbTestDenied }
			}

			total, _, err := mbScanDefault(context.Background(), st, handlers)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); !slices.Equal(mbWarnOps(w), c.warnOps) {
				t.Errorf("warnings = %v; want under %v", w, c.warnOps)
			}
			for _, op := range c.notCalled {
				if n := len(mbInputs(calls, op)); n != 0 {
					t.Errorf("%s called %d times; want 0 after the failed write", op, n)
				}
			}
			if total != c.total {
				t.Errorf("total = %d; want %d", total, c.total)
			}
		})
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the later phases: a failed second member page keeps m-own, whose
// nodes and the network's proposals are still read; a failed accessor
// listing leaves the networks and their children read.
func TestScanManagedBlockchain_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Run("second member page", func(t *testing.T) {
		st := newTestStore(t)
		calls := newMBCalls()
		handlers := mbHandlers(calls, newMBFixture("aws", testRegion, testAccountID))
		list := handlers["ListMembers"]
		handlers["ListMembers"] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				return nil, mbTestBad
			}
			return list(in)
		}

		total, _, err := mbScanDefault(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "InvalidRequestException")
		if want := mbTestTotal - 2; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
		members := rowsByNativeID(t, st, TypeManagedBlockchainMember)
		if _, ok := members[mbARN(testAccountID, "members/m-own")]; !ok || len(members) != 1 {
			t.Errorf("members = %v; want m-own alone", members)
		}
		if got, want := mbFirstPageParents(calls, "ListNodes"), []string{"n-eth/", "n-fabric/m-own"}; !slices.Equal(got, want) {
			t.Errorf("ListNodes parents = %v; want %v", got, want)
		}
		if n := len(rowsByNativeID(t, st, TypeManagedBlockchainProposal)); n != 2 {
			t.Errorf("proposals = %d; want 2", n)
		}
	})
	t.Run("accessors", func(t *testing.T) {
		st := newTestStore(t)
		handlers := mbHandlers(newMBCalls(), newMBFixture("aws", testRegion, testAccountID))
		handlers["ListAccessors"] = func(any) (any, error) { return nil, mbTestBad }

		total, _, err := mbScanDefault(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := mbTestTotal - 2; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
	})
}

// A NextToken returned twice ends the listing with errRepeatedNextToken,
// keeping both pages read; the later phases still run.
func TestScanManagedBlockchain_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	fx := newMBFixture("aws", testRegion, testAccountID)
	handlers := mbHandlers(newMBCalls(), fx)
	handlers["ListAccessors"] = func(in any) (any, error) {
		page := fx.accessors[0]
		if in.(*managedblockchain.ListAccessorsInput).NextToken != nil {
			page = fx.accessors[1]
		}
		return &managedblockchain.ListAccessorsOutput{Accessors: page, NextToken: sdkaws.String("same")}, nil
	}

	total, _, err := mbScanDefault(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != mbTestTotal {
		t.Errorf("total = %d; want %d", total, mbTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeManagedBlockchainAccessor)); n != 2 {
		t.Errorf("accessors = %d; want both pages' 2", n)
	}
}

// Elements without the identifier their row needs: an accessor, member or
// node without an Arn, a proposal without a ProposalId and a network with
// neither an Arn nor an Id are not stored and are warned about once under
// their listing, named by the identifier they have; a network without an Id
// is stored, but its children are not listed and it is warned about. The
// nodes of this account's member listed without an Arn are still listed; no
// listing is sent an empty NetworkId.
func TestScanManagedBlockchain_ElementsWithoutIdentifiers(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMBCalls()
	fx := newMBFixture("aws", testRegion, testAccountID)
	fx.accessors[1][0].Arn = nil
	fx.members["n-fabric"][0] = append(fx.members["n-fabric"][0], mbtypes.MemberSummary{Id: sdkaws.String("m-noarn"), IsOwned: sdkaws.Bool(true)})
	fx.nodes["n-eth/"][0] = append(fx.nodes["n-eth/"][0], mbtypes.NodeSummary{Id: sdkaws.String("nd-noarn")})
	fx.proposals["n-fabric"][1] = append(fx.proposals["n-fabric"][1], mbtypes.ProposalSummary{Arn: sdkaws.String(mbARN("", "proposals/p-noid"))})
	fx.networks[1] = append(fx.networks[1],
		mbtypes.NetworkSummary{Arn: sdkaws.String(mbARN("", "networks/n-noid")), Name: sdkaws.String("noid")},
		mbtypes.NetworkSummary{Name: sdkaws.String("anonymous")})

	total, _, err := mbScanDefault(context.Background(), st, mbHandlers(calls, fx))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// One accessor lost, the Id-less network gained.
	if want := mbTestTotal - 1 + 1; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	if _, ok := rowsByNativeID(t, st, TypeManagedBlockchainNetwork)[mbARN("", "networks/n-noid")]; !ok {
		t.Error("the network without an Id was not stored")
	}
	w := warns()
	got := mbWarnOps(w)
	slices.Sort(got)
	want := []string{"managedblockchain:ListAccessors", "managedblockchain:ListMembers", "managedblockchain:ListNetworks", "managedblockchain:ListNetworks", "managedblockchain:ListNodes", "managedblockchain:ListProposals"}
	if !slices.Equal(got, want) {
		t.Errorf("warnings = %v; want %v", got, want)
	}
	// Each not stored element is named by the identifier it has.
	for op, first := range map[string]string{
		"managedblockchain:ListAccessors": "ac-bare",
		"managedblockchain:ListProposals": mbARN("", "proposals/p-noid"),
	} {
		i := slices.IndexFunc(w, func(x store.ScanWarning) bool { return x.Service == op })
		if i < 0 || !strings.HasSuffix(w[i].Message, "first "+first) {
			t.Errorf("%s warning = %v; want it to name %s first", op, w, first)
		}
	}
	if got, want := mbFirstPageParents(calls, "ListMembers"), []string{"n-fabric"}; !slices.Equal(got, want) {
		t.Errorf("ListMembers parents = %v; want %v", got, want)
	}
	if got, want := mbFirstPageParents(calls, "ListNodes"), []string{"n-eth/", "n-fabric/m-gone", "n-fabric/m-noarn", "n-fabric/m-own"}; !slices.Equal(got, want) {
		t.Errorf("ListNodes parents = %v; want %v", got, want)
	}
}

// A Fabric network listed without an Arn is not stored and is warned about,
// but its members and their nodes are stored; its proposals, whose NativeIDs
// need its ARN, are not listed.
func TestScanManagedBlockchain_NetworkWithoutARN(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMBCalls()
	fx := newMBFixture("aws", testRegion, testAccountID)
	fx.networks[1] = append(fx.networks[1], mbtypes.NetworkSummary{Id: sdkaws.String("n-noarn"), Framework: mbtypes.FrameworkHyperledgerFabric})
	member := mbARN(testAccountID, "members/m-x")
	node := mbARN(testAccountID, "nodes/nd-x")
	fx.members["n-noarn"] = [][]mbtypes.MemberSummary{{{Arn: sdkaws.String(member), Id: sdkaws.String("m-x"), IsOwned: sdkaws.Bool(true)}}}
	fx.nodes["n-noarn/m-x"] = [][]mbtypes.NodeSummary{{{Arn: sdkaws.String(node), Id: sdkaws.String("nd-x")}}}
	fx.proposals["n-noarn"] = [][]mbtypes.ProposalSummary{{{Arn: sdkaws.String(mbARN("", "proposals/p-x")), ProposalId: sdkaws.String("p-x"), ProposedByMemberId: sdkaws.String("m-x")}}}

	total, _, err := mbScanDefault(context.Background(), st, mbHandlers(calls, fx))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := mbTestTotal + 2; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	if n := len(rowsByNativeID(t, st, TypeManagedBlockchainNetwork)); n != 2 {
		t.Errorf("networks = %d; want the 2 with an ARN", n)
	}
	if _, ok := rowsByNativeID(t, st, TypeManagedBlockchainMember)[member]; !ok {
		t.Error("the member of the network without an ARN was not stored")
	}
	if _, ok := rowsByNativeID(t, st, TypeManagedBlockchainNode)[node]; !ok {
		t.Error("the node of the network without an ARN was not stored")
	}
	if got, want := mbFirstPageParents(calls, "ListProposals"), []string{"n-fabric"}; !slices.Equal(got, want) {
		t.Errorf("ListProposals parents = %v; want %v", got, want)
	}
	if got := mbWarnOps(warns()); !slices.Equal(got, []string{"managedblockchain:ListNetworks"}) {
		t.Errorf("warnings = %v; want one under ListNetworks", got)
	}
}

// A member without an Id is stored, but its nodes are not listed and it is
// warned about under ListMembers alone: when it is the network's only member
// of this account, the network still had one listed, so nothing is warned
// under ListNodes.
func TestScanManagedBlockchain_OwnedMemberWithoutID(t *testing.T) {
	noID := mbtypes.MemberSummary{Arn: sdkaws.String(mbARN(testAccountID, "members/m-noid")), IsOwned: sdkaws.Bool(true)}
	for _, c := range []struct {
		name         string
		members      func(fx mbFixture) [][]mbtypes.MemberSummary
		total        int
		nodesParents []string
	}{
		{"beside owned members", func(fx mbFixture) [][]mbtypes.MemberSummary {
			pages := fx.members["n-fabric"]
			pages[0] = append(pages[0], noID)
			return pages
		}, mbTestTotal + 1, []string{"n-eth/", "n-fabric/m-gone", "n-fabric/m-own"}},
		// The three members and m-own's two nodes give way to m-noid.
		{"only owned member", func(mbFixture) [][]mbtypes.MemberSummary {
			return [][]mbtypes.MemberSummary{{noID}}
		}, mbTestTotal - 3 - 2 + 1, []string{"n-eth/"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newMBCalls()
			fx := newMBFixture("aws", testRegion, testAccountID)
			fx.members["n-fabric"] = c.members(fx)

			total, _, err := mbScanDefault(context.Background(), st, mbHandlers(calls, fx))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != c.total {
				t.Errorf("total = %d; want %d", total, c.total)
			}
			if _, ok := rowsByNativeID(t, st, TypeManagedBlockchainMember)[mbARN(testAccountID, "members/m-noid")]; !ok {
				t.Errorf("m-noid not stored")
			}
			if got := mbFirstPageParents(calls, "ListNodes"); !slices.Equal(got, c.nodesParents) {
				t.Errorf("ListNodes parents = %v; want %v", got, c.nodesParents)
			}
			if got := mbWarnOps(warns()); !slices.Equal(got, []string{"managedblockchain:ListMembers"}) {
				t.Errorf("warnings = %v; want one under ListMembers", got)
			}
		})
	}
}

// A proposal listed without an Arn is stored untagged, and the tag read it
// could not make is an error.
func TestScanManagedBlockchain_ProposalWithoutARN(t *testing.T) {
	st := newTestStore(t)
	fx := newMBFixture("aws", testRegion, testAccountID)
	fx.proposals["n-fabric"][0][0].Arn = nil

	total, _, err := mbScanDefault(context.Background(), st, mbHandlers(newMBCalls(), fx))
	if !errors.Is(err, errMBProposalNoARN) {
		t.Fatalf("err = %v; want errMBProposalNoARN", err)
	}
	if total != mbTestTotal {
		t.Errorf("total = %d; want %d", total, mbTestTotal)
	}
	r, ok := rowsByNativeID(t, st, TypeManagedBlockchainProposal)[mbARN("", "networks/n-fabric")+"/proposal/p-own"]
	if !ok || r.TagsJSON != nil {
		t.Errorf("proposal stored %v, TagsJSON %v; want stored untagged", ok, r.TagsJSON)
	}
}

// A denied member listing leaves the Fabric network without a member of this
// account: its nodes cannot be listed, which is warned about under ListNodes
// naming the network, beside the ListMembers denial. Every proposal's tags
// are still read.
func TestScanManagedBlockchain_MembersDeniedWarnsNodesNotListed(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMBCalls()
	handlers := mbHandlers(calls, newMBFixture("aws", testRegion, testAccountID))
	handlers["ListMembers"] = func(any) (any, error) { return nil, mbTestDenied }

	total, _, err := mbScanDefault(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := mbTestTotal - 3 - 2; total != want {
		t.Errorf("total = %d; want %d without the members and Fabric nodes", total, want)
	}
	w := warns()
	if got, want := mbWarnOps(w), []string{"managedblockchain:ListMembers", "managedblockchain:ListNodes"}; !slices.Equal(got, want) {
		t.Fatalf("warnings = %v; want %v", w, want)
	}
	if !strings.HasSuffix(w[1].Message, "first n-fabric") {
		t.Errorf("ListNodes warning %q; want it to name n-fabric", w[1].Message)
	}
	if got, want := mbFirstPageParents(calls, "ListNodes"), []string{"n-eth/"}; !slices.Equal(got, want) {
		t.Errorf("ListNodes parents = %v; want %v", got, want)
	}
	for _, id := range []string{"p-own", "p-other"} {
		r := rowsByNativeID(t, st, TypeManagedBlockchainProposal)[mbARN("", "networks/n-fabric")+"/proposal/"+id]
		assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeManagedBlockchainProposal})
	}
}

// A Fabric network whose only member listed is another account's has no
// member of this account to list nodes by, which is warned about under
// ListNodes naming the network.
func TestScanManagedBlockchain_OnlyOtherAccountsMemberWarnsNodesNotListed(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMBCalls()
	fx := newMBFixture("aws", testRegion, testAccountID)
	fx.members["n-fabric"] = [][]mbtypes.MemberSummary{{fx.members["n-fabric"][1][0]}}

	total, _, err := mbScanDefault(context.Background(), st, mbHandlers(calls, fx))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// m-other is stored; m-own, m-gone and m-own's two nodes are not.
	if want := mbTestTotal - 2 - 2; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	w := warns()
	if got, want := mbWarnOps(w), []string{"managedblockchain:ListNodes"}; !slices.Equal(got, want) {
		t.Fatalf("warnings = %v; want %v", w, want)
	}
	if !strings.HasSuffix(w[0].Message, "first n-fabric") {
		t.Errorf("ListNodes warning %q; want it to name n-fabric", w[0].Message)
	}
	if got, want := mbFirstPageParents(calls, "ListNodes"), []string{"n-eth/"}; !slices.Equal(got, want) {
		t.Errorf("ListNodes parents = %v; want %v", got, want)
	}
}

// A network listed with no Framework is read as Fabric: its members and
// proposals are listed, and its nodes per member this account owns. A
// member listed without IsOwned is this account's: its tags are read and
// its nodes listed.
func TestScanManagedBlockchain_UnsetFrameworkAndOwnership(t *testing.T) {
	st := newTestStore(t)
	calls := newMBCalls()
	fx := newMBFixture("aws", testRegion, testAccountID)
	member := mbARN(testAccountID, "members/m-unset")
	fx.networks[1] = append(fx.networks[1], mbtypes.NetworkSummary{Arn: sdkaws.String(mbARN("", "networks/n-unset")), Id: sdkaws.String("n-unset")})
	fx.members["n-unset"] = [][]mbtypes.MemberSummary{{{Arn: sdkaws.String(member), Id: sdkaws.String("m-unset")}}}
	fx.tags[member] = map[string]string{"owner": "me"}

	total, _, err := mbScanDefault(context.Background(), st, mbHandlers(calls, fx))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := mbTestTotal + 2; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	for op, want := range map[string][]string{
		"ListMembers":   {"n-fabric", "n-unset"},
		"ListProposals": {"n-fabric", "n-unset"},
		"ListNodes":     {"n-eth/", "n-fabric/m-gone", "n-fabric/m-own", "n-unset/m-unset"},
	} {
		if got := mbFirstPageParents(calls, op); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v; want %v", op, got, want)
		}
	}
	if !slices.Contains(mbTagARNs(calls), member) {
		t.Errorf("ListTagsForResource ARNs = %v; want %s among them", mbTagARNs(calls), member)
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeManagedBlockchainMember)[member].TagsJSON, map[string]string{"owner": "me"})
}

// ListProposals models ResourceNotFoundException for a network deleted since
// it was listed: the network's proposals are skipped silently.
func TestScanManagedBlockchain_ProposalsOfDeletedNetwork(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	handlers := mbHandlers(newMBCalls(), newMBFixture("aws", testRegion, testAccountID))
	handlers["ListProposals"] = func(any) (any, error) { return nil, mbTestGone }

	total, _, err := mbScanDefault(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mbTestTotal-2 {
		t.Errorf("total = %d; want %d", total, mbTestTotal-2)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// In GovCloud and China the tag reads are sent the listed ARNs unchanged,
// a proposal's included.
func TestScanManagedBlockchain_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, partition := range []string{"aws-us-gov", "aws-cn"} {
		t.Run(partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newMBCalls()
			acct := partitionAccount(partition)
			region := acct.Regions[0]

			if _, _, err := mbScan(context.Background(), st, acct, mbHandlers(calls, newMBFixture(partition, region, testAccountID))); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := mbReadARNs()
			for i, arn := range want {
				want[i] = strings.Replace(strings.Replace(arn, "arn:aws:", "arn:"+partition+":", 1), ":"+testRegion+":", ":"+region+":", 1)
			}
			slices.Sort(want)
			if got := mbTagARNs(calls); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			node := mbTestARN(partition, region, testAccountID, "nodes/nd-eth")
			assertTagsMap(t, rowsByNativeID(t, st, TypeManagedBlockchainNode)[node].TagsJSON, map[string]string{"type": TypeManagedBlockchainNode})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it, and the later phases are not run.
func TestScanManagedBlockchain_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newMBCalls()
	handlers := mbHandlers(calls, newMBFixture("aws", testRegion, testAccountID))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListNetworks"]
	handlers["ListNetworks"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := mbScanDefault(ctx, st, handlers)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := len(mbInputs(calls, "ListMembers")); n != 0 {
		t.Errorf("ListMembers called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanManagedBlockchain_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "managedblockchain.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := mbHandlers(newMBCalls(), newMBFixture("aws", testRegion, testAccountID))
		handlers["ListProposals"] = func(any) (any, error) { return nil, nx }
		total, _, err := mbScanDefault(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := mbTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanManagedBlockchain(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
