package aws

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kendra"
	kendratypes "github.com/aws/aws-sdk-go-v2/service/kendra/types"
	"github.com/icearp/disco-cli/store"
)

const (
	kendraTestIdx1 = "11111111-1111-1111-1111-111111111111"
	kendraTestIdx2 = "22222222-2222-2222-2222-222222222222"
	kendraTestExpA = "arn:aws:kendra:us-east-1:123456789012:index/" + kendraTestIdx1 + "/experience/exp-a"
	kendraTestExpB = "arn:aws:kendra:us-east-1:123456789012:index/" + kendraTestIdx2 + "/experience/exp-b"
	kendraTestThes = "arn:aws:kendra:us-east-1:123456789012:index/" + kendraTestIdx1 + "/thesaurus/th-1"

	kendraTestUser1 = "aaaaaaaa-0000-0000-0000-000000000001"
	kendraTestUser2 = "aaaaaaaa-0000-0000-0000-000000000002"
	kendraTestGroup = "bbbbbbbb-0000-0000-0000-000000000003"

	kendraTestEntA1 = kendraTestExpA + "/entity/" + kendraTestUser1
	kendraTestEntA2 = kendraTestExpA + "/entity/" + kendraTestGroup
	kendraTestEntB1 = kendraTestExpB + "/entity/" + kendraTestUser2
	kendraTestPerA1 = kendraTestExpA + "/persona/" + kendraTestUser1
	kendraTestPerA2 = kendraTestExpA + "/persona/" + kendraTestGroup
	kendraTestPerB1 = kendraTestExpB + "/persona/" + kendraTestUser2
)

var (
	kendraDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: kendra:ListExperienceEntities")
	kendraBadInput = apiErr("ConflictException", "bad")
	kendraCreated  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

// kendraExpKey names an experience by the (index, experience) pair the child
// ops take, so a call carrying the wrong index finds nothing.
func kendraExpKey(indexID, expID *string) string {
	return sdkaws.ToString(indexID) + "/" + sdkaws.ToString(expID)
}

// kendraHandlers answers every Kendra op the scan calls: two indices with one
// experience each (A in index 1, B in index 2), one thesaurus in index 1, no
// other per-index children. Experience A's entities and personas come over
// two pages (plus an element without an entity id to skip), B's in one. Each
// child call is recorded by experience NativeID in entityCalls / personaCalls.
func kendraHandlers(entityCalls, personaCalls *parentCalls) map[string]func(in any) (any, error) {
	byExp := map[string]string{
		kendraExpKey(sdkaws.String(kendraTestIdx1), sdkaws.String("exp-a")): kendraTestExpA,
		kendraExpKey(sdkaws.String(kendraTestIdx2), sdkaws.String("exp-b")): kendraTestExpB,
	}
	empty := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	return map[string]func(in any) (any, error){
		"ListIndices": empty(&kendra.ListIndicesOutput{IndexConfigurationSummaryItems: []kendratypes.IndexConfigurationSummary{
			{Id: sdkaws.String(kendraTestIdx1), Name: sdkaws.String("one"), Status: kendratypes.IndexStatusActive},
			{Id: sdkaws.String(kendraTestIdx2), Name: sdkaws.String("two"), Status: kendratypes.IndexStatusActive},
		}}),
		"DescribeIndex":                   empty(&kendra.DescribeIndexOutput{}),
		"ListDataSources":                 empty(&kendra.ListDataSourcesOutput{}),
		"ListFaqs":                        empty(&kendra.ListFaqsOutput{}),
		"ListAccessControlConfigurations": empty(&kendra.ListAccessControlConfigurationsOutput{}),
		"ListFeaturedResultsSets":         empty(&kendra.ListFeaturedResultsSetsOutput{}),
		"ListQuerySuggestionsBlockLists":  empty(&kendra.ListQuerySuggestionsBlockListsOutput{}),
		"ListExperiences": func(in any) (any, error) {
			id := "exp-a"
			if sdkaws.ToString(in.(*kendra.ListExperiencesInput).IndexId) == kendraTestIdx2 {
				id = "exp-b"
			}
			return &kendra.ListExperiencesOutput{SummaryItems: []kendratypes.ExperiencesSummary{
				{Id: sdkaws.String(id), Name: sdkaws.String(id), Status: kendratypes.ExperienceStatusActive},
			}}, nil
		},
		"ListThesauri": func(in any) (any, error) {
			if sdkaws.ToString(in.(*kendra.ListThesauriInput).IndexId) != kendraTestIdx1 {
				return &kendra.ListThesauriOutput{}, nil
			}
			return &kendra.ListThesauriOutput{ThesaurusSummaryItems: []kendratypes.ThesaurusSummary{{Id: sdkaws.String("th-1")}}}, nil
		},
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
}

func scanKendraTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, kendraDenied)
	total, _, err := scanKendra(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanKendra_StoresEntitiesAndPersonas(t *testing.T) {
	st := newTestStore(t)
	var entityCalls, personaCalls parentCalls
	total, err := scanKendraTopLevel(t, st, kendraHandlers(&entityCalls, &personaCalls))
	if err != nil {
		t.Fatalf("scanKendra: %v", err)
	}
	// 2 indices + 2 experiences + 1 thesaurus + 3 entities + 3 personas
	if total != 11 {
		t.Errorf("total = %d, want 11", total)
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
	h := kendraHandlers(&entityCalls, &personaCalls)
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
// (childFanOut stores none of its rows) and is returned; the older phases'
// rows and the other new phase's rows stay.
func TestScanKendra_FailedExperienceChild(t *testing.T) {
	throttled := apiErr("ThrottlingException", "slow down")
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
			{"throttled everywhere", map[string]error{"exp-a": throttled, "exp-b": throttled}, "", false, false, 1},
			{"not ready everywhere", map[string]error{"exp-a": notReady, "exp-b": notReady}, "", false, false, 1},
			{"not ready one", map[string]error{"exp-a": notReady}, "", false, true, 1},
			{"other error", map[string]error{"exp-a": kendraBadInput}, "ConflictException", false, false, 0},
		} {
			t.Run(phase.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "kendra:"+phase.op)
				var entityCalls, personaCalls parentCalls
				h := kendraHandlers(&entityCalls, &personaCalls)
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
					t.Error("thesaurus missing; the new phases must not stop the older phases")
				}
			})
		}
	}
}

