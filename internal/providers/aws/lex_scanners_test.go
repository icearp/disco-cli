package aws

import (
	"context"
	"errors"
	"maps"
	"net"
	"path"
	"slices"
	"strings"
	"sync"
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

var lxThrottle = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "slow down"}

// lxTagFixture answers every op of a scan and records its calls. ListBots
// lists B1, then B2 and a bot without an id; ListTestSets T1, then T2 and one
// without an id or a name; B1's aliases are TSTALIASID, then AL1 and one without an id,
// and B2's TSTALIASID; each bot has version 1, and B1 one without an id too;
// B1 has a resource policy and B2 none; no bot has a DRAFT locale. The bot B1, its alias AL1 and the test set
// T1 are tagged with {"id": <their id>}; the other rows have no tags. Every
// alias, version and test set and every DescribeBot body carries a
// CreationDateTime, which no row stores as CreatedAt. errs
// fails a call: "<Op>/<key>", where key is the ARN for ListTagsForResource,
// the bot id for DescribeBot, ListBotVersions and DescribeResourcePolicy, and
// the bot id or page token, then "/" and the token for a later page, for the
// paged listings (ListBots and ListTestSets have no bot id: "p2" is their
// second page). Handlers run on fan-out goroutines, so mu guards the state
// they share.
type lxTagFixture struct {
	partition, region string
	mu                sync.Mutex
	errs              map[string]error
	tagARNs           []string
	keys              map[string][]string
	maxResults        map[string][]int32
}

const lxTagTotal = 10 // 2 bots, 3 aliases, 2 versions, 1 policy, 2 test sets

func newLXTagFixture(partition, region string) *lxTagFixture {
	return &lxTagFixture{partition: partition, region: region, errs: map[string]error{}, keys: map[string][]string{}, maxResults: map[string][]int32{}}
}

// arn is the fixture's ARN of resource, built independently of lexARN.
func (f *lxTagFixture) arn(resource string) string {
	return "arn:" + f.partition + ":lex:" + f.region + ":" + testAccountID + ":" + resource
}

// taggedARNs is the ARN of every row whose tags the scan reads, sorted.
func (f *lxTagFixture) taggedARNs() []string {
	return slices.Sorted(slices.Values([]string{
		f.arn("bot/B1"), f.arn("bot/B2"),
		f.arn("bot-alias/B1/TSTALIASID"), f.arn("bot-alias/B1/AL1"), f.arn("bot-alias/B2/TSTALIASID"),
		f.arn("test-set/T1"), f.arn("test-set/T2"),
	}))
}

// call records op's call under key with in's MaxResults and returns the
// error errs holds for it.
func (f *lxTagFixture) call(op, key string, in any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[op] = append(f.keys[op], key)
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		f.maxResults[op] = append(f.maxResults[op], *n)
	}
	return f.errs[op+"/"+key]
}

func (f *lxTagFixture) called(op string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.keys[op]))
}

func (f *lxTagFixture) sortedTagARNs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.tagARNs))
}

// pageKey is a paged listing's errs key: key, then "/" and the token for a
// later page.
func lxPageKey(key string, token *string) string {
	if token == nil {
		return key
	}
	return strings.TrimPrefix(key+"/"+*token, "/")
}

