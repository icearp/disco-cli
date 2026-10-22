package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/globalaccelerator"
	gatypes "github.com/aws/aws-sdk-go-v2/service/globalaccelerator/types"
	"github.com/icearp/disco-cli/store"
)

const (
	gaTestStdAcc = "arn:aws:globalaccelerator::123456789012:accelerator/std-1"
	gaTestCRAcc1 = "arn:aws:globalaccelerator::123456789012:accelerator/cr-1"
	gaTestCRAcc2 = "arn:aws:globalaccelerator::123456789012:accelerator/cr-2"
	gaTestCRL1   = gaTestCRAcc1 + "/listener/l1"
	gaTestCRL2   = gaTestCRAcc1 + "/listener/l2"
	gaTestCRL3   = gaTestCRAcc2 + "/listener/l3"
	gaTestCREG1  = gaTestCRL1 + "/endpoint-group/eg1"
	gaTestCREG2  = gaTestCRL1 + "/endpoint-group/eg2"
	gaTestCREG3  = gaTestCRL3 + "/endpoint-group/eg3"
	gaTestCidr1  = "203.0.113.0/24"
	gaTestCidr2  = "198.51.100.0/24"
)

var (
	gaTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: globalaccelerator:List")
	errGATest    = errors.New("ga test failure")
)

// gaCheckLimit fails a call that does not ask for the op's maximum page size.
func gaCheckLimit(maxResults *int32) error {
	if maxResults == nil || *maxResults != 100 {
		return fmt.Errorf("MaxResults = %v, want 100", maxResults)
	}
	return nil
}

