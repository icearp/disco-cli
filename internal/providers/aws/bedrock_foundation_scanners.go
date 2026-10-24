package aws

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/icearp/disco-cli/store"
)

// scanBedrockGuardrails stores the region's guardrails with their tags and
// returns the stored ones that carry an Id, the parents of the
// guardrail-version phase. A first page refused by an SCP's explicit deny
// ends the listing silently.
func scanBedrockGuardrails(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (guardrails []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "bedrock:ListGuardrails", "guardrails", "Id",
		bacPages(isSCPExplicitDeny, func(ctx context.Context, token *string) ([]bedrocktypes.GuardrailSummary, *string, error) {
			out, err := client.ListGuardrails(ctx, &bedrock.ListGuardrailsInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Guardrails, out.NextToken, nil
		}),
		func(g bedrocktypes.GuardrailSummary) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockGuardrail, "guardrails", g.Arn, cmp.Or(sv(g.Name), sv(g.Id)), sv(g.Id), g)
		},
		func(g bedrocktypes.GuardrailSummary) string { return sv(g.Id) },
		tags, p)
}

// isBedrockARGap reports the first-page refusals of
// ListAutomatedReasoningPolicies that mean the feature is not offered to this
// account or region: the canned 403 "Your account is not authorized to invoke
// this API operation" (a real IAM deny names the action: "User: arn:... is
// not authorized to perform: <action>"), the empty-body AccessDeniedException
// of its region-gap variant, and an SCP's explicit deny.
func isBedrockARGap(err error) bool {
	return isAccessDeniedWithMessage(err, "not authorized to invoke this API operation") ||
		isClosedToNewCustomers(err) || isSCPExplicitDeny(err)
}

// scanBedrockARPolicies stores the region's automated-reasoning policies with
// their tags and returns the stored ones, keyed by ARN, the parents of the
// policy-version and test-case phases.
func scanBedrockARPolicies(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (policies []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "bedrock:ListAutomatedReasoningPolicies", "automated-reasoning policies", "PolicyArn",
		bacPages(isBedrockARGap, func(ctx context.Context, token *string) ([]bedrocktypes.AutomatedReasoningPolicySummary, *string, error) {
			out, err := client.ListAutomatedReasoningPolicies(ctx, &bedrock.ListAutomatedReasoningPoliciesInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AutomatedReasoningPolicySummaries, out.NextToken, nil
		}),
		func(pol bedrocktypes.AutomatedReasoningPolicySummary) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockAutomatedReasoningPolicy, "automated-reasoning policies",
				pol.PolicyArn, cmp.Or(sv(pol.Name), sv(pol.PolicyId)), sv(pol.PolicyId), pol)
		},
		func(pol bedrocktypes.AutomatedReasoningPolicySummary) string { return sv(pol.PolicyArn) },
		tags, p)
}

// isBedrockRouterGap reports the first-page refusals of ListPromptRouters
// that mean the op is not deployed in this region: two ValidationException
// shapes, the older "operation is not recognized" and the newer canned "You
// don't have the permissions to perform the requested operation.", and an
// SCP's explicit deny. A real IAM denial is an AccessDeniedException naming
// the action, and is warned about.
func isBedrockRouterGap(err error) bool {
	return isAPIErrorWithMessage(err, "ValidationException", "operation is not recognized") ||
		isAPIErrorWithMessage(err, "ValidationException", "don't have the permissions to perform the requested operation") ||
		isSCPExplicitDeny(err)
}

// scanBedrockPromptRouters stores the region's prompt routers with their
// tags. AWS-supplied default routers carry Type "default" and are
// ManagedByProvider, so their tags are not read (bedrockTags).
func scanBedrockPromptRouters(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrock:ListPromptRouters",
		bacPages(isBedrockRouterGap, func(ctx context.Context, token *string) ([]bedrocktypes.PromptRouterSummary, *string, error) {
			out, err := client.ListPromptRouters(ctx, &bedrock.ListPromptRoutersInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.PromptRouterSummaries, out.NextToken, nil
		}),
		func(r bedrocktypes.PromptRouterSummary) (*store.Resource, string, string) {
			row, why, key := bacRow(acct, region, scanID, TypeBedrockIntelligentPromptRouter, "prompt routers",
				r.PromptRouterArn, cmp.Or(sv(r.PromptRouterName), sv(r.PromptRouterArn)), sv(r.PromptRouterName), r)
			if row != nil {
				// The SDK enum is lowercase; a case-insensitive match guards against drift.
				row.ManagedByProvider = strings.EqualFold(string(r.Type), "Default")
			}
			return row, why, key
		}, tags, p)
}