func (f *lxTagFixture) handlers() map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListBots": func(in any) (any, error) {
			req := in.(*lexmodelsv2.ListBotsInput)
			if err := f.call("ListBots", lxPageKey("", req.NextToken), in); err != nil {
				return nil, err
			}
			if req.NextToken == nil {
				return &lexmodelsv2.ListBotsOutput{BotSummaries: []lextypes.BotSummary{
					{BotId: ptrStr("B1"), BotName: ptrStr("bot-1"), BotStatus: lextypes.BotStatusAvailable},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &lexmodelsv2.ListBotsOutput{BotSummaries: []lextypes.BotSummary{
				{BotId: ptrStr("B2"), BotName: ptrStr("bot-2"), BotStatus: lextypes.BotStatusAvailable},
				{BotName: ptrStr("nameless")},
			}}, nil
		},
		"DescribeBot": func(in any) (any, error) {
			id := sv(in.(*lexmodelsv2.DescribeBotInput).BotId)
			if err := f.call("DescribeBot", id, in); err != nil {
				return nil, err
			}
			return &lexmodelsv2.DescribeBotOutput{
				BotId: &id, BotName: ptrStr("described-" + id), BotStatus: lextypes.BotStatusAvailable, CreationDateTime: &lxCreated,
				RoleArn: ptrStr("arn:" + f.partition + ":iam::" + testAccountID + ":role/lex-" + id),
			}, nil
		},
		"ListBotAliases": func(in any) (any, error) {
			req := in.(*lexmodelsv2.ListBotAliasesInput)
			id := sv(req.BotId)
			if err := f.call("ListBotAliases", lxPageKey(id, req.NextToken), in); err != nil {
				return nil, err
			}
			alias := func(aliasID string) lextypes.BotAliasSummary {
				return lextypes.BotAliasSummary{BotAliasId: nonEmptyPtr(aliasID), BotAliasName: ptrStr("alias-" + aliasID), BotAliasStatus: lextypes.BotAliasStatusAvailable, BotVersion: ptrStr("1"), CreationDateTime: &lxCreated}
			}
			if id != "B1" {
				return &lexmodelsv2.ListBotAliasesOutput{BotAliasSummaries: []lextypes.BotAliasSummary{alias("TSTALIASID")}}, nil
			}
			if req.NextToken == nil {
				return &lexmodelsv2.ListBotAliasesOutput{BotAliasSummaries: []lextypes.BotAliasSummary{alias("TSTALIASID")}, NextToken: ptrStr("p2")}, nil
			}
			return &lexmodelsv2.ListBotAliasesOutput{BotAliasSummaries: []lextypes.BotAliasSummary{alias("AL1"), alias("")}}, nil
		},
		"ListBotVersions": func(in any) (any, error) {
			id := sv(in.(*lexmodelsv2.ListBotVersionsInput).BotId)
			if err := f.call("ListBotVersions", id, in); err != nil {
				return nil, err
			}
			out := &lexmodelsv2.ListBotVersionsOutput{BotVersionSummaries: []lextypes.BotVersionSummary{
				{BotVersion: ptrStr("1"), BotName: ptrStr("bot-" + id), BotStatus: lextypes.BotStatusAvailable, CreationDateTime: &lxCreated},
			}}
			if id == "B1" {
				out.BotVersionSummaries = append(out.BotVersionSummaries, lextypes.BotVersionSummary{BotName: ptrStr("bot-B1"), CreationDateTime: &lxCreated})
			}
			return out, nil
		},
		"DescribeResourcePolicy": func(in any) (any, error) {
			arn := sv(in.(*lexmodelsv2.DescribeResourcePolicyInput).ResourceArn)
			id := lexBotIDOf(arn)
			if err := f.call("DescribeResourcePolicy", id, in); err != nil {
				return nil, err
			}
			if id != "B1" {
				return nil, lxGone
			}
			return &lexmodelsv2.DescribeResourcePolicyOutput{ResourceArn: &arn, Policy: ptrStr(`{"Version":"2012-10-17"}`), RevisionId: ptrStr("r1")}, nil
		},
		"ListTestSets": func(in any) (any, error) {
			req := in.(*lexmodelsv2.ListTestSetsInput)
			if err := f.call("ListTestSets", lxPageKey("", req.NextToken), in); err != nil {
				return nil, err
			}
			if req.NextToken == nil {
				return &lexmodelsv2.ListTestSetsOutput{TestSets: []lextypes.TestSetSummary{
					{TestSetId: ptrStr("T1"), TestSetName: ptrStr("ts-1"), Status: lextypes.TestSetStatusReady, Modality: lextypes.TestSetModalityText, CreationDateTime: &lxCreated},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &lexmodelsv2.ListTestSetsOutput{TestSets: []lextypes.TestSetSummary{
				{TestSetId: ptrStr("T2"), TestSetName: ptrStr("ts-2"), Status: lextypes.TestSetStatusReady, CreationDateTime: &lxCreated},
				{Status: lextypes.TestSetStatusReady, CreationDateTime: &lxCreated},
			}}, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			arn := sv(in.(*lexmodelsv2.ListTagsForResourceInput).ResourceARN)
			f.mu.Lock()
			f.tagARNs = append(f.tagARNs, arn)
			err := f.errs["ListTagsForResource/"+arn]
			f.mu.Unlock()
			if err != nil {
				return nil, err
			}
			for _, id := range []string{"bot/B1", "bot-alias/B1/AL1", "test-set/T1"} {
				if arn == f.arn(id) {
					return &lexmodelsv2.ListTagsForResourceOutput{Tags: map[string]string{"id": path.Base(id)}}, nil
				}
			}
			return &lexmodelsv2.ListTagsForResourceOutput{}, nil
		},
		"ListBotLocales": func(any) (any, error) { return &lexmodelsv2.ListBotLocalesOutput{}, nil },
	}
}

// scan runs the scan's real entry point against f in f's partition and
// region; every op f does not answer is denied.
func (f *lxTagFixture) scan(ctx context.Context, st *store.Store) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.Partition = f.partition
	acct.cfg = opStubConfig(f.handlers(), lxDenied)
	return scanLex(ctx, acct, f.region, st, testScanID)
}

func lxWarnOps(w []store.ScanWarning) []string {
	ops := make([]string, len(w))
	for i, x := range w {
		ops[i] = x.Service
	}
	return slices.Sorted(slices.Values(ops))
}

// lxNoIDKeys is the key each warning names as its first element, by op: the
// text after "; first ".
func lxNoIDKeys(w []store.ScanWarning) map[string]string {
	keys := make(map[string]string, len(w))
	for _, x := range w {
		_, key, _ := strings.Cut(x.Message, "; first ")
		keys[x.Service] = key
	}
	return keys
}

// Every row is stored under the ARN built from its listed ids, and the bots,
// aliases and test sets with the tags ListTagsForResource returns (nil for
// none): the op is sent exactly their ARNs, never a version's or a policy's.
// Each bot's attributes are its DescribeBot body, its name and status its
// summary's. Elements without an id warn once per listing, named by their
// name, their bot, or noIdentifier when they have neither. Every listing
// sends MaxResults 1000.
func TestScanLex_StoresTagsOfTaggedTypes(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newLXTagFixture("aws", testRegion)

	total, inserted, err := f.scan(context.Background(), st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != lxTagTotal || inserted != lxTagTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, lxTagTotal, lxTagTotal)
	}
	if got, want := f.sortedTagARNs(), f.taggedARNs(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
	wantIDs := map[string][]string{
		TypeLexBot:            {f.arn("bot/B1"), f.arn("bot/B2")},
		TypeLexBotAlias:       {f.arn("bot-alias/B1/AL1"), f.arn("bot-alias/B1/TSTALIASID"), f.arn("bot-alias/B2/TSTALIASID")},
		TypeLexBotVersion:     {f.arn("bot/B1/version/1"), f.arn("bot/B2/version/1")},
		TypeLexResourcePolicy: {f.arn("bot/B1/policy")},
		TypeLexTestSet:        {f.arn("test-set/T1"), f.arn("test-set/T2")},
	}
	rows := map[string]map[string]store.Resource{}
	for typ, want := range wantIDs {
		rows[typ] = rowsByNativeID(t, st, typ)
		if got := slices.Sorted(maps.Keys(rows[typ])); !slices.Equal(got, want) {
			t.Errorf("%s rows = %v; want %v", typ, got, want)
		}
	}
	for typ, tagged := range map[string]string{TypeLexBot: "bot/B1", TypeLexBotAlias: "bot-alias/B1/AL1", TypeLexTestSet: "test-set/T1"} {
		for id, r := range rows[typ] {
			if id == f.arn(tagged) {
				assertTagsMap(t, r.TagsJSON, map[string]string{"id": path.Base(tagged)})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", id, *r.TagsJSON)
			}
		}
	}
	for _, typ := range []string{TypeLexBotVersion, TypeLexResourcePolicy} {
		for id, r := range rows[typ] {
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", id, *r.TagsJSON)
			}
		}
	}
	for _, c := range []struct {
		typ, id, name, status, attr string
	}{
		{TypeLexBot, "bot/B1", "bot-1", "Available", `"RoleArn":"arn:aws:iam::123456789012:role/lex-B1"`},
		{TypeLexBotAlias, "bot-alias/B1/AL1", "alias-AL1", "Available", `"BotVersion":"1"`},
		{TypeLexBotVersion, "bot/B2/version/1", "1", "Available", `"BotName":"bot-B2"`},
		{TypeLexResourcePolicy, "bot/B1/policy", "policy", "", `"RevisionId":"r1"`},
		{TypeLexTestSet, "test-set/T1", "ts-1", "Ready", `"Modality":"Text"`},
	} {
		r := rows[c.typ][f.arn(c.id)]
		if sv(r.Name) != c.name || sv(r.Status) != c.status || sv(r.Region) != testRegion || !strings.Contains(r.AttributesJSON, c.attr) {
			t.Errorf("%s name/status/region = %q/%q/%q, attrs %s; want %q/%q/%q, %s", c.id, sv(r.Name), sv(r.Status), sv(r.Region), r.AttributesJSON, c.name, c.status, testRegion, c.attr)
		}
		if r.CreatedAt != nil {
			t.Errorf("%s CreatedAt = %q; want nil", c.id, *r.CreatedAt)
		}
	}
	if p := rows[TypeLexResourcePolicy][f.arn("bot/B1/policy")]; p.Status != nil {
		t.Errorf("policy Status = %q; want nil", *p.Status)
	}
	for op, want := range map[string][]int32{
		"ListBots": {1000, 1000}, "ListBotAliases": {1000, 1000, 1000}, "ListBotVersions": {1000, 1000}, "ListTestSets": {1000, 1000},
	} {
		if got := f.maxResults[op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", op, got, want)
		}
	}
	wantKeys := map[string]string{
		lexListBotsOp:                 "nameless",
		"lexmodelsv2:ListBotAliases":  "alias- of bot B1",
		"lexmodelsv2:ListBotVersions": "a version of bot B1",
		"lexmodelsv2:ListTestSets":    noIdentifier,
	}
	if w := warns(); len(w) != len(wantKeys) || !maps.Equal(lxNoIDKeys(w), wantKeys) {
		t.Errorf("warnings = %v; want one per listing with an element without an id, naming %v", w, wantKeys)
	}
}

// A test set without an id is named in its listing's warning by its name, and
// an alias with neither an id nor a name by noIdentifier and its bot. Each
// case replaces the listing's second page, which only B1's aliases have.
func TestScanLex_NoIDKeys(t *testing.T) {
	for _, c := range []struct {
		op, handler, want string
		page              any
	}{
		{
			"lexmodelsv2:ListTestSets", "ListTestSets", "ts-x",
			&lexmodelsv2.ListTestSetsOutput{TestSets: []lextypes.TestSetSummary{{TestSetName: ptrStr("ts-x")}}},
		},
		{
			"lexmodelsv2:ListBotAliases", "ListBotAliases", noIdentifier + " of bot B1",
			&lexmodelsv2.ListBotAliasesOutput{BotAliasSummaries: []lextypes.BotAliasSummary{{BotAliasStatus: lextypes.BotAliasStatusAvailable}}},
		},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newLXTagFixture("aws", testRegion)
			handlers := f.handlers()
			first := handlers[c.handler]
			handlers[c.handler] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") == nil {
					return first(in)
				}
				return c.page, nil
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(handlers, lxDenied)

			if _, _, err := scanLex(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := lxNoIDKeys(warns())[c.op]; got != c.want {
				t.Errorf("%s warning key = %q; want %q", c.op, got, c.want)
			}
		})
	}
}