// gaHandlers answers every op the scanner calls: one standard accelerator
// with no children, two custom routing accelerators over two pages with three
// listeners and three endpoint groups between them, and two live BYOIP ranges
// plus a deprovisioned one. listenerCalls and egCalls record the parents the
// child ops were called for.
func gaHandlers(listenerCalls, egCalls *parentCalls) map[string]func(in any) (any, error) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return map[string]func(in any) (any, error){
		"ListAccelerators": func(any) (any, error) {
			return &globalaccelerator.ListAcceleratorsOutput{Accelerators: []gatypes.Accelerator{{AcceleratorArn: ptrStr(gaTestStdAcc)}}}, nil
		},
		"ListCrossAccountAttachments": func(any) (any, error) {
			return &globalaccelerator.ListCrossAccountAttachmentsOutput{}, nil
		},
		"ListListeners":      func(any) (any, error) { return &globalaccelerator.ListListenersOutput{}, nil },
		"ListEndpointGroups": func(any) (any, error) { return &globalaccelerator.ListEndpointGroupsOutput{}, nil },
		"ListCustomRoutingAccelerators": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListCustomRoutingAcceleratorsInput)
			if err := gaCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			if p.NextToken == nil {
				return &globalaccelerator.ListCustomRoutingAcceleratorsOutput{NextToken: ptrStr("p2"), Accelerators: []gatypes.CustomRoutingAccelerator{{
					AcceleratorArn: ptrStr(gaTestCRAcc1), Name: ptrStr("game-servers"),
					Status: gatypes.CustomRoutingAcceleratorStatusDeployed, CreatedTime: &created, DnsName: ptrStr("a1.awsglobalaccelerator.com"),
				}}}, nil
			}
			return &globalaccelerator.ListCustomRoutingAcceleratorsOutput{Accelerators: []gatypes.CustomRoutingAccelerator{
				{AcceleratorArn: ptrStr(gaTestCRAcc2), Status: gatypes.CustomRoutingAcceleratorStatusInProgress},
				{Name: ptrStr("no-arn")},
			}}, nil
		},
		"ListCustomRoutingListeners": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListCustomRoutingListenersInput)
			listenerCalls.add(sv(p.AcceleratorArn))
			if err := gaCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case sv(p.AcceleratorArn) == gaTestCRAcc2:
				return &globalaccelerator.ListCustomRoutingListenersOutput{Listeners: []gatypes.CustomRoutingListener{{ListenerArn: ptrStr(gaTestCRL3)}}}, nil
			case p.NextToken == nil:
				return &globalaccelerator.ListCustomRoutingListenersOutput{NextToken: ptrStr("p2"), Listeners: []gatypes.CustomRoutingListener{{
					ListenerArn: ptrStr(gaTestCRL1), PortRanges: []gatypes.PortRange{{FromPort: ptrInt32(5000), ToPort: ptrInt32(10000)}},
				}}}, nil
			default:
				return &globalaccelerator.ListCustomRoutingListenersOutput{Listeners: []gatypes.CustomRoutingListener{{ListenerArn: ptrStr(gaTestCRL2)}, {}}}, nil
			}
		},
		"ListCustomRoutingEndpointGroups": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListCustomRoutingEndpointGroupsInput)
			egCalls.add(sv(p.ListenerArn))
			if err := gaCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case sv(p.ListenerArn) == gaTestCRL2:
				return &globalaccelerator.ListCustomRoutingEndpointGroupsOutput{}, nil
			case sv(p.ListenerArn) == gaTestCRL3:
				return &globalaccelerator.ListCustomRoutingEndpointGroupsOutput{EndpointGroups: []gatypes.CustomRoutingEndpointGroup{
					{EndpointGroupArn: ptrStr(gaTestCREG3)}, {EndpointGroupRegion: ptrStr("eu-west-1")},
				}}, nil
			case p.NextToken == nil:
				return &globalaccelerator.ListCustomRoutingEndpointGroupsOutput{NextToken: ptrStr("p2"), EndpointGroups: []gatypes.CustomRoutingEndpointGroup{{
					EndpointGroupArn: ptrStr(gaTestCREG1), EndpointGroupRegion: ptrStr("us-east-1"),
					EndpointDescriptions: []gatypes.CustomRoutingEndpointDescription{{EndpointId: ptrStr("subnet-0abc")}},
				}}}, nil
			default:
				return &globalaccelerator.ListCustomRoutingEndpointGroupsOutput{EndpointGroups: []gatypes.CustomRoutingEndpointGroup{{
					EndpointGroupArn: ptrStr(gaTestCREG2), EndpointGroupRegion: ptrStr("eu-west-1"),
				}}}, nil
			}
		},
		"ListByoipCidrs": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListByoipCidrsInput)
			if err := gaCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			if p.NextToken == nil {
				return &globalaccelerator.ListByoipCidrsOutput{NextToken: ptrStr("p2"), ByoipCidrs: []gatypes.ByoipCidr{{
					Cidr: ptrStr(gaTestCidr1), State: gatypes.ByoipCidrStateAdvertising,
					Events: []gatypes.ByoipCidrEvent{{Message: ptrStr("advertised")}},
				}}}, nil
			}
			return &globalaccelerator.ListByoipCidrsOutput{ByoipCidrs: []gatypes.ByoipCidr{
				{Cidr: ptrStr(gaTestCidr2), State: gatypes.ByoipCidrStateFailedDeprovision},
				{Cidr: ptrStr("192.0.2.0/24"), State: gatypes.ByoipCidrStateDeprovisioned},
				{State: gatypes.ByoipCidrStateReady},
			}}, nil
		},
	}
}

func scanGATopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, gaTestDenied)
	total, _, err := scanGlobalAccelerator(context.Background(), acct, "", st, testScanID)
	return total, err
}

func gaAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	return m
}

func gaByoipID(cidr string) string { return gaByoipCidrNativeID(testAccountID, cidr) }

