package aws

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lexmodelsv2"
	lextypes "github.com/aws/aws-sdk-go-v2/service/lexmodelsv2/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	lxDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: lex:List"}
	lxBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	lxGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
	lxCreated  = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

const (
	lxBot1     = "arn:aws:lex:us-east-1:123456789012:bot/B1"
	lxBot2     = "arn:aws:lex:us-east-1:123456789012:bot/B2"
	lxLocB1En  = "arn:aws:lex:us-east-1:123456789012:bot/B1/version/DRAFT/locale/en_US"
	lxLocB1Fr  = "arn:aws:lex:us-east-1:123456789012:bot/B1/version/DRAFT/locale/fr_FR"
	lxLocB2En  = "arn:aws:lex:us-east-1:123456789012:bot/B2/version/DRAFT/locale/en_US"
	lxIntB1EnA = lxLocB1En + "/intent/A"
	lxIntB2EnA = lxLocB2En + "/intent/A"
)

var (
	lxBots    = []childParent{{id: "B1", arn: lxBot1}, {id: "B2", arn: lxBot2}}
	lxLocales = []lexLocale{
		{botID: "B1", botARN: lxBot1, localeID: "en_US", nativeID: lxLocB1En},
		{botID: "B2", botARN: lxBot2, localeID: "en_US", nativeID: lxLocB2En},
	}
	lxIntents = []lexIntent{
		{botID: "B1", localeID: "en_US", intentID: "A", nativeID: lxIntB1EnA},
		{botID: "B2", localeID: "en_US", intentID: "A", nativeID: lxIntB2EnA},
	}
)

func lxClient(handlers map[string]func(in any) (any, error)) *lexmodelsv2.Client {
	return lexmodelsv2.NewFromConfig(opStubConfig(handlers, lxDenied))
}

// lxDraftPageRequest reports whether a list call asks for the DRAFT version
// at the documented page cap.
func lxDraftPageRequest(version *string, maxResults *int32) bool {
	return sv(version) == lexDraftVersion && maxResults != nil && *maxResults == lexMaxPageSize
}

// lxPaged answers a child list op: a key failing in fail returns that error,
// a call for any version but DRAFT or any page size but the cap is rejected, the B1 parent gets page one
// (child A) then page two (child B and one without an id), and every other
// parent gets one page with child A. Every call's key is recorded.
func lxPaged(calls *parentCalls, fail map[string]error, call func(in any) (key string, version *string, maxResults *int32, token *string), page func(ids []string, next *string) any) func(in any) (any, error) {
	return func(in any) (any, error) {
		key, version, maxResults, token := call(in)
		calls.add(key)
		if !lxDraftPageRequest(version, maxResults) {
			return nil, lxBadInput
		}
		if err := fail[key]; err != nil {
			return nil, err
		}
		switch {
		case !strings.HasPrefix(key, "B1"):
			return page([]string{"A"}, nil), nil
		case token == nil:
			return page([]string{"A"}, ptrStr("p2")), nil
		default:
			return page([]string{"B", ""}, nil), nil
		}
	}
}

func lxLocalesHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*lexmodelsv2.ListBotLocalesInput)
		calls.add(sv(req.BotId))
		if !lxDraftPageRequest(req.BotVersion, req.MaxResults) {
			return nil, lxBadInput
		}
		if err := fail[sv(req.BotId)]; err != nil {
			return nil, err
		}
		loc := func(id string) lextypes.BotLocaleSummary {
			if id == "" {
				return lextypes.BotLocaleSummary{LocaleName: ptrStr("no-id")}
			}
			return lextypes.BotLocaleSummary{LocaleId: ptrStr(id), LocaleName: ptrStr("summary-" + id), BotLocaleStatus: lextypes.BotLocaleStatusNotBuilt}
		}
		switch {
		case sv(req.BotId) != "B1":
			return &lexmodelsv2.ListBotLocalesOutput{BotLocaleSummaries: []lextypes.BotLocaleSummary{loc("en_US")}}, nil
		case req.NextToken == nil:
			return &lexmodelsv2.ListBotLocalesOutput{BotLocaleSummaries: []lextypes.BotLocaleSummary{loc("en_US")}, NextToken: ptrStr("p2")}, nil
		default:
			return &lexmodelsv2.ListBotLocalesOutput{BotLocaleSummaries: []lextypes.BotLocaleSummary{loc("fr_FR"), loc("")}}, nil
		}
	}
}

