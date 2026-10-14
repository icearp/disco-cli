package aws

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/icearp/disco-cli/store"
)

var (
	kinDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: kinesis:ListChannels")
	kinValidation = apiErr("ValidationException", "bad")
)

const (
	kinStreamARN   = "arn:aws:kinesis:us-east-1:123456789012:stream/orders"
	kinConsumerARN = "arn:aws:kinesis:us-east-1:123456789012:stream/orders/consumer/proc:1700000000"
	kinChannel1ARN = "arn:aws:kinesis:us-east-1:123456789012:channel/c1"
	kinChannel2ARN = "arn:aws:kinesis:us-east-1:123456789012:channel/c2"
	kinChannel3ARN = "arn:aws:kinesis:us-east-1:123456789012:channel/c3"
)

// kinChannelPages serves a two-page ListChannels: page one answers a call
// without a token, page two the call carrying "p2", unless pageTwoErr replaces
// it. Every call's token and page size are recorded.
type kinChannelPages struct {
	mu         sync.Mutex
	tokens     []string
	maxResults []int32
}

// handler serves c1 (ACTIVE, one source stream) on page one; c2 (no status),
// c3 (DELETING) and an element without an ARN on page two.
func (p *kinChannelPages) handler(pageTwoErr error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req, ok := in.(*kinesis.ListChannelsInput)
		if !ok {
			return nil, errors.New("unexpected ListChannels input")
		}
		p.mu.Lock()
		p.tokens = append(p.tokens, sv(req.NextToken))
		var n int32
		if req.MaxResults != nil {
			n = *req.MaxResults
		}
		p.maxResults = append(p.maxResults, n)
		p.mu.Unlock()
		if req.NextToken == nil {
			created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			return &kinesis.ListChannelsOutput{ChannelSummaries: []kinesistypes.ChannelSummary{{
				ChannelARN: ptrStr(kinChannel1ARN), ChannelId: ptrStr("c1"), ChannelName: ptrStr("orders-to-s3"),
				ChannelStatus: kinesistypes.ChannelStatusActive, ChannelDestinationType: kinesistypes.ChannelDestinationTypeS3,
				ChannelCreationTimestamp: &created,
				Streams:                  []kinesistypes.ChannelStreamIdentifier{{StreamARN: ptrStr(kinStreamARN)}},
			}}, NextToken: ptrStr("p2")}, nil
		}
		if pageTwoErr != nil {
			return nil, pageTwoErr
		}
		return &kinesis.ListChannelsOutput{ChannelSummaries: []kinesistypes.ChannelSummary{
			{ChannelARN: ptrStr(kinChannel2ARN), ChannelId: ptrStr("c2"), ChannelName: ptrStr("no-status")},
			{ChannelARN: ptrStr(kinChannel3ARN), ChannelId: ptrStr("c3"), ChannelName: ptrStr("going"), ChannelStatus: kinesistypes.ChannelStatusDeleting},
			{ChannelId: ptrStr("no-arn"), ChannelName: ptrStr("no-arn")},
		}}, nil
	}
}

