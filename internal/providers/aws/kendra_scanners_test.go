package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kendra"
	kendratypes "github.com/aws/aws-sdk-go-v2/service/kendra/types"
	"github.com/icearp/disco-cli/store"
)

// The fixture: ListIndices serves index 1 on its first page and index 2 on
// its second. Each index has, over two pages, a "-t" then a "-b" item of
// every taggable kind (kendraTaggedKinds), plus one access control
// configuration and one experience (A in index 1, B in index 2).
// ListTagsForResource answers with tags for index 1 and every "-t" item.

const (
	kendraTestIdx1 = "11111111-1111-1111-1111-111111111111"
	kendraTestIdx2 = "22222222-2222-2222-2222-222222222222"
	kendraTestExpA = "arn:aws:kendra:us-east-1:123456789012:index/" + kendraTestIdx1 + "/experience/exp-a"
	kendraTestExpB = "arn:aws:kendra:us-east-1:123456789012:index/" + kendraTestIdx2 + "/experience/exp-b"
	kendraTestThes = "arn:aws:kendra:us-east-1:123456789012:index/" + kendraTestIdx1 + "/thesaurus/th1-t"

	kendraTestUser1 = "aaaaaaaa-0000-0000-0000-000000000001"
	kendraTestUser2 = "aaaaaaaa-0000-0000-0000-000000000002"
	kendraTestGroup = "bbbbbbbb-0000-0000-0000-000000000003"

	kendraTestEntA1 = kendraTestExpA + "/entity/" + kendraTestUser1
	kendraTestEntA2 = kendraTestExpA + "/entity/" + kendraTestGroup
	kendraTestEntB1 = kendraTestExpB + "/entity/" + kendraTestUser2
	kendraTestPerA1 = kendraTestExpA + "/persona/" + kendraTestUser1
	kendraTestPerA2 = kendraTestExpA + "/persona/" + kendraTestGroup
	kendraTestPerB1 = kendraTestExpB + "/persona/" + kendraTestUser2

	// kendraTotal is every row the fixture stores: per index, the index,
	// 10 taggable items, an access control configuration and an
	// experience; then 3 entities and 3 personas.
	kendraTotal = 2*(1+10+1+1) + 3 + 3
)