// lxDescribeLocaleHandler describes any DRAFT locale; a "bot/locale" key in
// fail returns that error.
func lxDescribeLocaleHandler(fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*lexmodelsv2.DescribeBotLocaleInput)
		if sv(req.BotVersion) != lexDraftVersion {
			return nil, lxBadInput
		}
		if err := fail[sv(req.BotId)+"/"+sv(req.LocaleId)]; err != nil {
			return nil, err
		}
		return &lexmodelsv2.DescribeBotLocaleOutput{
			BotId: req.BotId, BotVersion: req.BotVersion, LocaleId: req.LocaleId,
			LocaleName: ptrStr("name-" + sv(req.LocaleId)), BotLocaleStatus: lextypes.BotLocaleStatusBuilt,
			CreationDateTime: &lxCreated, NluIntentConfidenceThreshold: ptrFloat(0.4),
		}, nil
	}
}

func ptrFloat(f float64) *float64 { return &f }

func lxIntentsHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return lxPaged(calls, fail,
		func(in any) (string, *string, *int32, *string) {
			req := in.(*lexmodelsv2.ListIntentsInput)
			return sv(req.BotId) + "/" + sv(req.LocaleId), req.BotVersion, req.MaxResults, req.NextToken
		},
		func(ids []string, next *string) any {
			out := &lexmodelsv2.ListIntentsOutput{NextToken: next}
			for _, id := range ids {
				out.IntentSummaries = append(out.IntentSummaries, lextypes.IntentSummary{IntentId: nonEmptyPtr(id), IntentName: ptrStr("intent-" + id)})
			}
			return out
		})
}

func lxSlotTypesHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return lxPaged(calls, fail,
		func(in any) (string, *string, *int32, *string) {
			req := in.(*lexmodelsv2.ListSlotTypesInput)
			return sv(req.BotId) + "/" + sv(req.LocaleId), req.BotVersion, req.MaxResults, req.NextToken
		},
		func(ids []string, next *string) any {
			out := &lexmodelsv2.ListSlotTypesOutput{NextToken: next}
			for _, id := range ids {
				out.SlotTypeSummaries = append(out.SlotTypeSummaries, lextypes.SlotTypeSummary{
					SlotTypeId: nonEmptyPtr(id), SlotTypeName: ptrStr("slottype-" + id), SlotTypeCategory: lextypes.SlotTypeCategoryCustom,
				})
			}
			return out
		})
}

func lxSlotsHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return lxPaged(calls, fail,
		func(in any) (string, *string, *int32, *string) {
			req := in.(*lexmodelsv2.ListSlotsInput)
			return sv(req.BotId) + "/" + sv(req.LocaleId) + "/" + sv(req.IntentId), req.BotVersion, req.MaxResults, req.NextToken
		},
		func(ids []string, next *string) any {
			out := &lexmodelsv2.ListSlotsOutput{NextToken: next}
			for _, id := range ids {
				out.SlotSummaries = append(out.SlotSummaries, lextypes.SlotSummary{SlotId: nonEmptyPtr(id), SlotName: ptrStr("slot-" + id), SlotTypeId: ptrStr("AMAZON.Number")})
			}
			return out
		})
}

// scanLXLocales seeds the bot rows and lists the locales of bots.
func scanLXLocales(t *testing.T, st *store.Store, bots []childParent, listFail, describeFail map[string]error) ([]lexLocale, int, *parentCalls, error) {
	t.Helper()
	for _, b := range bots {
		seedParentRows(t, st, TypeLexBot, b.arn)
	}
	var calls parentCalls
	client := lxClient(map[string]func(any) (any, error){
		"ListBotLocales":    lxLocalesHandler(&calls, listFail),
		"DescribeBotLocale": lxDescribeLocaleHandler(describeFail),
	})
	locales, total, _, err := scanLexBotLocales(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, bots)
	return locales, total, &calls, err
}

