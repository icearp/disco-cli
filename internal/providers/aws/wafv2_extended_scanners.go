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

// wafv2RegionalResourceTypes are the ResourceType values ListResourcesForWebACL
// is called with for a regional web ACL, each with the disco scanner service
// that owns the protected resource. The op lists one resource type per call
// (ALB when none is given). A type is requested only in regions its owner is
// dispatched to (account.dispatchesTo), because WAF's answer for a type whose
// service AWS does not offer in the region is unverified. Verified Access has
// no service of its own and goes with aws:ec2.
//
// Every value of the pinned SDK's enum is here except AMPLIFY. An Amplify
// application takes a CLOUDFRONT-scope web ACL (the SDK's Scope doc), and disco
// does not list associations of CLOUDFRONT-scope ACLs: Amplify associations
// are an open gap, not handled.
var wafv2RegionalResourceTypes = []struct {
	rt    types.ResourceType
	owner string
}{
	{types.ResourceTypeApplicationLoadBalancer, "aws:elasticloadbalancingv2"},
	{types.ResourceTypeApiGateway, "aws:apigateway"},
	{types.ResourceTypeAppsync, "aws:appsync"},
	{types.ResourceTypeCognitioUserPool, "aws:cognito"},
	{types.ResourceTypeAppRunnerService, "aws:apprunner"},
	{types.ResourceTypeVerifiedAccessInstance, "aws:ec2"},
	{types.ResourceTypeAgentcoreGateway, "aws:bedrockagentcore"},
}

// wafv2ResourceTypesIn returns the wafv2RegionalResourceTypes whose owning
// service is dispatched to region.
func wafv2ResourceTypesIn(acct *account, region string) []types.ResourceType {
	var rts []types.ResourceType
	for _, t := range wafv2RegionalResourceTypes {
		if acct.dispatchesTo(t.owner, region) {
			rts = append(rts, t.rt)
		}
	}
	return rts
}

// scanWAFv2WebACLAssociations stores the resources associated with each
// regional WebACL, listed once per wafv2ResourceTypesIn value. Synth
// ARN: webACLArn + /association/{resourceArn}. An ACL deleted since it was
// stored (WAFNonexistentItemException, in the op's model) has no associations
// left to list. A (ACL, resource type) listing that is denied (the op needs
// permissions specific to the resource type), or still fails transiently after
// the SDK's retries, is skipped with one warning per cause for all such
// listings. A resource type WAF rejects (isWAFv2ResourceTypeRejected) warns
// once per type. Any other error is returned, naming its ACL and resource type,
// after every other listing's associations are stored.
func scanWAFv2WebACLAssociations(ctx context.Context, client wafv2API, acct *account, region string, st *store.Store, scanID string, webACLArns []string) (int, int, error) {
	const op = "wafv2:ListResourcesForWebACL"
	var (
		batch           []*store.Resource
		denied, unkeyed skipTally
		transient       transientTally
		failed          []error
		rejected        = map[types.ResourceType]*skipTally{}
		rts             = wafv2ResourceTypesIn(acct, region)
	)
acls:
	for _, wa := range webACLArns {
		for _, rt := range rts {
			out, err := client.ListResourcesForWebACL(ctx, &wafv2.ListResourcesForWebACLInput{WebACLArn: &wa, ResourceType: rt})
			key := wa + " " + string(rt)
			switch {
			case err == nil:
			case isAPIErrorCode(err, "WAFNonexistentItemException"):
				continue acls
			case transient.drop(key, err):
				continue
			case isAccessDenied(err):
				denied.skipped(key, err)
				continue
			case isWAFv2ResourceTypeRejected(err):
				if rejected[rt] == nil {
					rejected[rt] = &skipTally{}
				}
				rejected[rt].skipped(wa, err)
				continue
			default:
				failed = append(failed, fmt.Errorf("%s: %w", key, err))
				continue
			}
			batch = append(batch, wafv2AssociationRows(acct, region, scanID, wa, out.ResourceArns, &unkeyed)...)
		}
	}
	if denied.count > 0 {
		_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf("%d (web ACL, resource type) listing(s) skipped, first %s: %w", denied.count, denied.first, denied.err))
	}
	for _, rt := range rts {
		if t := rejected[rt]; t != nil {
			t.warnCode(st, op, acct, region, "WAFInvalidParameterException", "web ACL(s) listed for resource type "+string(rt),
				"WAF may not support this resource type in the region")
		}
	}
	if transient.count > 0 {
		_ = skipIfTransient(st, op, acct.ID, region, fmt.Errorf("%d (web ACL, resource type) listing(s) skipped, first %s: %w", transient.count, transient.first, transient.err))
	}
	warnNotStored(st, op, acct, region, &unkeyed, "associated resources have an empty ARN")
	return upsertListed(st, batch, op, errors.Join(failed...))
}

// isWAFv2ResourceTypeRejected reports whether WAF refused the ResourceType of a
// ListResourcesForWebACL call (WAFInvalidParameterException with Field
// RESOURCE_TYPE). disco passes only values of the SDK enum, so the likely cause
// is a resource type WAF does not support in the region, a per-region feature
// gap rather than a bad input; it is warned about, never skipped silently,
// because the rejection has not been observed live. A rejection of any other
// field stays an error.
func isWAFv2ResourceTypeRejected(err error) bool {
	var pe *types.WAFInvalidParameterException
	return errors.As(err, &pe) && pe.Field == types.ParameterExceptionFieldResourceType
}

// wafv2AssociationRows builds one association row per resource ARN listed for
// web ACL wa; an empty ARN is tallied in unkeyed instead of stored.
func wafv2AssociationRows(acct *account, region, scanID, wa string, resourceArns []string, unkeyed *skipTally) []*store.Resource {
	var rows []*store.Resource
	for _, rArn := range resourceArns {
		if rArn == "" {
			unkeyed.skipped("of web ACL "+wa, nil)
			continue
		}
		label := rArn
		rows = append(rows, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeWAFv2WebACLAssociation, NativeID: wa + "/association/" + rArn,
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(map[string]string{"WebACLArn": wa, "ResourceArn": rArn}), DiscoveredBy: scanID,
		})
	}
	return rows
}
