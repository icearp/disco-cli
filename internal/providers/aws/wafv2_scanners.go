package aws

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

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

// wafv2API is the narrow set of WAFv2 operations called by scanWAFv2Scope.
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
}

// scanWAFv2 discovers WAFv2 resources in one region, across both scopes:
// REGIONAL (per region) and CLOUDFRONT (global, reachable only from
// CloudFront's home region — fetched only there to avoid duplicates).
//
// The API-key phase runs after the older per-scope phases whatever they
// returned, so a failure on either side does not stop the other from listing.
// Errors join through phaseErrs: transient ones are demoted to warnings only
// beside a hard error, so the dispatcher neither hides the hard error nor
// reads an all-transient failure as a complete scan.
func scanWAFv2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := wafv2.NewFromConfig(acct.cfg, func(o *wafv2.Options) { o.Region = region })

	scopes := []types.Scope{types.ScopeRegional}
	// The CLOUDFRONT scope protects CloudFront distributions and answers only in
	// CloudFront's home region, so only that region's scan lists it; a partition
	// with no CloudFront home makes no CLOUDFRONT call. In aws-cn this follows
	// the SDK's CloudFront home (cn-northwest-1); that WAFv2 serves the
	// CLOUDFRONT scope there is unverified against a live account.
	if acct.homeGate(st, "cloudfront", "wafv2:ListWebACLs", region) {
		scopes = append(scopes, types.ScopeCloudfront)
	}

	var errs phaseErrs
	total, inserted, err := scanWAFv2Scopes(ctx, client, acct, region, scopes, st, scanID)
	errs.add("wafv2", err)
	for _, scope := range scopes {
		t, i, kerr := scanWAFv2APIKeys(ctx, client, acct, region, scope, st, scanID)
		total += t
		inserted += i
		errs.add(wafv2ListAPIKeysOp, kerr)
	}
	return total, inserted, errs.join(st, acct, region)
}

// scanWAFv2Scopes runs the per-scope phases of every scope, whatever the
// others returned. Its counts are every row the scopes stored.
func scanWAFv2Scopes(ctx context.Context, client wafv2API, acct *account, region string, scopes []types.Scope, st *store.Store, scanID string) (total, inserted int, err error) {
	var errs phaseErrs
	for _, scope := range scopes {
		t, i, err := scanWAFv2Scope(ctx, client, acct, region, scope, st, scanID)
		total += t
		inserted += i
		errs.add("wafv2", err)
	}
	return total, inserted, errs.join(st, acct, region)
}

const wafv2ListAPIKeysOp = "wafv2:ListAPIKeys"

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
func wafv2APIKeyNativeID(region, accountID string, scope types.Scope, apiKey string) string {
	return "arn:aws:wafv2:" + region + ":" + accountID + ":" + wafv2ARNScope[scope] + "/apikey/" + apiKey
}