// A rescan stores the same rows and inserts none.
func TestScanLex_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	f := newLXTagFixture("aws", testRegion)
	if _, _, err := f.scan(context.Background(), st); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := f.scan(context.Background(), st)
	if err != nil || total != lxTagTotal || inserted != 0 {
		t.Errorf("rescan total, inserted, err = %d, %d, %v; want %d, 0, nil", total, inserted, err, lxTagTotal)
	}
}

// With no bots and no test sets, no per-bot op and no tag read is called.
func TestScanLex_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	f := newLXTagFixture("aws", testRegion)
	handlers := f.handlers()
	handlers["ListBots"] = func(in any) (any, error) {
		if err := f.call("ListBots", "", in); err != nil {
			return nil, err
		}
		return &lexmodelsv2.ListBotsOutput{}, nil
	}
	handlers["ListTestSets"] = func(in any) (any, error) {
		if err := f.call("ListTestSets", "", in); err != nil {
			return nil, err
		}
		return &lexmodelsv2.ListTestSetsOutput{}, nil
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, lxDenied)

	total, _, err := scanLex(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	for _, op := range []string{"ListBots", "ListTestSets"} {
		if got := f.called(op); len(got) != 1 {
			t.Errorf("%s calls = %v; want 1", op, got)
		}
	}
	for _, op := range []string{"DescribeBot", "ListBotAliases", "ListBotVersions", "DescribeResourcePolicy"} {
		if got := f.called(op); len(got) != 0 {
			t.Errorf("%s calls = %v; want none", op, got)
		}
	}
	if got := f.sortedTagARNs(); len(got) != 0 {
		t.Errorf("ListTagsForResource ARNs = %v; want none", got)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently, and a bot dropped so is no
// parent of the per-bot phases. A denial keeps the row untagged and warns
// once under the tag op with the scan's scope; a throttle keeps it and is
// transient; any other error keeps it and is an error. Every listing with an
// element without an id warns about it, B1's aliases and versions only when
// B1 is stored.
func TestScanLex_TagReadFailures(t *testing.T) {
	const (
		aliases  = "lexmodelsv2:ListBotAliases"
		versions = "lexmodelsv2:ListBotVersions"
		sets     = "lexmodelsv2:ListTestSets"
	)
	for _, c := range []struct {
		name      string
		resource  string
		typ       string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		warnOps   []string // sorted
	}{
		{"bot/not found", "bot/B1", TypeLexBot, lxGone, -1, "", lxTagTotal - 5, []string{lexListBotsOp, sets}},
		{"bot/denied", "bot/B1", TypeLexBot, lxDenied, -1, "", lxTagTotal, []string{aliases, versions, lexListBotsOp, lexTagsOp, sets}},
		{"bot alias/not found", "bot-alias/B1/AL1", TypeLexBotAlias, lxGone, -1, "", lxTagTotal - 1, []string{aliases, versions, lexListBotsOp, sets}},
		{"bot alias/transient", "bot-alias/B1/AL1", TypeLexBotAlias, lxThrottle, outcomeTransient, "ThrottlingException", lxTagTotal, []string{aliases, versions, lexListBotsOp, sets}},
		{"test set/not found", "test-set/T1", TypeLexTestSet, lxGone, -1, "", lxTagTotal - 1, []string{aliases, versions, lexListBotsOp, sets}},
		{"test set/other error", "test-set/T1", TypeLexTestSet, lxBadInput, outcomeError, "ValidationException", lxTagTotal, []string{aliases, versions, lexListBotsOp, sets}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newLXTagFixture("aws", testRegion)
			arn := f.arn(c.resource)
			f.errs["ListTagsForResource/"+arn] = c.err

			total, _, err := f.scan(context.Background(), st)
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
			r, kept := rowsByNativeID(t, st, c.typ)[arn]
			if wantKept := c.err != lxGone; kept != wantKept {
				t.Fatalf("row stored = %v; want %v", kept, wantKept)
			}
			if kept && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			if got := lxWarnOps(w); !slices.Equal(got, c.warnOps) {
				t.Fatalf("warnings = %v; want ops %v", w, c.warnOps)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A bot whose tag read finds it deleted is no parent: its aliases, versions
// and policy are not read.
func TestScanLex_BotGoneHasNoChildren(t *testing.T) {
	st := newTestStore(t)
	f := newLXTagFixture("aws", testRegion)
	f.errs["ListTagsForResource/"+f.arn("bot/B1")] = lxGone

	if _, _, err := f.scan(context.Background(), st); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, op := range []string{"ListBotAliases", "ListBotVersions", "DescribeResourcePolicy"} {
		if got := slices.Compact(f.called(op)); !slices.Equal(got, []string{"B2"}) {
			t.Errorf("%s keys = %v; want [B2]", op, got)
		}
	}
}

// Denied tag reads in three phases send one warning for the whole scan; the
// rows are kept.
func TestScanLex_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, lexTagsOp)
	f := newLXTagFixture("aws", testRegion)
	for _, r := range []string{"bot/B2", "bot-alias/B2/TSTALIASID", "test-set/T2"} {
		f.errs["ListTagsForResource/"+f.arn(r)] = lxDenied
	}

	total, _, err := f.scan(context.Background(), st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != lxTagTotal || *warns != 1 {
		t.Errorf("total = %d, %s warnings = %d; want %d, 1", total, lexTagsOp, *warns, lxTagTotal)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanLex_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, lexTagsOp)
	f := newLXTagFixture("aws", testRegion)
	f.errs["ListTagsForResource/"+f.arn("bot-alias/B1/AL1")] = lxThrottle
	f.errs["ListTagsForResource/"+f.arn("test-set/T1")] = lxBadInput

	_, _, err := f.scan(context.Background(), st)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", lexTagsOp, *warns)
	}
}

// A failed DescribeBot. ResourceNotFoundException is modeled, so a bot
// deleted since it was listed is dropped silently, with no tag read and no
// children. Any other failure keeps the BotSummary as the row, its tags read:
// a denial warns once under DescribeBot, a throttle is transient, and any
// other error is an error.
func TestScanLex_DescribeBotFailures(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		outcome serviceOutcome // -1: no error
		code    string
		warn    bool
	}{
		{"not found", lxGone, -1, "", false},
		{"denied", lxDenied, -1, "", true},
		{"transient", lxThrottle, outcomeTransient, "ThrottlingException", false},
		{"other error", lxBadInput, outcomeError, "ValidationException", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "lexmodelsv2:DescribeBot")
			f := newLXTagFixture("aws", testRegion)
			f.errs["DescribeBot/B1"] = c.err

			total, _, err := f.scan(context.Background(), st)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			b1 := f.arn("bot/B1")
			r, kept := rowsByNativeID(t, st, TypeLexBot)[b1]
			tagRead := slices.Contains(f.sortedTagARNs(), b1)
			if c.err == lxGone {
				if kept || tagRead || total != lxTagTotal-5 {
					t.Errorf("stored = %v, tags read = %v, total = %d; want neither, total %d", kept, tagRead, total, lxTagTotal-5)
				}
				return
			}
			if !kept || !tagRead || total != lxTagTotal {
				t.Fatalf("stored = %v, tags read = %v, total = %d; want both, total %d", kept, tagRead, total, lxTagTotal)
			}
			if sv(r.Name) != "bot-1" || strings.Contains(r.AttributesJSON, "RoleArn") || !strings.Contains(r.AttributesJSON, `"BotName":"bot-1"`) {
				t.Errorf("name = %q, attrs = %s; want the BotSummary", sv(r.Name), r.AttributesJSON)
			}
			assertTagsMap(t, r.TagsJSON, map[string]string{"id": "B1"})
			if want := map[bool]int{true: 1, false: 0}[c.warn]; *warns != want {
				t.Errorf("DescribeBot warnings = %d; want %d", *warns, want)
			}
		})
	}
}

// One bot's failed per-bot listing keeps the other bot's rows and the pages
// read before it, and stops no later phase: a denial warns once under its op,
// any other error is returned, ResourceNotFoundException from ListBotAliases
// or ListBotVersions included, since neither op models it. A bot without a
// policy has no row and no warning. warns counts the op's warnings: a
// denial's, plus one for B1's alias without an id when B1's second alias page
// is read.
func TestScanLex_PerBotFailureKeepsSiblingsAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op, key  string
		err      error
		typ      string
		wantRows []string // resources stored of typ
		warns    int
	}{
		{"ListBotAliases", "B1/p2", lxBadInput, TypeLexBotAlias, []string{"bot-alias/B1/TSTALIASID", "bot-alias/B2/TSTALIASID"}, 0},
		{"ListBotAliases", "B2", lxDenied, TypeLexBotAlias, []string{"bot-alias/B1/AL1", "bot-alias/B1/TSTALIASID"}, 2},
		{"ListBotAliases", "B2", lxGone, TypeLexBotAlias, []string{"bot-alias/B1/AL1", "bot-alias/B1/TSTALIASID"}, 1},
		{"ListBotVersions", "B1", lxBadInput, TypeLexBotVersion, []string{"bot/B2/version/1"}, 0},
		{"ListBotVersions", "B1", lxDenied, TypeLexBotVersion, []string{"bot/B2/version/1"}, 1},
		{"ListBotVersions", "B1", lxGone, TypeLexBotVersion, []string{"bot/B2/version/1"}, 0},
		{"DescribeResourcePolicy", "B1", lxDenied, TypeLexResourcePolicy, nil, 1},
		{"DescribeResourcePolicy", "B1", lxBadInput, TypeLexResourcePolicy, nil, 0},
	} {
		code := c.err.(*smithy.GenericAPIError).Code
		t.Run(c.op+"/"+c.key+"/"+code, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "lexmodelsv2:"+c.op)
			f := newLXTagFixture("aws", testRegion)
			f.errs[c.op+"/"+c.key] = c.err

			_, _, err := f.scan(context.Background(), st)
			denied := c.err == lxDenied
			if denied {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertAPICode(t, err, code)
			}
			if *warns != c.warns {
				t.Errorf("%s warnings = %d; want %d", c.op, *warns, c.warns)
			}
			want := make([]string, len(c.wantRows))
			for i, r := range c.wantRows {
				want[i] = f.arn(r)
			}
			if got := slices.Sorted(maps.Keys(rowsByNativeID(t, st, c.typ))); !slices.Equal(got, want) {
				t.Errorf("%s rows = %v; want %v", c.typ, got, want)
			}
			if n := len(rowsByNativeID(t, st, TypeLexTestSet)); n != 2 {
				t.Errorf("test set rows = %d; want 2, the phase after the failure", n)
			}
		})
	}
}