// A hard error in an older phase after the experiences were stored still lets
// the entity and persona phases run over them, and is returned with every
// phase's counts.
func TestScanKendra_OlderPhaseErrorStillRunsNewPhases(t *testing.T) {
	st := newTestStore(t)
	var entityCalls, personaCalls parentCalls
	h := kendraHandlers(&entityCalls, &personaCalls)
	h["ListThesauri"] = func(any) (any, error) { return nil, kendraBadInput }
	total, err := scanKendraTopLevel(t, st, h)
	assertAPICode(t, err, "ConflictException")
	// 2 indices + experience A (index 2 never reached) + 2 entities + 2 personas of A
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeKendraExperienceEntity)); !slices.Equal(got, []string{kendraTestEntA1, kendraTestEntA2}) {
		t.Errorf("entity rows = %v, want experience A's", got)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeKendraEntityPersona)); !slices.Equal(got, []string{kendraTestPerA1, kendraTestPerA2}) {
		t.Errorf("persona rows = %v, want experience A's", got)
	}
}

// Access-control configurations are listed before experiences, as before the
// entity and persona phases existed, so a hard ListExperiences error does not
// cost the index its access-control rows.
func TestScanKendra_ExperiencesErrorKeepsAccessControlConfigurations(t *testing.T) {
	st := newTestStore(t)
	var entityCalls, personaCalls parentCalls
	h := kendraHandlers(&entityCalls, &personaCalls)
	h["ListAccessControlConfigurations"] = func(any) (any, error) {
		return &kendra.ListAccessControlConfigurationsOutput{AccessControlConfigurations: []kendratypes.AccessControlConfigurationSummary{{Id: sdkaws.String("acc-1")}}}, nil
	}
	h["ListExperiences"] = func(any) (any, error) { return nil, kendraBadInput }
	_, err := scanKendraTopLevel(t, st, h)
	assertAPICode(t, err, "ConflictException")
	want := "arn:aws:kendra:us-east-1:123456789012:index/" + kendraTestIdx1 + "/access-control-configuration/acc-1"
	if got := nativeIDs(rowsByNativeID(t, st, TypeKendraAccessControlConfiguration)); !slices.Equal(got, []string{want}) {
		t.Errorf("access-control rows = %v, want index 1's", got)
	}
	if n := len(rowsByNativeID(t, st, TypeKendraExperienceEntity)) + len(rowsByNativeID(t, st, TypeKendraEntityPersona)); n != 0 {
		t.Errorf("entity+persona rows = %d, want 0 with no experience stored", n)
	}
}

// A transient error in an older phase is demoted to a warning so that either
// new phase's hard error still reaches the dispatcher as a hard error.
func TestScanKendra_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	for _, op := range []string{"ListExperienceEntities", "ListEntityPersonas"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "kendra")
			var entityCalls, personaCalls parentCalls
			h := kendraHandlers(&entityCalls, &personaCalls)
			h["ListThesauri"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
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
