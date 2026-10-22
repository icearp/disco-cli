package aws

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/medialive"
	medialivetypes "github.com/aws/aws-sdk-go-v2/service/medialive/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/store"
)

const mlTestRegion = "us-east-1"

var mlTestTags = map[string]string{"env": "prod"}

// mlParentInputs are the request fields that name the parent a per-parent
// listing reads; mlTwoPages checks every other pointer field is unset.
var mlParentInputs = map[string]bool{"ClusterId": true, "MultiplexId": true}

// mlTwoPages makes serve the first of two pages: a call without NextToken
// gets serve's output with NextToken "t1", a call with "t1" gets an empty
// last page. A token not forwarded therefore repeats page one and ends the
// listing with errRepeatedNextToken. Every call must leave MaxResults and the
// filters (Scope, GroupIdentifier, SignalMapIdentifier, the reservation
// filters, ...) unset: MaxResults has no documented cap, and a filter would
// narrow the listing.
func mlTwoPages(t *testing.T, op string, serve func(in any) (any, error)) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := reflect.ValueOf(in).Elem()
		for i := range req.NumField() {
			name := req.Type().Field(i).Name
			f := req.Field(i)
			if name == "NextToken" || mlParentInputs[name] || f.Kind() != reflect.Pointer {
				continue
			}
			if !f.IsNil() {
				t.Errorf("%s %s = %v, want unset", op, name, f.Elem().Interface())
			}
		}
		switch token := req.FieldByName("NextToken").Interface().(*string); sv(token) {
		case "":
			out, err := serve(in)
			if err == nil {
				reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(ptrStr("t1")))
			}
			return out, err
		case "t1":
			out, err := serve(in)
			if err != nil {
				return nil, err
			}
			return reflect.New(reflect.TypeOf(out).Elem()).Interface(), nil
		default:
			t.Errorf("%s NextToken = %q, want t1", op, sv(token))
			return nil, errors.New("bad token")
		}
	}
}

// mlAllHandlers serves every MediaLive listing as two pages (mlTwoPages).
// Each account-wide listing of a taggable element serves "<k>-1", named and
// tagged, and "<k>-2", unnamed and untagged; the listings whose element has
// no Tags serve two such elements without tags. Clusters c-1 and c-2 and
// multiplexes m-1 and m-2 each get one child per per-parent listing; a call
// for any other parent fails the test.
func mlAllHandlers(t *testing.T) map[string]func(in any) (any, error) {
	h := mlOnePageHandlers(t)
	for op, f := range h {
		h[op] = mlTwoPages(t, op, f)
	}
	return h
}

