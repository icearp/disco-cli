package aws

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/elementalinference"
	eitypes "github.com/aws/aws-sdk-go-v2/service/elementalinference/types"
	"github.com/icearp/disco-cli/store"
)

var (
	eiDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: elemental-inference:List")
	eiValidation = apiErr("ValidationException", "bad")
)

const (
	eiFeed1ARN = "arn:aws:elemental-inference:us-east-1:123456789012:feed/f1"
	eiFeed2ARN = "arn:aws:elemental-inference:us-east-1:123456789012:feed/f2"
	eiFeed3ARN = "arn:aws:elemental-inference:us-east-1:123456789012:feed/f3"
	eiFeed4ARN = "arn:aws:elemental-inference:us-east-1:123456789012:feed/f4"
	eiFeed5ARN = "arn:aws:elemental-inference:us-east-1:123456789012:feed/f5"
	eiDict1ARN = "arn:aws:elemental-inference:us-east-1:123456789012:dictionary/d1"
	eiDict2ARN = "arn:aws:elemental-inference:us-east-1:123456789012:dictionary/d2"
	eiDict3ARN = "arn:aws:elemental-inference:us-east-1:123456789012:dictionary/d3"
	eiDict4ARN = "arn:aws:elemental-inference:us-east-1:123456789012:dictionary/d4"
)

// eiPages serves a two-page list: page one answers a call without a token,
// page two the call carrying "p2", unless pageTwoErr replaces it. Every
// call's token and page size are recorded.
type eiPages struct {
	mu         sync.Mutex
	tokens     []string
	maxResults []int32
}

func (p *eiPages) record(token *string, maxResults *int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tokens = append(p.tokens, sv(token))
	var n int32
	if maxResults != nil {
		n = *maxResults
	}
	p.maxResults = append(p.maxResults, n)
}

// feeds serves f1 (ACTIVE) on page one; f2 (no status), f3 (DELETED), f4
// (DELETING), f5 (ARCHIVED) and an element without an ARN on page two.
func (p *eiPages) feeds(pageTwoErr error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req, ok := in.(*elementalinference.ListFeedsInput)
		if !ok {
			return nil, errors.New("unexpected ListFeeds input")
		}
		p.record(req.NextToken, req.MaxResults)
		if req.NextToken == nil {
			return &elementalinference.ListFeedsOutput{Feeds: []eitypes.FeedSummary{{
				Arn: ptrStr(eiFeed1ARN), Id: ptrStr("f1"), Name: ptrStr("main-event"), Status: eitypes.FeedStatusActive,
				Association: &eitypes.FeedAssociation{AssociatedResourceName: ptrStr("channel-1")},
			}}, NextToken: ptrStr("p2")}, nil
		}
		if pageTwoErr != nil {
			return nil, pageTwoErr
		}
		return &elementalinference.ListFeedsOutput{Feeds: []eitypes.FeedSummary{
			{Arn: ptrStr(eiFeed2ARN), Id: ptrStr("f2"), Name: ptrStr("backup")},
			{Arn: ptrStr(eiFeed3ARN), Id: ptrStr("f3"), Name: ptrStr("gone"), Status: eitypes.FeedStatusDeleted},
			{Arn: ptrStr(eiFeed4ARN), Id: ptrStr("f4"), Name: ptrStr("going"), Status: eitypes.FeedStatusDeleting},
			{Arn: ptrStr(eiFeed5ARN), Id: ptrStr("f5"), Name: ptrStr("old"), Status: eitypes.FeedStatusArchived},
			{Id: ptrStr("no-arn"), Name: ptrStr("no-arn")},
		}}, nil
	}
}

