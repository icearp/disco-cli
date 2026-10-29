package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/globalaccelerator"
	gatypes "github.com/aws/aws-sdk-go-v2/service/globalaccelerator/types"
	"github.com/icearp/disco-cli/store"
)

const (
	gaTestStd1   = "arn:aws:globalaccelerator::123456789012:accelerator/std-1"
	gaTestStd2   = "arn:aws:globalaccelerator::123456789012:accelerator/std-2"
	gaTestL1     = gaTestStd1 + "/listener/l1"
	gaTestL2     = gaTestStd1 + "/listener/l2"
	gaTestEG1    = gaTestL1 + "/endpoint-group/eg1"
	gaTestEG2    = gaTestL2 + "/endpoint-group/eg2"
	gaTestAtt1   = "arn:aws:globalaccelerator::123456789012:attachment/att-1"
	gaTestAtt2   = "arn:aws:globalaccelerator::123456789012:attachment/att-2"
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
	// gaTestTotal is every row the fixture stores: 2 standard accelerators,
	// 2 attachments, 2 listeners, 2 endpoint groups, 2 custom routing
	// accelerators, 3 custom routing listeners, 3 custom routing endpoint
	// groups and 2 BYOIP ranges.
	gaTestTotal = 18
	gaTestScope = testAccountID + "/us-west-2"
	// gaTestTagsOp is the tag op's label, spelled out so that a typo in the
	// scanner's constant fails the tests.
	gaTestTagsOp = "globalaccelerator:ListTagsForResource"
)

var (
	gaTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: globalaccelerator:List")
	gaTestThrottle = apiErr("Throttling", "Rate exceeded")
	gaTestInvalid  = apiErr("InvalidArgumentException", "bad argument")
	// gaTestTagged is the tags ListTagsForResource returns by ARN; every
	// other ARN it is sent has none.
	gaTestTagged = map[string]map[string]string{
		gaTestStd1:   {"team": "edge"},
		gaTestAtt1:   {"env": "prod"},
		gaTestCRAcc1: {"app": "game"},
	}
	// gaTestTagReads is every ARN the fixture's scan sends the tag op.
	gaTestTagReads = []string{gaTestStd1, gaTestStd2, gaTestAtt1, gaTestAtt2, gaTestCRAcc1, gaTestCRAcc2}
)

// gaCheckLimit fails a call that does not ask for the op's maximum page size.
func gaCheckLimit(maxResults *int32) error {
	if maxResults == nil || *maxResults != 100 {
		return fmt.Errorf("MaxResults = %v, want 100", maxResults)
	}
	return nil
}

// gaCalls records the calls of a scan: each listing's parent per call ("" for
// an account-wide listing) and the ARNs sent to the tag op. errs answers the
// tag op with an error by ARN; repeat names the op whose second page repeats
// the first page's NextToken.
type gaCalls struct {
	mu      sync.Mutex
	parents map[string][]string
	tagARNs []string
	errs    map[string]error
	repeat  string
}

func newGACalls() *gaCalls {
	return &gaCalls{parents: map[string][]string{}, errs: map[string]error{}}
}

func (c *gaCalls) record(op, parent string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.parents[op] = append(c.parents[op], parent)
}

func (c *gaCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.parents[op])
}

// listedFor is the distinct parents op was called for, sorted.
func (c *gaCalls) listedFor(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	got := slices.Clone(c.parents[op])
	slices.Sort(got)
	return slices.Compact(got)
}

// sentTags is the ARNs sent to the tag op, sorted, one per call.
func (c *gaCalls) sentTags() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	got := slices.Clone(c.tagARNs)
	slices.Sort(got)
	return got
}

// lastToken is the NextToken of op's last page: none, or the first page's
// token again when op is the one c repeats.
func (c *gaCalls) lastToken(op string) *string {
	if c.repeat == op {
		return ptrStr("p2")
	}
	return nil
}