// scanBedrockInferenceProfiles stores APPLICATION profiles (customer-created)
// as aws:bedrock:application-inference-profile and SYSTEM_DEFINED profiles
// (the AWS-managed cross-region routing catalog) as
// aws:bedrock:inference-profile with ManagedByProvider set, so only the
// application profiles' tags are read (bedrockTags). A first page refused by
// an SCP's explicit deny ends the listing silently.
func scanBedrockInferenceProfiles(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrock:ListInferenceProfiles",
		bacPages(isSCPExplicitDeny, func(ctx context.Context, token *string) ([]bedrocktypes.InferenceProfileSummary, *string, error) {
			out, err := client.ListInferenceProfiles(ctx, &bedrock.ListInferenceProfilesInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.InferenceProfileSummaries, out.NextToken, nil
		}),
		func(ip bedrocktypes.InferenceProfileSummary) (*store.Resource, string, string) {
			typ, managed := TypeBedrockApplicationInferenceProfile, false
			if ip.Type != bedrocktypes.InferenceProfileTypeApplication {
				typ, managed = TypeBedrockInferenceProfile, true
			}
			r, why, key := bacRow(acct, region, scanID, typ, "inference profiles",
				ip.InferenceProfileArn, cmp.Or(sv(ip.InferenceProfileName), sv(ip.InferenceProfileId)), sv(ip.InferenceProfileId), ip)
			if r != nil {
				r.ManagedByProvider = managed
			}
			return r, why, key
		}, tags, p)
}

// withoutDraft drops the DRAFT entry from a version listing: DRAFT is the
// parent's working copy, which the parent's own row already represents.
func withoutDraft[Item any](items []Item, version func(Item) *string) []Item {
	kept := items[:0:0]
	for _, it := range items {
		if sv(version(it)) != "DRAFT" {
			kept = append(kept, it)
		}
	}
	return kept
}

// bedrockVersionRow is the row of a version whose list element carries its
// parent's ARN rather than its own, stored under that ARN and the version.
// An element without either gives no row: the reason names the items
// (what), and key, the parent, names the item in the warning about them.
func bedrockVersionRow(acct *account, region, scanID, typ, what string, parentARN, version *string, key string, item any) (*store.Resource, string, string) {
	ver := sv(version)
	switch {
	case sv(parentARN) == "":
		return nil, what + " have an empty ARN", cmp.Or(ver, "(a version with no number)") + " of " + key
	case ver == "":
		return nil, what + " have an empty Version", "a version of " + key
	}
	return bacRowAt(acct, region, scanID, typ, sv(parentARN)+":"+ver, ver, item), "", ""
}

// scanBedrockGuardrailVersions lists the versions of each guardrail the
// guardrail phase stored (ListGuardrails with GuardrailIdentifier set),
// concurrently (storeTokenEachParent). A version's element carries the
// guardrail's ARN, since GuardrailArn's pattern admits no version, so its
// NativeID is that ARN and the version. A guardrail ResourceNotFoundException
// says was deleted since it was listed is skipped; a denial is warned about.
func scanBedrockGuardrailVersions(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, guardrailIDs []string) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "bedrock:ListGuardrails", []string{"ResourceNotFoundException"}, guardrailIDs,
		func(ctx context.Context, id string, token *string) ([]bedrocktypes.GuardrailSummary, *string, error) {
			out, err := client.ListGuardrails(ctx, &bedrock.ListGuardrailsInput{GuardrailIdentifier: &id, NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return withoutDraft(out.Guardrails, func(g bedrocktypes.GuardrailSummary) *string { return g.Version }), out.NextToken, nil
		},
		func(id string, g bedrocktypes.GuardrailSummary) (*store.Resource, string, string) {
			return bedrockVersionRow(acct, region, scanID, TypeBedrockGuardrailVersion, "guardrail versions", g.Arn, g.Version, "guardrail "+id, g)
		})
}