func sortedLXLocales(ls []lexLocale) []lexLocale {
	return slices.SortedFunc(slices.Values(ls), func(a, b lexLocale) int { return strings.Compare(a.nativeID, b.nativeID) })
}

func TestScanLexBotLocales_StoresEveryPageOfEveryBot(t *testing.T) {
	st := newTestStore(t)
	locales, total, calls, err := scanLXLocales(t, st, lxBots, nil, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"B1", "B2"}) {
		t.Errorf("bots listed = %v, want [B1 B2]", got)
	}
	rows := rowsByNativeID(t, st, TypeLexBotLocale)
	ids := slices.Sorted(maps.Keys(rows))
	if want := []string{lxLocB1En, lxLocB1Fr, lxLocB2En}; total != 3 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 3, %v", total, ids, want)
	}
	fr := rows[lxLocB1Fr]
	if sv(fr.Name) != "name-fr_FR" || sv(fr.Region) != "us-east-1" || sv(fr.Status) != "Built" ||
		sv(fr.CreatedAt) != "2026-09-01T08:00:00Z" || !strings.Contains(fr.AttributesJSON, `"NluIntentConfidenceThreshold":0.4`) {
		t.Errorf("fr_FR name/region/status/created = %q/%q/%q/%q, attrs = %s", sv(fr.Name), sv(fr.Region), sv(fr.Status), sv(fr.CreatedAt), fr.AttributesJSON)
	}
	want := []lexLocale{
		{botID: "B1", botARN: lxBot1, localeID: "en_US", nativeID: lxLocB1En},
		{botID: "B1", botARN: lxBot1, localeID: "fr_FR", nativeID: lxLocB1Fr},
		{botID: "B2", botARN: lxBot2, localeID: "en_US", nativeID: lxLocB2En},
	}
	if got := sortedLXLocales(locales); !slices.Equal(got, want) {
		t.Errorf("locales = %v, want %v", got, want)
	}
	assertContainsNative(t, st, lxBot1, lxLocB1En)
	assertContainsNative(t, st, lxBot1, lxLocB1Fr)
	assertContainsNative(t, st, lxBot2, lxLocB2En)
}

func TestScanLexBotLocales_Empty(t *testing.T) {
	st := newTestStore(t)
	client := lxClient(map[string]func(any) (any, error){"ListBotLocales": func(any) (any, error) { return &lexmodelsv2.ListBotLocalesOutput{}, nil }})
	locales, total, _, err := scanLexBotLocales(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, lxBots)
	if err != nil || total != 0 || len(locales) != 0 || len(rowsByNativeID(t, st, TypeLexBotLocale)) != 0 {
		t.Errorf("total = %d, locales = %v, err = %v; want 0 rows, nil", total, locales, err)
	}
}

// A denied bot, and a bot not found (the op models no not-found code), each
// warn once for the op however many bots fail, and keep the other bots; any
// other error fails the phase.
func TestScanLexBotLocales_FailedBot(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "lexmodelsv2:ListBotLocales")
		locales, total, _, err := scanLXLocales(t, st, lxBots, map[string]error{"B1": lxDenied}, nil)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		want := []lexLocale{{botID: "B2", botARN: lxBot2, localeID: "en_US", nativeID: lxLocB2En}}
		if *warns != 1 || total != 1 || !slices.Equal(locales, want) {
			t.Errorf("warnings = %d, total = %d, locales = %v; want 1, 1, %v", *warns, total, locales, want)
		}
	})
	t.Run("gone", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "lexmodelsv2:ListBotLocales")
		bots := append(slices.Clone(lxBots), childParent{id: "B3", arn: "arn:aws:lex:us-east-1:123456789012:bot/B3"})
		locales, total, _, err := scanLXLocales(t, st, bots, map[string]error{"B2": lxGone, "B3": lxGone}, nil)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		want := []lexLocale{
			{botID: "B1", botARN: lxBot1, localeID: "en_US", nativeID: lxLocB1En},
			{botID: "B1", botARN: lxBot1, localeID: "fr_FR", nativeID: lxLocB1Fr},
		}
		if got := sortedLXLocales(locales); *warns != 1 || total != 2 || !slices.Equal(got, want) {
			t.Errorf("warnings = %d, total = %d, locales = %v; want 1, 2, %v", *warns, total, got, want)
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		locales, _, _, err := scanLXLocales(t, st, lxBots, map[string]error{"B2": lxBadInput}, nil)
		assertAPICode(t, err, "ValidationException")
		if locales != nil || len(rowsByNativeID(t, st, TypeLexBotLocale)) != 0 {
			t.Errorf("locales = %v, want none on a hard error", locales)
		}
	})
}