var (
	kendraDenied         = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: kendra:ListExperienceEntities")
	kendraBadInput       = apiErr("ConflictException", "bad")
	kendraThrottled      = apiErr("ThrottlingException", "slow down")
	kendraGone           = apiErr("ResourceNotFoundException", "index gone")
	kendraTagUnavailable = apiErr("ResourceUnavailableException", "resource unavailable")
	kendraCreated        = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

// kendraTestARN is the commercial ARN of res in the test account and region,
// spelled out rather than built with arnFor, so that it pins the Service
// Reference ARN format.
func kendraTestARN(res string) string { return "arn:aws:kendra:us-east-1:123456789012:" + res }

func kendraIndexARN(index string) string { return kendraTestARN("index/" + index) }

// kendraNum is a fixture index's number.
func kendraNum(index string) string {
	if index == kendraTestIdx2 {
		return "2"
	}
	return "1"
}

// kendraChildID is the id of index's item of a kind on page ("t" or "b").
func kendraChildID(short, index, page string) string { return short + kendraNum(index) + "-" + page }

func kendraIndexSummary(index string) kendratypes.IndexConfigurationSummary {
	return kendratypes.IndexConfigurationSummary{
		Id: sdkaws.String(index), Name: sdkaws.String("index " + kendraNum(index)),
		Status: kendratypes.IndexStatusActive, Edition: kendratypes.IndexEditionEnterpriseEdition, CreatedAt: &kendraCreated,
	}
}

func kendraDescribe(index string) *kendra.DescribeIndexOutput {
	return &kendra.DescribeIndexOutput{
		Id: sdkaws.String(index), Name: sdkaws.String("index " + kendraNum(index)),
		Status: kendratypes.IndexStatusActive, Edition: kendratypes.IndexEditionEnterpriseEdition, CreatedAt: &kendraCreated,
		RoleArn: sdkaws.String("arn:aws:iam::123456789012:role/kendra-" + kendraNum(index)),
	}
}

func kendraACC(index string) kendratypes.AccessControlConfigurationSummary {
	return kendratypes.AccessControlConfigurationSummary{Id: sdkaws.String("acc" + kendraNum(index))}
}

func kendraExperienceOf(index string) kendratypes.ExperiencesSummary {
	id := "exp-a"
	if index == kendraTestIdx2 {
		id = "exp-b"
	}
	return kendratypes.ExperiencesSummary{Id: sdkaws.String(id), Name: sdkaws.String(id), Status: kendratypes.ExperienceStatusActive, CreatedAt: &kendraCreated}
}

// kendraTaggedKind is a per-index kind whose tags are read: its listing, the
// ARN segment of its items, the prefix of their ids, the element with an id
// and the listing's page of one element.
type kendraTaggedKind struct {
	op, typ, kind, short string
	elem                 func(id string) any
	out                  func(elem any, next *string) any
}

var kendraTaggedKinds = []kendraTaggedKind{
	{
		"ListDataSources", TypeKendraDataSource, "data-source", "ds",
		func(id string) any {
			return kendratypes.DataSourceSummary{Id: &id, Name: sdkaws.String("name " + id), Status: kendratypes.DataSourceStatusActive, Type: kendratypes.DataSourceTypeS3, CreatedAt: &kendraCreated}
		},
		func(e any, next *string) any {
			return &kendra.ListDataSourcesOutput{SummaryItems: []kendratypes.DataSourceSummary{e.(kendratypes.DataSourceSummary)}, NextToken: next}
		},
	},
	{
		"ListFaqs", TypeKendraFaq, "faq", "faq",
		func(id string) any {
			return kendratypes.FaqSummary{Id: &id, Name: sdkaws.String("name " + id), Status: kendratypes.FaqStatusActive, CreatedAt: &kendraCreated}
		},
		func(e any, next *string) any {
			return &kendra.ListFaqsOutput{FaqSummaryItems: []kendratypes.FaqSummary{e.(kendratypes.FaqSummary)}, NextToken: next}
		},
	},
	{
		"ListFeaturedResultsSets", TypeKendraFeaturedResultsSet, "featured-results-set", "frs",
		func(id string) any {
			return kendratypes.FeaturedResultsSetSummary{FeaturedResultsSetId: &id, FeaturedResultsSetName: sdkaws.String("name " + id), Status: kendratypes.FeaturedResultsSetStatusActive, CreationTimestamp: sdkaws.Int64(1767323045000)}
		},
		func(e any, next *string) any {
			return &kendra.ListFeaturedResultsSetsOutput{FeaturedResultsSetSummaryItems: []kendratypes.FeaturedResultsSetSummary{e.(kendratypes.FeaturedResultsSetSummary)}, NextToken: next}
		},
	},
	{
		"ListQuerySuggestionsBlockLists", TypeKendraQuerySuggestionsBlockList, "query-suggestions-block-list", "bl",
		func(id string) any {
			return kendratypes.QuerySuggestionsBlockListSummary{Id: &id, Name: sdkaws.String("name " + id), Status: kendratypes.QuerySuggestionsBlockListStatusActive, ItemCount: sdkaws.Int32(3), CreatedAt: &kendraCreated}
		},
		func(e any, next *string) any {
			return &kendra.ListQuerySuggestionsBlockListsOutput{BlockListSummaryItems: []kendratypes.QuerySuggestionsBlockListSummary{e.(kendratypes.QuerySuggestionsBlockListSummary)}, NextToken: next}
		},
	},
	{
		"ListThesauri", TypeKendraThesaurus, "thesaurus", "th",
		func(id string) any {
			return kendratypes.ThesaurusSummary{Id: &id, Name: sdkaws.String("name " + id), Status: kendratypes.ThesaurusStatusActive, CreatedAt: &kendraCreated}
		},
		func(e any, next *string) any {
			return &kendra.ListThesauriOutput{ThesaurusSummaryItems: []kendratypes.ThesaurusSummary{e.(kendratypes.ThesaurusSummary)}, NextToken: next}
		},
	},
}

// kendraChildARN is the ARN of index's item of kind k on page.
func kendraChildARN(k kendraTaggedKind, index, page string) string {
	return kendraTestARN("index/" + index + "/" + k.kind + "/" + kendraChildID(k.short, index, page))
}

// kendraWant is a row the fixture stores: its Name ("" for none), its Status
// (nil for none) and the body its attributes are.
type kendraWant struct {
	typ, arn, name string
	status         *string
	element        any
}

// kendraRows is every index and per-index row the fixture stores.
func kendraRows() []kendraWant {
	active := sdkaws.String("ACTIVE")
	var rows []kendraWant
	for _, idx := range []string{kendraTestIdx1, kendraTestIdx2} {
		rows = append(rows, kendraWant{TypeKendraIndex, kendraIndexARN(idx), "index " + kendraNum(idx), active, kendraDescribe(idx)})
		for _, k := range kendraTaggedKinds {
			for _, page := range []string{"t", "b"} {
				id := kendraChildID(k.short, idx, page)
				rows = append(rows, kendraWant{k.typ, kendraChildARN(k, idx, page), "name " + id, active, k.elem(id)})
			}
		}
		acc, exp := kendraACC(idx), kendraExperienceOf(idx)
		rows = append(rows,
			kendraWant{TypeKendraAccessControlConfiguration, kendraTestARN("index/" + idx + "/access-control-configuration/" + sv(acc.Id)), "", nil, acc},
			kendraWant{TypeKendraExperience, kendraTestARN("index/" + idx + "/experience/" + sv(exp.Id)), sv(exp.Id), active, exp},
		)
	}
	return rows
}

// kendraCalls records the stubbed calls and answers ListTagsForResource from
// tags and errs, keyed by ARN.
type kendraCalls struct{ *bacCalls }

func newKendraCalls() *kendraCalls {
	c := &kendraCalls{newBACCalls()}
	c.tags[kendraIndexARN(kendraTestIdx1)] = map[string]string{"type": TypeKendraIndex}
	for _, k := range kendraTaggedKinds {
		for _, idx := range []string{kendraTestIdx1, kendraTestIdx2} {
			c.tags[kendraChildARN(k, idx, "t")] = map[string]string{"type": k.typ}
		}
	}
	return c
}

func (c *kendraCalls) listTags(in any) (any, error) {
	arn := sv(in.(*kendra.ListTagsForResourceInput).ResourceARN)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var out []kendratypes.Tag
	for k, v := range tags {
		out = append(out, kendratypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &kendra.ListTagsForResourceOutput{Tags: out}, nil
}

// kendraIndexOf is the index a per-index listing was sent.
func kendraIndexOf(in any) string { return sv(bacField[*string](in, "IndexId")) }

// perIndex answers op for the index it is sent, page "t" first, then "b".
func (c *kendraCalls) perIndex(op string, out func(index, page string, next *string) any) func(any) (any, error) {
	return c.answer(op, func(in any) any {
		if bacField[*string](in, "NextToken") == nil {
			return out(kendraIndexOf(in), "t", sdkaws.String("p2"))
		}
		return out(kendraIndexOf(in), "b", nil)
	})
}

// kendraExpKey names an experience by the (index, experience) pair the child
// ops take, so a call carrying the wrong index finds nothing.
func kendraExpKey(indexID, expID *string) string {
	return sdkaws.ToString(indexID) + "/" + sdkaws.ToString(expID)
}

// kendraHandlers answers every Kendra op the scan calls with the fixture.
// Experience A's entities and personas come over two pages (plus an element
// without an entity id to skip), B's in one. Each child call is recorded by
// experience NativeID in entityCalls / personaCalls.
func kendraHandlers(c *kendraCalls, entityCalls, personaCalls *parentCalls) map[string]func(in any) (any, error) {
	byExp := map[string]string{
		kendraExpKey(sdkaws.String(kendraTestIdx1), sdkaws.String("exp-a")): kendraTestExpA,
		kendraExpKey(sdkaws.String(kendraTestIdx2), sdkaws.String("exp-b")): kendraTestExpB,
	}
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListIndices": c.answer("ListIndices", func(in any) any {
			if in.(*kendra.ListIndicesInput).NextToken == nil {
				return &kendra.ListIndicesOutput{IndexConfigurationSummaryItems: []kendratypes.IndexConfigurationSummary{kendraIndexSummary(kendraTestIdx1)}, NextToken: sdkaws.String("p2")}
			}
			return &kendra.ListIndicesOutput{IndexConfigurationSummaryItems: []kendratypes.IndexConfigurationSummary{kendraIndexSummary(kendraTestIdx2)}}
		}),
		"DescribeIndex": c.answer("DescribeIndex", func(in any) any { return kendraDescribe(sv(in.(*kendra.DescribeIndexInput).Id)) }),
		"ListAccessControlConfigurations": c.answer("ListAccessControlConfigurations", func(in any) any {
			return &kendra.ListAccessControlConfigurationsOutput{AccessControlConfigurations: []kendratypes.AccessControlConfigurationSummary{kendraACC(kendraIndexOf(in))}}
		}),
		"ListExperiences": c.answer("ListExperiences", func(in any) any {
			return &kendra.ListExperiencesOutput{SummaryItems: []kendratypes.ExperiencesSummary{kendraExperienceOf(kendraIndexOf(in))}}
		}),
		"ListExperienceEntities": func(in any) (any, error) {
			p := in.(*kendra.ListExperienceEntitiesInput)
			exp, ok := byExp[kendraExpKey(p.IndexId, p.Id)]
			if !ok {
				return nil, apiErr("ResourceNotFoundException", "unknown experience")
			}
			entityCalls.add(exp)
			if exp == kendraTestExpB {
				return &kendra.ListExperienceEntitiesOutput{SummaryItems: []kendratypes.ExperienceEntitiesSummary{
					{EntityId: sdkaws.String(kendraTestUser2), EntityType: kendratypes.EntityTypeUser},
				}}, nil
			}
			if p.NextToken == nil {
				return &kendra.ListExperienceEntitiesOutput{
					SummaryItems: []kendratypes.ExperienceEntitiesSummary{
						{EntityId: sdkaws.String(kendraTestUser1), EntityType: kendratypes.EntityTypeUser},
						{EntityType: kendratypes.EntityTypeUser},
					},
					NextToken: sdkaws.String("page2"),
				}, nil
			}
			return &kendra.ListExperienceEntitiesOutput{SummaryItems: []kendratypes.ExperienceEntitiesSummary{
				{
					EntityId: sdkaws.String(kendraTestGroup), EntityType: kendratypes.EntityTypeGroup,
					DisplayData: &kendratypes.EntityDisplayData{GroupName: sdkaws.String("search-admins")},
				},
			}}, nil
		},
		"ListEntityPersonas": func(in any) (any, error) {
			p := in.(*kendra.ListEntityPersonasInput)
			exp, ok := byExp[kendraExpKey(p.IndexId, p.Id)]
			if !ok {
				return nil, apiErr("ResourceNotFoundException", "unknown experience")
			}
			personaCalls.add(exp)
			if sdkaws.ToInt32(p.MaxResults) != kendraEntityPersonaPageLimit {
				return nil, apiErr("ValidationException", "MaxResults must be at most 100")
			}
			if exp == kendraTestExpB {
				return &kendra.ListEntityPersonasOutput{SummaryItems: []kendratypes.PersonasSummary{
					{EntityId: sdkaws.String(kendraTestUser2), Persona: kendratypes.PersonaViewer},
				}}, nil
			}
			if p.NextToken == nil {
				return &kendra.ListEntityPersonasOutput{
					SummaryItems: []kendratypes.PersonasSummary{
						{EntityId: sdkaws.String(kendraTestUser1), Persona: kendratypes.PersonaViewer},
						{Persona: kendratypes.PersonaViewer},
					},
					NextToken: sdkaws.String("page2"),
				}, nil
			}
			return &kendra.ListEntityPersonasOutput{SummaryItems: []kendratypes.PersonasSummary{
				{EntityId: sdkaws.String(kendraTestGroup), Persona: kendratypes.PersonaOwner, CreatedAt: sdkaws.Time(kendraCreated)},
			}}, nil
		},
	}
	for _, k := range kendraTaggedKinds {
		h[k.op] = c.perIndex(k.op, func(index, page string, next *string) any {
			return k.out(k.elem(kendraChildID(k.short, index, page)), next)
		})
	}
	return h
}

