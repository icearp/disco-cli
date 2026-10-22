package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/customerprofiles"
	cptypes "github.com/aws/aws-sdk-go-v2/service/customerprofiles/types"
	"github.com/icearp/disco-cli/store"
)

const cpTestDomainARN = "arn:aws:profile:us-east-1:123456789012:domains/d1"

// cpTestItem is one listed item: its name (or ARN suffix) and tags.
type cpTestItem struct {
	name string
	tags map[string]string
}

// cpChildCase is one per-domain child list op: input reads the request the
// scanner sent, page builds a response holding items.
type cpChildCase struct {
	op, scanOp, typ string
	maxResults      int32
	tagged          bool // false when the element carries no tags
	// nativeIDs are the NativeIDs of the items named "<prefix>-1" and "-2".
	nativeIDs [2]string
	prefix    string
	input     func(t *testing.T, in any) (domain, token *string, maxResults *int32)
	page      func(items []cpTestItem, next *string) any
}

func cpChildCases() []cpChildCase {
	const base = cpTestDomainARN + "/"
	esARN := func(n string) *string {
		return ptrStr("arn:aws:profile:us-east-1:123456789012:domains/d1/event-streams/" + n)
	}
	segARN := func(n string) *string {
		return ptrStr("arn:aws:profile:us-east-1:123456789012:domains/d1/segment-definitions/" + n)
	}
	return []cpChildCase{
		{
			op: "ListCalculatedAttributeDefinitions", scanOp: cpCalcAttrDefsOp, typ: TypeCPCalculatedAttributeDefinition,
			maxResults: 100, tagged: true, prefix: "ca",
			nativeIDs: [2]string{base + "calculated-attributes/ca-1", base + "calculated-attributes/ca-2"},
			input: func(_ *testing.T, in any) (*string, *string, *int32) {
				i := in.(*customerprofiles.ListCalculatedAttributeDefinitionsInput)
				return i.DomainName, i.NextToken, i.MaxResults
			},
			page: func(items []cpTestItem, next *string) any {
				out := &customerprofiles.ListCalculatedAttributeDefinitionsOutput{NextToken: next}
				for _, it := range items {
					out.Items = append(out.Items, cptypes.ListCalculatedAttributeDefinitionItem{CalculatedAttributeName: nilIfEmpty(it.name), Tags: it.tags})
				}
				return out
			},
		},
		{
			op: "ListEventStreams", scanOp: cpEventStreamsOp, typ: TypeCPEventStream,
			maxResults: 100, tagged: true, prefix: "es",
			nativeIDs: [2]string{base + "event-streams/es-1", base + "event-streams/es-2"},
			input: func(_ *testing.T, in any) (*string, *string, *int32) {
				i := in.(*customerprofiles.ListEventStreamsInput)
				return i.DomainName, i.NextToken, i.MaxResults
			},
			page: func(items []cpTestItem, next *string) any {
				out := &customerprofiles.ListEventStreamsOutput{NextToken: next}
				for _, it := range items {
					out.Items = append(out.Items, cptypes.EventStreamSummary{EventStreamArn: esARN(it.name), EventStreamName: ptrStr(it.name), Tags: it.tags})
				}
				return out
			},
		},
		{
			op: "ListEventTriggers", scanOp: cpEventTriggersOp, typ: TypeCPEventTrigger,
			maxResults: 100, tagged: true, prefix: "et",
			nativeIDs: [2]string{base + "event-triggers/et-1", base + "event-triggers/et-2"},
			input: func(_ *testing.T, in any) (*string, *string, *int32) {
				i := in.(*customerprofiles.ListEventTriggersInput)
				return i.DomainName, i.NextToken, i.MaxResults
			},
			page: func(items []cpTestItem, next *string) any {
				out := &customerprofiles.ListEventTriggersOutput{NextToken: next}
				for _, it := range items {
					out.Items = append(out.Items, cptypes.EventTriggerSummaryItem{EventTriggerName: nilIfEmpty(it.name), Tags: it.tags})
				}
				return out
			},
		},
		{
			op: "ListIntegrations", scanOp: cpIntegrationsOp, typ: TypeCPIntegration,
			maxResults: 100, tagged: true, prefix: "in",
			nativeIDs: [2]string{base + "integrations/in-1", base + "integrations/in-2"},
			input: func(t *testing.T, in any) (*string, *string, *int32) {
				i := in.(*customerprofiles.ListIntegrationsInput)
				if i.IncludeHidden != nil {
					t.Errorf("ListIntegrations IncludeHidden = %v, want unset", *i.IncludeHidden)
				}
				return i.DomainName, i.NextToken, i.MaxResults
			},
			page: func(items []cpTestItem, next *string) any {
				out := &customerprofiles.ListIntegrationsOutput{NextToken: next}
				for _, it := range items {
					out.Items = append(out.Items, cptypes.ListIntegrationItem{Uri: nilIfEmpty(it.name), Tags: it.tags})
				}
				return out
			},
		},
		{
			op: "ListProfileObjectTypes", scanOp: cpObjectTypesOp, typ: TypeCPObjectType,
			maxResults: 100, tagged: true, prefix: "ot",
			nativeIDs: [2]string{base + "object-types/ot-1", base + "object-types/ot-2"},
			input: func(_ *testing.T, in any) (*string, *string, *int32) {
				i := in.(*customerprofiles.ListProfileObjectTypesInput)
				return i.DomainName, i.NextToken, i.MaxResults
			},
			page: func(items []cpTestItem, next *string) any {
				out := &customerprofiles.ListProfileObjectTypesOutput{NextToken: next}
				for _, it := range items {
					out.Items = append(out.Items, cptypes.ListProfileObjectTypeItem{ObjectTypeName: nilIfEmpty(it.name), Tags: it.tags})
				}
				return out
			},
		},
		{
			op: "ListSegmentDefinitions", scanOp: cpSegmentDefinitionsOp, typ: TypeCPSegmentDefinition,
			maxResults: 500, tagged: true, prefix: "sd",
			nativeIDs: [2]string{base + "segment-definitions/sd-1", base + "segment-definitions/sd-2"},
			input: func(_ *testing.T, in any) (*string, *string, *int32) {
				i := in.(*customerprofiles.ListSegmentDefinitionsInput)
				return i.DomainName, i.NextToken, i.MaxResults
			},
			page: func(items []cpTestItem, next *string) any {
				out := &customerprofiles.ListSegmentDefinitionsOutput{NextToken: next}
				for _, it := range items {
					out.Items = append(out.Items, cptypes.SegmentDefinitionItem{SegmentDefinitionArn: segARN(it.name), SegmentDefinitionName: ptrStr(it.name), Tags: it.tags})
				}
				return out
			},
		},
		{
			op: "ListRecommenders", scanOp: cpRecommendersOp, typ: TypeCPRecommender,
			maxResults: 100, tagged: true, prefix: "rc",
			nativeIDs: [2]string{base + "recommenders/rc-1", base + "recommenders/rc-2"},
			input: func(_ *testing.T, in any) (*string, *string, *int32) {
				i := in.(*customerprofiles.ListRecommendersInput)
				return i.DomainName, i.NextToken, i.MaxResults
			},
			page: func(items []cpTestItem, next *string) any {
				out := &customerprofiles.ListRecommendersOutput{NextToken: next}
				for _, it := range items {
					out.Recommenders = append(out.Recommenders, cptypes.RecommenderSummary{RecommenderName: nilIfEmpty(it.name), Tags: it.tags})
				}
				return out
			},
		},
		{
			op: "ListDomainLayouts", scanOp: cpLayoutsOp, typ: TypeCustomerProfilesLayouts,
			maxResults: 100, tagged: true, prefix: "ly",
			nativeIDs: [2]string{base + "layout/ly-1", base + "layout/ly-2"},
			input: func(_ *testing.T, in any) (*string, *string, *int32) {
				i := in.(*customerprofiles.ListDomainLayoutsInput)
				return i.DomainName, i.NextToken, i.MaxResults
			},
			page: func(items []cpTestItem, next *string) any {
				out := &customerprofiles.ListDomainLayoutsOutput{NextToken: next}
				for _, it := range items {
					out.Items = append(out.Items, cptypes.LayoutItem{LayoutDefinitionName: nilIfEmpty(it.name), Tags: it.tags})
				}
				return out
			},
		},
		{
			op: "ListDomainObjectTypes", scanOp: cpDomainObjectTypesOp, typ: TypeCustomerProfilesDomainObjectTypes,
			maxResults: 100, tagged: true, prefix: "dot",
			nativeIDs: [2]string{base + "object-type/dot-1", base + "object-type/dot-2"},
			input: func(_ *testing.T, in any) (*string, *string, *int32) {
				i := in.(*customerprofiles.ListDomainObjectTypesInput)
				return i.DomainName, i.NextToken, i.MaxResults
			},
			page: func(items []cpTestItem, next *string) any {
				out := &customerprofiles.ListDomainObjectTypesOutput{NextToken: next}
				for _, it := range items {
					out.Items = append(out.Items, cptypes.DomainObjectTypesListItem{ObjectTypeName: nilIfEmpty(it.name), Tags: it.tags})
				}
				return out
			},
		},
		{
			op: "ListRecommenderFilters", scanOp: cpRecommenderFiltersOp, typ: TypeCustomerProfilesRecommenderFilters,
			maxResults: 100, tagged: true, prefix: "rf",
			nativeIDs: [2]string{base + "recommender-filter/rf-1", base + "recommender-filter/rf-2"},
			input: func(_ *testing.T, in any) (*string, *string, *int32) {
				i := in.(*customerprofiles.ListRecommenderFiltersInput)
				return i.DomainName, i.NextToken, i.MaxResults
			},
			page: func(items []cpTestItem, next *string) any {
				out := &customerprofiles.ListRecommenderFiltersOutput{NextToken: next}
				for _, it := range items {
					out.RecommenderFilters = append(out.RecommenderFilters, cptypes.RecommenderFilterSummary{RecommenderFilterName: nilIfEmpty(it.name), Tags: it.tags})
				}
				return out
			},
		},
		{
			op: "ListRecommenderSchemas", scanOp: cpRecommenderSchemasOp, typ: TypeCustomerProfilesRecommenderSchemas,
			maxResults: 100, tagged: false, prefix: "rs",
			nativeIDs: [2]string{base + "recommender-schema/rs-1", base + "recommender-schema/rs-2"},
			input: func(_ *testing.T, in any) (*string, *string, *int32) {
				i := in.(*customerprofiles.ListRecommenderSchemasInput)
				return i.DomainName, i.NextToken, i.MaxResults
			},
			page: func(items []cpTestItem, next *string) any {
				out := &customerprofiles.ListRecommenderSchemasOutput{NextToken: next}
				for _, it := range items {
					out.RecommenderSchemas = append(out.RecommenderSchemas, cptypes.RecommenderSchemaSummary{RecommenderSchemaName: nilIfEmpty(it.name)})
				}
				return out
			},
		},
	}
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// cpPage is one scripted response: the items and NextToken of a page, or err.
// Items are tagged {"n": name} unless untagged is set.
type cpPage struct {
	items    []string
	next     string
	untagged bool
	err      error
}

// handler serves the op from script, keyed by domain then by the NextToken
// sent ("" for the first page).
// A request whose domain or token the script lacks, or whose MaxResults is
// not the model's cap, fails the test.
func (c cpChildCase) handler(t *testing.T, mu *sync.Mutex, calls map[string]int, script map[string]map[string]cpPage) func(in any) (any, error) {
	return func(in any) (any, error) {
		domain, token, maxResults := c.input(t, in)
		mu.Lock()
		calls[c.op]++
		mu.Unlock()
		if maxResults == nil || *maxResults != c.maxResults {
			t.Errorf("%s MaxResults = %v, want %d", c.op, maxResults, c.maxResults)
		}
		p, ok := script[sv(domain)][sv(token)]
		if !ok {
			return nil, fmt.Errorf("%s: unscripted domain %q token %q", c.op, sv(domain), sv(token))
		}
		if p.err != nil {
			return nil, p.err
		}
		items := make([]cpTestItem, len(p.items))
		for i, n := range p.items {
			items[i] = cpTestItem{name: n}
			if !p.untagged {
				items[i].tags = map[string]string{"n": n}
			}
		}
		return c.page(items, nilIfEmpty(p.next)), nil
	}
}

// twoPages is the default child script: d1 lists "<prefix>-1" with tags on
// page one and "<prefix>-2" without on page two; d2 lists nothing.
func (c cpChildCase) twoPages() map[string]map[string]cpPage {
	return map[string]map[string]cpPage{
		"d1": {"": {items: []string{c.prefix + "-1"}, next: "tok"}, "tok": {items: []string{c.prefix + "-2"}, untagged: true}},
		"d2": {"": {}},
	}
}

// cpHandlers serves every op: ListDomains lists d1 (tagged) on page one and
// d2 (untagged) on page two, GetDomain returns a body with
// DefaultExpirationDays and tags of its own, and each child op follows
// twoPages.
func cpHandlers(t *testing.T, calls map[string]int) map[string]func(in any) (any, error) {
	mu := &sync.Mutex{}
	count := func(op string) {
		mu.Lock()
		calls[op]++
		mu.Unlock()
	}
	h := map[string]func(in any) (any, error){
		"ListDomains": func(in any) (any, error) {
			count("ListDomains")
			i := in.(*customerprofiles.ListDomainsInput)
			if i.MaxResults == nil || *i.MaxResults != 100 {
				t.Errorf("ListDomains MaxResults = %v, want 100", i.MaxResults)
			}
			switch sv(i.NextToken) {
			case "":
				return &customerprofiles.ListDomainsOutput{
					Items:     []cptypes.ListDomainItem{{DomainName: ptrStr("d1"), Tags: map[string]string{"team": "crm"}}},
					NextToken: ptrStr("dtok"),
				}, nil
			case "dtok":
				return &customerprofiles.ListDomainsOutput{Items: []cptypes.ListDomainItem{{DomainName: ptrStr("d2")}}}, nil
			}
			return nil, fmt.Errorf("ListDomains: unexpected NextToken %q", sv(i.NextToken))
		},
		"GetDomain": func(in any) (any, error) {
			count("GetDomain")
			name := in.(*customerprofiles.GetDomainInput).DomainName
			return &customerprofiles.GetDomainOutput{DomainName: name, DefaultExpirationDays: ptrInt32(7), Tags: map[string]string{"from": "get"}}, nil
		},
	}
	for _, c := range cpChildCases() {
		h[c.op] = c.handler(t, mu, calls, c.twoPages())
	}
	return h
}

func cpClient(h map[string]func(in any) (any, error)) *customerprofiles.Client {
	return customerprofiles.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
}

// TestScanCPAll_StoresEveryTypeWithTags pins, per type, the NativeID, Name and
// tags of a tagged and an untagged item, read across two pages of every op
// with the model's MaxResults cap; the domain's tags come from its ListDomains
// element and its attributes from GetDomain.
func TestScanCPAll_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	total, inserted, err := scanCPAll(context.Background(), cpClient(cpHandlers(t, map[string]int{})), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 24 || inserted != 24 {
		t.Errorf("total, inserted = %d, %d; want 24, 24", total, inserted)
	}

	domains := rowsByNativeID(t, st, TypeCPDomain)
	if len(domains) != 2 {
		t.Fatalf("domains stored = %d, want 2", len(domains))
	}
	d1, ok := domains[cpTestDomainARN]
	if !ok {
		t.Fatalf("domain %s not stored; got %v", cpTestDomainARN, domains)
	}
	if sv(d1.Name) != "d1" {
		t.Errorf("domain Name = %q, want d1", sv(d1.Name))
	}
	assertTagsMap(t, d1.TagsJSON, map[string]string{"team": "crm"})
	var body struct{ DefaultExpirationDays *int32 }
	if err := json.Unmarshal([]byte(d1.AttributesJSON), &body); err != nil || body.DefaultExpirationDays == nil || *body.DefaultExpirationDays != 7 {
		t.Errorf("domain attributes = %s, want the GetDomain body (DefaultExpirationDays 7)", d1.AttributesJSON)
	}
	d2, ok := domains["arn:aws:profile:us-east-1:123456789012:domains/d2"]
	if !ok {
		t.Fatalf("domain d2 not stored; got %v", domains)
	}
	if d2.TagsJSON != nil {
		t.Errorf("untagged domain TagsJSON = %s, want nil", *d2.TagsJSON)
	}

	for _, c := range cpChildCases() {
		t.Run(c.typ, func(t *testing.T) {
			rows := rowsByNativeID(t, st, c.typ)
			if len(rows) != 2 {
				t.Fatalf("stored %d rows, want 2: %v", len(rows), rows)
			}
			for i, id := range c.nativeIDs {
				r, ok := rows[id]
				if !ok {
					t.Fatalf("row %s not stored; got %v", id, rows)
				}
				name := fmt.Sprintf("%s-%d", c.prefix, i+1)
				if sv(r.Name) != name {
					t.Errorf("%s Name = %q, want %q", id, sv(r.Name), name)
				}
				if i == 0 && c.tagged {
					assertTagsMap(t, r.TagsJSON, map[string]string{"n": name})
				} else if r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %s, want nil", id, *r.TagsJSON)
				}
			}
		})
	}
}