// A failed later page of ListBots or ListTestSets keeps the page read before
// it, is returned, and stops no later phase. A denied ListBots is warned
// about under its op instead, and the later phases run.
func TestScanLex_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		name, key string
		err       error
		wantBots  []string
		wantSets  []string
	}{
		{"bots later page", "ListBots/p2", lxBadInput, []string{"bot/B1"}, []string{"test-set/T1", "test-set/T2"}},
		{"bots denied", "ListBots/", lxDenied, nil, []string{"test-set/T1", "test-set/T2"}},
		{"test sets later page", "ListTestSets/p2", lxBadInput, []string{"bot/B1", "bot/B2"}, []string{"test-set/T1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newLXTagFixture("aws", testRegion)
			f.errs[c.key] = c.err
			var locales parentCalls
			handlers := f.handlers()
			handlers["ListBotLocales"] = lxLocalesHandler(&locales, nil)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(handlers, lxDenied)

			_, _, err := scanLex(context.Background(), acct, testRegion, st, testScanID)
			denied := c.err == lxDenied
			if denied {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if !slices.Contains(lxWarnOps(warns()), lexListBotsOp) {
					t.Errorf("warnings = %v; want one under %s", warns(), lexListBotsOp)
				}
			} else {
				assertAPICode(t, err, "ValidationException")
			}
			for typ, resources := range map[string][]string{TypeLexBot: c.wantBots, TypeLexTestSet: c.wantSets} {
				want := make([]string, len(resources))
				for i, r := range resources {
					want[i] = f.arn(r)
				}
				if got := slices.Sorted(maps.Keys(rowsByNativeID(t, st, typ))); !slices.Equal(got, want) {
					t.Errorf("%s rows = %v; want %v", typ, got, want)
				}
			}
			wantLocaleBots := make([]string, len(c.wantBots))
			for i, b := range c.wantBots {
				wantLocaleBots[i] = path.Base(b)
			}
			if got := locales.distinct(); !slices.Equal(got, wantLocaleBots) {
				t.Errorf("ListBotLocales bots = %v; want %v, the bots stored", got, wantLocaleBots)
			}
		})
	}
}