// kendraFixture is kendraHandlers with fresh calls and no recorded child
// experiences.
func kendraFixture() (*kendraCalls, map[string]func(in any) (any, error)) {
	c := newKendraCalls()
	var entityCalls, personaCalls parentCalls
	return c, kendraHandlers(c, &entityCalls, &personaCalls)
}

// kendraListOps is every listing made per index or for the account, with
// MaxResults, in phase order.
var kendraListOps = []string{
	"ListIndices", "ListDataSources", "ListFaqs", "ListAccessControlConfigurations", "ListExperiences",
	"ListFeaturedResultsSets", "ListQuerySuggestionsBlockLists", "ListThesauri",
}

// kendraScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func kendraScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, kendraDenied)
	return scanKendra(ctx, acct, testRegion, st, testScanID)
}

func scanKendraTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	total, _, err := kendraScan(context.Background(), t, st, handlers)
	return total, err
}

func kendraRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Indices and the five taggable per-index kinds store the tags
// ListTagsForResource returns for their ARN, nil for a resource without
// tags; exactly their ARNs, in the Service Reference format, are sent to the
// tag op, and no access control configuration, experience, entity or
// persona is. Both index pages and both pages of every index's taggable
// listings are read, every listing asks for the model's page cap of 100,
// and the scan reports no error or warning. Each row keeps the base scan's
// Name, Status, verbatim element (an index its DescribeIndex body) and an
// unset CreatedAt.
func TestScanKendra_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls, h := kendraFixture()

	total, inserted, err := kendraScan(context.Background(), t, st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != kendraTotal || inserted != kendraTotal {
		t.Errorf("total, inserted = %d, %d; want %d", total, inserted, kendraTotal)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	for _, want := range kendraRows() {
		r, ok := kendraRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		if tagged, ok := calls.tags[want.arn]; ok {
			assertTagsMap(t, r.TagsJSON, tagged)
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", want.typ, want.arn, *r.TagsJSON)
		}
		if want.typ != TypeKendraAccessControlConfiguration && want.typ != TypeKendraExperience {
			wantTagARNs = append(wantTagARNs, want.arn)
		}
		if sv(r.Name) != want.name || (r.Name == nil) != (want.name == "") {
			t.Errorf("%s %s Name = %v; want %q", want.typ, want.arn, r.Name, want.name)
		}
		if (r.Status == nil) != (want.status == nil) || sv(r.Status) != sv(want.status) {
			t.Errorf("%s %s Status = %v; want %v", want.typ, want.arn, r.Status, want.status)
		}
		if r.CreatedAt != nil {
			t.Errorf("%s %s CreatedAt = %s; want unset", want.typ, want.arn, *r.CreatedAt)
		}
		if sv(r.Region) != testRegion || r.AttributesJSON != mustJSON(want.element) {
			t.Errorf("%s %s Region, attributes = %v, %s; want %s, %s", want.typ, want.arn, r.Region, r.AttributesJSON, testRegion, mustJSON(want.element))
		}
	}
	if len(wantTagARNs) != 22 {
		t.Fatalf("fixture taggable rows = %d; want 22", len(wantTagARNs))
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	wantCalls := map[string]int{"ListIndices": 2, "ListAccessControlConfigurations": 2, "ListExperiences": 2}
	for _, op := range kendraListOps {
		want, ok := wantCalls[op]
		if !ok {
			want = 4 // two pages for each of two indices
		}
		caps := calls.maxResults[op]
		if len(caps) != want {
			t.Errorf("%s calls with MaxResults = %d; want %d", op, len(caps), want)
		}
		for _, n := range caps {
			if n != 100 {
				t.Errorf("%s MaxResults = %d; want 100", op, n)
			}
		}
	}
}

// An aws-us-gov account scanned in us-gov-west-1 sends gov ARNs to the tag
// op and stores their tags.
func TestScanKendra_GovCloudARNsReachTagOp(t *testing.T) {
	const govRegion = "us-gov-west-1"
	const govPrefix = "arn:aws-us-gov:kendra:us-gov-west-1:123456789012:"
	index := govPrefix + "index/" + kendraTestIdx1
	thesaurus := govPrefix + "index/" + kendraTestIdx1 + "/thesaurus/th1-t"
	st := newTestStore(t)
	calls, h := kendraFixture()
	calls.tags[index] = map[string]string{"env": "gov"}
	calls.tags[thesaurus] = map[string]string{"env": "gov-thesaurus"}
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, kendraDenied)

	total, _, err := scanKendra(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != kendraTotal {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, kendraTotal)
	}
	if len(calls.tagARNs) != 22 {
		t.Errorf("tag reads = %d; want 22", len(calls.tagARNs))
	}
	for _, arn := range calls.tagARNs {
		if !strings.HasPrefix(arn, govPrefix) {
			t.Errorf("tag read ARN %s; want the %s prefix", arn, govPrefix)
		}
	}
	for _, c := range []struct{ typ, arn, env string }{
		{TypeKendraIndex, index, "gov"}, {TypeKendraThesaurus, thesaurus, "gov-thesaurus"},
	} {
		if !slices.Contains(calls.tagARNs, c.arn) {
			t.Errorf("ListTagsForResource never sent %s", c.arn)
		}
		r, ok := kendraRowOf(t, st, c.typ, c.arn)
		if !ok {
			t.Fatalf("%s %s not stored", c.typ, c.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"env": c.env})
		if sv(r.Region) != govRegion {
			t.Errorf("%s Region = %v; want %s", c.arn, r.Region, govRegion)
		}
	}
}

// No index stores nothing, makes no per-index listing, reads no tags and
// reports nothing. Indices whose listings are empty store only the indices,
// each per-index listing made once per index.
func TestScanKendra_EmptyListings(t *testing.T) {
	t.Run("no index", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls, h := kendraFixture()
		h["ListIndices"] = calls.answer("ListIndices", func(any) any { return &kendra.ListIndicesOutput{} })

		total, inserted, err := kendraScan(context.Background(), t, st, h)
		if err != nil || total != 0 || inserted != 0 {
			t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
		}
		if *warns != 0 || len(calls.tagARNs) != 0 || calls.count("DescribeIndex") != 0 || calls.count("ListThesauri") != 0 || calls.count("ListExperiences") != 0 {
			t.Errorf("warnings, tag reads, DescribeIndex, ListThesauri, ListExperiences calls = %d, %d, %d, %d, %d; want 0",
				*warns, len(calls.tagARNs), calls.count("DescribeIndex"), calls.count("ListThesauri"), calls.count("ListExperiences"))
		}
	})
	t.Run("indices without children", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls, h := kendraFixture()
		empty := map[string]any{
			"ListDataSources":                 &kendra.ListDataSourcesOutput{},
			"ListFaqs":                        &kendra.ListFaqsOutput{},
			"ListAccessControlConfigurations": &kendra.ListAccessControlConfigurationsOutput{},
			"ListExperiences":                 &kendra.ListExperiencesOutput{},
			"ListFeaturedResultsSets":         &kendra.ListFeaturedResultsSetsOutput{},
			"ListQuerySuggestionsBlockLists":  &kendra.ListQuerySuggestionsBlockListsOutput{},
			"ListThesauri":                    &kendra.ListThesauriOutput{},
		}
		for op, out := range empty {
			h[op] = calls.answer(op, func(any) any { return out })
		}

		total, _, err := kendraScan(context.Background(), t, st, h)
		if err != nil || total != 2 {
			t.Fatalf("scan = %d, %v; want 2, nil", total, err)
		}
		for op := range empty {
			if n := calls.count(op); n != 2 {
				t.Errorf("%s calls = %d; want 2, one per index", op, n)
			}
		}
		got := slices.Clone(calls.tagARNs)
		slices.Sort(got)
		if want := []string{kendraIndexARN(kendraTestIdx1), kendraIndexARN(kendraTestIdx2)}; *warns != 0 || !slices.Equal(got, want) {
			t.Errorf("warnings, tag reads = %d, %v; want 0, %v", *warns, got, want)
		}
	})
}