func TestScanGlobalAccelerator_StoresCustomRoutingAndByoip(t *testing.T) {
	st := newTestStore(t)
	var listenerCalls, egCalls parentCalls
	total, err := scanGATopLevel(t, st, gaHandlers(&listenerCalls, &egCalls))
	if err != nil {
		t.Fatalf("scanGlobalAccelerator: %v", err)
	}
	// 1 standard accelerator + 2 custom accelerators + 3 listeners + 3 endpoint groups + 2 BYOIP ranges
	if total != 11 {
		t.Errorf("total = %d, want 11", total)
	}

	accs := rowsByNativeID(t, st, TypeGlobalAcceleratorCustomRoutingAccelerator)
	if got := nativeIDs(accs); !slices.Equal(got, []string{gaTestCRAcc1, gaTestCRAcc2}) {
		t.Fatalf("custom accelerator rows = %v, want [%s %s]", got, gaTestCRAcc1, gaTestCRAcc2)
	}
	a1 := accs[gaTestCRAcc1]
	if sv(a1.Name) != "game-servers" || sv(a1.Status) != "DEPLOYED" || sv(a1.Region) != "global" || sv(a1.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("accelerator name/status/region/created = %q/%q/%q/%q, want game-servers/DEPLOYED/global/2026-01-02T03:04:05Z",
			sv(a1.Name), sv(a1.Status), sv(a1.Region), sv(a1.CreatedAt))
	}
	if got := gaAttrs(t, a1)["DnsName"]; got != "a1.awsglobalaccelerator.com" {
		t.Errorf("accelerator DnsName = %v, want a1.awsglobalaccelerator.com", got)
	}
	if got := sv(accs[gaTestCRAcc2].Status); got != "IN_PROGRESS" {
		t.Errorf("accelerator 2 status = %q, want IN_PROGRESS", got)
	}

	listeners := rowsByNativeID(t, st, TypeGlobalAcceleratorCustomRoutingListener)
	if got := nativeIDs(listeners); !slices.Equal(got, []string{gaTestCRL1, gaTestCRL2, gaTestCRL3}) {
		t.Fatalf("listener rows = %v, want [%s %s %s]", got, gaTestCRL1, gaTestCRL2, gaTestCRL3)
	}
	if got := gaAttrs(t, listeners[gaTestCRL1])["PortRanges"]; got == nil {
		t.Errorf("listener PortRanges missing from attributes %s", listeners[gaTestCRL1].AttributesJSON)
	}
	if got := listenerCalls.distinct(); !slices.Equal(got, []string{gaTestCRAcc1, gaTestCRAcc2}) {
		t.Errorf("listeners listed for %v, want [%s %s]", got, gaTestCRAcc1, gaTestCRAcc2)
	}

	groups := rowsByNativeID(t, st, TypeGlobalAcceleratorCustomRoutingEndpointGroup)
	if got := nativeIDs(groups); !slices.Equal(got, []string{gaTestCREG1, gaTestCREG2, gaTestCREG3}) {
		t.Fatalf("endpoint group rows = %v, want [%s %s %s]", got, gaTestCREG1, gaTestCREG2, gaTestCREG3)
	}
	if got := sv(groups[gaTestCREG2].Name); got != "eu-west-1" {
		t.Errorf("endpoint group name = %q, want eu-west-1", got)
	}
	if got := groups[gaTestCREG3].Name; got != nil {
		t.Errorf("endpoint group without a region has name %q, want none", *got)
	}
	if got := egCalls.distinct(); !slices.Equal(got, []string{gaTestCRL1, gaTestCRL2, gaTestCRL3}) {
		t.Errorf("endpoint groups listed for %v, want [%s %s %s]", got, gaTestCRL1, gaTestCRL2, gaTestCRL3)
	}

	assertContainsNative(t, st, gaTestCRAcc1, gaTestCRL1)
	assertContainsNative(t, st, gaTestCRAcc1, gaTestCRL2)
	assertContainsNative(t, st, gaTestCRAcc2, gaTestCRL3)
	assertContainsNative(t, st, gaTestCRL1, gaTestCREG1)
	assertContainsNative(t, st, gaTestCRL1, gaTestCREG2)
	assertContainsNative(t, st, gaTestCRL3, gaTestCREG3)
	assertContainsEdgesDeclared(t, st)

	cidrs := rowsByNativeID(t, st, TypeGlobalAcceleratorByoipCidr)
	if got := nativeIDs(cidrs); !slices.Equal(got, []string{gaByoipID(gaTestCidr2), gaByoipID(gaTestCidr1)}) {
		t.Fatalf("BYOIP rows = %v, want the two ranges that are not deprovisioned", got)
	}
	c1 := cidrs[gaByoipID(gaTestCidr1)]
	if sv(c1.Name) != gaTestCidr1 || sv(c1.Status) != "ADVERTISING" || sv(c1.Region) != "global" {
		t.Errorf("BYOIP name/status/region = %q/%q/%q, want %s/ADVERTISING/global", sv(c1.Name), sv(c1.Status), sv(c1.Region), gaTestCidr1)
	}
	if got := gaAttrs(t, c1)["Events"]; got == nil {
		t.Errorf("BYOIP Events missing from attributes %s", c1.AttributesJSON)
	}
	if got := sv(cidrs[gaByoipID(gaTestCidr2)].Status); got != "FAILED_DEPROVISION" {
		t.Errorf("BYOIP 2 status = %q, want FAILED_DEPROVISION", got)
	}
}