// An NXDOMAIN from the first ListBots page, with nothing stored, is a region
// that does not serve Lex: the scan is unavailable there and runs no later
// phase. Once bots are stored, a later phase's NXDOMAIN is a failure of this
// scan instead, and the stored rows keep their counts.
func TestScanLex_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "models-v2-lex.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		f := newLXTagFixture("aws", testRegion)
		f.errs["ListBots/"] = nx

		total, _, err := f.scan(context.Background(), st)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 {
			t.Errorf("total = %d; want 0", total)
		}
		if got := f.called("ListTestSets"); len(got) != 0 {
			t.Errorf("ListTestSets calls = %v; want none once the region proved unavailable", got)
		}
	})
	t.Run("after bots were stored", func(t *testing.T) {
		st := newTestStore(t)
		f := newLXTagFixture("aws", testRegion)
		f.errs["ListTestSets/"] = nx

		total, _, err := f.scan(context.Background(), st)
		assertGlueOutcome(t, err, outcomeError)
		if want := lxTagTotal - 2; total != want {
			t.Errorf("total = %d; want the %d rows stored before the test sets", total, want)
		}
		if n := len(rowsByNativeID(t, st, TypeLexBot)); n != 2 {
			t.Errorf("bot rows = %d; want 2", n)
		}
	})
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing; the later phases are not run, and the rows
// stored before it are kept.
func TestScanLex_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	f := newLXTagFixture("aws", testRegion)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var locales parentCalls
	handlers := f.handlers()
	handlers["ListTestSets"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}
	handlers["ListBotLocales"] = lxLocalesHandler(&locales, nil)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, lxDenied)

	total, _, err := scanLex(ctx, acct, testRegion, st, testScanID)
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
	if want := lxTagTotal - 2; total != want {
		t.Errorf("total = %d; want the %d rows stored before ctx ended", total, want)
	}
	if got := locales.distinct(); len(got) != 0 {
		t.Errorf("ListBotLocales bots = %v; want none after ctx ended", got)
	}
}