// A rescan stores the same rows without inserting any.
func TestScanKendra_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	_, h := kendraFixture()
	if _, _, err := kendraScan(context.Background(), t, st, h); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	_, h = kendraFixture()
	total, inserted, err := kendraScan(context.Background(), t, st, h)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != kendraTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, kendraTotal)
	}
}

// A failed tag read keeps the row without tags, whatever the code: a denial
// warns once under kendra:ListTagsForResource, a throttle after retries is a
// transient error, and any other code is a hard one. The op models no
// not-found, so a ResourceUnavailableException is hard too and drops
// nothing: an index answered so stays a parent, and every row is stored.
func TestScanKendra_TagReadFailures(t *testing.T) {
	for _, target := range []struct{ typ, arn string }{
		{TypeKendraThesaurus, kendraTestThes},
		{TypeKendraIndex, kendraIndexARN(kendraTestIdx2)},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantWarns int
			}{
				{"denied", kendraDenied, 0, "", 1},
				{"unavailable", kendraTagUnavailable, outcomeError, "ResourceUnavailableException", 0},
				{"other error", apiErr("ValidationException", "bad arn"), outcomeError, "ValidationException", 0},
				{"transient", kendraThrottled, outcomeTransient, "", 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, kendraTagsOp)
					calls, h := kendraFixture()
					calls.errs[target.arn] = c.err

					total, _, err := kendraScan(context.Background(), t, st, h)
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.code != "" {
						assertAPICode(t, err, c.code)
					}
					r, ok := kendraRowOf(t, st, target.typ, target.arn)
					if !ok {
						t.Fatal("row not stored; want it kept")
					}
					if r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if stored := countStoredRows(t, st); total != kendraTotal || stored != kendraTotal {
						t.Errorf("total = %d, stored rows = %d; want %d", total, stored, kendraTotal)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", kendraTagsOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// Denied tag reads in several phases are one warning for the scan.
func TestScanKendra_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, kendraTagsOp)
	calls, h := kendraFixture()
	denied := []struct{ typ, arn string }{
		{TypeKendraIndex, kendraIndexARN(kendraTestIdx1)},
		{TypeKendraFeaturedResultsSet, kendraChildARN(kendraTaggedKinds[2], kendraTestIdx2, "b")},
	}
	for _, d := range denied {
		calls.errs[d.arn] = kendraDenied
	}

	if _, _, err := kendraScan(context.Background(), t, st, h); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("%s warnings = %v; want one, scoped %s/%s", kendraTagsOp, *warnings, testAccountID, testRegion)
	}
	for _, d := range denied {
		if got, ok := kendraRowOf(t, st, d.typ, d.arn); !ok || got.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", d.arn, ok, got.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanKendra_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, kendraTagsOp)
	calls, h := kendraFixture()
	calls.errs[kendraChildARN(kendraTaggedKinds[0], kendraTestIdx1, "t")] = kendraThrottled
	calls.errs[kendraChildARN(kendraTaggedKinds[1], kendraTestIdx2, "b")] = apiErr("ValidationException", "bad arn")

	_, _, err := kendraScan(context.Background(), t, st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", kendraTagsOp, *warns)
	}
}

// A DescribeIndex ResourceNotFoundException drops the index, which is then
// neither tag-read nor a parent. Any other failure keeps the index with its
// list element as the attributes and its tags read: a denial warns once
// under kendra:DescribeIndex, a throttle after retries is a transient error,
// any other code a hard one.
func TestScanKendra_DescribeIndexFailures(t *testing.T) {
	arn := kendraIndexARN(kendraTestIdx2)
	for _, c := range []struct {
		name      string
		err       error
		kept      bool
		outcome   serviceOutcome
		wantWarns int
	}{
		{"not found", kendraGone, false, 0, 0},
		{"denied", kendraDenied, true, 0, 1},
		{"other error", kendraBadInput, true, outcomeError, 0},
		{"transient", kendraThrottled, true, outcomeTransient, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, kendraDescribeIndexOp)
			calls, h := kendraFixture()
			describe := h["DescribeIndex"]
			h["DescribeIndex"] = func(in any) (any, error) {
				if sv(in.(*kendra.DescribeIndexInput).Id) == kendraTestIdx2 {
					return nil, c.err
				}
				return describe(in)
			}

			total, _, err := kendraScan(context.Background(), t, st, h)
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
			}
			if c.outcome == outcomeError {
				assertAPICode(t, err, "ConflictException")
			}
			r, ok := kendraRowOf(t, st, TypeKendraIndex, arn)
			if ok != c.kept {
				t.Fatalf("index 2 stored = %v; want %v", ok, c.kept)
			}
			if slices.Contains(calls.tagARNs, arn) != c.kept {
				t.Errorf("index 2 tag read = %v; want %v", !c.kept, c.kept)
			}
			want := kendraTotal
			if c.kept {
				if r.AttributesJSON != mustJSON(kendraIndexSummary(kendraTestIdx2)) {
					t.Errorf("attributes = %s; want the list element", r.AttributesJSON)
				}
			} else {
				want -= 15
				if n := calls.count("ListThesauri"); n != 2 {
					t.Errorf("ListThesauri calls = %d; want 2, for index 1 only", n)
				}
			}
			if total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if len(*warnings) != c.wantWarns {
				t.Errorf("%s warnings = %v; want %d", kendraDescribeIndexOp, *warnings, c.wantWarns)
			}
		})
	}
}