func mlOnePageHandlers(t *testing.T) map[string]func(in any) (any, error) {
	parent := func(op string, id *string, want ...string) string {
		p := sv(id)
		for _, w := range want {
			if p == w {
				return p
			}
		}
		t.Errorf("%s parent = %q, want one of %v", op, p, want)
		return p
	}
	arn := func(kind, n string) *string { return ptrStr("arn:" + kind + "/" + n) }
	return map[string]func(in any) (any, error){
		"ListChannels": func(any) (any, error) {
			return &medialive.ListChannelsOutput{Channels: []medialivetypes.ChannelSummary{
				{Arn: arn("channel", "1"), Id: ptrStr("ch-1"), Name: ptrStr("name-ch-1"), Tags: mlTestTags},
				{Arn: arn("channel", "2"), Id: ptrStr("ch-2")},
			}}, nil
		},
		"ListCloudWatchAlarmTemplates": func(any) (any, error) {
			return &medialive.ListCloudWatchAlarmTemplatesOutput{CloudWatchAlarmTemplates: []medialivetypes.CloudWatchAlarmTemplateSummary{
				{Arn: arn("cwat", "1"), Id: ptrStr("cwat-1"), Name: ptrStr("name-cwat-1"), Tags: mlTestTags},
				{Arn: arn("cwat", "2"), Id: ptrStr("aws-cwat-2")},
			}}, nil
		},
		"ListCloudWatchAlarmTemplateGroups": func(any) (any, error) {
			return &medialive.ListCloudWatchAlarmTemplateGroupsOutput{CloudWatchAlarmTemplateGroups: []medialivetypes.CloudWatchAlarmTemplateGroupSummary{
				{Arn: arn("cwatg", "1"), Id: ptrStr("cwatg-1"), Name: ptrStr("name-cwatg-1"), Tags: mlTestTags},
				{Arn: arn("cwatg", "2"), Id: ptrStr("aws-cwatg-2")},
			}}, nil
		},
		"ListEventBridgeRuleTemplates": func(any) (any, error) {
			return &medialive.ListEventBridgeRuleTemplatesOutput{EventBridgeRuleTemplates: []medialivetypes.EventBridgeRuleTemplateSummary{
				{Arn: arn("ebrt", "1"), Id: ptrStr("ebrt-1"), Name: ptrStr("name-ebrt-1"), Tags: mlTestTags},
				{Arn: arn("ebrt", "2"), Id: ptrStr("ebrt-2")},
			}}, nil
		},
		"ListEventBridgeRuleTemplateGroups": func(any) (any, error) {
			return &medialive.ListEventBridgeRuleTemplateGroupsOutput{EventBridgeRuleTemplateGroups: []medialivetypes.EventBridgeRuleTemplateGroupSummary{
				{Arn: arn("ebrtg", "1"), Id: ptrStr("ebrtg-1"), Name: ptrStr("name-ebrtg-1"), Tags: mlTestTags},
				{Arn: arn("ebrtg", "2"), Id: ptrStr("ebrtg-2")},
			}}, nil
		},
		"ListInputs": func(any) (any, error) {
			return &medialive.ListInputsOutput{Inputs: []medialivetypes.Input{
				{Arn: arn("input", "1"), Id: ptrStr("in-1"), Name: ptrStr("name-in-1"), Tags: mlTestTags},
				{Arn: arn("input", "2"), Id: ptrStr("in-2")},
			}}, nil
		},
		"ListInputDevices": func(any) (any, error) {
			return &medialive.ListInputDevicesOutput{InputDevices: []medialivetypes.InputDeviceSummary{
				{Arn: arn("device", "1"), Id: ptrStr("dev-1"), Name: ptrStr("name-dev-1"), Tags: mlTestTags},
				{Arn: arn("device", "2"), Id: ptrStr("dev-2")},
			}}, nil
		},
		"ListInputSecurityGroups": func(any) (any, error) {
			return &medialive.ListInputSecurityGroupsOutput{InputSecurityGroups: []medialivetypes.InputSecurityGroup{
				{Arn: arn("isg", "1"), Id: ptrStr("isg-1"), Tags: mlTestTags},
				{Arn: arn("isg", "2"), Id: ptrStr("isg-2")},
			}}, nil
		},
		"ListReservations": func(any) (any, error) {
			return &medialive.ListReservationsOutput{Reservations: []medialivetypes.Reservation{
				{Arn: arn("reservation", "1"), ReservationId: ptrStr("res-1"), Name: ptrStr("name-res-1"), State: medialivetypes.ReservationStateActive, Tags: mlTestTags},
				{Arn: arn("reservation", "2"), ReservationId: ptrStr("res-2")},
			}}, nil
		},
		"ListMultiplexes": func(any) (any, error) {
			return &medialive.ListMultiplexesOutput{Multiplexes: []medialivetypes.MultiplexSummary{
				{Arn: arn("multiplex", "1"), Id: ptrStr("m-1"), Name: ptrStr("name-m-1"), Tags: mlTestTags},
				{Arn: arn("multiplex", "2"), Id: ptrStr("m-2")},
			}}, nil
		},
		"ListSignalMaps": func(any) (any, error) {
			return &medialive.ListSignalMapsOutput{SignalMaps: []medialivetypes.SignalMapSummary{
				{Arn: arn("signalmap", "1"), Id: ptrStr("sm-1"), Name: ptrStr("name-sm-1"), Tags: mlTestTags},
				{Arn: arn("signalmap", "2"), Id: ptrStr("sm-2")},
			}}, nil
		},
		"ListClusters": func(any) (any, error) {
			return &medialive.ListClustersOutput{Clusters: []medialivetypes.DescribeClusterSummary{
				{Arn: arn("cluster", "1"), Id: ptrStr("c-1"), Name: ptrStr("name-c-1")},
				{Arn: arn("cluster", "2"), Id: ptrStr("c-2")},
			}}, nil
		},
		"ListNetworks": func(any) (any, error) {
			return &medialive.ListNetworksOutput{Networks: []medialivetypes.DescribeNetworkSummary{
				{Arn: arn("network", "1"), Id: ptrStr("net-1"), Name: ptrStr("name-net-1")},
				{Arn: arn("network", "2"), Id: ptrStr("net-2")},
			}}, nil
		},
		"ListSdiSources": func(any) (any, error) {
			return &medialive.ListSdiSourcesOutput{SdiSources: []medialivetypes.SdiSourceSummary{
				{Arn: arn("sdi", "1"), Id: ptrStr("sdi-1"), Name: ptrStr("name-sdi-1")},
				{Arn: arn("sdi", "2"), Id: ptrStr("sdi-2")},
			}}, nil
		},
		"ListChannelPlacementGroups": func(in any) (any, error) {
			c := parent("ListChannelPlacementGroups", in.(*medialive.ListChannelPlacementGroupsInput).ClusterId, "c-1", "c-2")
			return &medialive.ListChannelPlacementGroupsOutput{ChannelPlacementGroups: []medialivetypes.DescribeChannelPlacementGroupSummary{
				{Arn: arn("cpg", c), Id: ptrStr("cpg-" + c), Name: ptrStr("name-cpg-" + c)},
			}}, nil
		},
		"ListNodes": func(in any) (any, error) {
			c := parent("ListNodes", in.(*medialive.ListNodesInput).ClusterId, "c-1", "c-2")
			return &medialive.ListNodesOutput{Nodes: []medialivetypes.DescribeNodeSummary{
				{Arn: arn("node", c), Id: ptrStr("node-" + c)},
			}}, nil
		},
		"ListMultiplexPrograms": func(in any) (any, error) {
			m := parent("ListMultiplexPrograms", in.(*medialive.ListMultiplexProgramsInput).MultiplexId, "m-1", "m-2")
			return &medialive.ListMultiplexProgramsOutput{MultiplexPrograms: []medialivetypes.MultiplexProgramSummary{
				{ProgramName: ptrStr("prog-" + m)},
			}}, nil
		},
	}
}