// dictionaries serves d1 (AVAILABLE) on page one; d2 (no status), d3
// (DELETED), d4 (DELETING) and an element without an ARN on page two.
func (p *eiPages) dictionaries(pageTwoErr error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req, ok := in.(*elementalinference.ListDictionariesInput)
		if !ok {
			return nil, errors.New("unexpected ListDictionaries input")
		}
		p.record(req.NextToken, req.MaxResults)
		if req.NextToken == nil {
			return &elementalinference.ListDictionariesOutput{Dictionaries: []eitypes.DictionarySummary{{
				Arn: ptrStr(eiDict1ARN), Id: ptrStr("d1"), Name: ptrStr("sports-terms"),
				Language: eitypes.DictionaryLanguageEng, Status: eitypes.DictionaryStatusAvailable,
			}}, NextToken: ptrStr("p2")}, nil
		}
		if pageTwoErr != nil {
			return nil, pageTwoErr
		}
		return &elementalinference.ListDictionariesOutput{Dictionaries: []eitypes.DictionarySummary{
			{Arn: ptrStr(eiDict2ARN), Id: ptrStr("d2"), Name: ptrStr("players")},
			{Arn: ptrStr(eiDict3ARN), Id: ptrStr("d3"), Name: ptrStr("gone"), Status: eitypes.DictionaryStatusDeleted},
			{Arn: ptrStr(eiDict4ARN), Id: ptrStr("d4"), Name: ptrStr("going"), Status: eitypes.DictionaryStatusDeleting},
			{Id: ptrStr("no-arn"), Name: ptrStr("no-arn")},
		}}, nil
	}
}

func eiClient(handlers map[string]func(in any) (any, error)) *elementalinference.Client {
	return elementalinference.NewFromConfig(opStubConfig(handlers, eiDenied))
}

func scanEIFeedsWith(st *store.Store, handler func(in any) (any, error)) (int, error) {
	total, _, err := scanEIFeeds(context.Background(), eiClient(map[string]func(any) (any, error){"ListFeeds": handler}), newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

func scanEIDictionariesWith(st *store.Store, handler func(in any) (any, error)) (int, error) {
	total, _, err := scanEIDictionaries(context.Background(), eiClient(map[string]func(any) (any, error){"ListDictionaries": handler}), newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// Both pages are stored at page size 100; an element without an ARN and a
// DELETED feed are skipped, and DELETING and ARCHIVED feeds are kept.
func TestScanEIFeeds_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	var pages eiPages
	total, err := scanEIFeedsWith(st, pages.feeds(nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(pages.tokens, []string{"", "p2"}) {
		t.Errorf("tokens sent = %q, want [\"\" \"p2\"]", pages.tokens)
	}
	if !slices.Equal(pages.maxResults, []int32{100, 100}) {
		t.Errorf("page sizes sent = %v, want [100 100]", pages.maxResults)
	}
	rows := rowsByNativeID(t, st, TypeElementalInferenceFeed)
	if total != 4 || len(rows) != 4 {
		t.Fatalf("total = %d, rows = %v; want f1, f2, f4 and f5", total, rows)
	}
	f1, ok := rows[eiFeed1ARN]
	if !ok {
		t.Fatalf("missing %s in %v", eiFeed1ARN, rows)
	}
	if sv(f1.Name) != "main-event" || sv(f1.Status) != "ACTIVE" || sv(f1.Region) != "us-east-1" {
		t.Errorf("f1 name/status/region = %q/%q/%q, want main-event/ACTIVE/us-east-1", sv(f1.Name), sv(f1.Status), sv(f1.Region))
	}
	if !strings.Contains(f1.AttributesJSON, `"AssociatedResourceName":"channel-1"`) {
		t.Errorf("f1 attrs = %s", f1.AttributesJSON)
	}
	if f2, ok := rows[eiFeed2ARN]; !ok || f2.Status != nil {
		t.Errorf("f2 missing or has a status: %v", rows)
	}
	if _, ok := rows[eiFeed3ARN]; ok {
		t.Errorf("DELETED feed %s stored", eiFeed3ARN)
	}
	for id, want := range map[string]string{eiFeed4ARN: "DELETING", eiFeed5ARN: "ARCHIVED"} {
		if r, ok := rows[id]; !ok || sv(r.Status) != want {
			t.Errorf("feed %s missing or status != %s: %v", id, want, rows)
		}
	}
}

func TestScanEIFeeds_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanEIFeedsWith(st, func(any) (any, error) { return &elementalinference.ListFeedsOutput{}, nil })
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeElementalInferenceFeed)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// A denial warns once and keeps the rows listed before it; any other error is
// returned with those rows stored.
func TestScanEIFeeds_Errors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		firstPage bool
		err       error
		wantCode  string
		wantTotal int
		wantWarns int
	}{
		{name: "first page denied", firstPage: true, err: eiDenied, wantWarns: 1},
		{name: "first page other", firstPage: true, err: eiValidation, wantCode: "ValidationException"},
		{name: "page two denied", err: eiDenied, wantTotal: 1, wantWarns: 1},
		{name: "page two other", err: eiValidation, wantCode: "ValidationException", wantTotal: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "elemental-inference:ListFeeds")
			var pages eiPages
			handler := pages.feeds(tc.err)
			if tc.firstPage {
				handler = func(any) (any, error) { return nil, tc.err }
			}
			total, err := scanEIFeedsWith(st, handler)
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			rows := rowsByNativeID(t, st, TypeElementalInferenceFeed)
			if total != tc.wantTotal || len(rows) != tc.wantTotal || *warns != tc.wantWarns {
				t.Errorf("total = %d, rows = %d, warnings = %d; want %d, %d, %d", total, len(rows), *warns, tc.wantTotal, tc.wantTotal, tc.wantWarns)
			}
			if _, ok := rows[eiFeed1ARN]; tc.wantTotal == 1 && !ok {
				t.Errorf("missing %s in %v", eiFeed1ARN, rows)
			}
		})
	}
}