func TestGAByoipCidrNativeID(t *testing.T) {
	if got, want := gaByoipCidrNativeID("123456789012", "203.0.113.0/24"), "arn:aws:globalaccelerator::123456789012:byoip-cidr/203.0.113.0/24"; got != want {
		t.Errorf("gaByoipCidrNativeID = %q, want %q", got, want)
	}
}

func TestScanGlobalAccelerator_NoCustomRoutingOrByoip(t *testing.T) {
	st := newTestStore(t)
	var listenerCalls, egCalls parentCalls
	h := gaHandlers(&listenerCalls, &egCalls)
	h["ListCustomRoutingAccelerators"] = func(any) (any, error) { return &globalaccelerator.ListCustomRoutingAcceleratorsOutput{}, nil }
	h["ListByoipCidrs"] = func(any) (any, error) { return &globalaccelerator.ListByoipCidrsOutput{}, nil }
	total, err := scanGATopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanGlobalAccelerator: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (the standard accelerator)", total)
	}
	if got := listenerCalls.distinct(); len(got) != 0 {
		t.Errorf("listeners listed for %v, want none", got)
	}
	for _, typ := range []string{
		TypeGlobalAcceleratorCustomRoutingAccelerator, TypeGlobalAcceleratorCustomRoutingListener,
		TypeGlobalAcceleratorCustomRoutingEndpointGroup, TypeGlobalAcceleratorByoipCidr,
	} {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
}

// The new phases run after the older ones whatever those return, and a new
// phase's failure costs no other phase its rows.
func TestScanGlobalAccelerator_PhasesIndependent(t *testing.T) {
	t.Run("older phase error", func(t *testing.T) {
		st := newTestStore(t)
		var listenerCalls, egCalls parentCalls
		h := gaHandlers(&listenerCalls, &egCalls)
		h["ListAccelerators"] = func(any) (any, error) { return nil, errGATest }
		total, err := scanGATopLevel(t, st, h)
		if !errors.Is(err, errGATest) {
			t.Fatalf("err = %v, want the ListAccelerators error", err)
		}
		if total != 10 {
			t.Errorf("total = %d, want 10 (every new-phase row)", total)
		}
		if n := len(rowsByNativeID(t, st, TypeGlobalAcceleratorCustomRoutingEndpointGroup)); n != 3 {
			t.Errorf("endpoint group rows = %d, want 3", n)
		}
		if n := len(rowsByNativeID(t, st, TypeGlobalAcceleratorByoipCidr)); n != 2 {
			t.Errorf("BYOIP rows = %d, want 2", n)
		}
	})
	t.Run("custom routing error", func(t *testing.T) {
		st := newTestStore(t)
		var listenerCalls, egCalls parentCalls
		h := gaHandlers(&listenerCalls, &egCalls)
		h["ListCustomRoutingAccelerators"] = func(any) (any, error) { return nil, errGATest }
		total, err := scanGATopLevel(t, st, h)
		if !errors.Is(err, errGATest) {
			t.Fatalf("err = %v, want the ListCustomRoutingAccelerators error", err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3 (standard accelerator + 2 BYOIP)", total)
		}
		if n := len(rowsByNativeID(t, st, TypeGlobalAcceleratorAccelerator)); n != 1 {
			t.Errorf("standard accelerator rows = %d, want 1", n)
		}
		if n := len(rowsByNativeID(t, st, TypeGlobalAcceleratorByoipCidr)); n != 2 {
			t.Errorf("BYOIP rows = %d, want 2", n)
		}
		if got := listenerCalls.distinct(); len(got) != 0 {
			t.Errorf("listeners listed for %v, want none", got)
		}
	})
	t.Run("BYOIP error", func(t *testing.T) {
		st := newTestStore(t)
		var listenerCalls, egCalls parentCalls
		h := gaHandlers(&listenerCalls, &egCalls)
		h["ListByoipCidrs"] = func(any) (any, error) { return nil, errGATest }
		total, err := scanGATopLevel(t, st, h)
		if !errors.Is(err, errGATest) {
			t.Fatalf("err = %v, want the ListByoipCidrs error", err)
		}
		if total != 9 {
			t.Errorf("total = %d, want 9 (standard + custom routing rows)", total)
		}
		if n := len(rowsByNativeID(t, st, TypeGlobalAcceleratorCustomRoutingEndpointGroup)); n != 3 {
			t.Errorf("endpoint group rows = %d, want 3", n)
		}
	})
}

// A phase's transient error is demoted to a warning under its op instead of
// buried in another phase's hard error.
func TestScanGlobalAccelerator_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	cases := []struct {
		name, transientOp, hardOp, warnOp string
	}{
		{"transient older phase", "ListAccelerators", "ListByoipCidrs", "globalaccelerator"},
		{"transient new phase", "ListByoipCidrs", "ListAccelerators", gaByoipCidrsOp},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var listenerCalls, egCalls parentCalls
			h := gaHandlers(&listenerCalls, &egCalls)
			h[c.transientOp] = func(any) (any, error) { return nil, io.EOF }
			h[c.hardOp] = func(any) (any, error) { return nil, errGATest }
			warnings := countOpWarnings(st, c.warnOp)
			total, err := scanGATopLevel(t, st, h)
			if !errors.Is(err, errGATest) {
				t.Fatalf("err = %v, want the %s error", err, c.hardOp)
			}
			if isTransientNetworkError(err) {
				t.Errorf("err = %v reads as transient, want the hard error alone", err)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.warnOp, *warnings)
			}
			// 2 custom accelerators + 3 listeners + 3 endpoint groups
			if total != 8 {
				t.Errorf("total = %d, want 8", total)
			}
			if n := len(rowsByNativeID(t, st, TypeGlobalAcceleratorCustomRoutingEndpointGroup)); n != 3 {
				t.Errorf("endpoint group rows = %d, want 3", n)
			}
		})
	}
}