// scanWAFv2APIKeys lists the CAPTCHA API keys of one scope. The pages read
// before an error are stored; a denial warns and is dropped. The output's
// ApplicationIntegrationURL is not stored: it belongs to the scope and region,
// not to any one key, and the row's attributes are the key's SDK element.
func scanWAFv2APIKeys(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string) (int, int, error) {
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
				Type: TypeWAFv2APIKey, NativeID: wafv2APIKeyNativeID(region, acct.ID, scope, key),
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

// scanWAFv2WebACLs stores the scope's web ACLs from their GetWebACL bodies and
// returns the ARNs of the ACLs stored. A failed listing keeps the pages read
// before it: a denial warns; any other error, and a repeated marker, is
// returned. A denied or transient GetWebACL keeps the ACL as its ListWebACLs
// summary, a different shape, with one warning per cause; an ACL GetWebACL no
// longer finds (WAFNonexistentItemException, in the op's model) was deleted
// since it was listed and is not stored. Any other GetWebACL error is
// returned after the other ACLs are stored. An ACL whose stored shape carries
// no ARN is not stored and warned about under the op that returned it.
func scanWAFv2WebACLs(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string) ([]string, int, int, error) {
	var (
		aclSummaries []types.WebACLSummary
		aclMarker    *string
		errs         phaseErrs
	)
	for {
		out, err := client.ListWebACLs(ctx, &wafv2.ListWebACLsInput{Scope: scope, NextMarker: aclMarker})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "wafv2:ListWebACLs", acct.ID, region, fmt.Errorf(
					"scope=%s: listing truncated, web ACLs on this and later pages not stored: %w", scope, err))
			} else {
				errs.add("wafv2:ListWebACLs", fmt.Errorf("wafv2:ListWebACLs scope=%s: %w", scope, err))
			}
			break
		}
		aclSummaries = append(aclSummaries, out.WebACLs...)
		done, err := lastPage(aclMarker, out.NextMarker)
		if err != nil {
			errs.add("wafv2:ListWebACLs", fmt.Errorf("wafv2:ListWebACLs scope=%s: %w", scope, err))
		}
		if done {
			break
		}
		aclMarker = out.NextMarker
	}
	var (
		mu                      sync.Mutex
		failed                  phaseErrs
		denied, noBody          skipTally
		transient               transientTally
		summaryNoARN, bodyNoARN skipTally
	)
	// Keyed by list position: a summary's ARN may be empty.
	keys := make([]string, len(aclSummaries))
	byKey := make(map[string]types.WebACLSummary, len(aclSummaries))
	for i, s := range aclSummaries {
		keys[i] = strconv.Itoa(i)
		byKey[keys[i]] = s
	}
	// A hard error is collected rather than returned, so one bad ACL neither
	// cancels the other reads nor discards their rows. describe therefore never
	// returns an error, and neither does describeFanOut.
	aclBatch, _ := describeFanOut(ctx, st, acct, region, "wafv2:GetWebACL", keys, neverSkip,
		func(ctx context.Context, key string) (*store.Resource, error) {
			s := byKey[key]
			out, err := client.GetWebACL(ctx, &wafv2.GetWebACLInput{
				Id:    s.Id,
				Name:  s.Name,
				Scope: scope,
			})
			arn, name, attrs := sv(s.ARN), s.Name, any(s)
			noARN := &summaryNoARN
			switch {
			case err == nil && out.WebACL != nil:
				arn, name, attrs, noARN = sv(out.WebACL.ARN), out.WebACL.Name, out.WebACL, &bodyNoARN
			case err == nil:
				// No body to store: kept as its summary, like a failed read.
			case isAPIErrorCode(err, "WAFNonexistentItemException"):
				return nil, nil
			case !isTransientNetworkError(err) && !isAccessDenied(err):
				mu.Lock()
				failed.add("wafv2:GetWebACL", fmt.Errorf("wafv2:GetWebACL %s: %w", sv(s.Name), err))
				mu.Unlock()
				return nil, nil
			}
			if arn == "" {
				noARN.skipped(fmt.Sprintf("%q", sv(name)), nil)
				return nil, nil
			}
			// Tallied only once the summary is known to be storable, since the
			// warning says it was stored.
			switch {
			case err == nil && out.WebACL == nil:
				noBody.skipped(arn, nil)
			case err != nil && !transient.drop(arn, err):
				denied.skipped(arn, err)
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeWAFv2WebACL,
				NativeID:       arn,
				Name:           name,
				Region:         &region,
				AttributesJSON: mustJSON(attrs),
				DiscoveredBy:   scanID,
			}, nil
		})
	errs = append(errs, failed...)
	warnNotStored(st, "wafv2:ListWebACLs", acct, region, &summaryNoARN, "web ACLs kept as their summary have no ARN")
	warnNotStored(st, "wafv2:GetWebACL", acct, region, &bodyNoARN, "web ACL bodies have no ARN")
	if len(aclBatch) == 0 {
		return nil, 0, 0, errs.join(st, acct, region)
	}
	n, err := st.UpsertResources(aclBatch)
	if err != nil {
		return nil, 0, 0, errors.Join(errs.join(st, acct, region), fmt.Errorf("upsert WAFv2 WebACLs: %w", err))
	}
	const kept = "scope=%s: %d web ACL(s) stored as their ListWebACLs summary, without the ACL body, first %s: %w"
	if denied.count > 0 {
		_ = skipIfAccessDenied(st, "wafv2:GetWebACL", acct.ID, region, fmt.Errorf(kept, scope, denied.count, denied.first, denied.err))
	}
	if transient.count > 0 {
		_ = skipIfTransient(st, "wafv2:GetWebACL", acct.ID, region, fmt.Errorf(kept, scope, transient.count, transient.first, transient.err))
	}
	if noBody.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: "wafv2:GetWebACL", Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("scope=%s: %d web ACL(s) stored as their ListWebACLs summary because GetWebACL returned no WebACL; first %s",
				scope, noBody.count, noBody.first),
		})
	}
	stored := make([]string, len(aclBatch))
	for i, r := range aclBatch {
		stored[i] = r.NativeID
	}
	return stored, len(aclBatch), n, errs.join(st, acct, region)
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
) (int, int, error) {
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
			arn := sv(it.arn)
			if arn == "" {
				arnless.skipped(fmt.Sprintf("%q", sv(it.name)), nil)
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           rtype,
				NativeID:       arn,
				Name:           it.name,
				Region:         &region,
				AttributesJSON: mustJSON(it.attrs),
				DiscoveredBy:   scanID,
			})
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
		listErr = fmt.Errorf("scope=%s: %w", scope, listErr)
	}
	return upsertListed(st, batch, op, listErr)
}

// scanWAFv2Scope runs one scope's phases, each whatever the others returned,
// and returns the counts of every row stored with their errors joined.
func scanWAFv2Scope(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string) (total, inserted int, err error) {
	var errs phaseErrs
	add := func(op string, t, i int, err error) {
		total += t
		inserted += i
		errs.add(op, err)
	}
	webACLARNs, t, i, err := scanWAFv2WebACLs(ctx, client, acct, region, scope, st, scanID)
	add("wafv2:ListWebACLs", t, i, err)
	t, i, err = scanWAFv2Summaries(st, acct, region, scanID, scope, "wafv2:ListRuleGroups", "rule groups", TypeWAFv2RuleGroup,
		func(marker *string) ([]wafv2Listed, *string, error) {
			out, err := client.ListRuleGroups(ctx, &wafv2.ListRuleGroupsInput{Scope: scope, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafv2Listed, len(out.RuleGroups))
			for i, s := range out.RuleGroups {
				items[i] = wafv2Listed{s.ARN, s.Name, s}
			}
			return items, out.NextMarker, nil
		})
	add("wafv2:ListRuleGroups", t, i, err)
	t, i, err = scanWAFv2Summaries(st, acct, region, scanID, scope, "wafv2:ListIPSets", "IP sets", TypeWAFv2IPSet,
		func(marker *string) ([]wafv2Listed, *string, error) {
			out, err := client.ListIPSets(ctx, &wafv2.ListIPSetsInput{Scope: scope, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafv2Listed, len(out.IPSets))
			for i, s := range out.IPSets {
				items[i] = wafv2Listed{s.ARN, s.Name, s}
			}
			return items, out.NextMarker, nil
		})
	add("wafv2:ListIPSets", t, i, err)
	t, i, err = scanWAFv2ScopeExtended(ctx, client, acct, region, scope, st, scanID, webACLARNs)
	add("wafv2", t, i, err)
	return total, inserted, errs.join(st, acct, region)
}