// TestCPListChildren_ErrorBranches pins how one domain's failed page is
// handled, through ListCalculatedAttributeDefinitions: the other domain's rows
// are always stored, and rows read before the failure are kept.
func TestCPListChildren_ErrorBranches(t *testing.T) {
	bad := apiErr("BadRequestException", "bad")
	denied := apiErr("AccessDeniedException", "denied")
	gone := apiErr("ResourceNotFoundException", "gone")
	const base = cpTestDomainARN + "/calculated-attributes/"
	const d2Row = "arn:aws:profile:us-east-1:123456789012:domains/d2/calculated-attributes/other"
	for _, c := range []struct {
		name     string
		d1       map[string]cpPage
		wantErr  error // nil: no error
		wantRows []string
		warnings int
	}{
		{"first page fails", map[string]cpPage{"": {err: bad}}, bad, []string{d2Row}, 0},
		{"later page fails", map[string]cpPage{"": {items: []string{"a"}, next: "tok"}, "tok": {err: bad}}, bad, []string{base + "a", d2Row}, 0},
		{"first page denied", map[string]cpPage{"": {err: denied}}, nil, []string{d2Row}, 1},
		{"later page denied", map[string]cpPage{"": {items: []string{"a"}, next: "tok"}, "tok": {err: denied}}, nil, []string{base + "a", d2Row}, 1},
		{"domain gone", map[string]cpPage{"": {err: gone}}, nil, []string{d2Row}, 0},
		{"domain gone on a later page", map[string]cpPage{"": {items: []string{"a"}, next: "tok"}, "tok": {err: gone}}, gone, []string{base + "a", d2Row}, 0},
		{"repeated token", map[string]cpPage{"": {items: []string{"a"}, next: "tok"}, "tok": {items: []string{"b"}, next: "tok"}}, errRepeatedNextToken, []string{base + "a", base + "b", d2Row}, 0},
		{"item without a name", map[string]cpPage{"": {items: []string{"", "a"}}}, nil, []string{base + "a", d2Row}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			cc := cpChildCases()[0]
			script := map[string]map[string]cpPage{"d1": c.d1, "d2": {"": {items: []string{"other"}}}}
			h := map[string]func(in any) (any, error){cc.op: cc.handler(t, &sync.Mutex{}, map[string]int{}, script)}
			st := newTestStore(t)
			warns := countOpWarnings(st, cpCalcAttrDefsOp)
			var scopes []string
			prev := st.OnWarn
			st.OnWarn = func(w store.ScanWarning) {
				scopes = append(scopes, w.Scope)
				prev(w)
			}
			total, _, err := scanCPCalcAttrDefs(context.Background(), cpClient(h), newTestAccount(testAccountID), testRegion, st, testScanID, []string{"d1", "d2"})
			if c.wantErr == nil && err != nil || c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != len(c.wantRows) {
				t.Errorf("total = %d, want %d", total, len(c.wantRows))
			}
			rows := rowsByNativeID(t, st, TypeCPCalculatedAttributeDefinition)
			if len(rows) != len(c.wantRows) {
				t.Errorf("stored %d rows, want %d: %v", len(rows), len(c.wantRows), rows)
			}
			for _, id := range c.wantRows {
				if _, ok := rows[id]; !ok {
					t.Errorf("row %s not stored", id)
				}
			}
			if *warns != c.warnings {
				t.Errorf("warnings = %d, want %d", *warns, c.warnings)
			}
			for _, s := range scopes {
				if s != testAccountID+"/"+testRegion {
					t.Errorf("warning scope = %q, want account/region", s)
				}
			}
		})
	}
}

