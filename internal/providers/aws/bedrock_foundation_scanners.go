package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
)

func scanBedrockFoundation(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanBedrockGuardrails(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBedrockARPolicies(ctx, client, acct, region, st, scanID) },
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

func scanBedrockGuardrails(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bedrock.NewListGuardrailsPaginator(client, &bedrock.ListGuardrailsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrock:ListGuardrails", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrock:ListGuardrails: %w", perr)
		}
		for _, g := range out.Guardrails {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
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
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert bedrock guardrails: %w", uerr)
	}
	return len(batch), n, nil
}

func scanBedrockARPolicies(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bedrock.NewListAutomatedReasoningPoliciesPaginator(client, &bedrock.ListAutomatedReasoningPoliciesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrock:ListAutomatedReasoningPolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrock:ListAutomatedReasoningPolicies: %w", perr)
		}
		for _, p := range out.AutomatedReasoningPolicySummaries {
			arn := sv(p.PolicyArn)
			if arn == "" {
				continue
			}
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
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert bedrock automated-reasoning-policies: %w", uerr)
	}
	return len(batch), n, nil
}

func scanBedrockPromptRouters(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bedrock.NewListPromptRoutersPaginator(client, &bedrock.ListPromptRoutersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
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
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBedrockIntelligentPromptRouter,
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