func (c *gaCalls) listTags(in any) (any, error) {
	arn := sv(in.(*globalaccelerator.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err := c.errs[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var tags []gatypes.Tag
	for k, v := range gaTestTagged[arn] {
		tags = append(tags, gatypes.Tag{Key: ptrStr(k), Value: ptrStr(v)})
	}
	return &globalaccelerator.ListTagsForResourceOutput{Tags: tags}, nil
}

// gaHandlers answers every op the scanner calls. Each listing below holds an
// element without its id (ARN, or Cidr for BYOIP) that is not stored:
//   - accelerators: std-1 (tagged), then std-2 and an ARN-less one;
//   - attachments: att-1 (tagged), then att-2 and an ARN-less one;
//   - listeners: std-1 has l1, then l2 and an ARN-less one; std-2 has none;
//   - endpoint groups: l1 has eg1; l2 has an ARN-less one, then eg2;
//   - custom routing accelerators: cr-1 (tagged), then cr-2 and an ARN-less
//     one;
//   - custom routing listeners: cr-1 has l1, then l2 and an ARN-less one;
//     cr-2 has l3;
//   - custom routing endpoint groups: l1 has eg1, then eg2; l2 has none; l3
//     has eg3 and an ARN-less one;
//   - BYOIP ranges: one advertised, then one whose deprovisioning failed, a
//     deprovisioned one and one without a Cidr.
func gaHandlers(c *gaCalls) map[string]func(in any) (any, error) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListAccelerators": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListAcceleratorsInput)
			c.record("ListAccelerators", "")
			if err := gaCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			if p.NextToken == nil {
				return &globalaccelerator.ListAcceleratorsOutput{NextToken: ptrStr("p2"), Accelerators: []gatypes.Accelerator{{
					AcceleratorArn: ptrStr(gaTestStd1), Name: ptrStr("web"), Status: gatypes.AcceleratorStatusDeployed,
					CreatedTime: &created, DnsName: ptrStr("std1.awsglobalaccelerator.com"),
				}}}, nil
			}
			return &globalaccelerator.ListAcceleratorsOutput{NextToken: c.lastToken("ListAccelerators"), Accelerators: []gatypes.Accelerator{
				{AcceleratorArn: ptrStr(gaTestStd2)}, {Name: ptrStr("no-arn")},
			}}, nil
		},
		"ListCrossAccountAttachments": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListCrossAccountAttachmentsInput)
			c.record("ListCrossAccountAttachments", "")
			if err := gaCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			if p.NextToken == nil {
				return &globalaccelerator.ListCrossAccountAttachmentsOutput{NextToken: ptrStr("p2"), CrossAccountAttachments: []gatypes.Attachment{{
					AttachmentArn: ptrStr(gaTestAtt1), Name: ptrStr("partners"), CreatedTime: &created, Principals: []string{"111122223333"},
				}}}, nil
			}
			return &globalaccelerator.ListCrossAccountAttachmentsOutput{NextToken: c.lastToken("ListCrossAccountAttachments"), CrossAccountAttachments: []gatypes.Attachment{
				{AttachmentArn: ptrStr(gaTestAtt2)}, {Name: ptrStr("no-arn-attachment")},
			}}, nil
		},
		"ListListeners": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListListenersInput)
			c.record("ListListeners", sv(p.AcceleratorArn))
			if err := gaCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case sv(p.AcceleratorArn) != gaTestStd1:
				return &globalaccelerator.ListListenersOutput{}, nil
			case p.NextToken == nil:
				return &globalaccelerator.ListListenersOutput{NextToken: ptrStr("p2"), Listeners: []gatypes.Listener{{
					ListenerArn: ptrStr(gaTestL1), Protocol: gatypes.ProtocolTcp,
				}}}, nil
			default:
				return &globalaccelerator.ListListenersOutput{NextToken: c.lastToken("ListListeners"), Listeners: []gatypes.Listener{
					{ListenerArn: ptrStr(gaTestL2)}, {},
				}}, nil
			}
		},
		"ListEndpointGroups": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListEndpointGroupsInput)
			c.record("ListEndpointGroups", sv(p.ListenerArn))
			if err := gaCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case sv(p.ListenerArn) == gaTestL1:
				return &globalaccelerator.ListEndpointGroupsOutput{EndpointGroups: []gatypes.EndpointGroup{{
					EndpointGroupArn: ptrStr(gaTestEG1), EndpointGroupRegion: ptrStr("us-east-1"),
				}}}, nil
			case p.NextToken == nil:
				return &globalaccelerator.ListEndpointGroupsOutput{NextToken: ptrStr("p2"), EndpointGroups: []gatypes.EndpointGroup{
					{EndpointGroupRegion: ptrStr("ap-south-1")},
				}}, nil
			default:
				return &globalaccelerator.ListEndpointGroupsOutput{NextToken: c.lastToken("ListEndpointGroups"), EndpointGroups: []gatypes.EndpointGroup{
					{EndpointGroupArn: ptrStr(gaTestEG2)},
				}}, nil
			}
		},
		"ListCustomRoutingAccelerators": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListCustomRoutingAcceleratorsInput)
			c.record("ListCustomRoutingAccelerators", "")
			if err := gaCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			if p.NextToken == nil {
				return &globalaccelerator.ListCustomRoutingAcceleratorsOutput{NextToken: ptrStr("p2"), Accelerators: []gatypes.CustomRoutingAccelerator{{
					AcceleratorArn: ptrStr(gaTestCRAcc1), Name: ptrStr("game-servers"),
					Status: gatypes.CustomRoutingAcceleratorStatusDeployed, CreatedTime: &created, DnsName: ptrStr("a1.awsglobalaccelerator.com"),
				}}}, nil
			}
			return &globalaccelerator.ListCustomRoutingAcceleratorsOutput{NextToken: c.lastToken("ListCustomRoutingAccelerators"), Accelerators: []gatypes.CustomRoutingAccelerator{
				{AcceleratorArn: ptrStr(gaTestCRAcc2), Status: gatypes.CustomRoutingAcceleratorStatusInProgress},
				{Name: ptrStr("no-arn")},
			}}, nil
		},
		"ListCustomRoutingListeners": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListCustomRoutingListenersInput)
			c.record("ListCustomRoutingListeners", sv(p.AcceleratorArn))
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
				return &globalaccelerator.ListCustomRoutingListenersOutput{NextToken: c.lastToken("ListCustomRoutingListeners"), Listeners: []gatypes.CustomRoutingListener{
					{ListenerArn: ptrStr(gaTestCRL2)}, {},
				}}, nil
			}
		},
		"ListCustomRoutingEndpointGroups": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListCustomRoutingEndpointGroupsInput)
			c.record("ListCustomRoutingEndpointGroups", sv(p.ListenerArn))
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
				return &globalaccelerator.ListCustomRoutingEndpointGroupsOutput{NextToken: c.lastToken("ListCustomRoutingEndpointGroups"), EndpointGroups: []gatypes.CustomRoutingEndpointGroup{{
					EndpointGroupArn: ptrStr(gaTestCREG2), EndpointGroupRegion: ptrStr("eu-west-1"),
				}}}, nil
			}
		},
		"ListByoipCidrs": func(in any) (any, error) {
			p := in.(*globalaccelerator.ListByoipCidrsInput)
			c.record("ListByoipCidrs", "")
			if err := gaCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			if p.NextToken == nil {
				return &globalaccelerator.ListByoipCidrsOutput{NextToken: ptrStr("p2"), ByoipCidrs: []gatypes.ByoipCidr{{
					Cidr: ptrStr(gaTestCidr1), State: gatypes.ByoipCidrStateAdvertising,
					Events: []gatypes.ByoipCidrEvent{{Message: ptrStr("advertised")}},
				}}}, nil
			}
			return &globalaccelerator.ListByoipCidrsOutput{NextToken: c.lastToken("ListByoipCidrs"), ByoipCidrs: []gatypes.ByoipCidr{
				{Cidr: ptrStr(gaTestCidr2), State: gatypes.ByoipCidrStateFailedDeprovision},
				{Cidr: ptrStr("192.0.2.0/24"), State: gatypes.ByoipCidrStateDeprovisioned},
				{State: gatypes.ByoipCidrStateReady},
			}}, nil
		},
	}
}

// gaScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func gaScan(st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, gaTestDenied)
	return scanGlobalAccelerator(context.Background(), acct, "", st, testScanID)
}

// gaFailTokenPages is next with every call that sends a NextToken (every page
// after a listing's first) failing with err.
func gaFailTokenPages(err error, next func(any) (any, error)) func(any) (any, error) {
	return func(in any) (any, error) {
		if v := reflect.ValueOf(in).Elem().FieldByName("NextToken"); !v.IsNil() {
			return nil, err
		}
		return next(in)
	}
}

// gaFailParent is next with every call for parent (its AcceleratorArn or
// ListenerArn) failing with err.
func gaFailParent(parent string, err error, next func(any) (any, error)) func(any) (any, error) {
	return func(in any) (any, error) {
		v := reflect.ValueOf(in).Elem()
		for _, field := range []string{"AcceleratorArn", "ListenerArn"} {
			if f := v.FieldByName(field); f.IsValid() && sv(f.Interface().(*string)) == parent {
				return nil, err
			}
		}
		return next(in)
	}
}

func gaAttr(t *testing.T, r store.Resource, key string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	return m[key]
}

func gaByoipID(cidr string) string { return gaByoipCidrNativeID("aws", testAccountID, cidr) }

// Every listing stores both its pages, each row under its ARN (a BYOIP
// range under its built id) with its name, status, creation time and the
// listed element as attributes, and every listing is sent MaxResults 100.
// The accelerators of both kinds and the attachments carry the tags
// ListTagsForResource returns for their ARN, nil for a resource without
// tags, and exactly their ARNs are sent to it, once each; the other types
// are stored untagged. Each child listing is called for every parent
// stored. An element without its id is not stored and warned about once
// under its listing, naming its name, or a child by its region (endpoint
// groups) within its parent. No error.
func TestScanGlobalAccelerator_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGACalls()
	total, inserted, err := gaScan(st, gaHandlers(calls))
	if err != nil {
		t.Fatalf("scanGlobalAccelerator: %v", err)
	}
	if total != gaTestTotal || inserted != gaTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, gaTestTotal, gaTestTotal)
	}
	if got, want := calls.sentTags(), slices.Sorted(slices.Values(gaTestTagReads)); !slices.Equal(got, want) {
		t.Errorf("ARNs sent to ListTagsForResource = %v; want %v", got, want)
	}

	const created = "2026-01-02T03:04:05Z"
	// name nil is a row stored with no Name; an endpoint group listed
	// without a region is stored under an empty Name, a custom routing one
	// under none.
	rows := []struct {
		typ, id         string
		name            *string
		status, created string
		attr            string
		attrWant        any
	}{
		{TypeGlobalAcceleratorAccelerator, gaTestStd1, ptrStr("web"), "DEPLOYED", created, "DnsName", "std1.awsglobalaccelerator.com"},
		{TypeGlobalAcceleratorAccelerator, gaTestStd2, nil, "", "", "AcceleratorArn", gaTestStd2},
		{TypeGlobalAcceleratorCrossAccountAttachment, gaTestAtt1, ptrStr("partners"), "", created, "Principals", []any{"111122223333"}},
		{TypeGlobalAcceleratorCrossAccountAttachment, gaTestAtt2, nil, "", "", "AttachmentArn", gaTestAtt2},
		{TypeGlobalAcceleratorListener, gaTestL1, ptrStr(gaTestL1), "", "", "Protocol", "TCP"},
		{TypeGlobalAcceleratorListener, gaTestL2, ptrStr(gaTestL2), "", "", "ListenerArn", gaTestL2},
		{TypeGlobalAcceleratorEndpointGroup, gaTestEG1, ptrStr("us-east-1"), "", "", "EndpointGroupRegion", "us-east-1"},
		{TypeGlobalAcceleratorEndpointGroup, gaTestEG2, ptrStr(""), "", "", "EndpointGroupArn", gaTestEG2},
		{TypeGlobalAcceleratorCustomRoutingAccelerator, gaTestCRAcc1, ptrStr("game-servers"), "DEPLOYED", created, "DnsName", "a1.awsglobalaccelerator.com"},
		{TypeGlobalAcceleratorCustomRoutingAccelerator, gaTestCRAcc2, nil, "IN_PROGRESS", "", "AcceleratorArn", gaTestCRAcc2},
		{TypeGlobalAcceleratorCustomRoutingListener, gaTestCRL1, ptrStr(gaTestCRL1), "", "", "PortRanges", []any{map[string]any{"FromPort": float64(5000), "ToPort": float64(10000)}}},
		{TypeGlobalAcceleratorCustomRoutingListener, gaTestCRL2, ptrStr(gaTestCRL2), "", "", "ListenerArn", gaTestCRL2},
		{TypeGlobalAcceleratorCustomRoutingListener, gaTestCRL3, ptrStr(gaTestCRL3), "", "", "ListenerArn", gaTestCRL3},
		{TypeGlobalAcceleratorCustomRoutingEndpointGroup, gaTestCREG1, ptrStr("us-east-1"), "", "", "EndpointDescriptions", []any{map[string]any{"EndpointId": "subnet-0abc"}}},
		{TypeGlobalAcceleratorCustomRoutingEndpointGroup, gaTestCREG2, ptrStr("eu-west-1"), "", "", "EndpointGroupRegion", "eu-west-1"},
		{TypeGlobalAcceleratorCustomRoutingEndpointGroup, gaTestCREG3, nil, "", "", "EndpointGroupArn", gaTestCREG3},
		{TypeGlobalAcceleratorByoipCidr, gaByoipID(gaTestCidr1), ptrStr(gaTestCidr1), "ADVERTISING", "", "Events", []any{map[string]any{"Message": "advertised", "Timestamp": nil}}},
		{TypeGlobalAcceleratorByoipCidr, gaByoipID(gaTestCidr2), ptrStr(gaTestCidr2), "FAILED_DEPROVISION", "", "Cidr", gaTestCidr2},
	}
	showName := func(p *string) string {
		if p == nil {
			return "none"
		}
		return fmt.Sprintf("%q", *p)
	}
	stored := 0
	for _, w := range rows {
		got := rowsByNativeID(t, st, w.typ)
		r, ok := got[w.id]
		if !ok {
			t.Errorf("%s %s not stored; stored %v", w.typ, w.id, nativeIDs(got))
			continue
		}
		stored++
		if (r.Name == nil) != (w.name == nil) || sv(r.Name) != sv(w.name) {
			t.Errorf("%s Name = %v; want %v", w.id, showName(r.Name), showName(w.name))
		}
		if sv(r.Status) != w.status || sv(r.CreatedAt) != w.created || sv(r.Region) != "global" {
			t.Errorf("%s status/created/region = %q/%q/%q; want %q/%q/global",
				w.id, sv(r.Status), sv(r.CreatedAt), sv(r.Region), w.status, w.created)
		}
		if w.status == "" && r.Status != nil {
			t.Errorf("%s Status = %q; want none", w.id, *r.Status)
		}
		if got := gaAttr(t, r, w.attr); !reflect.DeepEqual(got, w.attrWant) {
			t.Errorf("%s attribute %s = %v; want %v", w.id, w.attr, got, w.attrWant)
		}
		if want, tagged := gaTestTagged[w.id]; tagged {
			assertTagsMap(t, r.TagsJSON, want)
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", w.id, *r.TagsJSON)
		}
	}
	if stored != gaTestTotal {
		t.Errorf("rows checked = %d; want %d", stored, gaTestTotal)
	}

	for op, want := range map[string][]string{
		"ListListeners":                   {gaTestStd1, gaTestStd2},
		"ListEndpointGroups":              {gaTestL1, gaTestL2},
		"ListCustomRoutingListeners":      {gaTestCRAcc1, gaTestCRAcc2},
		"ListCustomRoutingEndpointGroups": {gaTestCRL1, gaTestCRL2, gaTestCRL3},
	} {
		if got := calls.listedFor(op); !slices.Equal(got, want) {
			t.Errorf("%s called for %v; want %v", op, got, want)
		}
	}

	assertContainsNative(t, st, gaTestCRAcc1, gaTestCRL1)
	assertContainsNative(t, st, gaTestCRAcc1, gaTestCRL2)
	assertContainsNative(t, st, gaTestCRAcc2, gaTestCRL3)
	assertContainsNative(t, st, gaTestCRL1, gaTestCREG1)
	assertContainsNative(t, st, gaTestCRL1, gaTestCREG2)
	assertContainsNative(t, st, gaTestCRL3, gaTestCREG3)
	assertContainsEdgesDeclared(t, st)
	// Standard listeners and endpoint groups are linked by the resolver's
	// attached-to edges, not contained.
	if rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, gaTestStd1), store.RelContains); err != nil || len(rels) != 0 {
		t.Errorf("standard accelerator contains %v (err %v); want nothing", rels, err)
	}

	wantWarn := map[string]string{
		"globalaccelerator:ListAccelerators":                "no-arn",
		"globalaccelerator:ListCrossAccountAttachments":     "no-arn-attachment",
		"globalaccelerator:ListListeners":                   "a listener of " + gaTestStd1,
		"globalaccelerator:ListEndpointGroups":              "ap-south-1 of " + gaTestL2,
		"globalaccelerator:ListCustomRoutingAccelerators":   "no-arn",
		"globalaccelerator:ListCustomRoutingListeners":      "a listener of " + gaTestCRAcc1,
		"globalaccelerator:ListCustomRoutingEndpointGroups": "eu-west-1 of " + gaTestCRL3,
		"globalaccelerator:ListByoipCidrs":                  noIdentifier,
	}
	w := warns()
	if len(w) != len(wantWarn) {
		t.Errorf("warnings = %v; want one per listing %v", w, wantWarn)
	}
	for _, x := range w {
		key, ok := wantWarn[x.Service]
		if !ok || x.Scope != gaTestScope || !strings.Contains(x.Message, key) {
			t.Errorf("warning %+v; want one under a listing in %s naming %q", x, gaTestScope, key)
		}
		delete(wantWarn, x.Service)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanGlobalAccelerator_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := gaScan(st, gaHandlers(newGACalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := gaScan(st, gaHandlers(newGACalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != gaTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, gaTestTotal)
	}
}

func TestGAByoipCidrNativeID(t *testing.T) {
	if got, want := gaByoipCidrNativeID("aws", "123456789012", "203.0.113.0/24"), "arn:aws:globalaccelerator::123456789012:byoip-cidr/203.0.113.0/24"; got != want {
		t.Errorf("gaByoipCidrNativeID = %q, want %q", got, want)
	}
}

// A failed tag read: the not-found code ListTagsForResource models for the
// row's type drops the row silently, and a dropped accelerator is no parent
// of the listener phase; the other type's not-found code keeps the row and is
// an error; a denial keeps the row untagged and warns once under the tag op;
// a throttle keeps it and is transient; any other error keeps it and is an
// error.
func TestScanGlobalAccelerator_TagReadFailures(t *testing.T) {
	kinds := []struct {
		typ, arn, notFound, otherNotFound string
		// childOp is the listing whose parents the row's kind are; droppedTotal
		// is the total once the row and its children are gone.
		childOp      string
		droppedTotal int
	}{
		{TypeGlobalAcceleratorAccelerator, gaTestStd2, "AcceleratorNotFoundException", "AttachmentNotFoundException", "ListListeners", gaTestTotal - 1},
		{TypeGlobalAcceleratorCrossAccountAttachment, gaTestAtt2, "AttachmentNotFoundException", "AcceleratorNotFoundException", "", gaTestTotal - 1},
		{TypeGlobalAcceleratorCustomRoutingAccelerator, gaTestCRAcc2, "AcceleratorNotFoundException", "AttachmentNotFoundException", "ListCustomRoutingListeners", gaTestTotal - 3},
	}
	for _, k := range kinds {
		cases := []struct {
			name      string
			err       error
			outcome   serviceOutcome // -1: no error
			code      string
			wantTotal int
			wantKept  bool
			wantWarns int
		}{
			{"not found", apiErr(k.notFound, "gone"), -1, "", k.droppedTotal, false, 0},
			{"other type's not found", apiErr(k.otherNotFound, "gone"), outcomeError, k.otherNotFound, gaTestTotal, true, 0},
			{"denied", gaTestDenied, -1, "", gaTestTotal, true, 1},
			{"transient", gaTestThrottle, outcomeTransient, "Throttling", gaTestTotal, true, 0},
			{"other error", gaTestInvalid, outcomeError, "InvalidArgumentException", gaTestTotal, true, 0},
		}
		for _, c := range cases {
			t.Run(k.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := bacWarnings(st, gaTestTagsOp)
				calls := newGACalls()
				calls.errs[k.arn] = c.err
				total, _, err := gaScan(st, gaHandlers(calls))
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
				r, ok := rowsByNativeID(t, st, k.typ)[k.arn]
				if ok != c.wantKept {
					t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
				}
				if ok && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				if k.childOp != "" && slices.Contains(calls.listedFor(k.childOp), k.arn) != c.wantKept {
					t.Errorf("%s called for %s: %v; want %v", k.childOp, k.arn, !c.wantKept, c.wantKept)
				}
				if len(*warns) != c.wantWarns {
					t.Fatalf("%s warnings = %v; want %d", gaTestTagsOp, *warns, c.wantWarns)
				}
				for _, x := range *warns {
					if x.Scope != gaTestScope || !strings.Contains(x.Message, k.arn) {
						t.Errorf("warning %+v; want one in %s naming %s", x, gaTestScope, k.arn)
					}
				}
			})
		}
	}
}

// Denied tag reads in three listings send one warning for the whole scan,
// under the tag op, naming the first denied row; the rows are kept untagged.
func TestScanGlobalAccelerator_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := bacWarnings(st, gaTestTagsOp)
	calls := newGACalls()
	denied := []struct{ typ, arn string }{
		{TypeGlobalAcceleratorAccelerator, gaTestStd1},
		{TypeGlobalAcceleratorCrossAccountAttachment, gaTestAtt1},
		{TypeGlobalAcceleratorCustomRoutingAccelerator, gaTestCRAcc1},
	}
	for _, d := range denied {
		calls.errs[d.arn] = gaTestDenied
	}
	total, _, err := gaScan(st, gaHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != gaTestTotal {
		t.Errorf("total = %d; want %d", total, gaTestTotal)
	}
	if len(*warns) != 1 || (*warns)[0].Scope != gaTestScope || !strings.Contains((*warns)[0].Message, gaTestStd1) {
		t.Fatalf("%s warnings = %v; want 1 in %s naming %s", gaTestTagsOp, *warns, gaTestScope, gaTestStd1)
	}
	for _, d := range denied {
		if r, ok := rowsByNativeID(t, st, d.typ)[d.arn]; !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v; want stored untagged", d.arn, ok)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanGlobalAccelerator_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, gaTestTagsOp)
	calls := newGACalls()
	calls.errs[gaTestCRAcc1] = gaTestThrottle
	calls.errs[gaTestAtt2] = gaTestInvalid
	_, _, err := gaScan(st, gaHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidArgumentException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", gaTestTagsOp, *warns)
	}
}

// A failed write stops the scan: no later phase runs, and no warning is sent
// about rows the write lost; the phases before it keep their rows. Each
// listing tagged per row writes its first page before it lists the second,
// so the failed write also ends that listing after one call.
func TestScanGlobalAccelerator_FailedWriteStopsScan(t *testing.T) {
	cases := []struct {
		typ, op, firstRow string
		later             []string
		wantTotal         int // the rows of the phases before it
	}{
		{TypeGlobalAcceleratorAccelerator, "ListAccelerators", gaTestStd1, []string{
			"ListCrossAccountAttachments", "ListListeners", "ListEndpointGroups", "ListCustomRoutingAccelerators",
			"ListCustomRoutingListeners", "ListCustomRoutingEndpointGroups", "ListByoipCidrs",
		}, 0},
		{TypeGlobalAcceleratorCrossAccountAttachment, "ListCrossAccountAttachments", gaTestAtt1, []string{
			"ListListeners", "ListEndpointGroups", "ListCustomRoutingAccelerators",
			"ListCustomRoutingListeners", "ListCustomRoutingEndpointGroups", "ListByoipCidrs",
		}, 2},
		// 2 accelerators, 2 attachments, 2 listeners and 2 endpoint groups.
		{TypeGlobalAcceleratorCustomRoutingAccelerator, "ListCustomRoutingAccelerators", gaTestCRAcc1, []string{
			"ListCustomRoutingListeners", "ListCustomRoutingEndpointGroups", "ListByoipCidrs",
		}, 8},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			tagWarns := countOpWarnings(st, gaTestTagsOp)
			opWarns := countOpWarnings(st, "globalaccelerator:"+c.op)
			refuseStoreWrites(t, st, c.typ)
			calls := newGACalls()
			calls.errs[c.firstRow] = gaTestDenied
			total, _, err := gaScan(st, gaHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *tagWarns != 0 || *opWarns != 0 {
				t.Errorf("tag, %s warnings = %d, %d; want 0, 0", c.op, *tagWarns, *opWarns)
			}
			if n := calls.count(c.op); n != 1 {
				t.Errorf("%s called %d times; want 1, none after the failed write", c.op, n)
			}
			for _, op := range c.later {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times after the failed write; want 0", op, n)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// A listing's failed later page keeps the pages read before it, and its
// sibling parents' rows; its error does not stop the later phases.
func TestScanGlobalAccelerator_LaterPageFailureKeepsPages(t *testing.T) {
	cases := []struct {
		op        string
		wantTotal int // the rows of the failed page, and of its children, are lost
	}{
		{"ListAccelerators", gaTestTotal - 1},
		{"ListCrossAccountAttachments", gaTestTotal - 1},
		{"ListListeners", gaTestTotal - 2},
		{"ListEndpointGroups", gaTestTotal - 1},
		{"ListCustomRoutingAccelerators", gaTestTotal - 3},
		{"ListCustomRoutingListeners", gaTestTotal - 1},
		{"ListCustomRoutingEndpointGroups", gaTestTotal - 1},
		{"ListByoipCidrs", gaTestTotal - 1},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newGACalls()
			h := gaHandlers(calls)
			h[c.op] = gaFailTokenPages(gaTestInvalid, h[c.op])
			total, _, err := gaScan(st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidArgumentException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if n := calls.count("ListByoipCidrs"); n == 0 {
				t.Error("ListByoipCidrs (the last phase) not called")
			}
		})
	}
}

// A listing whose NextToken repeats ends as an error, keeping both pages.
func TestScanGlobalAccelerator_RepeatedTokenKeepsPages(t *testing.T) {
	for _, op := range []string{
		"ListAccelerators", "ListCrossAccountAttachments", "ListListeners", "ListEndpointGroups",
		"ListCustomRoutingAccelerators", "ListCustomRoutingListeners", "ListCustomRoutingEndpointGroups", "ListByoipCidrs",
	} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newGACalls()
			calls.repeat = op
			total, _, err := gaScan(st, gaHandlers(calls))
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != gaTestTotal {
				t.Errorf("total = %d; want %d", total, gaTestTotal)
			}
		})
	}
}

// A failure of one parent's child listing costs its siblings none of their
// rows: a parent the op reports gone is skipped silently; a denied one warns
// once under the op; any other error, a transient one included, is returned
// after the siblings' rows are stored, since the parent's children are lost.
// Each failed parent below lists the op's element without an ARN, so the
// op's only warning can be the denial.
func TestScanGlobalAccelerator_ChildListingFailures(t *testing.T) {
	parents := []struct {
		op, parent, gone string
		wantTotal        int
	}{
		{"ListListeners", gaTestStd1, "AcceleratorNotFoundException", gaTestTotal - 4},
		{"ListEndpointGroups", gaTestL2, "ListenerNotFoundException", gaTestTotal - 1},
		{"ListCustomRoutingListeners", gaTestCRAcc1, "AcceleratorNotFoundException", gaTestTotal - 4},
		{"ListCustomRoutingEndpointGroups", gaTestCRL3, "ListenerNotFoundException", gaTestTotal - 1},
	}
	for _, p := range parents {
		cases := []struct {
			name      string
			err       error
			outcome   serviceOutcome // -1: no error
			wantWarns int
		}{
			{"gone", apiErr(p.gone, "gone"), -1, 0},
			{"denied", gaTestDenied, -1, 1},
			{"other error", gaTestInvalid, outcomeError, 0},
			{"transient", io.EOF, outcomeTransient, 0},
		}
		for _, c := range cases {
			t.Run(p.op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "globalaccelerator:"+p.op)
				h := gaHandlers(newGACalls())
				h[p.op] = gaFailParent(p.parent, c.err, h[p.op])
				total, _, err := gaScan(st, h)
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
				}
				if total != p.wantTotal {
					t.Errorf("total = %d; want %d", total, p.wantTotal)
				}
				if *warns != c.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, c.wantWarns)
				}
			})
		}
	}
}