// A child op's other error fails the scan and stops the level below it.
func TestScanGlobalAccelerator_ChildErrorPropagates(t *testing.T) {
	for _, op := range []string{"ListCustomRoutingListeners", "ListCustomRoutingEndpointGroups"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var listenerCalls, egCalls parentCalls
			h := gaHandlers(&listenerCalls, &egCalls)
			h[op] = func(any) (any, error) { return nil, errGATest }
			_, err := scanGATopLevel(t, st, h)
			if !errors.Is(err, errGATest) {
				t.Fatalf("err = %v, want the %s error", err, op)
			}
			if n := len(rowsByNativeID(t, st, TypeGlobalAcceleratorCustomRoutingEndpointGroup)); n != 0 {
				t.Errorf("endpoint group rows = %d, want 0", n)
			}
			if n := len(rowsByNativeID(t, st, TypeGlobalAcceleratorCustomRoutingAccelerator)); n != 2 {
				t.Errorf("custom accelerator rows = %d, want 2", n)
			}
		})
	}
}

// A parent deleted between its listing and its child call is skipped
// silently, keeping its siblings' children.
func TestScanGlobalAccelerator_ParentGoneSkipped(t *testing.T) {
	cases := []struct {
		op, code, goneParent string
		typ                  string
		want                 []string
	}{
		{"ListCustomRoutingListeners", "AcceleratorNotFoundException", gaTestCRAcc2, TypeGlobalAcceleratorCustomRoutingListener, []string{gaTestCRL1, gaTestCRL2}},
		{"ListCustomRoutingEndpointGroups", "ListenerNotFoundException", gaTestCRL3, TypeGlobalAcceleratorCustomRoutingEndpointGroup, []string{gaTestCREG1, gaTestCREG2}},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			var listenerCalls, egCalls parentCalls
			h := gaHandlers(&listenerCalls, &egCalls)
			inner := h[c.op]
			h[c.op] = func(in any) (any, error) {
				var parent string
				switch p := in.(type) {
				case *globalaccelerator.ListCustomRoutingListenersInput:
					parent = sv(p.AcceleratorArn)
				case *globalaccelerator.ListCustomRoutingEndpointGroupsInput:
					parent = sv(p.ListenerArn)
				}
				if parent == c.goneParent {
					return nil, apiErr(c.code, "not found")
				}
				return inner(in)
			}
			warnings := countOpWarnings(st, "globalaccelerator:"+c.op)
			if _, err := scanGATopLevel(t, st, h); err != nil {
				t.Fatalf("scanGlobalAccelerator: %v", err)
			}
			if got := nativeIDs(rowsByNativeID(t, st, c.typ)); !slices.Equal(got, c.want) {
				t.Errorf("%s rows = %v, want %v", c.typ, got, c.want)
			}
			if *warnings != 0 {
				t.Errorf("warnings = %d, want 0", *warnings)
			}
		})
	}
}