func scanKinesisChannelsWith(st *store.Store, handler func(in any) (any, error)) (int, error) {
	client := kinesis.NewFromConfig(opStubConfig(map[string]func(any) (any, error){"ListChannels": handler}, kinDenied))
	total, _, err := scanKinesisChannels(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// Both pages are stored at page size 100; an element without an ARN is
// skipped, a DELETING channel is kept, and Status is set only when the summary
// carries one.
func TestScanKinesisChannels_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	var pages kinChannelPages
	total, err := scanKinesisChannelsWith(st, pages.handler(nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(pages.tokens, []string{"", "p2"}) {
		t.Errorf("tokens sent = %q, want [\"\" \"p2\"]", pages.tokens)
	}
	if !slices.Equal(pages.maxResults, []int32{100, 100}) {
		t.Errorf("page sizes sent = %v, want [100 100]", pages.maxResults)
	}
	rows := rowsByNativeID(t, st, TypeKinesisChannel)
	if total != 3 || len(rows) != 3 {
		t.Fatalf("total = %d, rows = %v; want c1, c2 and c3", total, rows)
	}
	c1, ok := rows[kinChannel1ARN]
	if !ok {
		t.Fatalf("missing %s in %v", kinChannel1ARN, rows)
	}
	if sv(c1.Name) != "orders-to-s3" || sv(c1.Status) != "ACTIVE" || sv(c1.Region) != "us-east-1" || sv(c1.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("c1 name/status/region/created = %q/%q/%q/%q, want orders-to-s3/ACTIVE/us-east-1/2026-01-02T03:04:05Z",
			sv(c1.Name), sv(c1.Status), sv(c1.Region), sv(c1.CreatedAt))
	}
	if !strings.Contains(c1.AttributesJSON, `"StreamARN":"arn:aws:kinesis:us-east-1:123456789012:stream/orders"`) {
		t.Errorf("c1 attrs = %s", c1.AttributesJSON)
	}
	c2, ok := rows[kinChannel2ARN]
	if !ok {
		t.Fatalf("missing %s in %v", kinChannel2ARN, rows)
	}
	if sv(c2.Name) != "no-status" || c2.Status != nil {
		t.Errorf("c2 name = %q, status = %v; want no-status, nil", sv(c2.Name), c2.Status)
	}
	if c3, ok := rows[kinChannel3ARN]; !ok || sv(c3.Status) != "DELETING" {
		t.Errorf("channel %s missing or not DELETING: %v", kinChannel3ARN, rows)
	}
}

func TestScanKinesisChannels_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanKinesisChannelsWith(st, func(any) (any, error) { return &kinesis.ListChannelsOutput{}, nil })
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeKinesisChannel)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// On the first call, the unrouted-operation shapes are a silent feature gap
// and a denial warns once; any other error is returned.
func TestScanKinesisChannels_FirstPageError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantWarns int
	}{
		{name: "unknown operation", err: apiErr("UnknownOperationException", "")},
		{name: "unrouted gateway", err: apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")},
		{name: "unrouted gateway denial", err: apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")},
		{name: "http 404", err: respErrCode(404)},
		{name: "denied", err: kinDenied, wantWarns: 1},
		{name: "other", err: kinValidation, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "kinesis:ListChannels")
			total, err := scanKinesisChannelsWith(st, func(any) (any, error) { return nil, tc.err })
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if total != 0 || *warns != tc.wantWarns {
				t.Errorf("total = %d, warnings = %d; want 0, %d", total, *warns, tc.wantWarns)
			}
		})
	}
}

// After a page has succeeded, a denial warns once and keeps page one's row;
// every other error, the not-deployed shapes included, is returned with page
// one's row stored.
func TestScanKinesisChannels_PageTwoError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantHTTP  int
		wantWarns int
	}{
		{name: "denied", err: kinDenied, wantWarns: 1},
		{name: "unknown operation", err: apiErr("UnknownOperationException", ""), wantCode: "UnknownOperationException"},
		{name: "unrouted gateway", err: apiErr("NotFoundException", "Unable to determine service/operation name to be authorized"), wantCode: "NotFoundException"},
		{name: "http 404", err: respErrCode(404), wantHTTP: 404},
		{name: "other", err: kinValidation, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "kinesis:ListChannels")
			var pages kinChannelPages
			total, err := scanKinesisChannelsWith(st, pages.handler(tc.err))
			switch {
			case tc.wantCode != "":
				assertAPICode(t, err, tc.wantCode)
			case tc.wantHTTP != 0:
				if c, ok := httpStatusCode(err); !ok || c != tc.wantHTTP {
					t.Fatalf("err = %v, want HTTP %d", err, tc.wantHTTP)
				}
			case err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeKinesisChannel)
			if _, ok := rows[kinChannel1ARN]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want c1 only", total, rows)
			}
		})
	}
}

// A failed write of the rows listed before a list error reports both errors.
func TestScanKinesisChannels_WriteAndListErrorsBothReported(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	var pages kinChannelPages
	_, err = scanKinesisChannelsWith(st, pages.handler(kinValidation))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want store.ErrStoreWrite", err)
	}
	assertAPICode(t, err, "ValidationException")
}