// A denied listing warns once under its own op and costs no other phase its
// rows; a denied index listing leaves no index to read children from.
func TestScanKendra_DeniedListingWarnsUnderItsOp(t *testing.T) {
	// Each per-index listing stores 2 rows a page, except the access control
	// configurations and experiences, one per index; denied experiences also
	// cost their 6 entities and personas.
	lost := map[string]int{"ListIndices": kendraTotal, "ListAccessControlConfigurations": 2, "ListExperiences": 8}
	for _, op := range kendraListOps {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "kendra:"+op)
			_, h := kendraFixture()
			h[op] = func(any) (any, error) { return nil, kendraDenied }

			total, _, err := kendraScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (kendra:%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
			want := kendraTotal - 4
			if n, ok := lost[op]; ok {
				want = kendraTotal - n
			}
			if stored := countStoredRows(t, st); total != want || stored != want {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, want)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanKendra_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeKendraDataSource)
	calls, h := kendraFixture()
	calls.errs[kendraChildARN(kendraTaggedKinds[0], kendraTestIdx1, "t")] = kendraDenied

	_, _, err := kendraScan(context.Background(), t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListFaqs", "ListExperiences", "ListThesauri"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A failed page keeps the pages read before it, and no listing's failure
// stops the indices or phases after it. An index a per-index listing no
// longer finds (ResourceNotFoundException) is skipped silently when nothing
// was read from it, and with one warning when a page was; any other error is
// returned for that index only.
func TestScanKendra_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	t.Run("index listing", func(t *testing.T) {
		st := newTestStore(t)
		_, h := kendraFixture()
		h["ListIndices"] = failOnCall(2, kendraBadInput, h["ListIndices"])

		total, _, err := kendraScan(context.Background(), t, st, h)
		assertAPICode(t, err, "ConflictException")
		// Index 1, its 12 children, experience A's 2 entities and 2 personas.
		if total != 17 {
			t.Errorf("total = %d; want 17", total)
		}
		if _, ok := kendraRowOf(t, st, TypeKendraThesaurus, kendraTestThes); !ok {
			t.Error("thesaurus of index 1, from the page read, missing")
		}
	})
	t.Run("per-index listings", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		gone := bacWarnings(st, "kendra:ListQuerySuggestionsBlockLists")
		_, h := kendraFixture()
		failFor := func(op, index string, laterPage bool, err error) {
			next := h[op]
			h[op] = func(in any) (any, error) {
				if kendraIndexOf(in) == index && (bacField[*string](in, "NextToken") != nil) == laterPage {
					return nil, err
				}
				return next(in)
			}
		}
		failFor("ListDataSources", kendraTestIdx1, true, kendraBadInput)
		failFor("ListFaqs", kendraTestIdx1, false, kendraGone)
		failFor("ListQuerySuggestionsBlockLists", kendraTestIdx2, true, kendraGone)
		failFor("ListThesauri", kendraTestIdx1, false, apiErr("ValidationException", "bad index"))

		total, _, err := kendraScan(context.Background(), t, st, h)
		for _, code := range []string{"ConflictException", "ValidationException"} {
			if !joinCarriesAPIErrorCode(err, code) {
				t.Errorf("err = %v; want it to carry %s", err, code)
			}
		}
		if joinCarriesAPIErrorCode(err, "ResourceNotFoundException") {
			t.Errorf("err = %v; want the vanished indices skipped", err)
		}
		if *warns != 1 || len(*gone) != 1 {
			t.Errorf("warnings = %d, ListQuerySuggestionsBlockLists warnings = %v; want 1, the index deleted mid-listing", *warns, *gone)
		}
		// Lost: data source 1-b, both FAQs and both thesauri of index 1, and
		// block list 2-b.
		if total != kendraTotal-6 {
			t.Errorf("total = %d; want %d", total, kendraTotal-6)
		}
		for _, want := range []struct{ typ, arn string }{
			{TypeKendraDataSource, kendraChildARN(kendraTaggedKinds[0], kendraTestIdx1, "t")},
			{TypeKendraFaq, kendraChildARN(kendraTaggedKinds[1], kendraTestIdx2, "b")},
			{TypeKendraQuerySuggestionsBlockList, kendraChildARN(kendraTaggedKinds[3], kendraTestIdx2, "t")},
			{TypeKendraThesaurus, kendraChildARN(kendraTaggedKinds[4], kendraTestIdx2, "b")},
			{TypeKendraEntityPersona, kendraTestPerB1},
		} {
			if _, ok := kendraRowOf(t, st, want.typ, want.arn); !ok {
				t.Errorf("%s %s missing", want.typ, want.arn)
			}
		}
	})
}