// TestCPListChildren_FailedWriteWarnsNothing pins that a phase whose write
// failed records neither its denial nor its unnamed-item warning.
func TestCPListChildren_FailedWriteWarnsNothing(t *testing.T) {
	cc := cpChildCases()[0]
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, cpCalcAttrDefsOp)
	script := map[string]map[string]cpPage{
		"d1": {"": {items: []string{"", "a"}}},
		"d2": {"": {err: apiErr("AccessDeniedException", "denied")}},
	}
	serve := cc.handler(t, &sync.Mutex{}, map[string]int{}, script)
	h := map[string]func(in any) (any, error){cc.op: func(in any) (any, error) {
		closeStore()
		return serve(in)
	}}
	_, _, err := scanCPCalcAttrDefs(context.Background(), cpClient(h), newTestAccount(testAccountID), testRegion, st, testScanID, []string{"d1", "d2"})
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d, want 0", *warns)
	}
}

// TestScanCPDomains_GetDomainErrors pins each GetDomain outcome: the row kept
// or dropped, where its attributes come from, the warning and the error.
func TestScanCPDomains_GetDomainErrors(t *testing.T) {
	bad := apiErr("BadRequestException", "bad")
	for _, c := range []struct {
		name      string
		getErr    error
		wantRow   bool
		wantBody  bool // attributes are the GetDomain body
		wantErr   error
		getWarned int
		transient bool // the warning is skipIfTransient's, not skipIfAccessDenied's
	}{
		{"ok", nil, true, true, nil, 0, false},
		{"deleted", apiErr("ResourceNotFoundException", "gone"), false, false, nil, 0, false},
		{"denied", apiErr("AccessDeniedException", "denied"), true, false, nil, 1, false},
		{"throttled", apiErr("ThrottlingException", "slow down"), true, false, nil, 1, true},
		{"other", bad, true, false, bad, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := cpHandlers(t, map[string]int{})
			h["ListDomains"] = func(any) (any, error) {
				return &customerprofiles.ListDomainsOutput{Items: []cptypes.ListDomainItem{{DomainName: ptrStr("d1"), Tags: map[string]string{"team": "crm"}}}}, nil
			}
			if c.getErr != nil {
				h["GetDomain"] = func(any) (any, error) { return nil, c.getErr }
			}
			st := newTestStore(t)
			warns := recordWarnings(st)
			var errs phaseErrs
			names, total, _, werr := scanCPDomains(context.Background(), cpClient(h), newTestAccount(testAccountID), testRegion, st, testScanID, &errs)
			if werr != nil {
				t.Fatalf("write err = %v", werr)
			}
			err := errs.join(st, newTestAccount(testAccountID), testRegion)
			if c.wantErr == nil && err != nil || c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			got := cpOpWarnings(t, *warns, cpGetDomainOp, c.getWarned)
			for _, w := range got {
				if isTransientWarning(w) != c.transient {
					t.Errorf("GetDomain warning transient = %v, want %v: %+v", !c.transient, c.transient, w)
				}
			}
			rows := rowsByNativeID(t, st, TypeCPDomain)
			r, ok := rows[cpTestDomainARN]
			if ok != c.wantRow || total != len(rows) || len(names) != len(rows) {
				t.Fatalf("row stored = %v (total %d, names %v), want %v", ok, total, names, c.wantRow)
			}
			if !ok {
				return
			}
			assertTagsMap(t, r.TagsJSON, map[string]string{"team": "crm"})
			var body struct{ DefaultExpirationDays *int32 }
			if err := json.Unmarshal([]byte(r.AttributesJSON), &body); err != nil {
				t.Fatal(err)
			}
			if got := body.DefaultExpirationDays != nil; got != c.wantBody {
				t.Errorf("attributes from GetDomain = %v, want %v: %s", got, c.wantBody, r.AttributesJSON)
			}
		})
	}
}