func mlScanAll(t *testing.T, st *store.Store, h map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	client := medialive.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	total, _, err := scanMLAll(context.Background(), client, newTestAccount(testAccountID), mlTestRegion, st, testScanID)
	return total, err
}

func mlTestProgram(mid, name string) string {
	return "arn:aws:medialive:" + mlTestRegion + ":" + testAccountID + ":multiplexprogram/" + mid + "/" + name
}

// TestScanMLAll_StoresTags pins, for every type whose listed element carries
// Tags, that the stored row's TagsJSON is that element's tags and that an
// untagged element stores none; the types whose element has no Tags store
// none. It also pins each type's NativeID and Name.
func TestScanMLAll_StoresTags(t *testing.T) {
	st := newTestStore(t)
	total, err := mlScanAll(t, st, mlAllHandlers(t))
	if err != nil {
		t.Fatalf("scanMLAll: %v", err)
	}
	if total != 34 {
		t.Errorf("total = %d, want 34", total)
	}
	type row struct{ nativeID, name string }
	cases := []struct {
		typ      string
		tagged   row // zero when the type stores no tags
		untagged []row
	}{
		{TypeMediaLiveChannel, row{"arn:channel/1", "name-ch-1"}, []row{{"arn:channel/2", "ch-2"}}},
		{TypeMediaLiveCloudWatchAlarmTemplate, row{"arn:cwat/1", "name-cwat-1"}, []row{{"arn:cwat/2", "aws-cwat-2"}}},
		{TypeMediaLiveCloudWatchAlarmTemplateGroup, row{"arn:cwatg/1", "name-cwatg-1"}, []row{{"arn:cwatg/2", "aws-cwatg-2"}}},
		{TypeMediaLiveEventBridgeRuleTemplate, row{"arn:ebrt/1", "name-ebrt-1"}, []row{{"arn:ebrt/2", "ebrt-2"}}},
		{TypeMediaLiveEventBridgeRuleTemplateGroup, row{"arn:ebrtg/1", "name-ebrtg-1"}, []row{{"arn:ebrtg/2", "ebrtg-2"}}},
		{TypeMediaLiveInput, row{"arn:input/1", "name-in-1"}, []row{{"arn:input/2", "in-2"}}},
		{TypeMediaLiveInputDevice, row{"arn:device/1", "name-dev-1"}, []row{{"arn:device/2", "dev-2"}}},
		{TypeMediaLiveInputSecurityGroup, row{"arn:isg/1", "isg-1"}, []row{{"arn:isg/2", "isg-2"}}},
		{TypeMediaLiveReservation, row{"arn:reservation/1", "name-res-1"}, []row{{"arn:reservation/2", "arn:reservation/2"}}},
		{TypeMediaLiveMultiplex, row{"arn:multiplex/1", "name-m-1"}, []row{{"arn:multiplex/2", "m-2"}}},
		{TypeMediaLiveSignalMap, row{"arn:signalmap/1", "name-sm-1"}, []row{{"arn:signalmap/2", "sm-2"}}},
		{TypeMediaLiveCluster, row{}, []row{{"arn:cluster/1", "name-c-1"}, {"arn:cluster/2", "c-2"}}},
		{TypeMediaLiveNetwork, row{}, []row{{"arn:network/1", "name-net-1"}, {"arn:network/2", "net-2"}}},
		{TypeMediaLiveSdiSource, row{}, []row{{"arn:sdi/1", "name-sdi-1"}, {"arn:sdi/2", "sdi-2"}}},
		{TypeMediaLiveChannelPlacementGroup, row{}, []row{{"arn:cpg/c-1", "name-cpg-c-1"}, {"arn:cpg/c-2", "name-cpg-c-2"}}},
		{TypeMediaLiveNode, row{}, []row{{"arn:node/c-1", "node-c-1"}, {"arn:node/c-2", "node-c-2"}}},
		{TypeMediaLiveMultiplexProgram, row{}, []row{{mlTestProgram("m-1", "prog-m-1"), "prog-m-1"}, {mlTestProgram("m-2", "prog-m-2"), "prog-m-2"}}},
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			rows := rowsByNativeID(t, st, c.typ)
			want := len(c.untagged)
			if c.tagged.nativeID != "" {
				want++
				r, ok := rows[c.tagged.nativeID]
				if !ok {
					t.Fatalf("%s not stored; got %d rows", c.tagged.nativeID, len(rows))
				}
				if got := sv(r.Name); got != c.tagged.name {
					t.Errorf("%s Name = %q, want %q", c.tagged.nativeID, got, c.tagged.name)
				}
				assertTagsMap(t, r.TagsJSON, mlTestTags)
			}
			if len(rows) != want {
				t.Errorf("stored %d rows, want %d", len(rows), want)
			}
			for _, u := range c.untagged {
				r, ok := rows[u.nativeID]
				if !ok {
					t.Fatalf("%s not stored; got %d rows", u.nativeID, len(rows))
				}
				if got := sv(r.Name); got != u.name {
					t.Errorf("%s Name = %q, want %q", u.nativeID, got, u.name)
				}
				if r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %s, want nil", u.nativeID, *r.TagsJSON)
				}
			}
		})
	}
}