// A locale deleted before its describe is skipped silently: it is no fan-out
// parent and the other locales are kept.
func TestScanLexBotLocales_DescribeGone(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "lexmodelsv2:DescribeBotLocale")
	locales, total, _, err := scanLXLocales(t, st, lxBots, nil, map[string]error{"B1/en_US": lxGone, "B2/en_US": lxGone})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	want := []lexLocale{{botID: "B1", botARN: lxBot1, localeID: "fr_FR", nativeID: lxLocB1Fr}}
	if *warns != 0 || total != 1 || !slices.Equal(locales, want) {
		t.Errorf("warnings = %d, total = %d, locales = %v; want 0, 1, %v", *warns, total, locales, want)
	}
}

// A denied describe warns once and stores the locale from its summary, so it
// stays a fan-out parent; the described locales keep their describe body.
func TestScanLexBotLocales_DescribeDenied(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "lexmodelsv2:DescribeBotLocale")
	locales, total, _, err := scanLXLocales(t, st, lxBots, nil, map[string]error{"B1/en_US": lxDenied, "B2/en_US": lxDenied})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	want := []lexLocale{
		{botID: "B1", botARN: lxBot1, localeID: "en_US", nativeID: lxLocB1En},
		{botID: "B1", botARN: lxBot1, localeID: "fr_FR", nativeID: lxLocB1Fr},
		{botID: "B2", botARN: lxBot2, localeID: "en_US", nativeID: lxLocB2En},
	}
	if got := sortedLXLocales(locales); *warns != 1 || total != 3 || !slices.Equal(got, want) {
		t.Errorf("warnings = %d, total = %d, locales = %v; want 1, 3, %v", *warns, total, got, want)
	}
	rows := rowsByNativeID(t, st, TypeLexBotLocale)
	en := rows[lxLocB2En]
	if sv(en.Name) != "summary-en_US" || sv(en.Status) != "NotBuilt" || en.CreatedAt != nil || !strings.Contains(en.AttributesJSON, `"LocaleName":"summary-en_US"`) ||
		strings.Contains(en.AttributesJSON, "NluIntentConfidenceThreshold") {
		t.Errorf("denied en_US name/status/created = %q/%q/%v, attrs = %s; want the summary", sv(en.Name), sv(en.Status), en.CreatedAt, en.AttributesJSON)
	}
	if fr := rows[lxLocB1Fr]; sv(fr.Name) != "name-fr_FR" || sv(fr.Status) != "Built" {
		t.Errorf("described fr_FR name/status = %q/%q, want the describe body", sv(fr.Name), sv(fr.Status))
	}
	assertContainsNative(t, st, lxBot2, lxLocB2En)
}

// With DescribeBotLocale denied throughout, every locale's intents, slot
// types and slots are still stored under it.
func TestScanLex_DescribeLocaleDeniedKeepsChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	handlers := lxTopLevelHandlers()
	delete(handlers, "DescribeBotLocale")
	acct.cfg = opStubConfig(handlers, lxDenied)

	if _, _, err := scanLex(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanLex: %v", err)
	}
	for typ, want := range map[string]int{TypeLexBotLocale: lxTopLocales, TypeLexSlotType: lxTopSlotTypes, TypeLexIntent: lxTopIntents, TypeLexSlot: lxTopSlots} {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s rows = %d, want %d", typ, n, want)
		}
	}
	assertContainsNative(t, st, lxLocB2En, lxLocB2En+"/intent/A")
}

