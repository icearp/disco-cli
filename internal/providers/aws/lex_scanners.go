package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/lexmodelsv2"
	lextypes "github.com/aws/aws-sdk-go-v2/service/lexmodelsv2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeLexBot, Service: "lex"})
	registerType(restype.Descriptor{Type: TypeLexBotAlias, Service: "lex"})
	registerType(restype.Descriptor{Type: TypeLexBotLocale, Service: "lex"})
	registerType(restype.Descriptor{Type: TypeLexBotVersion, Service: "lex"})
	registerType(restype.Descriptor{Type: TypeLexIntent, Service: "lex"})
	registerType(restype.Descriptor{Type: TypeLexResourcePolicy, Service: "lex"})
	registerType(restype.Descriptor{Type: TypeLexSlot, Service: "lex"})
	registerType(restype.Descriptor{Type: TypeLexSlotType, Service: "lex"})
	registerType(restype.Descriptor{Type: TypeLexTestSet, Service: "lex"})
	registerService(serviceEntry{
		name: "aws:lex",
		fn:   scanLex,
	})
}

type lexAPI interface {
	ListBots(context.Context, *lexmodelsv2.ListBotsInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListBotsOutput, error)
	DescribeBot(context.Context, *lexmodelsv2.DescribeBotInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.DescribeBotOutput, error)
	ListBotAliases(context.Context, *lexmodelsv2.ListBotAliasesInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListBotAliasesOutput, error)
	ListBotVersions(context.Context, *lexmodelsv2.ListBotVersionsInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListBotVersionsOutput, error)
	DescribeResourcePolicy(context.Context, *lexmodelsv2.DescribeResourcePolicyInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.DescribeResourcePolicyOutput, error)
	ListTestSets(context.Context, *lexmodelsv2.ListTestSetsInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListTestSetsOutput, error)
	ListBotLocales(context.Context, *lexmodelsv2.ListBotLocalesInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListBotLocalesOutput, error)
	DescribeBotLocale(context.Context, *lexmodelsv2.DescribeBotLocaleInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.DescribeBotLocaleOutput, error)
	ListIntents(context.Context, *lexmodelsv2.ListIntentsInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListIntentsOutput, error)
	ListSlotTypes(context.Context, *lexmodelsv2.ListSlotTypesInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListSlotTypesOutput, error)
	ListSlots(context.Context, *lexmodelsv2.ListSlotsInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListSlotsOutput, error)
}