// TestScanMLAll_RowFlags pins the per-row fields beside tags: an alarm
// template or group whose Id starts "aws-" is AWS-managed and the others are
// not, and a reservation's Status is its State, unset when the State is.
func TestScanMLAll_RowFlags(t *testing.T) {
	st := newTestStore(t)
	if _, err := mlScanAll(t, st, mlAllHandlers(t)); err != nil {
		t.Fatalf("scanMLAll: %v", err)
	}
	for _, c := range []struct {
		typ, customer, managed string
	}{
		{TypeMediaLiveCloudWatchAlarmTemplate, "arn:cwat/1", "arn:cwat/2"},
		{TypeMediaLiveCloudWatchAlarmTemplateGroup, "arn:cwatg/1", "arn:cwatg/2"},
	} {
		rows := rowsByNativeID(t, st, c.typ)
		if rows[c.customer].ManagedByProvider || !rows[c.managed].ManagedByProvider {
			t.Errorf("%s ManagedByProvider: %s = %v, %s = %v; want false, true", c.typ,
				c.customer, rows[c.customer].ManagedByProvider, c.managed, rows[c.managed].ManagedByProvider)
		}
	}
	res := rowsByNativeID(t, st, TypeMediaLiveReservation)
	if got := sv(res["arn:reservation/1"].Status); got != "ACTIVE" {
		t.Errorf("reservation 1 Status = %q, want ACTIVE", got)
	}
	if s := res["arn:reservation/2"].Status; s != nil {
		t.Errorf("reservation 2 Status = %q, want nil", *s)
	}
}

// mlPage is one page of a listing: elements keyed by these ARNs or names
// ("" for an element without one) and next as its NextToken, or err instead.
type mlPage struct {
	keys []string
	next string
	err  error
}

func mlChannelsHandler(t *testing.T, pages []mlPage) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListChannels": func(in any) (any, error) {
			idx, err := nmPageIndex(t, "ListChannels", sv(in.(*medialive.ListChannelsInput).NextToken), len(pages))
			if err != nil {
				return nil, err
			}
			p := pages[idx]
			if p.err != nil {
				return nil, p.err
			}
			out := &medialive.ListChannelsOutput{}
			for _, a := range p.keys {
				c := medialivetypes.ChannelSummary{Id: ptrStr("ch")}
				if a != "" {
					c.Arn = ptrStr(a)
				}
				out.Channels = append(out.Channels, c)
			}
			if p.next != "" {
				out.NextToken = ptrStr(p.next)
			}
			return out, nil
		},
	}
}