// An index every per-index listing no longer finds on its first page
// (ResourceNotFoundException, deleted since the index listing) is skipped
// silently: no error, no warning, none of its items of that kind stored, and
// the other index's items kept. An experience skipped so has no entity or
// persona listed.
func TestScanKendra_IndexGoneFromListingIsSkipped(t *testing.T) {
	typeOf := map[string]string{
		"ListAccessControlConfigurations": TypeKendraAccessControlConfiguration,
		"ListExperiences":                 TypeKendraExperience,
	}
	for _, k := range kendraTaggedKinds {
		typeOf[k.op] = k.typ
	}
	for _, op := range kendraListOps[1:] {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			_, h := kendraFixture()
			list := h[op]
			h[op] = func(in any) (any, error) {
				if kendraIndexOf(in) == kendraTestIdx2 && bacField[*string](in, "NextToken") == nil {
					return nil, kendraGone
				}
				return list(in)
			}

			total, _, err := kendraScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			lost := 0
			for _, want := range kendraRows() {
				if want.typ != typeOf[op] {
					continue
				}
				gone := strings.Contains(want.arn, "index/"+kendraTestIdx2+"/")
				if gone {
					lost++
				}
				if _, ok := kendraRowOf(t, st, want.typ, want.arn); ok == gone {
					t.Errorf("%s stored = %v; want %v", want.arn, ok, !gone)
				}
			}
			if op == "ListExperiences" {
				for _, child := range []struct{ typ, arn string }{
					{TypeKendraExperienceEntity, kendraTestEntB1},
					{TypeKendraEntityPersona, kendraTestPerB1},
				} {
					if _, ok := kendraRowOf(t, st, child.typ, child.arn); ok {
						t.Errorf("%s stored; want none for the skipped experience", child.arn)
					} else {
						lost++
					}
				}
			}
			if lost == 0 || total != kendraTotal-lost {
				t.Errorf("total = %d, rows of index 2 lost = %d; want %d and some lost", total, lost, kendraTotal-lost)
			}
		})
	}
}

// A status listed empty is stored as an empty Status, not as none, for an
// index and for a per-index item, as the base scan stored it.
func TestScanKendra_EmptyStatusIsStored(t *testing.T) {
	st := newTestStore(t)
	_, h := kendraFixture()
	h["ListIndices"] = func(any) (any, error) {
		idx := kendraIndexSummary(kendraTestIdx1)
		idx.Status = ""
		return &kendra.ListIndicesOutput{IndexConfigurationSummaryItems: []kendratypes.IndexConfigurationSummary{idx}}, nil
	}
	h["ListDataSources"] = func(any) (any, error) {
		return &kendra.ListDataSourcesOutput{SummaryItems: []kendratypes.DataSourceSummary{{Id: sdkaws.String("ds-empty")}}}, nil
	}

	if _, _, err := kendraScan(context.Background(), t, st, h); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	for _, want := range []struct{ typ, arn string }{
		{TypeKendraIndex, kendraIndexARN(kendraTestIdx1)},
		{TypeKendraDataSource, kendraTestARN("index/" + kendraTestIdx1 + "/data-source/ds-empty")},
	} {
		r, ok := kendraRowOf(t, st, want.typ, want.arn)
		if !ok || r.Status == nil || *r.Status != "" {
			t.Errorf("%s stored %v, Status %v; want an empty Status", want.arn, ok, r.Status)
		}
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanKendra_RepeatedTokenKeepsPages(t *testing.T) {
	for _, c := range []struct {
		op, typ, arn string
		out          func(in any) any
	}{
		{"ListIndices", TypeKendraIndex, kendraIndexARN(kendraTestIdx1), func(any) any {
			return &kendra.ListIndicesOutput{IndexConfigurationSummaryItems: []kendratypes.IndexConfigurationSummary{kendraIndexSummary(kendraTestIdx1)}, NextToken: sdkaws.String("same")}
		}},
		{"ListFeaturedResultsSets", TypeKendraFeaturedResultsSet, kendraChildARN(kendraTaggedKinds[2], kendraTestIdx2, "t"), func(in any) any {
			k := kendraTaggedKinds[2]
			return k.out(k.elem(kendraChildID(k.short, kendraIndexOf(in), "t")), sdkaws.String("same"))
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls, h := kendraFixture()
			h[c.op] = calls.answer(c.op, c.out)

			_, _, err := kendraScan(context.Background(), t, st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := kendraRowOf(t, st, c.typ, c.arn); !ok {
				t.Errorf("%s from the pages read missing", c.arn)
			}
		})
	}
}

// Items listed without an id are not stored or read and are warned about
// once per listing, under a key that is never empty.
func TestScanKendra_UnstorableItemsWarn(t *testing.T) {
	for _, c := range []struct {
		op, key string
		out     any
	}{
		{"ListIndices", "(an index with no name)", &kendra.ListIndicesOutput{IndexConfigurationSummaryItems: []kendratypes.IndexConfigurationSummary{{}, {}}}},
		{"ListThesauri", "(an item with no name) of index " + kendraTestIdx1, &kendra.ListThesauriOutput{ThesaurusSummaryItems: []kendratypes.ThesaurusSummary{{}}}},
		{"ListExperiences", "exp x of index " + kendraTestIdx1, &kendra.ListExperiencesOutput{SummaryItems: []kendratypes.ExperiencesSummary{{Name: sdkaws.String("exp x")}}}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "kendra:"+c.op)
			calls, h := kendraFixture()
			h[c.op] = func(any) (any, error) { return c.out, nil }

			if _, _, err := kendraScan(context.Background(), t, st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %v; want one naming %q", *warnings, c.key)
			}
			if c.op == "ListIndices" && (len(calls.tagARNs) != 0 || calls.count("DescribeIndex") != 0) {
				t.Errorf("tag reads, DescribeIndex calls = %d, %d; want 0, 0", len(calls.tagARNs), calls.count("DescribeIndex"))
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is reported once.
func TestScanKendra_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls, h := kendraFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h["ListExperiences"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}

	_, _, err := kendraScan(ctx, t, st, h)
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
	if n := calls.count("ListFeaturedResultsSets"); n != 0 {
		t.Errorf("ListFeaturedResultsSets called %d times after ctx ended", n)
	}
}

// A region without Kendra (NXDOMAIN on the index listing, nothing stored) is
// unavailable, and no other call is made. An NXDOMAIN of a tag read after
// rows were listed is a failure of this scan: the counts are kept.
func TestScanKendra_Unreachable(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "kendra.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		calls, h := kendraFixture()
		h["ListIndices"] = func(any) (any, error) { return nil, nx }
		total, _, err := kendraScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || len(calls.tagARNs) != 0 || calls.count("ListDataSources") != 0 {
			t.Errorf("total, tag reads, ListDataSources calls = %d, %d, %d; want 0", total, len(calls.tagARNs), calls.count("ListDataSources"))
		}
	})
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		calls, h := kendraFixture()
		calls.errs[kendraTestThes] = nx
		total, _, err := kendraScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeError)
		if total != kendraTotal {
			t.Errorf("total = %d; want %d", total, kendraTotal)
		}
	})
}