// TestScanCPDomains_ListErrors pins that a failed ListDomains page keeps the
// domains read before it and hands them to the child phases.
func TestScanCPDomains_ListErrors(t *testing.T) {
	bad := apiErr("BadRequestException", "bad")
	denied := apiErr("AccessDeniedException", "denied")
	d1 := cptypes.ListDomainItem{DomainName: ptrStr("d1")}
	for _, c := range []struct {
		name      string
		pages     map[string]any // NextToken sent -> output or error
		wantErr   error
		wantNames int
		warnings  int
	}{
		{"first page denied", map[string]any{"": denied}, nil, 0, 1},
		{"later page denied", map[string]any{"": &customerprofiles.ListDomainsOutput{Items: []cptypes.ListDomainItem{d1}, NextToken: ptrStr("t")}, "t": denied}, nil, 1, 1},
		{"later page fails", map[string]any{"": &customerprofiles.ListDomainsOutput{Items: []cptypes.ListDomainItem{d1}, NextToken: ptrStr("t")}, "t": bad}, bad, 1, 0},
		{"repeated token", map[string]any{"": &customerprofiles.ListDomainsOutput{Items: []cptypes.ListDomainItem{d1}, NextToken: ptrStr("t")}, "t": &customerprofiles.ListDomainsOutput{NextToken: ptrStr("t")}}, errRepeatedNextToken, 1, 0},
		{"domain without a name", map[string]any{"": &customerprofiles.ListDomainsOutput{Items: []cptypes.ListDomainItem{{}, d1}}}, nil, 1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := cpHandlers(t, map[string]int{})
			h["ListDomains"] = func(in any) (any, error) {
				p := c.pages[sv(in.(*customerprofiles.ListDomainsInput).NextToken)]
				if err, ok := p.(error); ok {
					return nil, err
				}
				return p, nil
			}
			st := newTestStore(t)
			warns := recordWarnings(st)
			var errs phaseErrs
			names, _, _, werr := scanCPDomains(context.Background(), cpClient(h), newTestAccount(testAccountID), testRegion, st, testScanID, &errs)
			if werr != nil {
				t.Fatalf("write err = %v", werr)
			}
			err := errs.join(st, newTestAccount(testAccountID), testRegion)
			if c.wantErr == nil && err != nil || c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if len(names) != c.wantNames || len(rowsByNativeID(t, st, TypeCPDomain)) != c.wantNames {
				t.Errorf("names = %v, want %d stored", names, c.wantNames)
			}
			cpOpWarnings(t, *warns, cpListDomainsOp, c.warnings)
		})
	}
}