// TestScanMLChannels_ListOutcomes pins the error handling of an account-wide
// listing: rows read before a failure are kept, a denial warns instead of
// failing, and an element without an ARN is warned about.
func TestScanMLChannels_ListOutcomes(t *testing.T) {
	bad := apiErr("BadRequestException", "bad")
	denied := apiErr("ForbiddenException", "not authorized to perform: medialive:ListChannels")
	for _, c := range []struct {
		name      string
		pages     []mlPage
		wantErr   error
		wantARNs  []string
		wantWarns int
	}{
		{"two pages, token forwarded", []mlPage{{keys: []string{"arn:a1"}, next: "t1"}, {keys: []string{"arn:a2"}}}, nil, []string{"arn:a1", "arn:a2"}, 0},
		{"later page fails keeps rows read", []mlPage{{keys: []string{"arn:a1"}, next: "t1"}, {err: bad}}, bad, []string{"arn:a1"}, 0},
		{"repeated token keeps rows read", []mlPage{{keys: []string{"arn:a1"}, next: "t1"}, {keys: []string{"arn:a2"}, next: "t1"}}, errRepeatedNextToken, []string{"arn:a1", "arn:a2"}, 0},
		{"denied warns", []mlPage{{err: denied}}, nil, nil, 1},
		{"denied after a page keeps rows and warns", []mlPage{{keys: []string{"arn:a1"}, next: "t1"}, {err: denied}}, nil, []string{"arn:a1"}, 1},
		{"empty ARN warns", []mlPage{{keys: []string{"", "arn:a1"}}}, nil, []string{"arn:a1"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var scopes []string
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == mediaLiveListChannelsOp {
					scopes = append(scopes, w.Scope)
				}
			}
			client := medialive.NewFromConfig(opStubConfig(mlChannelsHandler(t, c.pages), errors.New("unexpected op")))
			total, _, err := scanMLChannels(context.Background(), client, newTestAccount(testAccountID), mlTestRegion, st, testScanID)
			if c.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != len(c.wantARNs) {
				t.Errorf("total = %d, want %d", total, len(c.wantARNs))
			}
			rows := rowsByNativeID(t, st, TypeMediaLiveChannel)
			if len(rows) != len(c.wantARNs) {
				t.Errorf("stored %d channels, want %v", len(rows), c.wantARNs)
			}
			for _, a := range c.wantARNs {
				if _, ok := rows[a]; !ok {
					t.Errorf("channel %s not stored", a)
				}
			}
			if len(scopes) != c.wantWarns {
				t.Errorf("warnings = %d, want %d", len(scopes), c.wantWarns)
			}
			for _, s := range scopes {
				if want := testAccountID + "/" + mlTestRegion; s != want {
					t.Errorf("warning scope = %q, want %q", s, want)
				}
			}
		})
	}
}

// TestScanMLChannels_WriteFailsWarnsNothing pins that a failed write returns
// ErrStoreWrite and reports no warning, since none could say the rows read
// were stored.
func TestScanMLChannels_WriteFailsWarnsNothing(t *testing.T) {
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, mediaLiveListChannelsOp)
	h := mlChannelsHandler(t, []mlPage{{keys: []string{"", "arn:a1"}}})
	serve := h["ListChannels"]
	h["ListChannels"] = func(in any) (any, error) {
		closeStore()
		return serve(in)
	}
	client := medialive.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	_, _, err := scanMLChannels(context.Background(), client, newTestAccount(testAccountID), mlTestRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d, want 0", *warns)
	}
}

// mlHTTP404 is a NotFoundException as the SDK returns it, wrapped with its
// HTTP 404 response.
func mlHTTP404() error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
		Err:      apiErr("NotFoundException", "Not Found"),
	}
}

