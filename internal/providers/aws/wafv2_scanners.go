package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWAFv2WebACL, Service: "wafv2", Upstream: "AWS::WAFv2::WebACL"})
	registerType(restype.Descriptor{Type: TypeWAFv2RuleGroup, Service: "wafv2", Upstream: "AWS::WAFv2::RuleGroup", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFv2IPSet, Service: "wafv2", Upstream: "AWS::WAFv2::IPSet", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFv2LoggingConfiguration, Service: "wafv2"})
	registerType(restype.Descriptor{Type: TypeWAFv2RegexPatternSet, Service: "wafv2", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFv2WebACLAssociation, Service: "wafv2"})
	registerType(restype.Descriptor{Type: TypeWAFv2ManagedRuleSet, Service: "wafv2", Leaf: true})
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
}

// scanWAFv2 discovers WAFv2 web ACLs, rule groups, and IP sets in one region,
// across both scopes: REGIONAL (per region) and CLOUDFRONT (global, reachable
// only from us-east-1 — fetched only there to avoid duplicates).
func scanWAFv2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := wafv2.NewFromConfig(acct.cfg, func(o *wafv2.Options) { o.Region = region })

	scopes := []types.Scope{types.ScopeRegional}
	if region == "us-east-1" {
		scopes = append(scopes, types.ScopeCloudfront)
	}

	for _, scope := range scopes {
		t, i, err := scanWAFv2Scope(ctx, client, acct, region, scope, st, scanID)
		if err != nil {
			return 0, 0, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
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