// cpOpWarnings returns the warnings recorded for op, failing the test unless
// there are want of them, each scoped to the test account and region.
func cpOpWarnings(t *testing.T, warns []store.ScanWarning, op string, want int) []store.ScanWarning {
	t.Helper()
	var got []store.ScanWarning
	for _, w := range warns {
		if w.Service == op {
			got = append(got, w)
		}
	}
	if len(got) != want {
		t.Errorf("%s warnings = %+v, want %d", op, got, want)
	}
	for _, w := range got {
		if w.Scope != testAccountID+"/"+testRegion {
			t.Errorf("%s warning scope = %q, want %s/%s", op, w.Scope, testAccountID, testRegion)
		}
	}
	return got
}

// isTransientWarning reports whether w came from skipIfTransient, which marks
// its warnings with a fixed "transient: " prefix; skipIfAccessDenied adds none.
func isTransientWarning(w store.ScanWarning) bool {
	return strings.HasPrefix(w.Message, "transient: ")
}

// TestScanCPDomains_FailedWriteWarnsNothing pins that a failed domain write is
// returned and records no warning about the domains it did not store.
func TestScanCPDomains_FailedWriteWarnsNothing(t *testing.T) {
	h := cpHandlers(t, map[string]int{})
	st, closeStore := openClosingStore(t)
	h["ListDomains"] = func(any) (any, error) {
		closeStore()
		return &customerprofiles.ListDomainsOutput{Items: []cptypes.ListDomainItem{{}, {DomainName: ptrStr("d1")}}}, nil
	}
	h["GetDomain"] = func(any) (any, error) { return nil, apiErr("AccessDeniedException", "denied") }
	var n int
	st.OnWarn = func(store.ScanWarning) { n++ }
	var errs phaseErrs
	_, _, _, werr := scanCPDomains(context.Background(), cpClient(h), newTestAccount(testAccountID), testRegion, st, testScanID, &errs)
	if !errors.Is(werr, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", werr)
	}
	if n != 0 {
		t.Errorf("warnings = %d, want 0", n)
	}
}

