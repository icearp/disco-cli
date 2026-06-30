package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2/types"
)

// scanWAFv2ScopeExtended discovers per-scope logging configurations, regex
// pattern sets, and web-ACL associations. WebACLAssociation rows are
// synthesized per (web ACL ARN, associated resource ARN); LoggingConfig and
// RegexPatternSet expose native ARNs.
func scanWAFv2ScopeExtended(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string, aclSummaries []types.WebACLSummary) (total, inserted int, err error) {
	{
		t, i, ferr := scanWAFv2LoggingConfigs(ctx, client, acct, region, scope, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	{
		t, i, ferr := scanWAFv2RegexPatternSets(ctx, client, acct, region, scope, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	{
		t, i, ferr := scanWAFv2ManagedRuleSets(ctx, client, acct, region, scope, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	// Web-ACL associations only meaningful for REGIONAL scope (CloudFront
	// distributions associate via dedicated APIs, not ListResourcesForWebACL).
	if scope == types.ScopeRegional {
		for _, s := range aclSummaries {
			t, i, ferr := scanWAFv2WebACLAssociations(ctx, client, acct, region, st, scanID, sv(s.ARN))
			if ferr != nil {
				return total, inserted, ferr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

func scanWAFv2LoggingConfigs(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextMarker *string
	for {
		out, err := client.ListLoggingConfigurations(ctx, &wafv2.ListLoggingConfigurationsInput{Scope: scope, NextMarker: nextMarker})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wafv2:ListLoggingConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wafv2:ListLoggingConfigurations scope=%s: %w", scope, err)
		}
		for _, c := range out.LoggingConfigurations {
			arn := sv(c.ResourceArn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWAFv2LoggingConfiguration, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextMarker == nil {
			break
		}
		nextMarker = out.NextMarker
	}
	return upsertBatch(st, batch, "wafv2 logging-configurations")
}

func scanWAFv2RegexPatternSets(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextMarker *string
	for {
		out, err := client.ListRegexPatternSets(ctx, &wafv2.ListRegexPatternSetsInput{Scope: scope, NextMarker: nextMarker})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wafv2:ListRegexPatternSets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wafv2:ListRegexPatternSets scope=%s: %w", scope, err)
		}
		for _, r := range out.RegexPatternSets {
			arn := sv(r.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWAFv2RegexPatternSet, NativeID: arn,
				Name: r.Name, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextMarker == nil {
			break
		}
		nextMarker = out.NextMarker
	}
	return upsertBatch(st, batch, "wafv2 regex-pattern-sets")
}

// scanWAFv2ManagedRuleSets lists the managed rule sets owned by the account
// (relevant only to AWS Marketplace managed-rule-group sellers). Per-scope;
// AccessDenied is tolerated since most accounts are not sellers.
func scanWAFv2ManagedRuleSets(ctx context.Context, client wafv2API, acct *account, region string, scope types.Scope, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextMarker *string
	for {
		out, err := client.ListManagedRuleSets(ctx, &wafv2.ListManagedRuleSetsInput{Scope: scope, NextMarker: nextMarker})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wafv2:ListManagedRuleSets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wafv2:ListManagedRuleSets scope=%s: %w", scope, err)
		}
		for _, m := range out.ManagedRuleSets {
			arn := sv(m.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWAFv2ManagedRuleSet, NativeID: arn,
				Name: m.Name, Region: &region,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
		if out.NextMarker == nil {
			break
		}
		nextMarker = out.NextMarker
	}
	return upsertBatch(st, batch, "wafv2 managed-rule-sets")
}

// scanWAFv2WebACLAssociations enumerates resources associated with each
// regional WebACL. Synth ARN: webACLArn + /association/{resourceArn}.
func scanWAFv2WebACLAssociations(ctx context.Context, client wafv2API, acct *account, region string, st *store.Store, scanID string, webACLArn string) (int, int, error) {
	if webACLArn == "" {
		return 0, 0, nil
	}
	wa := webACLArn
	out, err := client.ListResourcesForWebACL(ctx, &wafv2.ListResourcesForWebACLInput{WebACLArn: &wa})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "wafv2:ListResourcesForWebACL", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("wafv2:ListResourcesForWebACL: %w", err)
	}
	var batch []*store.Resource
	for _, rArn := range out.ResourceArns {
		if rArn == "" {
			continue
		}
		arn := wa + "/association/" + rArn
		label := rArn
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeWAFv2WebACLAssociation, NativeID: arn,
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(map[string]string{"WebACLArn": wa, "ResourceArn": rArn}), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "wafv2 web-acl-associations")
}