// TestScanMLAll_AnywhereRegionGap pins that a region rejecting the MediaLive
// Anywhere listings on their first page reads as having none, silently, while
// the other phases are stored; a rejection after a page was read is returned
// with that page's rows kept.
func TestScanMLAll_AnywhereRegionGap(t *testing.T) {
	gap := mlHTTP404()
	unavailable := apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")
	t.Run("first page", func(t *testing.T) {
		h := mlAllHandlers(t)
		h["ListClusters"] = func(any) (any, error) { return nil, gap }
		h["ListNetworks"] = func(any) (any, error) { return nil, unavailable }
		for _, op := range []string{"ListNodes", "ListChannelPlacementGroups"} {
			h[op] = func(any) (any, error) {
				t.Errorf("%s called with no cluster listed", op)
				return nil, errors.New("unexpected")
			}
		}
		st := newTestStore(t)
		var warns int
		st.OnWarn = func(store.ScanWarning) { warns++ }
		if _, err := mlScanAll(t, st, h); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if warns != 0 {
			t.Errorf("warnings = %d, want 0", warns)
		}
		if n := len(rowsByNativeID(t, st, TypeMediaLiveSdiSource)); n != 2 {
			t.Errorf("stored %d SDI sources, want 2", n)
		}
	})
	t.Run("later page", func(t *testing.T) {
		h := mlAllHandlers(t)
		for _, op := range []string{"ListClusters", "ListNetworks"} {
			serve := h[op]
			h[op] = func(in any) (any, error) {
				if sv(reflect.ValueOf(in).Elem().FieldByName("NextToken").Interface().(*string)) != "" {
					return nil, gap
				}
				return serve(in)
			}
		}
		st := newTestStore(t)
		_, err := mlScanAll(t, st, h)
		if !errors.Is(err, gap) {
			t.Fatalf("err = %v, want the 404", err)
		}
		for _, typ := range []string{TypeMediaLiveCluster, TypeMediaLiveNetwork, TypeMediaLiveNode} {
			if n := len(rowsByNativeID(t, st, typ)); n != 2 {
				t.Errorf("stored %d %s, want 2", n, typ)
			}
		}
	})
}

// mlProgramsHandler serves ListMultiplexPrograms per multiplex from pages,
// page i of a parent answering NextToken "t<i>" (nmPageIndex). An empty
// name serves a program without a ProgramName.
func mlProgramsHandler(t *testing.T, pages map[string][]mlPage) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListMultiplexPrograms": func(in any) (any, error) {
			req := in.(*medialive.ListMultiplexProgramsInput)
			m := sv(req.MultiplexId)
			idx, err := nmPageIndex(t, "ListMultiplexPrograms "+m, sv(req.NextToken), len(pages[m]))
			if err != nil {
				return nil, err
			}
			p := pages[m][idx]
			if p.err != nil {
				return nil, p.err
			}
			out := &medialive.ListMultiplexProgramsOutput{}
			for _, name := range p.keys {
				prog := medialivetypes.MultiplexProgramSummary{}
				if name != "" {
					prog.ProgramName = ptrStr(name)
				}
				out.MultiplexPrograms = append(out.MultiplexPrograms, prog)
			}
			if p.next != "" {
				out.NextToken = ptrStr(p.next)
			}
			return out, nil
		},
	}
}

// TestScanMLMultiplexPrograms_ParentOutcomes pins the per-multiplex error
// handling: what each kind of failure on m-1 does to the result, and that
// m-2's rows are stored anyway. NotFoundException, which ListMultiplexPrograms
// models for a multiplex deleted since it was listed, skips it.
func TestScanMLMultiplexPrograms_ParentOutcomes(t *testing.T) {
	bad := apiErr("BadRequestException", "bad")
	notFound := apiErr("NotFoundException", "gone")
	denied := apiErr("ForbiddenException", "not authorized to perform: medialive:ListMultiplexPrograms")
	sibling := []mlPage{{keys: []string{"b"}}}
	p := mlTestProgram
	for _, c := range []struct {
		name      string
		m1        []mlPage
		wantErr   error
		want      []string
		wantWarns int
	}{
		{"two pages, token forwarded", []mlPage{{keys: []string{"a1"}, next: "t1"}, {keys: []string{"a2"}}}, nil, []string{p("m-1", "a1"), p("m-1", "a2"), p("m-2", "b")}, 0},
		{"hard error keeps sibling", []mlPage{{err: bad}}, bad, []string{p("m-2", "b")}, 0},
		{"later page fails keeps rows read", []mlPage{{keys: []string{"a1"}, next: "t1"}, {err: bad}}, bad, []string{p("m-1", "a1"), p("m-2", "b")}, 0},
		{"not found before any row is silent", []mlPage{{err: notFound}}, nil, []string{p("m-2", "b")}, 0},
		{"not found after a page keeps rows and warns", []mlPage{{keys: []string{"a1"}, next: "t1"}, {err: notFound}}, nil, []string{p("m-1", "a1"), p("m-2", "b")}, 1},
		{"denied warns", []mlPage{{err: denied}}, nil, []string{p("m-2", "b")}, 1},
		{"empty name warns", []mlPage{{keys: []string{"", "a1"}}}, nil, []string{p("m-1", "a1"), p("m-2", "b")}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var scopes []string
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == mediaLiveListMultiplexProgramsOp {
					scopes = append(scopes, w.Scope)
				}
			}
			h := mlProgramsHandler(t, map[string][]mlPage{"m-1": c.m1, "m-2": sibling})
			client := medialive.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
			total, _, err := scanMLMultiplexPrograms(context.Background(), client, newTestAccount(testAccountID), mlTestRegion, st, testScanID, []string{"m-1", "m-2"})
			if c.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != len(c.want) {
				t.Errorf("total = %d, want %d", total, len(c.want))
			}
			rows := rowsByNativeID(t, st, TypeMediaLiveMultiplexProgram)
			if len(rows) != len(c.want) {
				t.Errorf("stored %d programs, want %v", len(rows), c.want)
			}
			for _, id := range c.want {
				if _, ok := rows[id]; !ok {
					t.Errorf("program %s not stored", id)
				}
			}
			if len(scopes) != c.wantWarns {
				t.Errorf("warnings = %d, want %d", len(scopes), c.wantWarns)
			}
			for _, s := range scopes {
				if want := testAccountID + "/" + mlTestRegion; s != want {
					t.Errorf("warning scope = %q, want %q", s, want)
				}
			}
		})
	}
}

