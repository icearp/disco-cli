package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWAFv2WebACL, Service: "wafv2"})
	registerType(restype.Descriptor{Type: TypeWAFv2RuleGroup, Service: "wafv2"})
	registerType(restype.Descriptor{Type: TypeWAFv2IPSet, Service: "wafv2"})
	registerType(restype.Descriptor{Type: TypeWAFv2LoggingConfiguration, Service: "wafv2"})
	registerType(restype.Descriptor{Type: TypeWAFv2RegexPatternSet, Service: "wafv2"})
	registerType(restype.Descriptor{Type: TypeWAFv2WebACLAssociation, Service: "wafv2"})
	registerType(restype.Descriptor{Type: TypeWAFv2ManagedRuleSet, Service: "wafv2", Untaggable: true})
	// No Redact rule on APIKey, deliberately: it is the key a site passes to
	// AwsWafCaptcha.renderCaptcha in its client-side JavaScript, so every
	// visitor's browser receives it, and it only lets the token domains it
	// lists load the CAPTCHA puzzle
	// (https://docs.aws.amazon.com/waf/latest/developerguide/waf-js-captcha-api-key.html,
	// https://docs.aws.amazon.com/waf/latest/developerguide/waf-js-captcha-api-specification.html).
	registerType(restype.Descriptor{Type: TypeWAFv2APIKey, Service: "wafv2", Untaggable: true})
	registerService(serviceEntry{
		name: "aws:wafv2",
		fn:   scanWAFv2,
	})
}

// wafv2API is the narrow set of WAFv2 operations called by scanWAFv2.
type wafv2API interface {
	ListWebACLs(context.Context, *wafv2.ListWebACLsInput, ...func(*wafv2.Options)) (*wafv2.ListWebACLsOutput, error)
	GetWebACL(context.Context, *wafv2.GetWebACLInput, ...func(*wafv2.Options)) (*wafv2.GetWebACLOutput, error)
	ListRuleGroups(context.Context, *wafv2.ListRuleGroupsInput, ...func(*wafv2.Options)) (*wafv2.ListRuleGroupsOutput, error)
	ListIPSets(context.Context, *wafv2.ListIPSetsInput, ...func(*wafv2.Options)) (*wafv2.ListIPSetsOutput, error)
	ListLoggingConfigurations(context.Context, *wafv2.ListLoggingConfigurationsInput, ...func(*wafv2.Options)) (*wafv2.ListLoggingConfigurationsOutput, error)
	ListRegexPatternSets(context.Context, *wafv2.ListRegexPatternSetsInput, ...func(*wafv2.Options)) (*wafv2.ListRegexPatternSetsOutput, error)
	ListResourcesForWebACL(context.Context, *wafv2.ListResourcesForWebACLInput, ...func(*wafv2.Options)) (*wafv2.ListResourcesForWebACLOutput, error)
	ListManagedRuleSets(context.Context, *wafv2.ListManagedRuleSetsInput, ...func(*wafv2.Options)) (*wafv2.ListManagedRuleSetsOutput, error)
	ListAPIKeys(context.Context, *wafv2.ListAPIKeysInput, ...func(*wafv2.Options)) (*wafv2.ListAPIKeysOutput, error)
	ListTagsForResource(context.Context, *wafv2.ListTagsForResourceInput, ...func(*wafv2.Options)) (*wafv2.ListTagsForResourceOutput, error)
}

const (
	wafv2ListWebACLsOp          = "wafv2:ListWebACLs"
	wafv2GetWebACLOp            = "wafv2:GetWebACL"
	wafv2ListRuleGroupsOp       = "wafv2:ListRuleGroups"
	wafv2ListIPSetsOp           = "wafv2:ListIPSets"
	wafv2ListRegexPatternSetsOp = "wafv2:ListRegexPatternSets"
	wafv2ListAPIKeysOp          = "wafv2:ListAPIKeys"
	wafv2TagsOp                 = "wafv2:ListTagsForResource"
	// wafv2PageCap is the top of PaginationLimit (1-100), the Limit range of
	// ListWebACLs, ListRuleGroups, ListIPSets, ListRegexPatternSets and
	// ListTagsForResource.
	wafv2PageCap = 100
)