// scanLex discovers Lex V2 bots, aliases, versions, per-bot resource policies,
// test sets, and the DRAFT version's locales, intents, slot types and slots.
// List APIs return only IDs; ARNs synthesized per AWS Lex shape.
func scanLex(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := lexmodelsv2.NewFromConfig(acct.cfg, func(o *lexmodelsv2.Options) { o.Region = region })

	bots, t, i, ferr := scanLexBots(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, b := range bots {
		t, i, ferr = scanLexBotAliases(ctx, client, acct, region, st, scanID, b.id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanLexBotVersions(ctx, client, acct, region, st, scanID, b.id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanLexResourcePolicy(ctx, client, acct, region, st, scanID, b.arn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	t, i, ferr = scanLexTestSets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanLexDraftModel(ctx, client, acct, region, st, scanID, bots)
	total += t
	inserted += i
	return total, inserted, ferr
}

// scanLexTestSets discovers Lex V2 test sets. TestSetSummary carries only
// TestSetId (no ARN), so the NativeID is synthesized per AWS Lex shape.
func scanLexTestSets(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := lexmodelsv2.NewListTestSetsPaginator(client, &lexmodelsv2.ListTestSetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lexmodelsv2:ListTestSets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lexmodelsv2:ListTestSets: %w", err)
		}
		for _, ts := range out.TestSets {
			id := sv(ts.TestSetId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:lex:%s:%s:test-set/%s", region, acct.ID, id)
			status := string(ts.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexTestSet, NativeID: arn,
				Name: ts.TestSetName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(ts), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "lex test-sets")
}

func scanLexBots(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := lexmodelsv2.NewListBotsPaginator(client, &lexmodelsv2.ListBotsInput{})
	var batch []*store.Resource
	var bots []childParent
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "lexmodelsv2:ListBots", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("lexmodelsv2:ListBots: %w", err)
		}
		for _, b := range out.BotSummaries {
			id := sv(b.BotId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:lex:%s:%s:bot/%s", region, acct.ID, id)
			bots = append(bots, childParent{id: id, arn: arn})
			status := string(b.BotStatus)
			// Enrich via DescribeBot — list returns BotSummary (skeleton);
			// RoleArn lives on DescribeBotOutput. Fall back to summary on error.
			var attrsJSON string
			bid := id
			if dout, derr := client.DescribeBot(ctx, &lexmodelsv2.DescribeBotInput{BotId: &bid}); derr == nil {
				attrsJSON = mustJSON(dout)
			} else {
				attrsJSON = mustJSON(b)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexBot, NativeID: arn,
				Name: b.BotName, Region: &region, Status: &status,
				AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "lex bots")
	return bots, t, i, err
}

func scanLexBotAliases(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, botID string) (int, int, error) {
	bid := botID
	pager := lexmodelsv2.NewListBotAliasesPaginator(client, &lexmodelsv2.ListBotAliasesInput{BotId: &bid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lexmodelsv2:ListBotAliases", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lexmodelsv2:ListBotAliases: %w", err)
		}
		for _, a := range out.BotAliasSummaries {
			aid := sv(a.BotAliasId)
			if aid == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:lex:%s:%s:bot-alias/%s/%s", region, acct.ID, bid, aid)
			status := string(a.BotAliasStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexBotAlias, NativeID: arn,
				Name: a.BotAliasName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "lex bot-aliases")
}

func scanLexBotVersions(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, botID string) (int, int, error) {
	bid := botID
	pager := lexmodelsv2.NewListBotVersionsPaginator(client, &lexmodelsv2.ListBotVersionsInput{BotId: &bid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lexmodelsv2:ListBotVersions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lexmodelsv2:ListBotVersions: %w", err)
		}
		for _, v := range out.BotVersionSummaries {
			ver := sv(v.BotVersion)
			if ver == "" {
				continue
			}
			arn := lexBotVersionNativeID(region, acct.ID, bid, ver)
			label := ver
			status := string(v.BotStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexBotVersion, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "lex bot-versions")
}

func scanLexResourcePolicy(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, botARN string) (int, int, error) {
	out, err := client.DescribeResourcePolicy(ctx, &lexmodelsv2.DescribeResourcePolicyInput{ResourceArn: &botARN})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("lexmodelsv2:DescribeResourcePolicy: %w", err)
	}
	if sv(out.Policy) == "" {
		return 0, 0, nil
	}
	arn := botARN + "/policy"
	label := "policy"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeLexResourcePolicy, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "lex resource-policies")
}

// lexDraftVersion is the only bot version whose model is listed. Numbered
// versions are point-in-time snapshots that may differ from DRAFT; scanning
// each would multiply the locale, intent and slot rows per version and is out
// of scope.
const lexDraftVersion = "DRAFT"

// lexMaxPageSize is the documented MaxResults cap of the locale, intent, slot
// type and slot list ops.
const lexMaxPageSize = 1000

func lexBotVersionNativeID(region, accountID, botID, version string) string {
	return fmt.Sprintf("arn:aws:lex:%s:%s:bot/%s/version/%s", region, accountID, botID, version)
}

func lexBotLocaleNativeID(region, accountID, botID, localeID string) string {
	return lexBotVersionNativeID(region, accountID, botID, lexDraftVersion) + "/locale/" + localeID
}

func lexIntentNativeID(localeNativeID, intentID string) string {
	return localeNativeID + "/intent/" + intentID
}

func lexSlotTypeNativeID(localeNativeID, slotTypeID string) string {
	return localeNativeID + "/slot-type/" + slotTypeID
}

func lexSlotNativeID(intentNativeID, slotID string) string {
	return intentNativeID + "/slot/" + slotID
}

// lexLocale is a DRAFT locale listed this scan: the child list ops key on
// both ids, nativeID is its row's NativeID and botARN its parent's.
type lexLocale struct{ botID, botARN, localeID, nativeID string }

// lexIntent is a DRAFT intent stored this scan: ListSlots keys on all three
// ids, nativeID is its row's NativeID.
type lexIntent struct{ botID, localeID, intentID, nativeID string }

// scanLexDraftModel stores the DRAFT locales of the given bots, then each
// locale's slot types and intents, then each intent's slots. The slot type
// and intent phases are independent, so one failing does not stop the other.
func scanLexDraftModel(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, bots []childParent) (total, inserted int, err error) {
	locales, total, inserted, err := scanLexBotLocales(ctx, client, acct, region, st, scanID, bots)
	if err != nil {
		return total, inserted, err
	}
	t, i, slotTypeErr := scanLexSlotTypes(ctx, client, acct, region, st, scanID, locales)
	total += t
	inserted += i
	intents, t, i, intentErr := scanLexIntents(ctx, client, acct, region, st, scanID, locales)
	total += t
	inserted += i
	if intentErr != nil {
		return total, inserted, errors.Join(slotTypeErr, intentErr)
	}
	t, i, slotErr := scanLexSlots(ctx, client, acct, region, st, scanID, intents)
	total += t
	inserted += i
	return total, inserted, errors.Join(slotTypeErr, slotErr)
}

// lexWarnParentGone is the isSkip of the four DRAFT model list ops. None of
// them models a not-found code and none was seen live, so a
// ResourceNotFoundException (a parent deleted after it was listed, say) is
// not skipped silently: it warns once for op and drops only that parent, so
// its siblings are kept.
func lexWarnParentGone(st *store.Store, acct *account, region, op string) func(error) bool {
	var once sync.Once
	return func(err error) bool {
		if !isAPIErrorCode(err, "ResourceNotFoundException") {
			return false
		}
		once.Do(func() {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws",
				Service:  op,
				Scope:    acct.ID + "/" + region,
				Message:  "parent not found, its children not listed this scan: " + err.Error(),
			})
		})
		return true
	}
}

// lexListedLocale is a locale ListBotLocales returned, with its summary.
type lexListedLocale struct {
	lexLocale
	summary lextypes.BotLocaleSummary
}

// scanLexBotLocales lists each bot's DRAFT locales, then stores each locale
// under its bot from DescribeBotLocale, which carries the locale's settings
// (NLU threshold, voice, generative AI) the summary lacks. A locale deleted
// between the list and the describe is skipped. A denied describe warns once
// and stores the locale from its summary instead, because the list ops below
// it may still be allowed and need the locale row as their parent.
func scanLexBotLocales(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, bots []childParent) ([]lexLocale, int, int, error) {
	listed, err := listLexBotLocales(ctx, client, acct, region, st, bots)
	if err != nil {
		return nil, 0, 0, err
	}
	const op = "lexmodelsv2:DescribeBotLocale"
	byNativeID := make(map[string]lexListedLocale, len(listed))
	keys := make([]string, len(listed))
	for i, l := range listed {
		byNativeID[l.nativeID] = l
		keys[i] = l.nativeID
	}
	var denyOnce sync.Once
	draft := lexDraftVersion
	rows, err := describeFanOut(ctx, st, acct, region, op, keys,
		func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") },
		func(ctx context.Context, nativeID string) (*store.Resource, error) {
			l := byNativeID[nativeID]
			d, err := client.DescribeBotLocale(ctx, &lexmodelsv2.DescribeBotLocaleInput{BotId: &l.botID, BotVersion: &draft, LocaleId: &l.localeID})
			if isAccessDenied(err) {
				denyOnce.Do(func() { _ = skipIfAccessDenied(st, op, acct.ID, region, err) })
				return &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeLexBotLocale, NativeID: nativeID,
					Name: l.summary.LocaleName, Region: &region, Status: nonEmptyPtr(string(l.summary.BotLocaleStatus)),
					AttributesJSON: mustJSON(l.summary), DiscoveredBy: scanID,
				}, nil
			}
			if err != nil {
				return nil, err
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexBotLocale, NativeID: nativeID,
				Name: d.LocaleName, Region: &region, Status: nonEmptyPtr(string(d.BotLocaleStatus)),
				CreatedAt: tp(d.CreationDateTime), AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}, nil
		})
	if err != nil {
		return nil, 0, 0, err
	}
	locales := make([]lexLocale, len(rows))
	children := make([]containedChild, len(rows))
	for i, r := range rows {
		locales[i] = byNativeID[r.NativeID].lexLocale
		children[i] = containedChild{row: r, parentID: store.ResourceID("aws", acct.ID, locales[i].botARN)}
	}
	total, inserted, err := upsertContained(st, "lex bot-locales", children)
	if err != nil {
		return nil, 0, 0, err
	}
	return locales, total, inserted, nil
}

// listLexBotLocales lists the DRAFT locales of each bot concurrently
// (fanoutMed). A failed bot is dropped or returned per fanOutErrs.keyErr.
func listLexBotLocales(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, bots []childParent) ([]lexListedLocale, error) {
	const op = "lexmodelsv2:ListBotLocales"
	var (
		mu      sync.Mutex
		locales []lexListedLocale
	)
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: op, isSkip: lexWarnParentGone(st, acct, region, op)}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, bot := range bots {
		g.Go(func() error {
			var found []lexListedLocale
			draft := lexDraftVersion
			pager := lexmodelsv2.NewListBotLocalesPaginator(client, &lexmodelsv2.ListBotLocalesInput{BotId: &bot.id, BotVersion: &draft},
				func(o *lexmodelsv2.ListBotLocalesPaginatorOptions) { o.Limit = lexMaxPageSize })
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return errs.keyErr(bot.id, err)
				}
				for _, l := range out.BotLocaleSummaries {
					localeID := sv(l.LocaleId)
					if localeID == "" {
						continue
					}
					found = append(found, lexListedLocale{
						lexLocale: lexLocale{
							botID: bot.id, botARN: bot.arn, localeID: localeID,
							nativeID: lexBotLocaleNativeID(region, acct.ID, bot.id, localeID),
						},
						summary: l,
					})
				}
			}
			mu.Lock()
			locales = append(locales, found...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return locales, nil
}

// lexLocaleParents returns the locales as fan-out parents keyed by NativeID,
// which unlike the locale id is unique across bots, and the locales by that
// key.
func lexLocaleParents(locales []lexLocale) ([]childParent, map[string]lexLocale) {
	parents := make([]childParent, len(locales))
	byNativeID := make(map[string]lexLocale, len(locales))
	for i, l := range locales {
		parents[i] = childParent{id: l.nativeID, arn: l.nativeID}
		byNativeID[l.nativeID] = l
	}
	return parents, byNativeID
}

// scanLexSlotTypes lists each DRAFT locale's custom slot types under the
// locale.
func scanLexSlotTypes(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, locales []lexLocale) (int, int, error) {
	const op = "lexmodelsv2:ListSlotTypes"
	parents, byNativeID := lexLocaleParents(locales)
	return childFanOut(ctx, st, acct, region, op, parents,
		lexWarnParentGone(st, acct, region, op),
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			l := byNativeID[p.arn]
			draft := lexDraftVersion
			pager := lexmodelsv2.NewListSlotTypesPaginator(client, &lexmodelsv2.ListSlotTypesInput{BotId: &l.botID, BotVersion: &draft, LocaleId: &l.localeID},
				func(o *lexmodelsv2.ListSlotTypesPaginatorOptions) { o.Limit = lexMaxPageSize })
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, s := range out.SlotTypeSummaries {
					id := sv(s.SlotTypeId)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeLexSlotType, NativeID: lexSlotTypeNativeID(l.nativeID, id),
						Name: s.SlotTypeName, Region: &region,
						AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanLexIntents lists each DRAFT locale's intents under the locale and
// returns them as slot fan-out parents.
func scanLexIntents(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, locales []lexLocale) ([]lexIntent, int, int, error) {
	const op = "lexmodelsv2:ListIntents"
	var (
		mu      sync.Mutex
		intents []lexIntent
	)
	parents, byNativeID := lexLocaleParents(locales)
	total, inserted, err := childFanOut(ctx, st, acct, region, op, parents,
		lexWarnParentGone(st, acct, region, op),
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var (
				rows  []*store.Resource
				found []lexIntent
			)
			l := byNativeID[p.arn]
			draft := lexDraftVersion
			pager := lexmodelsv2.NewListIntentsPaginator(client, &lexmodelsv2.ListIntentsInput{BotId: &l.botID, BotVersion: &draft, LocaleId: &l.localeID},
				func(o *lexmodelsv2.ListIntentsPaginatorOptions) { o.Limit = lexMaxPageSize })
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, in := range out.IntentSummaries {
					id := sv(in.IntentId)
					if id == "" {
						continue
					}
					nativeID := lexIntentNativeID(l.nativeID, id)
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeLexIntent, NativeID: nativeID,
						Name: in.IntentName, Region: &region,
						AttributesJSON: mustJSON(in), DiscoveredBy: scanID,
					})
					found = append(found, lexIntent{botID: l.botID, localeID: l.localeID, intentID: id, nativeID: nativeID})
				}
			}
			mu.Lock()
			intents = append(intents, found...)
			mu.Unlock()
			return rows, nil
		})
	if err != nil {
		return nil, total, inserted, err
	}
	return intents, total, inserted, nil
}

// scanLexSlots lists each DRAFT intent's slots under the intent.
func scanLexSlots(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, intents []lexIntent) (int, int, error) {
	const op = "lexmodelsv2:ListSlots"
	parents := make([]childParent, len(intents))
	byNativeID := make(map[string]lexIntent, len(intents))
	for i, in := range intents {
		parents[i] = childParent{id: in.nativeID, arn: in.nativeID}
		byNativeID[in.nativeID] = in
	}
	return childFanOut(ctx, st, acct, region, op, parents,
		lexWarnParentGone(st, acct, region, op),
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			in := byNativeID[p.arn]
			draft := lexDraftVersion
			pager := lexmodelsv2.NewListSlotsPaginator(client, &lexmodelsv2.ListSlotsInput{
				BotId: &in.botID, BotVersion: &draft, LocaleId: &in.localeID, IntentId: &in.intentID,
			}, func(o *lexmodelsv2.ListSlotsPaginatorOptions) { o.Limit = lexMaxPageSize })
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, s := range out.SlotSummaries {
					id := sv(s.SlotId)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeLexSlot, NativeID: lexSlotNativeID(in.nativeID, id),
						Name: s.SlotName, Region: &region,
						AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}