// A repeated NextToken ends the bot listing with errRepeatedNextToken after
// its first page is stored.
func TestScanLex_RepeatedBotToken(t *testing.T) {
	st := newTestStore(t)
	f := newLXTagFixture("aws", testRegion)
	handlers := f.handlers()
	handlers["ListBots"] = func(any) (any, error) {
		return &lexmodelsv2.ListBotsOutput{BotSummaries: []lextypes.BotSummary{{BotId: ptrStr("B1"), BotName: ptrStr("bot-1")}}, NextToken: ptrStr("same")}, nil
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, lxDenied)

	_, _, err := scanLex(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if got := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeLexBot))); !slices.Equal(got, []string{f.arn("bot/B1")}) {
		t.Errorf("bot rows = %v; want [%s]", got, f.arn("bot/B1"))
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the bots stay stored.
func TestScanLex_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeLexBotAlias)
	f := newLXTagFixture("aws", testRegion)
	f.errs["ListTagsForResource/"+f.arn("bot-alias/B2/TSTALIASID")] = lxDenied

	total, _, err := f.scan(context.Background(), st)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if got, want := lxWarnOps(warns()), []string{lexListBotsOp}; !slices.Equal(got, want) {
		t.Errorf("warnings = %v; want only the bot listing's, sent before the failed write", warns())
	}
	for _, op := range []string{"ListBotVersions", "DescribeResourcePolicy", "ListTestSets"} {
		if got := f.called(op); len(got) != 0 {
			t.Errorf("%s calls = %v; want none after the failed write", op, got)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeLexBot)); total != 2 || n != 2 {
		t.Errorf("total = %d, bot rows = %d; want 2, 2", total, n)
	}
}