// scanBedrockARPolicyVersions lists the versions of each automated-reasoning
// policy the policy phase stored (ListAutomatedReasoningPolicies with
// PolicyArn set), concurrently (storeTokenEachParent), stored under
// {PolicyArn}:{Version}. Whether a version's PolicyArn already ends in that
// version is not settled: its doc says "The ARN of the policy", while its
// pattern admits a ":N" suffix. So the NativeID keeps its shape, and the
// version's tags are not read, since a doubled suffix would send the tag op
// an ARN naming no resource. A policy ResourceNotFoundException says was
// deleted since it was listed is skipped; a denial is warned about.
func scanBedrockARPolicyVersions(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, policyARNs []string) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "bedrock:ListAutomatedReasoningPolicies", []string{"ResourceNotFoundException"}, policyARNs,
		func(ctx context.Context, arn string, token *string) ([]bedrocktypes.AutomatedReasoningPolicySummary, *string, error) {
			out, err := client.ListAutomatedReasoningPolicies(ctx, &bedrock.ListAutomatedReasoningPoliciesInput{PolicyArn: &arn, NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return withoutDraft(out.AutomatedReasoningPolicySummaries, func(p bedrocktypes.AutomatedReasoningPolicySummary) *string { return p.Version }), out.NextToken, nil
		},
		func(arn string, p bedrocktypes.AutomatedReasoningPolicySummary) (*store.Resource, string, string) {
			return bedrockVersionRow(acct, region, scanID, TypeBedrockAutomatedReasoningPolicyVersion, "automated-reasoning policy versions", p.PolicyArn, p.Version, "policy "+arn, p)
		})
}

// scanBedrockFoundationModels mirrors the AWS catalog of available foundation
// models (ListFoundationModels, a single unpaginated call). The type's
// descriptor flags the rows ManagedByProvider: it's an AWS-owned catalog,
// hidden from default queries. A refusal by an SCP's explicit deny ends the
// listing silently.
func scanBedrockFoundationModels(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "bedrock:ListFoundationModels",
		bacPages(isSCPExplicitDeny, func(ctx context.Context, _ *string) ([]bedrocktypes.FoundationModelSummary, *string, error) {
			out, err := client.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{})
			if err != nil {
				return nil, nil, err
			}
			return out.ModelSummaries, nil, nil
		}),
		func(m bedrocktypes.FoundationModelSummary) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockFoundationModel, "foundation models", m.ModelArn, cmp.Or(sv(m.ModelName), sv(m.ModelId)), sv(m.ModelId), m)
		})
}

// scanBedrockEnforcedGuardrails stores the account's enforced guardrail
// configurations, those read before a failed page included. The element
// carries no ARN; its NativeID is built from ConfigId. A first page refused by
// an SCP's explicit deny ends the listing silently.
func scanBedrockEnforcedGuardrails(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "bedrock:ListEnforcedGuardrailsConfiguration",
		bacPages(isSCPExplicitDeny, func(ctx context.Context, token *string) ([]bedrocktypes.AccountEnforcedGuardrailOutputConfiguration, *string, error) {
			out, err := client.ListEnforcedGuardrailsConfiguration(ctx, &bedrock.ListEnforcedGuardrailsConfigurationInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.GuardrailsConfig, out.NextToken, nil
		}),
		func(g bedrocktypes.AccountEnforcedGuardrailOutputConfiguration) (*store.Resource, string, string) {
			id := sv(g.ConfigId)
			if id == "" {
				return nil, "enforced guardrail configurations have an empty ConfigId", cmp.Or(sv(g.GuardrailArn), "(a configuration with no guardrail)")
			}
			arn := fmt.Sprintf("arn:aws:bedrock:%s:%s:enforced-guardrail-configuration/%s", region, acct.ID, id)
			return bacRowAt(acct, region, scanID, TypeBedrockEnforcedGuardrailConfiguration, arn, id, g), "", ""
		})
}