// An account Kendra is closed to is not entitled: nothing is stored or
// warned about and no other call is made.
func TestScanKendra_ClosedToAccountIsNotEntitled(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls, h := kendraFixture()
	h["ListIndices"] = func(any) (any, error) {
		return nil, apiErr("NotAuthorizedException", "Your account is not authorized to make this call.")
	}

	total, _, err := kendraScan(context.Background(), t, st, h)
	assertGlueOutcome(t, err, outcomeNotEntitled)
	if total != 0 || *warns != 0 || len(calls.tagARNs) != 0 || calls.count("ListDataSources") != 0 {
		t.Errorf("total, warnings, tag reads, ListDataSources calls = %d, %d, %d, %d; want 0", total, *warns, len(calls.tagARNs), calls.count("ListDataSources"))
	}
}

func TestScanKendra_StoresEntitiesAndPersonas(t *testing.T) {
	st := newTestStore(t)
	var entityCalls, personaCalls parentCalls
	total, err := scanKendraTopLevel(t, st, kendraHandlers(newKendraCalls(), &entityCalls, &personaCalls))
	if err != nil {
		t.Fatalf("scanKendra: %v", err)
	}
	if total != kendraTotal {
		t.Errorf("total = %d, want %d", total, kendraTotal)
	}
	for name, calls := range map[string]*parentCalls{"ListExperienceEntities": &entityCalls, "ListEntityPersonas": &personaCalls} {
		if got := calls.distinct(); !slices.Equal(got, []string{kendraTestExpA, kendraTestExpB}) {
			t.Errorf("%s experiences = %v, want both", name, got)
		}
	}

	entities := rowsByNativeID(t, st, TypeKendraExperienceEntity)
	if got := nativeIDs(entities); !slices.Equal(got, []string{kendraTestEntA1, kendraTestEntA2, kendraTestEntB1}) {
		t.Fatalf("entity rows = %v, want both pages of A and B's entity", got)
	}
	e := entities[kendraTestEntA2]
	if e.Type != TypeKendraExperienceEntity || sv(e.Region) != testRegion {
		t.Errorf("entity row = type %q region %q", e.Type, sv(e.Region))
	}
	var entAttrs struct {
		EntityType  string `json:"EntityType"`
		DisplayData struct {
			GroupName string `json:"GroupName"`
		} `json:"DisplayData"`
	}
	if err := json.Unmarshal([]byte(e.AttributesJSON), &entAttrs); err != nil {
		t.Fatalf("entity attrs: %v", err)
	}
	if entAttrs.EntityType != "GROUP" || entAttrs.DisplayData.GroupName != "search-admins" {
		t.Errorf("entity attrs = %+v, want the SDK summary verbatim", entAttrs)
	}

	personas := rowsByNativeID(t, st, TypeKendraEntityPersona)
	if got := nativeIDs(personas); !slices.Equal(got, []string{kendraTestPerA1, kendraTestPerA2, kendraTestPerB1}) {
		t.Fatalf("persona rows = %v, want both pages of A and B's persona", got)
	}
	p := personas[kendraTestPerA2]
	if p.Type != TypeKendraEntityPersona || sv(p.Region) != testRegion {
		t.Errorf("persona row = type %q region %q", p.Type, sv(p.Region))
	}
	if sv(p.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("persona CreatedAt = %q, want 2026-01-02T03:04:05Z", sv(p.CreatedAt))
	}
	var perAttrs struct {
		Persona string `json:"Persona"`
	}
	if err := json.Unmarshal([]byte(p.AttributesJSON), &perAttrs); err != nil {
		t.Fatalf("persona attrs: %v", err)
	}
	if perAttrs.Persona != "OWNER" {
		t.Errorf("persona attrs = %+v, want Persona OWNER", perAttrs)
	}

	for _, pair := range [][2]string{
		{kendraTestExpA, kendraTestEntA1},
		{kendraTestExpA, kendraTestEntA2},
		{kendraTestExpB, kendraTestEntB1},
		{kendraTestExpA, kendraTestPerA1},
		{kendraTestExpA, kendraTestPerA2},
		{kendraTestExpB, kendraTestPerB1},
	} {
		assertContainsNative(t, st, pair[0], pair[1])
	}
}