// A listing denied after its first page keeps that page and its sibling
// parents' rows, and warns once.
func TestScanGlobalAccelerator_LaterPageDeniedKeepsPages(t *testing.T) {
	cases := []struct {
		op        string
		wantTotal int // the rows of the denied page, and of its children, are lost
		// wantWarns is the denial's warning, plus the warning about the
		// listing's element without an ARN when a page still read holds it.
		wantWarns int
	}{
		{"ListAccelerators", gaTestTotal - 1, 1},
		{"ListCrossAccountAttachments", gaTestTotal - 1, 1},
		{"ListListeners", gaTestTotal - 2, 1},
		{"ListEndpointGroups", gaTestTotal - 1, 2},
		{"ListCustomRoutingAccelerators", gaTestTotal - 3, 1},
		{"ListCustomRoutingListeners", gaTestTotal - 1, 1},
		{"ListCustomRoutingEndpointGroups", gaTestTotal - 1, 2},
		{"ListByoipCidrs", gaTestTotal - 1, 1},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "globalaccelerator:"+c.op)
			h := gaHandlers(newGACalls())
			h[c.op] = gaFailTokenPages(gaTestDenied, h[c.op])
			total, _, err := gaScan(st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if *warns != c.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, c.wantWarns)
			}
		})
	}
}