// wafv2Gone is the not-found code the GetWebACL and ListTagsForResource
// models list: the resource was deleted after it was listed.
var wafv2Gone = []string{"WAFNonexistentItemException"}

// errWAFv2NoWebACLBody is the error of a GetWebACL response without a
// WebACL; the web ACL is kept as its ListWebACLs summary.
var errWAFv2NoWebACLBody = errors.New("GetWebACL response has no WebACL")

// errWAFv2NoTagInfo is the error of a ListTagsForResource response without
// a TagInfoForResource; the row is kept without tags.
var errWAFv2NoTagInfo = errors.New("ListTagsForResource response has no TagInfoForResource")

// scanWAFv2 discovers WAFv2 resources in one region, across both scopes:
// REGIONAL (per region) and CLOUDFRONT (global, reachable only from
// CloudFront's home region — fetched only there to avoid duplicates). Both
// scopes are listed through the region's one client, so a row's tags are
// read through the client that listed it.
//
// Each listing is a phase of its own, run one at a time: every scope's
// phases, then the API keys of every scope. A phase's error does not stop
// the later ones; a failed write and ctx's end do (runHostPhases). Each error
// is kept under the op that failed, and denied tag reads and denied GetWebACL
// reads are one warning each for the scan.
func scanWAFv2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := wafv2.NewFromConfig(acct.cfg, func(o *wafv2.Options) { o.Region = region })

	scopes := []types.Scope{types.ScopeRegional}
	// The CLOUDFRONT scope protects CloudFront distributions and answers only in
	// CloudFront's home region, so only that region's scan lists it; a partition
	// with no CloudFront home makes no CLOUDFRONT call. In aws-cn this follows
	// the SDK's CloudFront home (cn-northwest-1); that WAFv2 serves the
	// CLOUDFRONT scope there is unverified against a live account.
	if acct.homeGate(st, "cloudfront", wafv2ListWebACLsOp, region) {
		scopes = append(scopes, types.ScopeCloudfront)
	}

	tags := wafv2Tags(client).sharingDenials()
	aclDetails := rowDetailReader{op: wafv2GetWebACLOp, what: "web ACL(s)", notFound: wafv2Gone}.sharingDetailDenials()
	var phases []hostPhase
	for _, scope := range scopes {
		phases = append(phases, wafv2ScopePhases(ctx, client, acct, region, scope, st, scanID, aclDetails, tags)...)
	}
	for _, scope := range scopes {
		phases = append(phases, countedPhase(0, wafv2ListAPIKeysOp, func() (int, error) {
			return scanWAFv2APIKeys(ctx, client, acct, region, scope, st, scanID)
		}))
	}
	total, err := runHostPhases(ctx, st, acct, region, "wafv2", 0, phases)
	aclDetails.warnDetailDenials(st, acct, region)
	tags.warnDenials(st, acct, region)
	return total, err
}

// wafv2ScopePhases is the phases of one scope, in the order they run: web
// ACLs, rule groups, IP sets, then the extended phases, which list the
// associations of the web ACLs the first phase stored.
func wafv2ScopePhases(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string, aclDetails rowDetailReader, tags rowTagReader) []hostPhase {
	var webACLARNs []string
	phases := []hostPhase{
		{op: wafv2ListWebACLsOp, run: func(p *phaseTotals) bool {
			var failed bool
			webACLARNs, failed = scanWAFv2WebACLs(ctx, client, acct, region, scope, st, scanID, aclDetails, tags, p)
			return failed
		}},
		{op: wafv2ListRuleGroupsOp, run: func(p *phaseTotals) bool {
			return storeWAFv2Tagged(ctx, st, acct, region, scanID, scope, wafv2ListRuleGroupsOp, "rule groups", TypeWAFv2RuleGroup,
				func(ctx context.Context, marker *string) ([]types.RuleGroupSummary, *string, error) {
					out, err := client.ListRuleGroups(ctx, &wafv2.ListRuleGroupsInput{Scope: scope, Limit: sdkaws.Int32(wafv2PageCap), NextMarker: marker})
					if err != nil {
						return nil, nil, err
					}
					return out.RuleGroups, out.NextMarker, nil
				},
				func(s types.RuleGroupSummary) (arn, name *string) { return s.ARN, s.Name },
				tags, p)
		}},
		{op: wafv2ListIPSetsOp, run: func(p *phaseTotals) bool {
			return storeWAFv2Tagged(ctx, st, acct, region, scanID, scope, wafv2ListIPSetsOp, "IP sets", TypeWAFv2IPSet,
				func(ctx context.Context, marker *string) ([]types.IPSetSummary, *string, error) {
					out, err := client.ListIPSets(ctx, &wafv2.ListIPSetsInput{Scope: scope, Limit: sdkaws.Int32(wafv2PageCap), NextMarker: marker})
					if err != nil {
						return nil, nil, err
					}
					return out.IPSets, out.NextMarker, nil
				},
				func(s types.IPSetSummary) (arn, name *string) { return s.ARN, s.Name },
				tags, p)
		}},
	}
	return append(phases, wafv2ExtendedPhases(ctx, client, acct, region, scope, st, scanID, tags, &webACLARNs)...)
}