// A denial warns once per op and keeps every row the op could list: the
// accelerators and BYOIP ranges from earlier pages, and the children of the
// parents not denied.
func TestScanGlobalAccelerator_AccessDenied(t *testing.T) {
	cases := []struct {
		op   string
		deny func(in any) bool
		typ  string
		want []string
	}{
		{
			"ListCustomRoutingAccelerators",
			func(in any) bool { return in.(*globalaccelerator.ListCustomRoutingAcceleratorsInput).NextToken != nil },
			TypeGlobalAcceleratorCustomRoutingListener,
			[]string{gaTestCRL1, gaTestCRL2},
		},
		{
			"ListCustomRoutingListeners",
			func(in any) bool { return true },
			TypeGlobalAcceleratorCustomRoutingAccelerator,
			[]string{gaTestCRAcc1, gaTestCRAcc2},
		},
		{
			"ListCustomRoutingEndpointGroups",
			func(in any) bool {
				return sv(in.(*globalaccelerator.ListCustomRoutingEndpointGroupsInput).ListenerArn) != gaTestCRL1
			},
			TypeGlobalAcceleratorCustomRoutingEndpointGroup,
			[]string{gaTestCREG1, gaTestCREG2},
		},
		{
			"ListByoipCidrs",
			func(in any) bool { return in.(*globalaccelerator.ListByoipCidrsInput).NextToken != nil },
			TypeGlobalAcceleratorByoipCidr,
			[]string{gaByoipID(gaTestCidr1)},
		},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			var listenerCalls, egCalls parentCalls
			h := gaHandlers(&listenerCalls, &egCalls)
			inner := h[c.op]
			h[c.op] = func(in any) (any, error) {
				if c.deny(in) {
					return nil, gaTestDenied
				}
				return inner(in)
			}
			warnings := countOpWarnings(st, "globalaccelerator:"+c.op)
			if _, err := scanGATopLevel(t, st, h); err != nil {
				t.Fatalf("scanGlobalAccelerator: %v", err)
			}
			if *warnings != 1 {
				t.Errorf("warnings = %d, want 1", *warnings)
			}
			if got := nativeIDs(rowsByNativeID(t, st, c.typ)); !slices.Equal(got, c.want) {
				t.Errorf("%s rows = %v, want %v", c.typ, got, c.want)
			}
		})
	}
}

// A parent whose child call still fails transiently is skipped with one
// summary warning, keeping its siblings' children.
func TestScanGlobalAccelerator_TransientParentSkipped(t *testing.T) {
	cases := []struct {
		op, flakyParent string
		typ             string
		want            []string
	}{
		{"ListCustomRoutingListeners", gaTestCRAcc1, TypeGlobalAcceleratorCustomRoutingListener, []string{gaTestCRL3}},
		{"ListCustomRoutingEndpointGroups", gaTestCRL1, TypeGlobalAcceleratorCustomRoutingEndpointGroup, []string{gaTestCREG3}},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			var listenerCalls, egCalls parentCalls
			h := gaHandlers(&listenerCalls, &egCalls)
			inner := h[c.op]
			h[c.op] = func(in any) (any, error) {
				var parent string
				switch p := in.(type) {
				case *globalaccelerator.ListCustomRoutingListenersInput:
					parent = sv(p.AcceleratorArn)
				case *globalaccelerator.ListCustomRoutingEndpointGroupsInput:
					parent = sv(p.ListenerArn)
				}
				if parent == c.flakyParent {
					return nil, io.EOF
				}
				return inner(in)
			}
			warnings := countOpWarnings(st, "globalaccelerator:"+c.op)
			if _, err := scanGATopLevel(t, st, h); err != nil {
				t.Fatalf("scanGlobalAccelerator: %v", err)
			}
			if *warnings != 1 {
				t.Errorf("warnings = %d, want 1", *warnings)
			}
			if got := nativeIDs(rowsByNativeID(t, st, c.typ)); !slices.Equal(got, c.want) {
				t.Errorf("%s rows = %v, want %v", c.typ, got, c.want)
			}
		})
	}
}