// A listing that fails outright does not stop the later phases; its child
// phase has no parents and makes no call.
func TestScanGlobalAccelerator_FailedListingRunsLaterPhases(t *testing.T) {
	st := newTestStore(t)
	calls := newGACalls()
	h := gaHandlers(calls)
	h["ListAccelerators"] = func(any) (any, error) { return nil, gaTestInvalid }
	total, _, err := gaScan(st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidArgumentException")
	// The 2 accelerators, 2 listeners and 2 endpoint groups are lost.
	if total != gaTestTotal-6 {
		t.Errorf("total = %d; want %d", total, gaTestTotal-6)
	}
	if n := calls.count("ListListeners"); n != 0 {
		t.Errorf("ListListeners called %d times; want 0", n)
	}
	if n := len(rowsByNativeID(t, st, TypeGlobalAcceleratorByoipCidr)); n != 2 {
		t.Errorf("BYOIP rows (the last phase) = %d; want 2", n)
	}
}

// A phase's transient error beside another phase's hard error is demoted to
// one warning under its own op, so the hard error decides the outcome. The
// warnings are compared with a scan whose phase fails with a hard error
// instead, so the demotion must add exactly one warning, under the op that
// failed, and none under any other.
func TestScanGlobalAccelerator_TransientPhaseDemotedBesideHardError(t *testing.T) {
	scan := func(t *testing.T, op, hardOp string, opErr error) map[string]int {
		t.Helper()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		h := gaHandlers(newGACalls())
		h[op] = func(any) (any, error) { return nil, opErr }
		h[hardOp] = func(any) (any, error) { return nil, gaTestInvalid }
		_, _, err := gaScan(st, h)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "InvalidArgumentException")
		got := map[string]int{}
		for _, w := range warns() {
			got[w.Service]++
		}
		return got
	}
	for _, op := range []string{
		"ListAccelerators", "ListCrossAccountAttachments", "ListListeners", "ListEndpointGroups",
		"ListCustomRoutingAccelerators", "ListCustomRoutingListeners", "ListCustomRoutingEndpointGroups", "ListByoipCidrs",
	} {
		t.Run(op, func(t *testing.T) {
			hardOp := "ListCrossAccountAttachments"
			if op == hardOp {
				hardOp = "ListByoipCidrs"
			}
			base := scan(t, op, hardOp, gaTestInvalid)
			got := scan(t, op, hardOp, io.EOF)
			label := "globalaccelerator:" + op
			if got[label] != base[label]+1 {
				t.Errorf("%s warnings = %d; want %d", label, got[label], base[label]+1)
			}
			for svc, n := range got {
				if svc != label && n != base[svc] {
					t.Errorf("%s warnings = %d; want %d", svc, n, base[svc])
				}
			}
		})
	}
}

