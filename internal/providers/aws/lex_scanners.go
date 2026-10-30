package aws

import (
	"cmp"
	"context"
	"strings"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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
	ListTagsForResource(context.Context, *lexmodelsv2.ListTagsForResourceInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListTagsForResourceOutput, error)
}

const (
	lexTagsOp     = "lexmodelsv2:ListTagsForResource"
	lexListBotsOp = "lexmodelsv2:ListBots"
)

// lexNotFound is the not-found code DescribeBot, DescribeResourcePolicy and
// ListTagsForResource model: a resource deleted after it was listed, or, for
// DescribeResourcePolicy, a bot without a policy. The list ops model none.
var lexNotFound = []string{"ResourceNotFoundException"}

// lexARN is the ARN of a Lex V2 resource, arn:{partition}:lex:{region}:
// {account}:{resource}. The list ops return ids, not ARNs, so every Lex
// NativeID is built here: the bot, bot alias and test set ARNs below are the
// Service Reference's ARN formats for those resources, and the other
// NativeIDs extend the bot's.
func lexARN(partition, region, accountID, resource string) string {
	return arnFor(partition, "lex", region, accountID, resource)
}

func lexBotARN(partition, region, accountID, botID string) string {
	return lexARN(partition, region, accountID, "bot/"+botID)
}

func lexBotAliasARN(partition, region, accountID, botID, aliasID string) string {
	return lexARN(partition, region, accountID, "bot-alias/"+botID+"/"+aliasID)
}

func lexTestSetARN(partition, region, accountID, testSetID string) string {
	return lexARN(partition, region, accountID, "test-set/"+testSetID)
}

// lexBotIDOf is the bot id in a bot row's NativeID, lexBotARN's
// ".../bot/{id}"; a bot id holds no "/" (model pattern ^[0-9a-zA-Z]+$).
func lexBotIDOf(botARN string) string {
	_, id, _ := strings.Cut(botARN, ":bot/")
	return id
}

// lexNoID is the outcome of a listed element without the id its NativeID is
// built from: it is not stored, since one empty id would merge every such
// element into one resource, and is warned about under why, named by key.
func lexNoID(why, key string) (*store.Resource, string, string) {
	return nil, why, cmp.Or(key, noIdentifier)
}