func TestScanKendra_ExperiencesWithoutEntitiesOrPersonas(t *testing.T) {
	st := newTestStore(t)
	var entityCalls, personaCalls parentCalls
	h := kendraHandlers(newKendraCalls(), &entityCalls, &personaCalls)
	h["ListExperienceEntities"] = func(in any) (any, error) {
		entityCalls.add(sdkaws.ToString(in.(*kendra.ListExperienceEntitiesInput).Id))
		return &kendra.ListExperienceEntitiesOutput{}, nil
	}
	h["ListEntityPersonas"] = func(in any) (any, error) {
		personaCalls.add(sdkaws.ToString(in.(*kendra.ListEntityPersonasInput).Id))
		return &kendra.ListEntityPersonasOutput{}, nil
	}
	if _, err := scanKendraTopLevel(t, st, h); err != nil {
		t.Fatalf("scanKendra: %v", err)
	}
	for _, typ := range []string{TypeKendraExperienceEntity, TypeKendraEntityPersona} {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
	for name, calls := range map[string]*parentCalls{"ListExperienceEntities": &entityCalls, "ListEntityPersonas": &personaCalls} {
		if got := calls.distinct(); !slices.Equal(got, []string{"exp-a", "exp-b"}) {
			t.Errorf("%s experiences = %v, want both", name, got)
		}
	}
}

// An experience deleted since listing, denied, failing transiently after
// retries, or refused with ValidationException (possibly not ready) drops only
// its own children: deleted is silent, and each of the others warns once
// however many experiences hit it. Any other error fails the phase
// (childFanOut stores none of its rows) and is returned; the earlier phases'
// rows and the other experience phase's rows stay.
func TestScanKendra_FailedExperienceChild(t *testing.T) {
	notReady := apiErr("ValidationException", "experience is not active")
	for _, phase := range []struct {
		op, sibling   string
		typ, sibType  string
		rowsA, rowsB  []string
		siblingRows   []string
		experienceIDs func(in any) string
	}{
		{
			op: "ListExperienceEntities", sibling: "ListEntityPersonas",
			typ: TypeKendraExperienceEntity, sibType: TypeKendraEntityPersona,
			rowsA: []string{kendraTestEntA1, kendraTestEntA2}, rowsB: []string{kendraTestEntB1},
			siblingRows:   []string{kendraTestPerA1, kendraTestPerA2, kendraTestPerB1},
			experienceIDs: func(in any) string { return sdkaws.ToString(in.(*kendra.ListExperienceEntitiesInput).Id) },
		},
		{
			op: "ListEntityPersonas", sibling: "ListExperienceEntities",
			typ: TypeKendraEntityPersona, sibType: TypeKendraExperienceEntity,
			rowsA: []string{kendraTestPerA1, kendraTestPerA2}, rowsB: []string{kendraTestPerB1},
			siblingRows:   []string{kendraTestEntA1, kendraTestEntA2, kendraTestEntB1},
			experienceIDs: func(in any) string { return sdkaws.ToString(in.(*kendra.ListEntityPersonasInput).Id) },
		},
	} {
		for _, tc := range []struct {
			name      string
			fail      map[string]error
			wantCode  string // "" means the scan returns nil
			wantA     bool
			wantB     bool
			wantWarns int
		}{
			{"experience gone", map[string]error{"exp-a": apiErr("ResourceNotFoundException", "gone")}, "", false, true, 0},
			{"denied everywhere", map[string]error{"exp-a": kendraDenied, "exp-b": kendraDenied}, "", false, false, 1},
			{"throttled everywhere", map[string]error{"exp-a": kendraThrottled, "exp-b": kendraThrottled}, "", false, false, 1},
			{"not ready everywhere", map[string]error{"exp-a": notReady, "exp-b": notReady}, "", false, false, 1},
			{"not ready one", map[string]error{"exp-a": notReady}, "", false, true, 1},
			{"other error", map[string]error{"exp-a": kendraBadInput}, "ConflictException", false, false, 0},
		} {
			t.Run(phase.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "kendra:"+phase.op)
				var entityCalls, personaCalls parentCalls
				h := kendraHandlers(newKendraCalls(), &entityCalls, &personaCalls)
				next := h[phase.op]
				h[phase.op] = func(in any) (any, error) {
					if err, ok := tc.fail[phase.experienceIDs(in)]; ok {
						return nil, err
					}
					return next(in)
				}
				_, err := scanKendraTopLevel(t, st, h)
				if tc.wantCode == "" {
					if err != nil {
						t.Fatalf("err = %v, want nil", err)
					}
				} else {
					assertAPICode(t, err, tc.wantCode)
					if isTransientNetworkError(err) {
						t.Errorf("err = %v reads as transient, want a hard error", err)
					}
				}
				var want []string
				if tc.wantA {
					want = append(want, phase.rowsA...)
				}
				if tc.wantB {
					want = append(want, phase.rowsB...)
				}
				if got := nativeIDs(rowsByNativeID(t, st, phase.typ)); !slices.Equal(got, want) {
					t.Errorf("%s rows = %v, want %v", phase.typ, got, want)
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
				}
				if got := nativeIDs(rowsByNativeID(t, st, phase.sibType)); !slices.Equal(got, phase.siblingRows) {
					t.Errorf("%s rows = %v, want %v; %s must not stop %s", phase.sibType, got, phase.siblingRows, phase.op, phase.sibling)
				}
				if _, ok := rowsByNativeID(t, st, TypeKendraThesaurus)[kendraTestThes]; !ok {
					t.Error("thesaurus missing; the experience phases must not stop the earlier phases")
				}
			})
		}
	}
}

// A hard error of one index's listing still lets the other index and the
// later phases run, and is returned with every phase's counts.
func TestScanKendra_FailedIndexListingStillRunsLaterPhases(t *testing.T) {
	st := newTestStore(t)
	_, h := kendraFixture()
	thesauri := h["ListThesauri"]
	h["ListThesauri"] = func(in any) (any, error) {
		if kendraIndexOf(in) == kendraTestIdx1 {
			return nil, kendraBadInput
		}
		return thesauri(in)
	}
	total, err := scanKendraTopLevel(t, st, h)
	assertAPICode(t, err, "ConflictException")
	// Every row but index 1's two thesauri.
	if total != kendraTotal-2 {
		t.Errorf("total = %d, want %d", total, kendraTotal-2)
	}
	if _, ok := kendraRowOf(t, st, TypeKendraThesaurus, kendraChildARN(kendraTaggedKinds[4], kendraTestIdx2, "t")); !ok {
		t.Error("thesaurus of index 2 missing; index 1's failure must not stop it")
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeKendraEntityPersona)); !slices.Equal(got, []string{kendraTestPerA1, kendraTestPerA2, kendraTestPerB1}) {
		t.Errorf("persona rows = %v, want both experiences'", got)
	}
}

// A hard ListExperiences error does not cost the indices their access-control
// rows: no phase's error stops another (runHostPhases).
func TestScanKendra_ExperiencesErrorKeepsAccessControlConfigurations(t *testing.T) {
	st := newTestStore(t)
	_, h := kendraFixture()
	h["ListExperiences"] = func(any) (any, error) { return nil, kendraBadInput }
	_, err := scanKendraTopLevel(t, st, h)
	assertAPICode(t, err, "ConflictException")
	want := []string{
		"arn:aws:kendra:us-east-1:123456789012:index/" + kendraTestIdx1 + "/access-control-configuration/acc1",
		"arn:aws:kendra:us-east-1:123456789012:index/" + kendraTestIdx2 + "/access-control-configuration/acc2",
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeKendraAccessControlConfiguration)); !slices.Equal(got, want) {
		t.Errorf("access-control rows = %v, want %v", got, want)
	}
	if n := len(rowsByNativeID(t, st, TypeKendraExperienceEntity)) + len(rowsByNativeID(t, st, TypeKendraEntityPersona)); n != 0 {
		t.Errorf("entity+persona rows = %d, want 0 with no experience stored", n)
	}
}

// A transient error of an earlier phase is demoted to a warning under its op
// so that either experience phase's hard error still reaches the dispatcher
// as a hard error.
func TestScanKendra_EarlierPhaseTransientDoesNotMaskHardError(t *testing.T) {
	for _, op := range []string{"ListExperienceEntities", "ListEntityPersonas"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, kendraListThesauriOp)
			_, h := kendraFixture()
			h["ListThesauri"] = func(any) (any, error) { return nil, kendraThrottled }
			h[op] = func(any) (any, error) { return nil, kendraBadInput }
			_, err := scanKendraTopLevel(t, st, h)
			assertAPICode(t, err, "ConflictException")
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v, want outcomeError; the hard %s error must not be masked", got, op)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
			}
		})
	}
}