// An account with nothing in any account-wide listing stores nothing, reads
// no tags, makes no child listing and warns about nothing.
func TestScanGlobalAccelerator_EmptyAccount(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGACalls()
	h := gaHandlers(calls)
	h["ListAccelerators"] = func(any) (any, error) { return &globalaccelerator.ListAcceleratorsOutput{}, nil }
	h["ListCrossAccountAttachments"] = func(any) (any, error) { return &globalaccelerator.ListCrossAccountAttachmentsOutput{}, nil }
	h["ListCustomRoutingAccelerators"] = func(any) (any, error) { return &globalaccelerator.ListCustomRoutingAcceleratorsOutput{}, nil }
	h["ListByoipCidrs"] = func(any) (any, error) { return &globalaccelerator.ListByoipCidrsOutput{}, nil }
	total, inserted, err := gaScan(st, h)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if got := calls.sentTags(); len(got) != 0 {
		t.Errorf("ARNs sent to ListTagsForResource = %v; want none", got)
	}
	for _, op := range []string{"ListListeners", "ListEndpointGroups", "ListCustomRoutingListeners", "ListCustomRoutingEndpointGroups"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// An NXDOMAIN with nothing stored is a region without the service; once rows
// were stored, an NXDOMAIN from a later listing or a tag read is a failure
// of this scan, and the stored rows keep their counts.
func TestScanGlobalAccelerator_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "globalaccelerator.us-west-2.amazonaws.com", IsNotFound: true}
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanGlobalAccelerator(context.Background(), acct, "", st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
	t.Run("later listing", func(t *testing.T) {
		st := newTestStore(t)
		h := gaHandlers(newGACalls())
		h["ListByoipCidrs"] = func(any) (any, error) { return nil, nx }
		total, _, err := gaScan(st, h)
		assertGlueOutcome(t, err, outcomeError)
		if total != gaTestTotal-2 {
			t.Errorf("total = %d; want %d", total, gaTestTotal-2)
		}
	})
	t.Run("tag read", func(t *testing.T) {
		st := newTestStore(t)
		calls := newGACalls()
		calls.errs[gaTestStd1] = nx
		total, _, err := gaScan(st, gaHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != gaTestTotal {
			t.Errorf("total = %d; want %d", total, gaTestTotal)
		}
	})
}
