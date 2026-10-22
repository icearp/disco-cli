package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2/types"
	"github.com/icearp/disco-cli/store"
)

// scanWAFv2ScopeExtended discovers per-scope logging configurations, regex
// pattern sets, managed rule sets and web-ACL associations. Every phase, and
// the association listing of every web ACL, runs whatever the others
// returned; the counts are every row stored and the errors join through
// phaseErrs. WebACLAssociation rows are synthesized per (web ACL ARN,
// associated resource ARN); LoggingConfig and RegexPatternSet expose native
// ARNs.
func scanWAFv2ScopeExtended(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string, webACLARNs []string) (total, inserted int, err error) {
	var errs phaseErrs
	add := func(op string, t, i int, err error) {
		total += t
		inserted += i
		errs.add(op, err)
	}
	t, i, err := scanWAFv2Summaries(st, acct, region, scanID, scope, "wafv2:ListLoggingConfigurations", "logging configurations", TypeWAFv2LoggingConfiguration,
		func(marker *string) ([]wafv2Listed, *string, error) {
			out, err := client.ListLoggingConfigurations(ctx, &wafv2.ListLoggingConfigurationsInput{Scope: scope, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafv2Listed, len(out.LoggingConfigurations))
			for i, c := range out.LoggingConfigurations {
				items[i].attrs = c
				// A configuration is keyed by the web ACL it belongs to; one
				// without that ARN is left without one, so it is not stored.
				if arn := sv(c.ResourceArn); arn != "" {
					// NativeID must differ from the WebACL ARN (== ResourceArn) or the two
					// would share one identity; suffix keeps it distinct + parent-derivable.
					id := arn + "/logging-configuration"
					items[i].arn, items[i].name = &id, c.ResourceArn
				}
			}
			return items, out.NextMarker, nil
		})
	add("wafv2:ListLoggingConfigurations", t, i, err)
	t, i, err = scanWAFv2Summaries(st, acct, region, scanID, scope, "wafv2:ListRegexPatternSets", "regex pattern sets", TypeWAFv2RegexPatternSet,
		func(marker *string) ([]wafv2Listed, *string, error) {
			out, err := client.ListRegexPatternSets(ctx, &wafv2.ListRegexPatternSetsInput{Scope: scope, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafv2Listed, len(out.RegexPatternSets))
			for i, r := range out.RegexPatternSets {
				items[i] = wafv2Listed{r.ARN, r.Name, r}
			}
			return items, out.NextMarker, nil
		})
	add("wafv2:ListRegexPatternSets", t, i, err)
	// Managed rule sets are owned only by AWS Marketplace managed-rule-group
	// sellers; most accounts list none.
	t, i, err = scanWAFv2Summaries(st, acct, region, scanID, scope, "wafv2:ListManagedRuleSets", "managed rule sets", TypeWAFv2ManagedRuleSet,
		func(marker *string) ([]wafv2Listed, *string, error) {
			out, err := client.ListManagedRuleSets(ctx, &wafv2.ListManagedRuleSetsInput{Scope: scope, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafv2Listed, len(out.ManagedRuleSets))
			for i, m := range out.ManagedRuleSets {
				items[i] = wafv2Listed{m.ARN, m.Name, m}
			}
			return items, out.NextMarker, nil
		})
	add("wafv2:ListManagedRuleSets", t, i, err)
	// Web-ACL associations only meaningful for REGIONAL scope (CloudFront
	// distributions associate via dedicated APIs, not ListResourcesForWebACL).
	if scope == types.ScopeRegional {
		t, i, err = scanWAFv2WebACLAssociations(ctx, client, acct, region, st, scanID, webACLARNs)
		add("wafv2:ListResourcesForWebACL", t, i, err)
	}
	return total, inserted, errs.join(st, acct, region)
}

// scanWAFv2WebACLAssociations stores the resources associated with each
// regional WebACL. Synth ARN: webACLArn + /association/{resourceArn}. An ACL
// deleted since it was stored (WAFNonexistentItemException, in the op's model)
// has no associations left to list. An ACL whose call is denied, or still
// fails transiently after the SDK's retries, is skipped with one warning per
// cause for all such ACLs. Any other error is returned, naming its ACL, after
// the other ACLs' associations are stored.
func scanWAFv2WebACLAssociations(ctx context.Context, client wafv2API, acct *account, region string, st *store.Store, scanID string, webACLArns []string) (int, int, error) {
	const op = "wafv2:ListResourcesForWebACL"
	var (
		batch           []*store.Resource
		denied, unkeyed skipTally
		transient       transientTally
		failed          []error
	)
	for _, wa := range webACLArns {
		out, err := client.ListResourcesForWebACL(ctx, &wafv2.ListResourcesForWebACLInput{WebACLArn: &wa})
		switch {
		case err == nil:
		case isAPIErrorCode(err, "WAFNonexistentItemException"):
			continue
		case transient.drop(wa, err):
			continue
		case isAccessDenied(err):
			denied.skipped(wa, err)
			continue
		default:
			failed = append(failed, fmt.Errorf("%s: %w", wa, err))
			continue
		}
		for _, rArn := range out.ResourceArns {
			if rArn == "" {
				unkeyed.skipped("of web ACL "+wa, nil)
				continue
			}
			label := rArn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWAFv2WebACLAssociation, NativeID: wa + "/association/" + rArn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(map[string]string{"WebACLArn": wa, "ResourceArn": rArn}), DiscoveredBy: scanID,
			})
		}
	}
	if denied.count > 0 {
		_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf("%d web ACL(s) skipped, first %s: %w", denied.count, denied.first, denied.err))
	}
	transient.warnSkipped(st, op, acct, region)
	warnNotStored(st, op, acct, region, &unkeyed, "associated resources have an empty ARN")
	return upsertListed(st, batch, op, errors.Join(failed...))
}