// Any other describe error fails the phase, reported as a DescribeBotLocale
// error for that locale.
func TestScanLexBotLocales_DescribeOther(t *testing.T) {
	st := newTestStore(t)
	_, _, _, err := scanLXLocales(t, st, lxBots, nil, map[string]error{"B2/en_US": lxBadInput})
	assertAPICode(t, err, "ValidationException")
	var oe *smithy.OperationError
	if !errors.As(err, &oe) || oe.OperationName != "DescribeBotLocale" || !strings.Contains(err.Error(), lxLocB2En) {
		t.Errorf("err = %v, want a DescribeBotLocale operation error naming %s", err, lxLocB2En)
	}
}

// lxChildPhase is one locale- or intent-keyed child list phase run against
// two parents, B1's (paged) and B2's.
type lxChildPhase struct {
	op, typ   string
	parentTyp string
	parents   [2]string // parent NativeIDs: B1's, B2's
	failKey   string    // B2's parent key in the handler
	handler   func(calls *parentCalls, fail map[string]error) func(in any) (any, error)
	run       func(client lexAPI, st *store.Store) (int, error)
	wantKeys  []string
	kind      string // NativeID collection segment
}

var lxChildPhases = []lxChildPhase{
	{
		op: "ListIntents", typ: TypeLexIntent, parentTyp: TypeLexBotLocale,
		parents: [2]string{lxLocB1En, lxLocB2En}, failKey: "B2/en_US", handler: lxIntentsHandler,
		run: func(client lexAPI, st *store.Store) (int, error) {
			_, total, _, err := scanLexIntents(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, lxLocales)
			return total, err
		},
		wantKeys: []string{"B1/en_US", "B2/en_US"}, kind: "/intent/",
	},
	{
		op: "ListSlotTypes", typ: TypeLexSlotType, parentTyp: TypeLexBotLocale,
		parents: [2]string{lxLocB1En, lxLocB2En}, failKey: "B2/en_US", handler: lxSlotTypesHandler,
		run: func(client lexAPI, st *store.Store) (int, error) {
			total, _, err := scanLexSlotTypes(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, lxLocales)
			return total, err
		},
		wantKeys: []string{"B1/en_US", "B2/en_US"}, kind: "/slot-type/",
	},
	{
		op: "ListSlots", typ: TypeLexSlot, parentTyp: TypeLexIntent,
		parents: [2]string{lxIntB1EnA, lxIntB2EnA}, failKey: "B2/en_US/A", handler: lxSlotsHandler,
		run: func(client lexAPI, st *store.Store) (int, error) {
			_, total, err := scanLexSlots(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, lxIntents)
			return total, err
		},
		wantKeys: []string{"B1/en_US/A", "B2/en_US/A"}, kind: "/slot/",
	},
}

func (p lxChildPhase) scan(t *testing.T, st *store.Store, handler func(in any) (any, error)) (int, error) {
	t.Helper()
	seedParentRows(t, st, p.parentTyp, p.parents[0], p.parents[1])
	return p.run(lxClient(map[string]func(any) (any, error){p.op: handler}), st)
}

// Both parents are listed for DRAFT, both of B1's pages are stored, the
// element without an id is dropped, the same child id under two parents
// stays two rows, and each child is contained by its parent.
func TestScanLexChildPhases_StoresEveryPageOfEveryParent(t *testing.T) {
	for _, p := range lxChildPhases {
		t.Run(p.op, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			total, err := p.scan(t, st, p.handler(&calls, nil))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := calls.distinct(); !slices.Equal(got, p.wantKeys) {
				t.Errorf("parents listed = %v, want %v", got, p.wantKeys)
			}
			b1A, b1B, b2A := p.parents[0]+p.kind+"A", p.parents[0]+p.kind+"B", p.parents[1]+p.kind+"A"
			rows := rowsByNativeID(t, st, p.typ)
			ids := slices.Sorted(maps.Keys(rows))
			if want := []string{b1A, b1B, b2A}; total != 3 || !slices.Equal(ids, want) {
				t.Fatalf("total = %d, rows = %v; want 3, %v", total, ids, want)
			}
			if r := rows[b1B]; !strings.HasSuffix(sv(r.Name), "-B") || sv(r.Region) != "us-east-1" || !strings.Contains(r.AttributesJSON, `Id":"B"`) {
				t.Errorf("B name/region = %q/%q, attrs = %s", sv(r.Name), sv(r.Region), r.AttributesJSON)
			}
			assertContainsNative(t, st, p.parents[0], b1A)
			assertContainsNative(t, st, p.parents[0], b1B)
			assertContainsNative(t, st, p.parents[1], b2A)
		})
	}
}