// wafv2Tags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference lists ipset,
// regexpatternset, rulegroup and webacl under ListTagsForResource, and each
// one's Create input takes Tags; managed rule sets, API keys, logging
// configurations and web ACL associations are not read. The op pages a
// resource's tags: every page is read, and a repeated NextMarker fails the
// read. WAFNonexistentItemException, its modeled not-found, drops a resource
// deleted after it was listed.
func wafv2Tags(client wafv2API) rowTagReader {
	return rowTagReader{
		op:       wafv2TagsOp,
		notFound: wafv2Gone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			tags, err := readTokenPages(ctx, func(ctx context.Context, marker *string) ([]types.Tag, *string, error) {
				out, err := client.ListTagsForResource(ctx, &wafv2.ListTagsForResourceInput{ResourceARN: &arn, Limit: sdkaws.Int32(wafv2PageCap), NextMarker: marker})
				if err != nil {
					return nil, nil, err
				}
				if out.TagInfoForResource == nil {
					return nil, nil, errWAFv2NoTagInfo
				}
				return out.TagInfoForResource.TagList, out.NextMarker, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t types.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// wafv2ScopeError is err of a listing of scope, naming that scope.
type wafv2ScopeError struct {
	scope types.Scope
	err   error
}

func (e wafv2ScopeError) Error() string { return fmt.Sprintf("scope=%s: %v", e.scope, e.err) }

func (e wafv2ScopeError) Unwrap() error { return e.err }

// wafv2ScopeListing stores one scope's listing of op with storePages, which
// is handed list as the pages to read, and returns whether a write failed.
// Each error a page returns names scope (wafv2ScopeError), and so does a
// repeated marker, which ends the listing once its page is stored and is
// added to p under op. The repeat is caught here because eachTokenPage's own
// check, which would otherwise catch it, names no scope, and in CloudFront's
// home region both scopes are listed.
func wafv2ScopeListing[Item any](scope types.Scope, op string, p *phaseTotals,
	list func(ctx context.Context, marker *string) ([]Item, *string, error),
	storePages func(page func(ctx context.Context, marker *string) ([]Item, *string, error)) (storeWriteFailed bool),
) bool {
	var repeated error
	failed := storePages(func(ctx context.Context, marker *string) ([]Item, *string, error) {
		items, next, err := list(ctx, marker)
		if err != nil {
			return nil, nil, wafv2ScopeError{scope, err}
		}
		if _, err := lastPage(marker, next); err != nil {
			repeated = wafv2ScopeError{scope, err}
			return items, nil, nil
		}
		return items, next, nil
	})
	if repeated != nil {
		p.errs.add(op, fmt.Errorf("%s: %w", op, repeated))
	}
	return failed
}

// wafv2Row is the row of an element of rtype listed under arn and named
// name, the element stored as its attributes.
func wafv2Row(acct *account, region, scanID, rtype string, arn, name *string, element any) *store.Resource {
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           rtype,
		NativeID:       sv(arn),
		Name:           name,
		Region:         &region,
		AttributesJSON: mustJSON(element),
		DiscoveredBy:   scanID,
	}
}

// storeWAFv2Tagged stores one scope's items of rtype, listed by list, each
// under the ARN key returns for it and with its tags, a page at a time
// (storeTokenPagesTags). An item without an ARN is not stored and is warned
// about once; what names the items in that warning. It reports whether a
// write failed, after which the scan must stop.
func storeWAFv2Tagged[Item any](ctx context.Context, st *store.Store, acct *account, region, scanID string, scope types.Scope, op, what, rtype string,
	list func(ctx context.Context, marker *string) ([]Item, *string, error),
	key func(Item) (arn, name *string),
	tags rowTagReader, p *phaseTotals,
) bool {
	return wafv2ScopeListing(scope, op, p, list, func(page func(ctx context.Context, marker *string) ([]Item, *string, error)) bool {
		return storeTokenPagesTags(ctx, st, acct, region, op, page,
			func(it Item) (*store.Resource, string, string) {
				arn, name := key(it)
				return arnRow(what, arn, name, wafv2Row(acct, region, scanID, rtype, arn, name, it))
			},
			tags, p)
	})
}

// scanWAFv2WebACLs stores the scope's web ACLs, each under its ListWebACLs
// ARN, with its GetWebACL body as its attributes and its tags, a page at a
// time, and returns the ARNs of the ACLs stored. An ACL GetWebACL no longer
// finds was deleted since it was listed and is not stored; any other
// GetWebACL failure, a response without a WebACL included, keeps the ACL as
// its summary (rowDetailReader's rules). aclDetails carries the op, the
// not-found and the shared denial tally; its get is set here. It reports
// whether a write failed, after which the scan must stop.
func scanWAFv2WebACLs(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string, aclDetails rowDetailReader, tags rowTagReader, p *phaseTotals) (stored []string, storeWriteFailed bool) {
	// GetWebACL is sent the summary's Id, which no row field holds. Each page's
	// rows are mapped before its reads start and read only by them, so the
	// concurrent reads never meet a write; the map is cleared once the page is
	// written, so it holds one page's rows at a time.
	ids := map[*store.Resource]*string{}
	details := aclDetails
	details.get = func(ctx context.Context, r *store.Resource) (any, error) {
		out, err := client.GetWebACL(ctx, &wafv2.GetWebACLInput{Id: ids[r], Name: r.Name, Scope: scope})
		if err != nil {
			return nil, err
		}
		if out.WebACL == nil {
			return nil, errWAFv2NoWebACLBody
		}
		return out.WebACL, nil
	}
	list := func(ctx context.Context, marker *string) ([]types.WebACLSummary, *string, error) {
		out, err := client.ListWebACLs(ctx, &wafv2.ListWebACLsInput{Scope: scope, Limit: sdkaws.Int32(wafv2PageCap), NextMarker: marker})
		if err != nil {
			return nil, nil, err
		}
		return out.WebACLs, out.NextMarker, nil
	}
	failed := wafv2ScopeListing(scope, wafv2ListWebACLsOp, p, list, func(page func(ctx context.Context, marker *string) ([]types.WebACLSummary, *string, error)) bool {
		return storeTokenPagesTagsWith(ctx, st, acct, region, wafv2ListWebACLsOp, page,
			func(s types.WebACLSummary) (*store.Resource, string, string) {
				r, why, key := arnRow("web ACLs", s.ARN, s.Name, wafv2Row(acct, region, scanID, TypeWAFv2WebACL, s.ARN, s.Name, s))
				if r != nil {
					ids[r] = s.Id
				}
				return r, why, key
			},
			tokenPageSteps{
				details: details,
				stored: func(rows []*store.Resource) {
					for _, r := range rows {
						stored = append(stored, r.NativeID)
					}
					clear(ids)
				},
			},
			tags, p)
	})
	return stored, failed
}

// wafv2ARNScope is the scope segment AWS writes in WAFv2 ARNs.
var wafv2ARNScope = map[types.Scope]string{
	types.ScopeRegional:   "regional",
	types.ScopeCloudfront: "global",
}

// wafv2APIKeyNativeID is an API key's identity. The API issues no ARN for a
// key and the Service Reference defines no apikey resource; WAF addresses a
// key by scope and key value (DeleteAPIKey, GetDecryptedAPIKey), and a
// non-CloudFront key is usable only in its own region. The id follows the
// sibling WAFv2 ARN shape with the key last, since its model pattern (\S)
// allows "/".
func wafv2APIKeyNativeID(partition, region, accountID string, scope types.Scope, apiKey string) string {
	return arnFor(partition, "wafv2", region, accountID, wafv2ARNScope[scope]+"/apikey/"+apiKey)
}

// scanWAFv2APIKeys lists the CAPTCHA API keys of one scope. The pages read
// before an error are stored; a denial warns and is dropped. The output's
// ApplicationIntegrationURL is not stored: it belongs to the scope and region,
// not to any one key, and the row's attributes are the key's SDK element.
func scanWAFv2APIKeys(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string) (int, error) {
	var (
		keys    []containedChild
		keyless skipTally
		marker  *string
		err     error
	)
	for {
		var out *wafv2.ListAPIKeysOutput
		out, err = client.ListAPIKeys(ctx, &wafv2.ListAPIKeysInput{Scope: scope, Limit: sdkaws.Int32(100), NextMarker: marker})
		if err != nil {
			err = fmt.Errorf("scope=%s: %w", scope, err)
			break
		}
		for _, k := range out.APIKeySummaries {
			key := sv(k.APIKey)
			if key == "" {
				keyless.skipped(fmt.Sprintf("with token domains %q", k.TokenDomains), nil)
				continue
			}
			keys = append(keys, containedChild{row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWAFv2APIKey, NativeID: wafv2APIKeyNativeID(acct.partition(), region, acct.ID, scope, key),
				Region: &region, CreatedAt: tp(k.CreationTimestamp),
				AttributesJSON: mustJSON(k), DiscoveredBy: scanID,
			}})
		}
		// ListAPIKeys has no paginator; a repeated marker would loop forever.
		done, perr := lastPage(marker, out.NextMarker)
		if done {
			if perr != nil {
				err = fmt.Errorf("scope=%s: %w", scope, perr)
			}
			break
		}
		marker = out.NextMarker
	}
	warnNotStored(st, wafv2ListAPIKeysOp, acct, region, &keyless, "API keys have no APIKey value")
	return upsertRegionChildren(st, acct, region, wafv2ListAPIKeysOp, keys, err)
}

// wafv2Listed is one listed WAFv2 item: arn keys its row (nil when it has none),
// name names it, attrs is the element stored.
type wafv2Listed struct {
	arn, name *string
	attrs     any
}

// scanWAFv2Summaries stores one scope's items of kind, listed by list, each
// keyed by the arn list returns for it. A failed listing keeps the pages read
// before it: a denial warns; any other error, and a repeated marker, is
// returned with the counts of what was stored. An item without an arn is not
// stored and is warned about once.
func scanWAFv2Summaries(st *store.Store, acct *account, region, scanID string, scope types.Scope, op, kind, rtype string,
	list func(marker *string) ([]wafv2Listed, *string, error),
) (int, error) {
	var (
		batch   []*store.Resource
		arnless skipTally
		marker  *string
		listErr error
	)
	for {
		items, next, err := list(marker)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf(
					"scope=%s: listing truncated, %s on this and later pages not stored: %w", scope, kind, err))
			} else {
				listErr = err
			}
			break
		}
		for _, it := range items {
			if sv(it.arn) == "" {
				arnless.skipped(fmt.Sprintf("%q", sv(it.name)), nil)
				continue
			}
			batch = append(batch, wafv2Row(acct, region, scanID, rtype, it.arn, it.name, it.attrs))
		}
		// The WAFv2 list ops have no paginator; a repeated marker would loop
		// forever.
		var done bool
		if done, listErr = lastPage(marker, next); done {
			break
		}
		marker = next
	}
	warnNotStored(st, op, acct, region, &arnless, kind+" have no ARN")
	if listErr != nil {
		listErr = wafv2ScopeError{scope, listErr}
	}
	return upsertListed(st, batch, op, listErr)
}