// A GovCloud or China ARN reaches the tag op unchanged and is the row's
// NativeID.
func TestScanLex_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range partitionFixtures {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			f := newLXTagFixture(c.partition, c.region)
			if _, _, err := f.scan(context.Background(), st); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := f.sortedTagARNs(), f.taggedARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			for typ, resource := range map[string]string{TypeLexBot: "bot/B1", TypeLexBotAlias: "bot-alias/B1/AL1", TypeLexTestSet: "test-set/T1"} {
				assertTagsMap(t, rowsByNativeID(t, st, typ)[f.arn(resource)].TagsJSON, map[string]string{"id": path.Base(resource)})
			}
			for typ, resource := range map[string]string{TypeLexBotVersion: "bot/B1/version/1", TypeLexResourcePolicy: "bot/B1/policy"} {
				if _, ok := rowsByNativeID(t, st, typ)[f.arn(resource)]; !ok {
					t.Errorf("%s row %s missing", typ, f.arn(resource))
				}
			}
		})
	}
}

// A bot whose DescribeResourcePolicy returns an empty Policy has no policy
// row, and no warning.
func TestScanLex_EmptyPolicyHasNoRow(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "lexmodelsv2:DescribeResourcePolicy")
	f := newLXTagFixture("aws", testRegion)
	handlers := f.handlers()
	describe := handlers["DescribeResourcePolicy"]
	handlers["DescribeResourcePolicy"] = func(in any) (any, error) {
		req := in.(*lexmodelsv2.DescribeResourcePolicyInput)
		if lexBotIDOf(sv(req.ResourceArn)) == "B2" {
			return &lexmodelsv2.DescribeResourcePolicyOutput{ResourceArn: req.ResourceArn}, nil
		}
		return describe(in)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, lxDenied)

	if _, _, err := scanLex(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeLexResourcePolicy))), []string{f.arn("bot/B1/policy")}; !slices.Equal(got, want) || *warns != 0 {
		t.Errorf("policy rows = %v, warnings = %d; want %v, 0", got, *warns, want)
	}
}