// TestScanCPAll_PhaseErrorRunsLaterPhases pins that a hard failure in a list
// phase, the domain listing included, does not stop the phases after it.
func TestScanCPAll_PhaseErrorRunsLaterPhases(t *testing.T) {
	bad := apiErr("BadRequestException", "bad")
	for _, c := range cpChildCases()[:len(cpChildCases())-1] {
		t.Run(c.op, func(t *testing.T) {
			h := cpHandlers(t, map[string]int{})
			h[c.op] = func(any) (any, error) { return nil, bad }
			st := newTestStore(t)
			_, _, err := scanCPAll(context.Background(), cpClient(h), newTestAccount(testAccountID), testRegion, st, testScanID)
			if !errors.Is(err, bad) {
				t.Fatalf("err = %v, want the %s error", err, c.op)
			}
			if n := len(rowsByNativeID(t, st, TypeCustomerProfilesRecommenderSchemas)); n != 2 {
				t.Errorf("recommender schemas stored = %d, want 2", n)
			}
		})
	}
	t.Run("ListDomains", func(t *testing.T) {
		h := cpHandlers(t, map[string]int{})
		listed := h["ListDomains"]
		h["ListDomains"] = func(in any) (any, error) {
			if sv(in.(*customerprofiles.ListDomainsInput).NextToken) == "dtok" {
				return nil, bad
			}
			return listed(in)
		}
		st := newTestStore(t)
		_, _, err := scanCPAll(context.Background(), cpClient(h), newTestAccount(testAccountID), testRegion, st, testScanID)
		if !errors.Is(err, bad) {
			t.Fatalf("err = %v, want the ListDomains error", err)
		}
		if n := len(rowsByNativeID(t, st, TypeCustomerProfilesRecommenderSchemas)); n != 2 {
			t.Errorf("recommender schemas of d1 stored = %d, want 2", n)
		}
	})
}