func TestScanLexChildPhases_Empty(t *testing.T) {
	empty := map[string]any{
		"ListIntents":   &lexmodelsv2.ListIntentsOutput{},
		"ListSlotTypes": &lexmodelsv2.ListSlotTypesOutput{},
		"ListSlots":     &lexmodelsv2.ListSlotsOutput{},
	}
	for _, p := range lxChildPhases {
		t.Run(p.op, func(t *testing.T) {
			st := newTestStore(t)
			total, err := p.scan(t, st, func(any) (any, error) { return empty[p.op], nil })
			if err != nil || total != 0 || len(rowsByNativeID(t, st, p.typ)) != 0 {
				t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
			}
		})
	}
}

// A denied parent, and a parent not found (none of the ops models a
// not-found code), each warn for the op and keep the other parent's rows;
// any other error fails the phase.
func TestScanLexChildPhases_FailedParent(t *testing.T) {
	for _, p := range lxChildPhases {
		for name, fail := range map[string]error{"denied": lxDenied, "gone": lxGone} {
			t.Run(p.op+"/"+name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "lexmodelsv2:"+p.op)
				var calls parentCalls
				total, err := p.scan(t, st, p.handler(&calls, map[string]error{p.failKey: fail}))
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if n := len(rowsByNativeID(t, st, p.typ)); *warns != 1 || total != 2 || n != 2 {
					t.Errorf("warnings = %d, total = %d, rows = %d; want 1, 2, 2", *warns, total, n)
				}
			})
		}
		t.Run(p.op+"/other", func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			_, err := p.scan(t, st, p.handler(&calls, map[string]error{p.failKey: lxBadInput}))
			assertAPICode(t, err, "ValidationException")
		})
	}
}

// lxTopLevelHandlers serves bots B1 and B2 with no aliases, versions or
// policies, one test set, and the DRAFT model of both bots.
func lxTopLevelHandlers() map[string]func(in any) (any, error) {
	var locales, intents, slotTypes, slots parentCalls
	return map[string]func(in any) (any, error){
		"ListBots": func(any) (any, error) {
			return &lexmodelsv2.ListBotsOutput{BotSummaries: []lextypes.BotSummary{
				{BotId: ptrStr("B1"), BotName: ptrStr("bot-1")}, {BotId: ptrStr("B2"), BotName: ptrStr("bot-2")},
			}}, nil
		},
		"DescribeBot": func(in any) (any, error) {
			return &lexmodelsv2.DescribeBotOutput{BotId: in.(*lexmodelsv2.DescribeBotInput).BotId}, nil
		},
		"ListBotAliases":         func(any) (any, error) { return &lexmodelsv2.ListBotAliasesOutput{}, nil },
		"ListBotVersions":        func(any) (any, error) { return &lexmodelsv2.ListBotVersionsOutput{}, nil },
		"DescribeResourcePolicy": func(any) (any, error) { return nil, lxGone },
		"ListTestSets": func(any) (any, error) {
			return &lexmodelsv2.ListTestSetsOutput{TestSets: []lextypes.TestSetSummary{{TestSetId: ptrStr("T1"), TestSetName: ptrStr("ts")}}}, nil
		},
		"ListBotLocales":    lxLocalesHandler(&locales, nil),
		"DescribeBotLocale": lxDescribeLocaleHandler(nil),
		"ListIntents":       lxIntentsHandler(&intents, nil),
		"ListSlotTypes":     lxSlotTypesHandler(&slotTypes, nil),
		"ListSlots":         lxSlotsHandler(&slots, nil),
	}
}

