package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mq"
	mqtypes "github.com/aws/aws-sdk-go-v2/service/mq/types"
	"github.com/icearp/disco-cli/store"
)

var mqDenied = apiErr("ForbiddenException", "User: arn:aws:iam::123:user/x is not authorized to perform: mq:DescribeSharedResources")

func mqTestBrokerARN(id string) string {
	return "arn:aws:mq:" + testRegion + ":" + testAccountID + ":broker:name-" + id + ":" + id
}

func mqTestShareARN(id string) string {
	return "arn:aws:ram:" + testRegion + ":" + testAccountID + ":resource-share/" + id
}

func newMQTestClient(handlers map[string]func(in any) (any, error)) *mq.Client {
	return mq.NewFromConfig(opStubConfig(handlers, mqDenied))
}

func mqSharedResource(shareID string) mqtypes.SharedResource {
	return mqtypes.SharedResource{
		ResourceArn:       sdkaws.String(mqTestShareARN(shareID)),
		Status:            mqtypes.SharedResourceStatusAvailable,
		Type:              mqtypes.SharedResourceTypeResourceShare,
		DnsNames:          []string{shareID + ".example.internal"},
		ResourceShareArns: []string{mqTestShareARN(shareID)},
	}
}

// mqBrokers returns the brokers as the broker phase hands them on, and stores
// their rows so the contains pairs the phase records have both endpoints.
func mqBrokers(t *testing.T, st *store.Store, ids ...string) []childParent {
	t.Helper()
	out := make([]childParent, 0, len(ids))
	for _, id := range ids {
		upsertTestResource(t, st, "aws", testAccountID, TypeMQBroker, mqTestBrokerARN(id), testRegion, "{}")
		out = append(out, childParent{id: id, arn: mqTestBrokerARN(id)})
	}
	return out
}

// mqSharedHandler answers DescribeSharedResources from pages keyed by broker
// id; errs keys a broker id (first page) or id+"/p2" (second page).
func mqSharedHandler(calls *parentCalls, pages map[string][][]mqtypes.SharedResource, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*mq.DescribeSharedResourcesInput)
		if got := sdkaws.ToInt32(req.MaxResults); got != mqSharedResourcesPageLimit {
			return nil, fmt.Errorf("MaxResults = %d; want %d", got, mqSharedResourcesPageLimit)
		}
		id := sdkaws.ToString(req.BrokerId)
		calls.add(id)
		page := 0
		if sdkaws.ToString(req.NextToken) == "p2" {
			page = 1
			if err := errs[id+"/p2"]; err != nil {
				return nil, err
			}
		} else if err := errs[id]; err != nil {
			return nil, err
		}
		out := &mq.DescribeSharedResourcesOutput{}
		if page < len(pages[id]) {
			out.SharedResources = pages[id][page]
		}
		if page == 0 && len(pages[id]) > 1 {
			out.NextToken = sdkaws.String("p2")
		}
		return out, nil
	}
}

func runMQSharedResources(t *testing.T, st *store.Store, h func(any) (any, error), brokers []childParent) (int, error) {
	t.Helper()
	client := newMQTestClient(map[string]func(any) (any, error){"DescribeSharedResources": h})
	total, _, err := scanMQSharedResources(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, brokers)
	return total, err
}