// kinesisScanHandlers serves one stream "orders" with one consumer, and the
// channel pages; overrides replace a handler by op name.
func kinesisScanHandlers(channels *kinChannelPages, overrides map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListStreams": func(any) (any, error) {
			return &kinesis.ListStreamsOutput{
				StreamNames:     []string{"orders"},
				StreamSummaries: []kinesistypes.StreamSummary{{StreamARN: ptrStr(kinStreamARN), StreamName: ptrStr("orders")}},
			}, nil
		},
		"DescribeStreamSummary": func(any) (any, error) {
			return &kinesis.DescribeStreamSummaryOutput{StreamDescriptionSummary: &kinesistypes.StreamDescriptionSummary{
				StreamARN: ptrStr(kinStreamARN), StreamName: ptrStr("orders"), StreamStatus: kinesistypes.StreamStatusActive,
			}}, nil
		},
		"ListStreamConsumers": func(any) (any, error) {
			return &kinesis.ListStreamConsumersOutput{Consumers: []kinesistypes.Consumer{{
				ConsumerARN: ptrStr(kinConsumerARN), ConsumerName: ptrStr("proc"), ConsumerStatus: kinesistypes.ConsumerStatusActive,
			}}}, nil
		},
		"ListChannels": channels.handler(nil),
	}
	for op, fn := range overrides {
		h[op] = fn
	}
	return h
}

// The channel phase runs in the scan's own region alongside streams and
// consumers.
func TestScanKinesis_StoresChannels(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var channels kinChannelPages
	var regions parentCalls
	cfg := opStubConfig(kinesisScanHandlers(&channels, nil), kinDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, _, err := scanKinesis(context.Background(), acct, "eu-west-1", st, testScanID)
	if err != nil {
		t.Fatalf("scanKinesis: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5 (1 stream, 1 consumer, 3 channels)", total)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	rows := rowsByNativeID(t, st, TypeKinesisChannel)
	for _, id := range []string{kinChannel1ARN, kinChannel2ARN, kinChannel3ARN} {
		if r, ok := rows[id]; !ok || sv(r.Region) != "eu-west-1" {
			t.Errorf("channel %s missing or not in eu-west-1: %v", id, rows)
		}
	}
}

// A channel-phase hard error reaches the caller with the stream and consumer
// rows and the channels' own page-one row stored and counted.
func TestScanKinesis_ChannelErrorAfterEarlierPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var channels kinChannelPages
	acct.cfg = opStubConfig(kinesisScanHandlers(&channels, map[string]func(any) (any, error){
		"ListChannels": channels.handler(kinValidation),
	}), kinDenied)

	total, _, err := scanKinesis(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "ValidationException")
	if total != 3 {
		t.Errorf("total = %d, want 3 (1 stream, 1 consumer, 1 channel)", total)
	}
	for typ, id := range map[string]string{
		TypeKinesisStream:         kinStreamARN,
		TypeKinesisStreamConsumer: kinConsumerARN,
		TypeKinesisChannel:        kinChannel1ARN,
	} {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("missing %s %s", typ, id)
		}
	}
}

// A hard error in the consumer phase does not cost the channels: they are
// stored and counted, and the consumer error still reaches the caller.
func TestScanKinesis_ConsumerErrorStillScansChannels(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var channels kinChannelPages
	acct.cfg = opStubConfig(kinesisScanHandlers(&channels, map[string]func(any) (any, error){
		"ListStreamConsumers": func(any) (any, error) { return nil, kinValidation },
	}), kinDenied)

	total, _, err := scanKinesis(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "ValidationException")
	if total != 4 {
		t.Errorf("total = %d, want 4 (1 stream, 3 channels)", total)
	}
	if n := len(rowsByNativeID(t, st, TypeKinesisChannel)); n != 3 {
		t.Errorf("channel rows = %d, want 3", n)
	}
}

// A hard error in the stream listing does not cost the channels, and the error
// reaches the caller.
func TestScanKinesis_StreamErrorStillScansChannels(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var channels kinChannelPages
	acct.cfg = opStubConfig(kinesisScanHandlers(&channels, map[string]func(any) (any, error){
		"ListStreams": func(any) (any, error) { return nil, kinValidation },
	}), kinDenied)

	total, _, err := scanKinesis(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "ValidationException")
	if total != 3 || len(rowsByNativeID(t, st, TypeKinesisChannel)) != 3 {
		t.Errorf("total = %d, want 3 channels", total)
	}
}

// A denied stream listing warns and does not cost the channels.
func TestScanKinesis_StreamDenialStillScansChannels(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var channels kinChannelPages
	acct.cfg = opStubConfig(kinesisScanHandlers(&channels, map[string]func(any) (any, error){
		"ListStreams": func(any) (any, error) { return nil, kinDenied },
	}), kinDenied)

	total, _, err := scanKinesis(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanKinesis: %v", err)
	}
	if total != 3 || len(rowsByNativeID(t, st, TypeKinesisChannel)) != 3 {
		t.Errorf("total = %d, want 3 channels", total)
	}
}