// Both pages are stored at page size 100, an element without an ARN and a
// DELETED dictionary are skipped, a DELETING one is kept, and Status is set
// only when the summary carries one.
func TestScanEIDictionaries_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	var pages eiPages
	total, err := scanEIDictionariesWith(st, pages.dictionaries(nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(pages.tokens, []string{"", "p2"}) {
		t.Errorf("tokens sent = %q, want [\"\" \"p2\"]", pages.tokens)
	}
	if !slices.Equal(pages.maxResults, []int32{100, 100}) {
		t.Errorf("page sizes sent = %v, want [100 100]", pages.maxResults)
	}
	rows := rowsByNativeID(t, st, TypeElementalInferenceDictionary)
	if total != 3 || len(rows) != 3 {
		t.Fatalf("total = %d, rows = %v; want d1, d2 and d4", total, rows)
	}
	d1, ok := rows[eiDict1ARN]
	if !ok {
		t.Fatalf("missing %s in %v", eiDict1ARN, rows)
	}
	if sv(d1.Name) != "sports-terms" || sv(d1.Status) != "AVAILABLE" || sv(d1.Region) != "us-east-1" {
		t.Errorf("d1 name/status/region = %q/%q/%q, want sports-terms/AVAILABLE/us-east-1", sv(d1.Name), sv(d1.Status), sv(d1.Region))
	}
	if !strings.Contains(d1.AttributesJSON, `"Language":"eng"`) {
		t.Errorf("d1 attrs = %s", d1.AttributesJSON)
	}
	d2, ok := rows[eiDict2ARN]
	if !ok {
		t.Fatalf("missing %s in %v", eiDict2ARN, rows)
	}
	if sv(d2.Name) != "players" || d2.Status != nil {
		t.Errorf("d2 name = %q, status = %v; want players, nil", sv(d2.Name), d2.Status)
	}
	if _, ok := rows[eiDict3ARN]; ok {
		t.Errorf("DELETED dictionary %s stored", eiDict3ARN)
	}
	if d4, ok := rows[eiDict4ARN]; !ok || sv(d4.Status) != "DELETING" {
		t.Errorf("dictionary %s missing or not DELETING: %v", eiDict4ARN, rows)
	}
}

func TestScanEIDictionaries_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanEIDictionariesWith(st, func(any) (any, error) { return &elementalinference.ListDictionariesOutput{}, nil })
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeElementalInferenceDictionary)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// On the first call, every shape an unrouted operation takes is a silent
// feature gap and a denial warns; any other error is returned.
func TestScanEIDictionaries_FirstPageError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantWarns int
	}{
		{name: "unknown operation", err: apiErr("UnknownOperationException", "")},
		{name: "unrouted gateway", err: apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")},
		{name: "http 404", err: respErrCode(404)},
		{name: "denied", err: eiDenied, wantWarns: 1},
		{name: "other", err: eiValidation, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "elemental-inference:ListDictionaries")
			total, err := scanEIDictionariesWith(st, func(any) (any, error) { return nil, tc.err })
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
func TestScanEIDictionaries_PageTwoError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantHTTP  int
		wantWarns int
	}{
		{name: "denied", err: eiDenied, wantWarns: 1},
		{name: "unknown operation", err: apiErr("UnknownOperationException", ""), wantCode: "UnknownOperationException"},
		{name: "http 404", err: respErrCode(404), wantHTTP: 404},
		{name: "other", err: eiValidation, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "elemental-inference:ListDictionaries")
			var pages eiPages
			total, err := scanEIDictionariesWith(st, pages.dictionaries(tc.err))
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
			rows := rowsByNativeID(t, st, TypeElementalInferenceDictionary)
			if _, ok := rows[eiDict1ARN]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want d1 only", total, rows)
			}
		})
	}
}

