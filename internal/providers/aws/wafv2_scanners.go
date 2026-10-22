package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
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
// REGIONAL (per region) and CLOUDFRONT (global, reachable only from us-east-1
// — fetched only there to avoid duplicates).
//
// The API-key phase runs after the older per-scope phases whatever they
// returned, so a failure on either side does not stop the other from listing.
// A lone error is returned as is, so the dispatcher classifies it (a transient
// one becomes a scan error and a partial scan). Only when several phases fail
// is each transient error demoted to a warning before the join, since one
// transient member would make the dispatcher read the whole join as transient.
func scanWAFv2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := wafv2.NewFromConfig(acct.cfg, func(o *wafv2.Options) { o.Region = region })

	scopes := []types.Scope{types.ScopeRegional}
	if region == "us-east-1" {
		scopes = append(scopes, types.ScopeCloudfront)
	}

	type phaseErr struct {
		op  string
		err error
	}
	var failed []phaseErr
	total, inserted, err := scanWAFv2Scopes(ctx, client, acct, region, scopes, st, scanID)
	if err != nil {
		failed = append(failed, phaseErr{"wafv2", err})
	}
	for _, scope := range scopes {
		t, i, kerr := scanWAFv2APIKeys(ctx, client, acct, region, scope, st, scanID)
		total += t
		inserted += i
		if kerr != nil {
			failed = append(failed, phaseErr{wafv2ListAPIKeysOp, kerr})
		}
	}
	switch len(failed) {
	case 0:
		return total, inserted, nil
	case 1:
		return total, inserted, failed[0].err
	}
	errs := make([]error, len(failed))
	for i, f := range failed {
		errs[i] = demoteTransient(st, f.op, acct, region, f.err)
	}
	return total, inserted, errors.Join(errs...)
}

// scanWAFv2Scopes runs the per-scope phases, stopping at the first scope that
// fails. Its counts are the sum of what each scope returned, and a failing
// scope can return 0, 0 after storing rows.
func scanWAFv2Scopes(ctx context.Context, client wafv2API, acct *account, region string, scopes []types.Scope, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, scope := range scopes {
		t, i, err := scanWAFv2Scope(ctx, client, acct, region, scope, st, scanID)
		total += t
		inserted += i
		if err != nil {
			return total, inserted, err
		}
	}
	return total, inserted, nil
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
		if out.NextMarker == nil {
			break
		}
		if marker != nil && *out.NextMarker == *marker {
			err = fmt.Errorf("scope=%s: NextMarker %q: %w", scope, *marker, errRepeatedNextToken)
			break
		}
		marker = out.NextMarker
	}
	warnNotStored(st, wafv2ListAPIKeysOp, acct, region, &keyless, "API keys have no APIKey value")
	return upsertRegionChildren(st, acct, region, wafv2ListAPIKeysOp, keys, err)
}

func scanWAFv2Scope(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string) (total, inserted int, err error) {
	// Web ACLs: list summaries then GetWebACL for full config.
	var aclSummaries []types.WebACLSummary
	var aclMarker *string
	for {
		out, err := client.ListWebACLs(ctx, &wafv2.ListWebACLsInput{Scope: scope, NextMarker: aclMarker})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wafv2:ListWebACLs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wafv2:ListWebACLs scope=%s: %w", scope, err)
		}
		aclSummaries = append(aclSummaries, out.WebACLs...)
		if out.NextMarker == nil {
			break
		}
		aclMarker = out.NextMarker
	}

	var (
		mu       sync.Mutex
		aclBatch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, s := range aclSummaries {
		g.Go(func() error {
			out, err := client.GetWebACL(gctx, &wafv2.GetWebACLInput{
				Id:    s.Id,
				Name:  s.Name,
				Scope: scope,
			})
			if err != nil {
				if isAccessDenied(err) {
					return nil
				}
				return fmt.Errorf("wafv2:GetWebACL %s: %w", sv(s.Name), err)
			}
			acl := out.WebACL
			arn := sv(acl.ARN)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeWAFv2WebACL,
				NativeID:       arn,
				Name:           acl.Name,
				Region:         &region,
				AttributesJSON: mustJSON(acl),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			aclBatch = append(aclBatch, r)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(aclBatch) > 0 {
		n, err := st.UpsertResources(aclBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert WAFv2 WebACLs: %w", err)
		}
		total += len(aclBatch)
		inserted += n
	}

	// Rule groups
	var rgBatch []*store.Resource
	var rgMarker *string
	for {
		out, err := client.ListRuleGroups(ctx, &wafv2.ListRuleGroupsInput{Scope: scope, NextMarker: rgMarker})
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, nil
			}
			return 0, 0, fmt.Errorf("wafv2:ListRuleGroups scope=%s: %w", scope, err)
		}
		for _, s := range out.RuleGroups {
			arn := sv(s.ARN)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeWAFv2RuleGroup,
				NativeID:       arn,
				Name:           s.Name,
				Region:         &region,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			}
			rgBatch = append(rgBatch, r)
		}
		if out.NextMarker == nil {
			break
		}
		rgMarker = out.NextMarker
	}
	if len(rgBatch) > 0 {
		n, err := st.UpsertResources(rgBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert WAFv2 rule groups: %w", err)
		}
		total += len(rgBatch)
		inserted += n
	}

	// IP sets
	var ipBatch []*store.Resource
	var ipMarker *string
	for {
		out, err := client.ListIPSets(ctx, &wafv2.ListIPSetsInput{Scope: scope, NextMarker: ipMarker})
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, nil
			}
			return 0, 0, fmt.Errorf("wafv2:ListIPSets scope=%s: %w", scope, err)
		}
		for _, s := range out.IPSets {
			arn := sv(s.ARN)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeWAFv2IPSet,
				NativeID:       arn,
				Name:           s.Name,
				Region:         &region,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			}
			ipBatch = append(ipBatch, r)
		}
		if out.NextMarker == nil {
			break
		}
		ipMarker = out.NextMarker
	}
	if len(ipBatch) > 0 {
		n, err := st.UpsertResources(ipBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert WAFv2 IP sets: %w", err)
		}
		total += len(ipBatch)
		inserted += n
	}

	{
		t, i, ferr := scanWAFv2ScopeExtended(ctx, client, acct, region, scope, st, scanID, aclSummaries)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}