// TestScanMLNodes_NotFoundIsAnError pins that ListNodes, which models no
// not-found error, returns a NotFoundException instead of skipping the
// cluster, and still stores the other cluster's nodes.
func TestScanMLNodes_NotFoundIsAnError(t *testing.T) {
	notFound := apiErr("NotFoundException", "gone")
	h := map[string]func(in any) (any, error){
		"ListNodes": func(in any) (any, error) {
			if sv(in.(*medialive.ListNodesInput).ClusterId) == "c-1" {
				return nil, notFound
			}
			return &medialive.ListNodesOutput{Nodes: []medialivetypes.DescribeNodeSummary{{Arn: ptrStr("arn:node/c-2")}}}, nil
		},
	}
	st := newTestStore(t)
	client := medialive.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	_, _, err := scanMLNodes(context.Background(), client, newTestAccount(testAccountID), mlTestRegion, st, testScanID, []string{"c-1", "c-2"})
	if !errors.Is(err, notFound) {
		t.Fatalf("err = %v, want the NotFoundException", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeMediaLiveNode)["arn:node/c-2"]; !ok {
		t.Error("node of c-2 not stored")
	}
}

// TestScanMLAll_ParentWithoutIDStoredAndWarned pins that a cluster or
// multiplex listed with an ARN but no id is stored with a warning, and that
// no child listing is called with an empty id; one listed with no ARN is
// warned about and not stored.
func TestScanMLAll_ParentWithoutIDStoredAndWarned(t *testing.T) {
	h := mlAllHandlers(t)
	h["ListClusters"] = func(any) (any, error) {
		return &medialive.ListClustersOutput{Clusters: []medialivetypes.DescribeClusterSummary{
			{Arn: ptrStr("arn:cluster/1")},
			{Id: ptrStr("c-2")},
		}}, nil
	}
	h["ListMultiplexes"] = func(any) (any, error) {
		return &medialive.ListMultiplexesOutput{Multiplexes: []medialivetypes.MultiplexSummary{
			{Arn: ptrStr("arn:multiplex/1")},
			{Id: ptrStr("m-2")},
		}}, nil
	}
	for _, op := range []string{"ListNodes", "ListChannelPlacementGroups", "ListMultiplexPrograms"} {
		h[op] = func(any) (any, error) {
			t.Errorf("%s called; want no child listing for a parent without an id", op)
			return nil, errors.New("unexpected")
		}
	}
	st := newTestStore(t)
	clusterWarns := countOpWarnings(st, mediaLiveListClustersOp)
	multiplexWarns := countOpWarnings(st, mediaLiveListMultiplexesOp)
	if _, err := mlScanAll(t, st, h); err != nil {
		t.Fatalf("scanMLAll: %v", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeMediaLiveCluster)["arn:cluster/1"]; !ok {
		t.Error("cluster arn:cluster/1 not stored")
	}
	if _, ok := rowsByNativeID(t, st, TypeMediaLiveMultiplex)["arn:multiplex/1"]; !ok {
		t.Error("multiplex arn:multiplex/1 not stored")
	}
	if *clusterWarns != 2 || *multiplexWarns != 2 {
		t.Errorf("warnings: clusters %d, multiplexes %d; want 2 each (no id, no ARN)", *clusterWarns, *multiplexWarns)
	}
}

// TestScanMLAll_NoChildIDWarnsOnlyAfterWrite pins that a parent stored without
// an id is not warned about when the write failed, since the warning would
// claim it was stored.
func TestScanMLAll_NoChildIDWarnsOnlyAfterWrite(t *testing.T) {
	for _, c := range []struct{ op, label string }{
		{"ListClusters", mediaLiveListClustersOp},
		{"ListMultiplexes", mediaLiveListMultiplexesOp},
	} {
		t.Run(c.op, func(t *testing.T) {
			st, closeStore := openClosingStore(t)
			warns := countOpWarnings(st, c.label)
			h := mlAllHandlers(t)
			h["ListClusters"] = func(any) (any, error) {
				return &medialive.ListClustersOutput{Clusters: []medialivetypes.DescribeClusterSummary{{Arn: ptrStr("arn:cluster/1")}}}, nil
			}
			h["ListMultiplexes"] = func(any) (any, error) {
				return &medialive.ListMultiplexesOutput{Multiplexes: []medialivetypes.MultiplexSummary{{Arn: ptrStr("arn:multiplex/1")}}}, nil
			}
			serve := h[c.op]
			h[c.op] = func(in any) (any, error) {
				closeStore()
				return serve(in)
			}
			if _, err := mlScanAll(t, st, h); !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d, want 0", *warns)
			}
		})
	}
}

// TestScanMLAll_PhaseErrorRunsLaterPhases pins that a failed listing does not
// stop the phases after it: its error is returned and every other phase's
// rows are stored.
func TestScanMLAll_PhaseErrorRunsLaterPhases(t *testing.T) {
	bad := apiErr("BadRequestException", "bad")
	for _, c := range []struct {
		failOp   string
		stillHas []string
	}{
		{"ListClusters", []string{TypeMediaLiveMultiplex, TypeMediaLiveMultiplexProgram, TypeMediaLiveChannel, TypeMediaLiveSignalMap}},
		{"ListMultiplexes", []string{TypeMediaLiveCluster, TypeMediaLiveNode, TypeMediaLiveChannel, TypeMediaLiveSignalMap}},
		{"ListChannels", []string{TypeMediaLiveChannelPlacementGroup, TypeMediaLiveInput, TypeMediaLiveSignalMap}},
		{"ListMultiplexPrograms", []string{TypeMediaLiveNetwork, TypeMediaLiveSdiSource, TypeMediaLiveSignalMap}},
	} {
		t.Run(c.failOp, func(t *testing.T) {
			h := mlAllHandlers(t)
			h[c.failOp] = func(any) (any, error) { return nil, bad }
			st := newTestStore(t)
			_, err := mlScanAll(t, st, h)
			if !errors.Is(err, bad) {
				t.Fatalf("err = %v, want the %s error", err, c.failOp)
			}
			for _, typ := range c.stillHas {
				if len(rowsByNativeID(t, st, typ)) == 0 {
					t.Errorf("%s not stored after %s failed", typ, c.failOp)
				}
			}
		})
	}
}

// TestScanMLAll_StoreWriteStops pins that a failed write stops the scan,
// whichever phase it hits: no phase after it calls AWS.
func TestScanMLAll_StoreWriteStops(t *testing.T) {
	for _, c := range []struct {
		closeIn  string
		notAfter []string
	}{
		{"ListClusters", []string{"ListMultiplexes", "ListSignalMaps"}},
		{"ListMultiplexes", []string{"ListChannels", "ListSignalMaps"}},
		{"ListChannels", []string{"ListChannelPlacementGroups", "ListSignalMaps"}},
	} {
		t.Run(c.closeIn, func(t *testing.T) {
			calls := map[string]int{}
			h := mlAllHandlers(t)
			for op, f := range h {
				h[op] = func(in any) (any, error) {
					calls[op]++
					return f(in)
				}
			}
			st, closeStore := openClosingStore(t)
			serve := h[c.closeIn]
			h[c.closeIn] = func(in any) (any, error) {
				closeStore()
				return serve(in)
			}
			if _, err := mlScanAll(t, st, h); !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			for _, op := range c.notAfter {
				if calls[op] != 0 {
					t.Errorf("%s called %d times after the failed write, want 0", op, calls[op])
				}
			}
		})
	}
}

// TestScanMLAll_TransientBesideHardError pins that a throttled phase beside a
// hard-failing one is demoted to one warning and the scan classified as an
// error, not as transient.
func TestScanMLAll_TransientBesideHardError(t *testing.T) {
	h := mlAllHandlers(t)
	h["ListClusters"] = func(any) (any, error) { return nil, apiErr("TooManyRequestsException", "slow down") }
	h["ListSignalMaps"] = func(any) (any, error) { return nil, apiErr("BadRequestException", "bad") }
	st := newTestStore(t)
	warns := countOpWarnings(st, mediaLiveListClustersOp)
	_, err := mlScanAll(t, st, h)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("transient warnings = %d, want 1", *warns)
	}
}