func TestScanMQSharedResources_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	noARN := mqSharedResource("ignored")
	noARN.ResourceArn = nil
	total, err := runMQSharedResources(t, st, mqSharedHandler(&calls, map[string][][]mqtypes.SharedResource{
		"b-1": {{mqSharedResource("rs-a")}, {mqSharedResource("rs-b"), noARN}},
		"b-2": {{mqSharedResource("rs-c")}},
	}, nil), mqBrokers(t, st, "b-1", "b-2"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (both pages of b-1, one of b-2, the ARN-less element dropped)", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"b-1", "b-2"}) {
		t.Errorf("brokers queried = %v; want [b-1 b-2]", got)
	}
	rows := rowsByNativeID(t, st, TypeMQSharedResource)
	if len(rows) != 3 {
		t.Errorf("rows = %d; want 3", len(rows))
	}
	for broker, share := range map[string]string{"b-1": "rs-b", "b-2": "rs-c"} {
		nid := mqTestBrokerARN(broker) + "/shared-resource/" + mqTestShareARN(share)
		r, ok := rows[nid]
		if !ok {
			t.Errorf("missing %s", nid)
			continue
		}
		if r.Type != TypeMQSharedResource || sv(r.Region) != testRegion || sv(r.Name) != mqTestShareARN(share) || sv(r.Status) != "AVAILABLE" {
			t.Errorf("%s: type, region, name, status = %s, %s, %s, %s", nid, r.Type, sv(r.Region), sv(r.Name), sv(r.Status))
		}
		var attrs struct {
			DNSNames []string `json:"DnsNames"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("decode attrs: %v", err)
		}
		if want := []string{share + ".example.internal"}; !reflect.DeepEqual(attrs.DNSNames, want) {
			t.Errorf("%s DnsNames = %v; want %v", nid, attrs.DNSNames, want)
		}
		assertContainsNative(t, st, mqTestBrokerARN(broker), nid)
	}
}

func TestScanMQSharedResources_Empty(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := runMQSharedResources(t, st, mqSharedHandler(&calls, nil, nil), mqBrokers(t, st, "b-1"))
	if err != nil || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeMQSharedResource)); n != 0 {
		t.Errorf("rows = %d; want 0", n)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"b-1"}) {
		t.Errorf("brokers queried = %v; want [b-1]", got)
	}
}

func TestScanMQSharedResources_PerBrokerErrors(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantWarns int
		wantErr   bool
	}{
		{"gone is silent", apiErr("NotFoundException", "gone"), 0, false},
		{"denial warns once", mqDenied, 1, false},
		{"bad request warns once", apiErr("BadRequestException", "not supported"), 1, false},
		{"transient warns once", apiErr("ThrottlingException", "Rate exceeded"), 1, false},
		{"other fails", apiErr("ConflictException", "bad"), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, mqDescribeSharedResourcesOp)
			var calls parentCalls
			h := mqSharedHandler(&calls, map[string][][]mqtypes.SharedResource{
				"b-1": {{mqSharedResource("rs-a")}}, "b-2": {{mqSharedResource("rs-b")}}, "b-3": {{mqSharedResource("rs-c")}},
			}, map[string]error{"b-2": tc.err, "b-3": tc.err})
			_, err := runMQSharedResources(t, st, h, mqBrokers(t, st, "b-1", "b-2", "b-3"))
			if tc.wantErr {
				if !isAPIErrorCode(err, "ConflictException") {
					t.Errorf("err = %v; want the ConflictException", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeMQSharedResource)
			if _, ok := rows[mqTestBrokerARN("b-1")+"/shared-resource/"+mqTestShareARN("rs-a")]; !ok || len(rows) != 1 {
				t.Errorf("rows = %d (b-1's present: %v); want only b-1's", len(rows), ok)
			}
		})
	}
}

// A hard error on a broker's second page keeps its first page's rows.
func TestScanMQSharedResources_HardErrorKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := runMQSharedResources(t, st, mqSharedHandler(&calls, map[string][][]mqtypes.SharedResource{
		"b-1": {{mqSharedResource("rs-a")}, {mqSharedResource("rs-b")}},
	}, map[string]error{"b-1/p2": apiErr("ConflictException", "bad")}), mqBrokers(t, st, "b-1"))
	if !isAPIErrorCode(err, "ConflictException") || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1 and the ConflictException", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeMQSharedResource)[mqTestBrokerARN("b-1")+"/shared-resource/"+mqTestShareARN("rs-a")]; !ok {
		t.Errorf("first page's row missing")
	}
}

// A NextToken that repeats the request's token ends the listing.
func TestScanMQSharedResources_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var calls atomic.Int32
	h := func(any) (any, error) {
		if calls.Add(1) > 3 {
			return nil, fmt.Errorf("paginator kept calling on a repeated token")
		}
		return &mq.DescribeSharedResourcesOutput{SharedResources: []mqtypes.SharedResource{mqSharedResource("rs-a")}, NextToken: sdkaws.String("same")}, nil
	}
	if _, err := runMQSharedResources(t, st, h, mqBrokers(t, st, "b-1")); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("calls = %d; want 2", n)
	}
}

// mqTopLevelHandlers lists brokers b-1 (current configuration c-1, so one
// configuration association) and b-2 (none), one configuration, and one
// shared resource per broker.
func mqTopLevelHandlers(calls *parentCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListBrokers": func(any) (any, error) {
			return &mq.ListBrokersOutput{BrokerSummaries: []mqtypes.BrokerSummary{
				{BrokerId: sdkaws.String("b-1")}, {BrokerId: sdkaws.String("b-2")},
			}}, nil
		},
		"DescribeBroker": func(in any) (any, error) {
			id := sdkaws.ToString(in.(*mq.DescribeBrokerInput).BrokerId)
			out := &mq.DescribeBrokerOutput{
				BrokerId: sdkaws.String(id), BrokerArn: sdkaws.String(mqTestBrokerARN(id)),
				BrokerName: sdkaws.String("name-" + id), BrokerState: mqtypes.BrokerStateRunning,
			}
			if id == "b-1" {
				out.Configurations = &mqtypes.Configurations{Current: &mqtypes.ConfigurationId{Id: sdkaws.String("c-1")}}
			}
			return out, nil
		},
		"ListConfigurations": func(any) (any, error) {
			return &mq.ListConfigurationsOutput{Configurations: []mqtypes.Configuration{{
				Arn: sdkaws.String("arn:aws:mq:" + testRegion + ":" + testAccountID + ":configuration:c-1"), Name: sdkaws.String("c"),
			}}}, nil
		},
		"DescribeSharedResources": mqSharedHandler(calls, map[string][][]mqtypes.SharedResource{
			"b-1": {{mqSharedResource("rs-1")}}, "b-2": {{mqSharedResource("rs-2")}},
		}, nil),
	}
}

func runScanMQ(st *store.Store, h map[string]func(any) (any, error)) (int, error) {
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, mqDenied)}
	total, _, err := scanMQ(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanMQ_SharedResourcesOfListedBrokers(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := runScanMQ(st, mqTopLevelHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 6 {
		t.Errorf("total = %d; want 6 (2 brokers + 1 configuration + 1 association + 2 shared resources)", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"b-1", "b-2"}) {
		t.Errorf("brokers queried = %v; want [b-1 b-2]", got)
	}
	for broker, share := range map[string]string{"b-1": "rs-1", "b-2": "rs-2"} {
		assertContainsNative(t, st, mqTestBrokerARN(broker), mqTestBrokerARN(broker)+"/shared-resource/"+mqTestShareARN(share))
	}
}

// A hard error in the configuration or shared-resource phase is returned and
// leaves the other phases' rows: the association and shared-resource phases
// run even when ListConfigurations fails, and the shared-resource phase runs
// after every older phase.
func TestScanMQ_PhaseErrorsAreIndependent(t *testing.T) {
	for _, op := range []string{"ListConfigurations", "DescribeSharedResources"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			h := mqTopLevelHandlers(&calls)
			h[op] = func(any) (any, error) { return nil, apiErr("ConflictException", "bad") }
			total, err := runScanMQ(st, h)
			if !isAPIErrorCode(err, "ConflictException") {
				t.Errorf("err = %v; want the %s ConflictException", err, op)
			}
			want := map[string]int{TypeMQBroker: 2, TypeMQConfiguration: 1, TypeMQConfigurationAssociation: 1, TypeMQSharedResource: 2}
			if op == "ListConfigurations" {
				want[TypeMQConfiguration] = 0
			} else {
				want[TypeMQSharedResource] = 0
			}
			wantTotal := 0
			for typ, n := range want {
				wantTotal += n
				if got := len(rowsByNativeID(t, st, typ)); got != n {
					t.Errorf("%s rows = %d; want %d", typ, got, n)
				}
			}
			if total != wantTotal {
				t.Errorf("total = %d; want %d", total, wantTotal)
			}
		})
	}
}

// A throttled configuration phase is demoted to one warning, so the
// shared-resource phase's hard error still reaches the dispatcher as a hard
// error.
func TestScanMQ_TransientDoesNotMaskSharedResourceError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "mq:ListConfigurations")
	var calls parentCalls
	h := mqTopLevelHandlers(&calls)
	h["ListConfigurations"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["DescribeSharedResources"] = func(any) (any, error) { return nil, apiErr("ConflictException", "bad") }
	_, err := runScanMQ(st, h)
	if !isAPIErrorCode(err, "ConflictException") {
		t.Errorf("err = %v; want the DescribeSharedResources ConflictException", err)
	}
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the throttled configuration phase", *warns)
	}
}

// A failed association write is returned as a store-write error, and the
// shared-resource phase after it still runs.
func TestScanMQ_AssociationWriteErrorIsReturned(t *testing.T) {
	st := newTestStore(t)
	// SQLite trigger refusing only association rows; the abort is a constraint
	// error, which the store does not retry.
	if _, err := st.DB().Exec(`CREATE TRIGGER refuse_mq_assoc BEFORE INSERT ON resources
		WHEN NEW.type = '` + TypeMQConfigurationAssociation + `'
		BEGIN SELECT RAISE(ABORT, 'test: association write refused'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	var calls parentCalls
	total, err := runScanMQ(st, mqTopLevelHandlers(&calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want store.ErrStoreWrite from the association phase", err)
	}
	if n := len(rowsByNativeID(t, st, TypeMQSharedResource)); n != 2 {
		t.Errorf("shared resource rows = %d; want 2", n)
	}
	if total != 5 {
		t.Errorf("total = %d; want 5 (every phase but the failed association)", total)
	}
}

// NotFoundException from every broker warns once, as the op may not be served
// in the region; from only some brokers it stays silent (deleted brokers).
func TestScanMQSharedResources_NotFound(t *testing.T) {
	gone := apiErr("NotFoundException", "gone")
	cases := []struct {
		name      string
		errs      map[string]error
		wantWarns int
		wantRows  int
	}{
		{"every broker warns once", map[string]error{"b-1": gone, "b-2": gone}, 1, 0},
		{"one of two is silent", map[string]error{"b-2": gone}, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, mqDescribeSharedResourcesOp)
			var calls parentCalls
			h := mqSharedHandler(&calls, map[string][][]mqtypes.SharedResource{
				"b-1": {{mqSharedResource("rs-a")}}, "b-2": {{mqSharedResource("rs-b")}},
			}, tc.errs)
			if _, err := runMQSharedResources(t, st, h, mqBrokers(t, st, "b-1", "b-2")); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			if n := len(rowsByNativeID(t, st, TypeMQSharedResource)); n != tc.wantRows {
				t.Errorf("rows = %d; want %d", n, tc.wantRows)
			}
		})
	}
}