// In either phase, a failed write of the rows listed before a list error
// reports both errors.
func TestScanEIPhases_WriteAndListErrorsBothReported(t *testing.T) {
	for _, tc := range []struct {
		name string
		scan func(*store.Store, *eiPages) (int, error)
	}{
		{name: "feeds", scan: func(st *store.Store, p *eiPages) (int, error) { return scanEIFeedsWith(st, p.feeds(eiValidation)) }},
		{name: "dictionaries", scan: func(st *store.Store, p *eiPages) (int, error) {
			return scanEIDictionariesWith(st, p.dictionaries(eiValidation))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
			st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if err := st.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			var pages eiPages
			_, err = tc.scan(st, &pages)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Errorf("err = %v, want store.ErrStoreWrite", err)
			}
			assertAPICode(t, err, "ValidationException")
		})
	}
}

func TestScanElementalInference_StoresFeedsAndDictionaries(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var feeds, dicts eiPages
	var regions parentCalls
	cfg := opStubConfig(map[string]func(any) (any, error){
		"ListFeeds":        feeds.feeds(nil),
		"ListDictionaries": dicts.dictionaries(nil),
	}, eiDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, _, err := scanElementalInference(context.Background(), acct, "eu-west-1", st, testScanID)
	if err != nil {
		t.Fatalf("scanElementalInference: %v", err)
	}
	if total != 7 {
		t.Errorf("total = %d, want 7 (4 feeds, 3 dictionaries)", total)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	for typ, ids := range map[string][]string{
		TypeElementalInferenceFeed:       {eiFeed1ARN, eiFeed2ARN, eiFeed4ARN, eiFeed5ARN},
		TypeElementalInferenceDictionary: {eiDict1ARN, eiDict2ARN, eiDict4ARN},
	} {
		rows := rowsByNativeID(t, st, typ)
		for _, id := range ids {
			if r, ok := rows[id]; !ok || sv(r.Region) != "eu-west-1" {
				t.Errorf("%s row %s missing or not in eu-west-1: %v", typ, id, rows)
			}
		}
	}
}

// The dictionary phase runs after the feeds: its hard error reaches the
// caller with the feeds and its own page-one row stored and counted.
func TestScanElementalInference_DictionaryErrorAfterFeeds(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var feeds, dicts eiPages
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListFeeds":        feeds.feeds(nil),
		"ListDictionaries": dicts.dictionaries(eiValidation),
	}, eiDenied)

	total, _, err := scanElementalInference(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "ValidationException")
	if total != 5 {
		t.Errorf("total = %d, want 5 (4 feeds, 1 dictionary)", total)
	}
	if n := len(rowsByNativeID(t, st, TypeElementalInferenceFeed)); n != 4 {
		t.Errorf("feed rows = %d, want 4", n)
	}
	if _, ok := rowsByNativeID(t, st, TypeElementalInferenceDictionary)[eiDict1ARN]; !ok {
		t.Errorf("missing dictionary %s", eiDict1ARN)
	}
}

// A feed phase hard error stops the scan before the dictionaries and reaches
// the caller with the feeds listed before it counted.
func TestScanElementalInference_FeedErrorStopsScan(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var feeds, dicts eiPages
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListFeeds":        feeds.feeds(eiValidation),
		"ListDictionaries": dicts.dictionaries(nil),
	}, eiDenied)

	total, _, err := scanElementalInference(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "ValidationException")
	if total != 1 {
		t.Errorf("total = %d, want 1 (feed f1)", total)
	}
	if len(dicts.tokens) != 0 {
		t.Errorf("ListDictionaries called %d times after a feed error, want 0", len(dicts.tokens))
	}
}