// Fan-out sizes of lxTopLevelHandlers: 2 bots + 1 test set; 3 locales (B1:
// en_US fr_FR, B2: en_US); A and B under each B1 locale and A under B2's, so 5
// intents and 5 slot types; A and B under each of the 4 B1 intents and A under
// B2's, so 9 slots.
const (
	lxTopExisting  = 3
	lxTopLocales   = 3
	lxTopSlotTypes = 5
	lxTopIntents   = 5
	lxTopSlots     = 9
)

func TestScanLex_StoresDraftModel(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(lxTopLevelHandlers(), lxDenied)

	total, _, err := scanLex(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanLex: %v", err)
	}
	if want := lxTopExisting + lxTopLocales + lxTopSlotTypes + lxTopIntents + lxTopSlots; total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
	for typ, want := range map[string]int{TypeLexBotLocale: lxTopLocales, TypeLexSlotType: lxTopSlotTypes, TypeLexIntent: lxTopIntents, TypeLexSlot: lxTopSlots} {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s rows = %d, want %d", typ, n, want)
		}
	}
	assertContainsNative(t, st, lxBot2, lxLocB2En)
	assertContainsNative(t, st, lxLocB1Fr, lxLocB1Fr+"/intent/A")
	assertContainsNative(t, st, lxLocB1Fr, lxLocB1Fr+"/slot-type/A")
	assertContainsNative(t, st, lxLocB1Fr+"/intent/A", lxLocB1Fr+"/intent/A/slot/A")
}

// The DRAFT model phases run after every existing phase (the test set row,
// stored last of those, is present), a phase's hard error reaches the caller
// with the counts stored before it, the slot type and intent phases do not
// stop each other, and a failed intent phase leaves no slot parents.
func TestScanLex_DraftModelPhaseErrors(t *testing.T) {
	for _, tc := range []struct {
		op        string
		wantTotal int
		wantRows  map[string]int
	}{
		{
			op: "ListBotLocales", wantTotal: lxTopExisting,
			wantRows: map[string]int{TypeLexBotLocale: 0, TypeLexSlotType: 0, TypeLexIntent: 0, TypeLexSlot: 0},
		},
		{
			op: "ListSlotTypes", wantTotal: lxTopExisting + lxTopLocales + lxTopIntents + lxTopSlots,
			wantRows: map[string]int{TypeLexBotLocale: lxTopLocales, TypeLexSlotType: 0, TypeLexIntent: lxTopIntents, TypeLexSlot: lxTopSlots},
		},
		{
			op: "ListIntents", wantTotal: lxTopExisting + lxTopLocales + lxTopSlotTypes,
			wantRows: map[string]int{TypeLexBotLocale: lxTopLocales, TypeLexSlotType: lxTopSlotTypes, TypeLexIntent: 0, TypeLexSlot: 0},
		},
		{
			op: "ListSlots", wantTotal: lxTopExisting + lxTopLocales + lxTopSlotTypes + lxTopIntents,
			wantRows: map[string]int{TypeLexBotLocale: lxTopLocales, TypeLexSlotType: lxTopSlotTypes, TypeLexIntent: lxTopIntents, TypeLexSlot: 0},
		},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			handlers := lxTopLevelHandlers()
			var failed atomic.Bool
			handlers[tc.op] = func(any) (any, error) {
				failed.Store(true)
				return nil, lxBadInput
			}
			acct.cfg = opStubConfig(handlers, lxDenied)

			total, _, err := scanLex(context.Background(), acct, testRegion, st, testScanID)
			if !failed.Load() {
				t.Fatalf("%s never called", tc.op)
			}
			assertAPICode(t, err, "ValidationException")
			if n := len(rowsByNativeID(t, st, TypeLexTestSet)); n != 1 {
				t.Errorf("test set rows = %d, want 1 before the %s phase", n, tc.op)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			for typ, want := range tc.wantRows {
				if n := len(rowsByNativeID(t, st, typ)); n != want {
					t.Errorf("%s rows = %d, want %d", typ, n, want)
				}
			}
		})
	}
}