// scanLex discovers Lex V2 bots, aliases, versions, per-bot resource policies,
// test sets, and the DRAFT version's locales, intents, slot types and slots,
// with the tags of the bots, aliases and test sets (lexTags). Every listing
// runs as its own phase, one at a time and in that order: the per-bot phases
// read the bots the first phase stored, and each DRAFT model phase the
// parents an earlier one stored. A phase's error does not stop the later
// ones; a failed write and ctx's end do, and so does a first phase that
// stores nothing and fails only with NXDOMAIN, since the region does not
// serve Lex (runHostPhases). The tag reads' denials are one warning for the
// whole scan.
func scanLex(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := lexmodelsv2.NewFromConfig(acct.cfg, func(o *lexmodelsv2.Options) { o.Region = region })
	tags := lexTags(client).sharingDenials()
	var (
		bots    []childParent
		locales []lexLocale
		intents []lexIntent
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "lex", 0, []hostPhase{
		{op: lexListBotsOp, run: func(p *phaseTotals) bool {
			var failed bool
			bots, failed = scanLexBots(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "lexmodelsv2:ListBotAliases", run: func(p *phaseTotals) bool {
			return scanLexBotAliases(ctx, client, acct, region, st, scanID, bots, tags, p)
		}},
		{op: "lexmodelsv2:ListBotVersions", run: func(p *phaseTotals) bool {
			return scanLexBotVersions(ctx, client, acct, region, st, scanID, bots, p)
		}},
		{op: "lexmodelsv2:DescribeResourcePolicy", run: func(p *phaseTotals) bool {
			return scanLexResourcePolicies(ctx, client, acct, region, st, scanID, bots, p)
		}},
		{op: "lexmodelsv2:ListTestSets", run: func(p *phaseTotals) bool {
			return scanLexTestSets(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "lexmodelsv2:ListBotLocales", func() (t, i int, err error) {
			locales, t, i, err = scanLexBotLocales(ctx, client, acct, region, st, scanID, bots)
			return t, i, err
		}),
		countedPhase(0, "lexmodelsv2:ListSlotTypes", func() (int, int, error) {
			return scanLexSlotTypes(ctx, client, acct, region, st, scanID, locales)
		}),
		countedPhase(0, "lexmodelsv2:ListIntents", func() (t, i int, err error) {
			intents, t, i, err = scanLexIntents(ctx, client, acct, region, st, scanID, locales)
			return t, i, err
		}),
		countedPhase(0, "lexmodelsv2:ListSlots", func() (int, int, error) {
			return scanLexSlots(ctx, client, acct, region, st, scanID, intents)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// lexTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN lexARN builds from the listed ids. The Service Reference
// lists the bot, bot alias and test set resources on the op, in those ARN
// formats, and CreateBot (BotTags, and TestBotAliasTags for the test alias it
// creates), CreateBotAlias and StartTestSetGeneration take tags. The op
// returns every tag at once. ResourceNotFoundException, its modeled
// not-found, drops a resource deleted after it was listed.
//
// The other types get no tag read: the Service Reference lists bot versions
// on neither the op nor TagResource, whose doc names only bots, aliases and
// channels, and CreateBotVersion takes no tags; the op lists no locale,
// intent, slot type, slot or resource policy resource (the Service
// Reference's intent and slot type versions have Lex V1 ARNs).
func lexTags(client lexAPI) rowTagReader {
	return rowTagReader{
		op:       lexTagsOp,
		notFound: lexNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &lexmodelsv2.ListTagsForResourceInput{ResourceARN: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanLexBots stores the bots a page at a time, each row's attributes
// replaced with its DescribeBot body, which carries what BotSummary lacks
// (RoleArn, which resolveLexBotRole reads, DataPrivacy, BotMembers). A bot
// DescribeBot no longer finds was deleted since the listing and is dropped;
// any other failure keeps the BotSummary as the row (rowDetailReader). Name
// and Status come from the BotSummary. It returns the bots stored, as the
// parents of the per-bot and DRAFT model phases, and reports whether a write
// failed, after which the scan must stop.
func scanLexBots(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (bots []childParent, storeWriteFailed bool) {
	steps := tokenPageSteps{
		details: rowDetailReader{
			op: "lexmodelsv2:DescribeBot", what: "bots", notFound: lexNotFound,
			get: func(ctx context.Context, r *store.Resource) (any, error) {
				id := lexBotIDOf(r.NativeID)
				out, err := client.DescribeBot(ctx, &lexmodelsv2.DescribeBotInput{BotId: &id})
				if err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		stored: func(rows []*store.Resource) {
			for _, r := range rows {
				bots = append(bots, childParent{id: lexBotIDOf(r.NativeID), arn: r.NativeID})
			}
		},
	}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, lexListBotsOp,
		func(ctx context.Context, token *string) ([]lextypes.BotSummary, *string, error) {
			out, err := client.ListBots(ctx, &lexmodelsv2.ListBotsInput{MaxResults: sdkaws.Int32(lexMaxPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.BotSummaries, out.NextToken, nil
		},
		func(b lextypes.BotSummary) (*store.Resource, string, string) {
			id := sv(b.BotId)
			if id == "" {
				return lexNoID("bots have no BotId", sv(b.BotName))
			}
			status := string(b.BotStatus)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexBot, NativeID: lexBotARN(acct.partition(), region, acct.ID, id),
				Name: b.BotName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			}, "", ""
		}, steps, tags, p)
	return bots, failed
}

// scanLexBotAliases stores each bot's aliases, the test alias TSTALIASID
// included, with their tags; one bot's failure costs the others none of
// theirs (storeEachParentPagesTags). ListBotAliases models no not-found, so
// a bot deleted since it was listed fails the phase. It reports whether a
// write failed, after which the scan must stop.
func scanLexBotAliases(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, bots []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "lexmodelsv2:ListBotAliases", nil, bots,
		func(ctx context.Context, botID string, token *string) ([]lextypes.BotAliasSummary, *string, error) {
			out, err := client.ListBotAliases(ctx, &lexmodelsv2.ListBotAliasesInput{BotId: &botID, MaxResults: sdkaws.Int32(lexMaxPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.BotAliasSummaries, out.NextToken, nil
		},
		func(botID string, a lextypes.BotAliasSummary) (*store.Resource, string, string) {
			aliasID := sv(a.BotAliasId)
			if aliasID == "" {
				return lexNoID("bot aliases have no BotAliasId", cmp.Or(sv(a.BotAliasName), noIdentifier)+" of bot "+botID)
			}
			status := string(a.BotAliasStatus)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexBotAlias, NativeID: lexBotAliasARN(acct.partition(), region, acct.ID, botID, aliasID),
				Name: a.BotAliasName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		}, tags, p)
}

// scanLexBotVersions stores each bot's versions; one bot's failure costs the
// others none of theirs (storeEachParentPages). ListBotVersions models no
// not-found, so a bot deleted since it was listed fails the phase. It
// reports whether a write failed, after which the scan must stop.
func scanLexBotVersions(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, bots []childParent, p *phaseTotals) bool {
	return storeEachParentPages(ctx, st, acct, region, "lexmodelsv2:ListBotVersions", nil, bots,
		func(ctx context.Context, botID string, token *string) ([]lextypes.BotVersionSummary, *string, error) {
			out, err := client.ListBotVersions(ctx, &lexmodelsv2.ListBotVersionsInput{BotId: &botID, MaxResults: sdkaws.Int32(lexMaxPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.BotVersionSummaries, out.NextToken, nil
		},
		func(botID string, v lextypes.BotVersionSummary) (*store.Resource, string, string) {
			ver := sv(v.BotVersion)
			if ver == "" {
				return lexNoID("bot versions have no BotVersion", "a version of bot "+botID)
			}
			status := string(v.BotStatus)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexBotVersion, NativeID: lexBotVersionNativeID(acct.partition(), region, acct.ID, botID, ver),
				Name: &ver, Region: &region, Status: &status,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			}, "", ""
		}, p)
}

// scanLexResourcePolicies stores each bot's resource policy, read with one
// DescribeResourcePolicy call per bot (storeEachParentPages, each bot's
// "listing" that one call): a bot without a policy
// (ResourceNotFoundException, or an empty Policy) has no row, a denied bot
// is warned about once, and any other error is returned after the other
// bots' policies are stored. It reports whether a write failed, after which
// the scan must stop.
func scanLexResourcePolicies(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, bots []childParent, p *phaseTotals) bool {
	return storeEachParentPages(ctx, st, acct, region, "lexmodelsv2:DescribeResourcePolicy", lexNotFound, bots,
		func(ctx context.Context, botID string, _ *string) ([]*lexmodelsv2.DescribeResourcePolicyOutput, *string, error) {
			arn := lexBotARN(acct.partition(), region, acct.ID, botID)
			out, err := client.DescribeResourcePolicy(ctx, &lexmodelsv2.DescribeResourcePolicyInput{ResourceArn: &arn})
			if err != nil {
				return nil, nil, err
			}
			if sv(out.Policy) == "" {
				return nil, nil, nil
			}
			return []*lexmodelsv2.DescribeResourcePolicyOutput{out}, nil, nil
		},
		func(botID string, out *lexmodelsv2.DescribeResourcePolicyOutput) (*store.Resource, string, string) {
			label := "policy"
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexResourcePolicy, NativeID: lexBotARN(acct.partition(), region, acct.ID, botID) + "/policy",
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
			}, "", ""
		}, p)
}

// scanLexTestSets stores the test sets, each page written as soon as its
// rows' tags are read. It reports whether a write failed, after which the
// scan must stop.
func scanLexTestSets(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "lexmodelsv2:ListTestSets",
		func(ctx context.Context, token *string) ([]lextypes.TestSetSummary, *string, error) {
			out, err := client.ListTestSets(ctx, &lexmodelsv2.ListTestSetsInput{MaxResults: sdkaws.Int32(lexMaxPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TestSets, out.NextToken, nil
		},
		func(ts lextypes.TestSetSummary) (*store.Resource, string, string) {
			id := sv(ts.TestSetId)
			if id == "" {
				return lexNoID("test sets have no TestSetId", sv(ts.TestSetName))
			}
			status := string(ts.Status)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexTestSet, NativeID: lexTestSetARN(acct.partition(), region, acct.ID, id),
				Name: ts.TestSetName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(ts), DiscoveredBy: scanID,
			}, "", ""
		}, tags, p)
}

// lexDraftVersion is the only bot version whose model is listed. Numbered
// versions are point-in-time snapshots that may differ from DRAFT; scanning
// each would multiply the locale, intent and slot rows per version and is out
// of scope.
const lexDraftVersion = "DRAFT"

// lexMaxPageSize is the cap of the MaxResults shape (1-1000) every Lex V2
// list op read here takes.
const lexMaxPageSize = 1000

func lexBotVersionNativeID(partition, region, accountID, botID, version string) string {
	return lexBotARN(partition, region, accountID, botID) + "/version/" + version
}

func lexBotLocaleNativeID(partition, region, accountID, botID, localeID string) string {
	return lexBotVersionNativeID(partition, region, accountID, botID, lexDraftVersion) + "/locale/" + localeID
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
							nativeID: lexBotLocaleNativeID(acct.partition(), region, acct.ID, bot.id, localeID),
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
