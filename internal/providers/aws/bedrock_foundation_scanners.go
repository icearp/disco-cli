package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
)

func scanBedrockFoundation(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	guardIDs, t, i, ferr := scanBedrockGuardrails(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanBedrockGuardrailVersions(ctx, client, acct, region, st, scanID, guardIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	policyArns, t, i, ferr := scanBedrockARPolicies(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanBedrockARPolicyVersions(ctx, client, acct, region, st, scanID, policyArns)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanBedrockPromptRouters(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanBedrockInferenceProfiles(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanBedrockEnforcedGuardrails(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanBedrockGuardrails(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bedrock.NewListGuardrailsPaginator(client, &bedrock.ListGuardrailsInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isSCPExplicitDeny(perr) {
				return nil, 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrock:ListGuardrails", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("bedrock:ListGuardrails: %w", perr)
		}
		for _, g := range out.Guardrails {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			ids = append(ids, sv(g.Id))
			label := sv(g.Name)
			if label == "" {
				label = sv(g.Id)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBedrockGuardrail,
				NativeID:       arn,
				Name:           &label,
				Region:         &region,
				AttributesJSON: mustJSON(g),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return ids, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return ids, 0, 0, fmt.Errorf("upsert bedrock guardrails: %w", uerr)
	}
	return ids, len(batch), n, nil
}

// scanBedrockGuardrailVersions calls ListGuardrails(GuardrailIdentifier=id)
// per-guardrail — when set, returns versions of that guardrail.
func scanBedrockGuardrailVersions(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, guardrailIDs []string) (int, int, error) {
	if len(guardrailIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, gid := range guardrailIDs {
		id := gid
		pager := bedrock.NewListGuardrailsPaginator(client, &bedrock.ListGuardrailsInput{GuardrailIdentifier: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrock:ListGuardrails(versions) %s: %w", gid, perr)
			}
			for _, g := range out.Guardrails {
				arn := sv(g.Arn)
				ver := sv(g.Version)
				if arn == "" || ver == "" || ver == "DRAFT" {
					continue
				}
				vlabel := ver
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockGuardrailVersion, NativeID: arn + ":" + ver,
					Name: &vlabel, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
				})
			}
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert bedrock guardrail-versions: %w", uerr)
	}
	return len(batch), n, nil
}

func scanBedrockARPolicies(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bedrock.NewListAutomatedReasoningPoliciesPaginator(client, &bedrock.ListAutomatedReasoningPoliciesInput{})
	var arns []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			// Feature-gap canned 403: "Your account is not authorized to invoke
			// this API operation" — distinct from the per-action IAM-deny
			// "User: arn:... is not authorized to perform: <action>" form.
			// AR Policies is gated to limited regions/accounts.
			if isAccessDeniedWithMessage(perr, "not authorized to invoke this API operation") {
				return nil, 0, 0, nil
			}
			if isSCPExplicitDeny(perr) {
				return nil, 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrock:ListAutomatedReasoningPolicies", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("bedrock:ListAutomatedReasoningPolicies: %w", perr)
		}
		for _, p := range out.AutomatedReasoningPolicySummaries {
			arn := sv(p.PolicyArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			label := sv(p.Name)
			if label == "" {
				label = sv(p.PolicyId)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBedrockAutomatedReasoningPolicy,
				NativeID:       arn,
				Name:           &label,
				Region:         &region,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return arns, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return arns, 0, 0, fmt.Errorf("upsert bedrock automated-reasoning-policies: %w", uerr)
	}
	return arns, len(batch), n, nil
}

// scanBedrockARPolicyVersions calls ListAutomatedReasoningPolicies with
// PolicyArn set per-policy — returns versions of that policy.
func scanBedrockARPolicyVersions(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, policyArns []string) (int, int, error) {
	if len(policyArns) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, pa := range policyArns {
		arnRef := pa
		pager := bedrock.NewListAutomatedReasoningPoliciesPaginator(client, &bedrock.ListAutomatedReasoningPoliciesInput{PolicyArn: &arnRef})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrock:ListAutomatedReasoningPolicies(versions) %s: %w", pa, perr)
			}
			for _, p := range out.AutomatedReasoningPolicySummaries {
				arn := sv(p.PolicyArn)
				ver := sv(p.Version)
				if arn == "" || ver == "" || ver == "DRAFT" {
					continue
				}
				vlabel := ver
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockAutomatedReasoningPolicyVersion, NativeID: arn + ":" + ver,
					Name: &vlabel, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert bedrock automated-reasoning-policy-versions: %w", uerr)
	}
	return len(batch), n, nil
}

func scanBedrockPromptRouters(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bedrock.NewListPromptRoutersPaginator(client, &bedrock.ListPromptRoutersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			// Op not deployed in this region. AWS returns two feature-gap
			// shapes under ValidationException — older "operation is not
			// recognized" and newer canned "You don't have the permissions
			// to perform the requested operation." Real IAM denials surface
			// as AccessDeniedException with an action-identifying body and
			// route through skipIfAccessDenied below.
			if isAPIErrorWithMessage(perr, "ValidationException", "operation is not recognized") ||
				isAPIErrorWithMessage(perr, "ValidationException", "don't have the permissions to perform the requested operation") {
				return 0, 0, nil
			}
			if isSCPExplicitDeny(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrock:ListPromptRouters", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrock:ListPromptRouters: %w", perr)
		}
		for _, p := range out.PromptRouterSummaries {
			arn := sv(p.PromptRouterArn)
			if arn == "" {
				continue
			}
			label := sv(p.PromptRouterName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider:    "aws",
				AccountID:   acct.ID,
				AccountName: &acct.Name,
				Type:        TypeBedrockIntelligentPromptRouter,
				NativeID:    arn,
				Name:        &label,
				Region:      &region,
				// AWS-supplied default routers carry Type="Default"; customer-defined
				// routers carry Type="Custom". SDK enum is lowercase but case-insensitive
				// match guards against AWS surface drift.
				ManagedByProvider: strings.EqualFold(string(p.Type), "Default"),
				AttributesJSON:    mustJSON(p),
				DiscoveredBy:      scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert bedrock prompt-routers: %w", uerr)
	}
	return len(batch), n, nil
}

// scanBedrockInferenceProfiles emits only APPLICATION-type profiles —
// SYSTEM_DEFINED profiles are managed by AWS and listed separately.
func scanBedrockInferenceProfiles(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bedrock.NewListInferenceProfilesPaginator(client, &bedrock.ListInferenceProfilesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isSCPExplicitDeny(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrock:ListInferenceProfiles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrock:ListInferenceProfiles: %w", perr)
		}
		for _, p := range out.InferenceProfileSummaries {
			if string(p.Type) != "APPLICATION" {
				continue
			}
			arn := sv(p.InferenceProfileArn)
			if arn == "" {
				continue
			}
			label := sv(p.InferenceProfileName)
			if label == "" {
				label = sv(p.InferenceProfileId)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBedrockApplicationInferenceProfile,
				NativeID:       arn,
				Name:           &label,
				Region:         &region,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert bedrock inference-profiles: %w", uerr)
	}
	return len(batch), n, nil
}

func scanBedrockEnforcedGuardrails(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bedrock.NewListEnforcedGuardrailsConfigurationPaginator(client, &bedrock.ListEnforcedGuardrailsConfigurationInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isSCPExplicitDeny(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrock:ListEnforcedGuardrailsConfiguration", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrock:ListEnforcedGuardrailsConfiguration: %w", perr)
		}
		for _, g := range out.GuardrailsConfig {
			id := sv(g.ConfigId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:bedrock:%s:%s:enforced-guardrail-configuration/%s", region, acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBedrockEnforcedGuardrailConfiguration,
				NativeID:       arn,
				Name:           &label,
				Region:         &region,
				AttributesJSON: mustJSON(g),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert bedrock enforced-guardrails: %w", uerr)
	}
	return len(batch), n, nil
}