// TestScanCPAll_StoreWriteStopsPhases pins that a failed write stops the
// phases after it and is returned as ErrStoreWrite.
func TestScanCPAll_StoreWriteStopsPhases(t *testing.T) {
	cases := cpChildCases()
	for _, c := range []struct{ closeIn, notAfter string }{
		{"ListDomains", cases[0].op},
		{cases[0].op, cases[1].op},
		{cases[9].op, cases[10].op},
	} {
		t.Run(c.closeIn, func(t *testing.T) {
			calls := map[string]int{}
			h := cpHandlers(t, calls)
			st, closeStore := openClosingStore(t)
			serve := h[c.closeIn]
			h[c.closeIn] = func(in any) (any, error) {
				closeStore()
				return serve(in)
			}
			_, _, err := scanCPAll(context.Background(), cpClient(h), newTestAccount(testAccountID), testRegion, st, testScanID)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			if calls[c.notAfter] != 0 {
				t.Errorf("%s called %d times after the failed write, want 0", c.notAfter, calls[c.notAfter])
			}
		})
	}
}

// TestScanCPAll_TransientBesideHardError pins that a throttled phase beside a
// hard-failed one is demoted to one warning, so the scan reads as an error.
func TestScanCPAll_TransientBesideHardError(t *testing.T) {
	cases := cpChildCases()
	h := cpHandlers(t, map[string]int{})
	h[cases[0].op] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h[cases[1].op] = func(any) (any, error) { return nil, apiErr("BadRequestException", "bad") }
	st := newTestStore(t)
	warns := countOpWarnings(st, cases[0].scanOp)
	_, _, err := scanCPAll(context.Background(), cpClient(h), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError; err = %v", got, err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d, want 1", cases[0].scanOp, *warns)
	}
}
