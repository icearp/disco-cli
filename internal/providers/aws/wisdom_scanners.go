package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/qconnect"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWisdomAssistant, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomAssistantAssociation, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomAIAgent, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomAIAgentVersion, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomAIGuardrail, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomAIGuardrailVersion, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomAIPrompt, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomAIPromptVersion, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomKnowledgeBase, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomMessageTemplate, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomMessageTemplateVersion, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomQuickResponse, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomContent, Service: "wisdom"})
	registerType(restype.Descriptor{Type: TypeWisdomContentAssociation, Service: "wisdom"})
	registerService(serviceEntry{
		name: "aws:wisdom",
		fn:   scanWisdom,
	})
}

// wisdomAPI — narrow surface of qconnect ops. AWS renamed Wisdom to Amazon Q
// Connect: SDK package is qconnect, CFN types remain AWS::Wisdom::*.
type wisdomAPI interface {
	ListAssistants(context.Context, *qconnect.ListAssistantsInput, ...func(*qconnect.Options)) (*qconnect.ListAssistantsOutput, error)
	ListAssistantAssociations(context.Context, *qconnect.ListAssistantAssociationsInput, ...func(*qconnect.Options)) (*qconnect.ListAssistantAssociationsOutput, error)
	ListAIAgents(context.Context, *qconnect.ListAIAgentsInput, ...func(*qconnect.Options)) (*qconnect.ListAIAgentsOutput, error)
	ListAIAgentVersions(context.Context, *qconnect.ListAIAgentVersionsInput, ...func(*qconnect.Options)) (*qconnect.ListAIAgentVersionsOutput, error)
	ListAIGuardrails(context.Context, *qconnect.ListAIGuardrailsInput, ...func(*qconnect.Options)) (*qconnect.ListAIGuardrailsOutput, error)
	ListAIGuardrailVersions(context.Context, *qconnect.ListAIGuardrailVersionsInput, ...func(*qconnect.Options)) (*qconnect.ListAIGuardrailVersionsOutput, error)
	ListAIPrompts(context.Context, *qconnect.ListAIPromptsInput, ...func(*qconnect.Options)) (*qconnect.ListAIPromptsOutput, error)
	ListAIPromptVersions(context.Context, *qconnect.ListAIPromptVersionsInput, ...func(*qconnect.Options)) (*qconnect.ListAIPromptVersionsOutput, error)
	ListKnowledgeBases(context.Context, *qconnect.ListKnowledgeBasesInput, ...func(*qconnect.Options)) (*qconnect.ListKnowledgeBasesOutput, error)
	ListMessageTemplates(context.Context, *qconnect.ListMessageTemplatesInput, ...func(*qconnect.Options)) (*qconnect.ListMessageTemplatesOutput, error)
	ListMessageTemplateVersions(context.Context, *qconnect.ListMessageTemplateVersionsInput, ...func(*qconnect.Options)) (*qconnect.ListMessageTemplateVersionsOutput, error)
	ListQuickResponses(context.Context, *qconnect.ListQuickResponsesInput, ...func(*qconnect.Options)) (*qconnect.ListQuickResponsesOutput, error)
	ListContents(context.Context, *qconnect.ListContentsInput, ...func(*qconnect.Options)) (*qconnect.ListContentsOutput, error)
	ListContentAssociations(context.Context, *qconnect.ListContentAssociationsInput, ...func(*qconnect.Options)) (*qconnect.ListContentAssociationsOutput, error)
}

// wisdomChild keys per-(parent, id) for version fan-out.
type wisdomChild struct {
	parent string
	id     string
}

func scanWisdom(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := qconnect.NewFromConfig(acct.cfg, func(o *qconnect.Options) { o.Region = region })

	assistantIDs, t, i, ferr := scanWisdomAssistants(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	kbIDs, t, i, ferr := scanWisdomKnowledgeBases(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	agentKeys, t, i, ferr := scanWisdomAIAgents(ctx, client, acct, region, st, scanID, assistantIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	guardKeys, t, i, ferr := scanWisdomAIGuardrails(ctx, client, acct, region, st, scanID, assistantIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	promptKeys, t, i, ferr := scanWisdomAIPrompts(ctx, client, acct, region, st, scanID, assistantIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	mtKeys, t, i, ferr := scanWisdomMessageTemplates(ctx, client, acct, region, st, scanID, kbIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	contentKeys, t, i, ferr := scanWisdomContents(ctx, client, acct, region, st, scanID, kbIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanWisdomContentAssociations(ctx, client, acct, region, st, scanID, contentKeys)
		},
		func() (int, int, error) {
			return scanWisdomAssistantAssocs(ctx, client, acct, region, st, scanID, assistantIDs)
		},
		func() (int, int, error) {
			return scanWisdomAIAgentVersions(ctx, client, acct, region, st, scanID, agentKeys)
		},
		func() (int, int, error) {
			return scanWisdomAIGuardrailVersions(ctx, client, acct, region, st, scanID, guardKeys)
		},
		func() (int, int, error) {
			return scanWisdomAIPromptVersions(ctx, client, acct, region, st, scanID, promptKeys)
		},
		func() (int, int, error) {
			return scanWisdomMessageTemplateVersions(ctx, client, acct, region, st, scanID, mtKeys)
		},
		func() (int, int, error) {
			return scanWisdomQuickResponses(ctx, client, acct, region, st, scanID, kbIDs)
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

func scanWisdomAssistants(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := qconnect.NewListAssistantsPaginator(client, &qconnect.ListAssistantsInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "qconnect:ListAssistants", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("qconnect:ListAssistants: %w", perr)
		}
		for _, a := range out.AssistantSummaries {
			arn := sv(a.AssistantArn)
			if arn == "" {
				continue
			}
			id := sv(a.AssistantId)
			ids = append(ids, id)
			label := sv(a.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWisdomAssistant, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "wisdom assistants")
	return ids, t, i, err
}

func scanWisdomKnowledgeBases(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := qconnect.NewListKnowledgeBasesPaginator(client, &qconnect.ListKnowledgeBasesInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "qconnect:ListKnowledgeBases", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("qconnect:ListKnowledgeBases: %w", perr)
		}
		for _, k := range out.KnowledgeBaseSummaries {
			arn := sv(k.KnowledgeBaseArn)
			if arn == "" {
				continue
			}
			id := sv(k.KnowledgeBaseId)
			ids = append(ids, id)
			label := sv(k.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWisdomKnowledgeBase, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(k), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "wisdom knowledge-bases")
	return ids, t, i, err
}

func scanWisdomAssistantAssocs(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, assistantIDs []string) (int, int, error) {
	if len(assistantIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, aid := range assistantIDs {
		id := aid
		pager := qconnect.NewListAssistantAssociationsPaginator(client, &qconnect.ListAssistantAssociationsInput{AssistantId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("qconnect:ListAssistantAssociations %s: %w", aid, perr)
			}
			for _, a := range out.AssistantAssociationSummaries {
				arn := sv(a.AssistantAssociationArn)
				if arn == "" {
					continue
				}
				label := sv(a.AssistantAssociationId)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomAssistantAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "wisdom assistant-associations")
}

func scanWisdomAIAgents(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, assistantIDs []string) ([]wisdomChild, int, int, error) {
	if len(assistantIDs) == 0 {
		return nil, 0, 0, nil
	}
	var keys []wisdomChild
	var batch []*store.Resource
	for _, aid := range assistantIDs {
		id := aid
		pager := qconnect.NewListAIAgentsPaginator(client, &qconnect.ListAIAgentsInput{AssistantId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return nil, 0, 0, fmt.Errorf("qconnect:ListAIAgents %s: %w", aid, perr)
			}
			for _, a := range out.AiAgentSummaries {
				arn := sv(a.AiAgentArn)
				if arn == "" {
					continue
				}
				agentID := sv(a.AiAgentId)
				keys = append(keys, wisdomChild{parent: aid, id: agentID})
				label := sv(a.Name)
				if label == "" {
					label = agentID
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomAIAgent, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "wisdom ai-agents")
	return keys, t, i, err
}

func scanWisdomAIAgentVersions(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, keys []wisdomChild) (int, int, error) {
	if len(keys) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, k := range keys {
		aid := k.parent
		ag := k.id
		pager := qconnect.NewListAIAgentVersionsPaginator(client, &qconnect.ListAIAgentVersionsInput{AssistantId: &aid, AiAgentId: &ag})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("qconnect:ListAIAgentVersions %s/%s: %w", k.parent, k.id, perr)
			}
			for _, v := range out.AiAgentVersionSummaries {
				if v.AiAgentSummary == nil {
					continue
				}
				arn := sv(v.AiAgentSummary.AiAgentArn)
				if arn == "" {
					continue
				}
				ver := int64(0)
				if v.VersionNumber != nil {
					ver = *v.VersionNumber
				}
				vlabel := fmt.Sprintf("v%d", ver)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomAIAgentVersion, NativeID: fmt.Sprintf("%s:%d", arn, ver),
					Name: &vlabel, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "wisdom ai-agent-versions")
}

func scanWisdomAIGuardrails(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, assistantIDs []string) ([]wisdomChild, int, int, error) {
	if len(assistantIDs) == 0 {
		return nil, 0, 0, nil
	}
	var keys []wisdomChild
	var batch []*store.Resource
	for _, aid := range assistantIDs {
		id := aid
		pager := qconnect.NewListAIGuardrailsPaginator(client, &qconnect.ListAIGuardrailsInput{AssistantId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return nil, 0, 0, fmt.Errorf("qconnect:ListAIGuardrails %s: %w", aid, perr)
			}
			for _, g := range out.AiGuardrailSummaries {
				arn := sv(g.AiGuardrailArn)
				if arn == "" {
					continue
				}
				gid := sv(g.AiGuardrailId)
				keys = append(keys, wisdomChild{parent: aid, id: gid})
				label := sv(g.Name)
				if label == "" {
					label = gid
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomAIGuardrail, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
				})
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "wisdom ai-guardrails")
	return keys, t, i, err
}

func scanWisdomAIGuardrailVersions(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, keys []wisdomChild) (int, int, error) {
	if len(keys) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, k := range keys {
		aid := k.parent
		gid := k.id
		pager := qconnect.NewListAIGuardrailVersionsPaginator(client, &qconnect.ListAIGuardrailVersionsInput{AssistantId: &aid, AiGuardrailId: &gid})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("qconnect:ListAIGuardrailVersions %s/%s: %w", k.parent, k.id, perr)
			}
			for _, v := range out.AiGuardrailVersionSummaries {
				if v.AiGuardrailSummary == nil {
					continue
				}
				arn := sv(v.AiGuardrailSummary.AiGuardrailArn)
				if arn == "" {
					continue
				}
				ver := int64(0)
				if v.VersionNumber != nil {
					ver = *v.VersionNumber
				}
				vlabel := fmt.Sprintf("v%d", ver)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomAIGuardrailVersion, NativeID: fmt.Sprintf("%s:%d", arn, ver),
					Name: &vlabel, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "wisdom ai-guardrail-versions")
}

func scanWisdomAIPrompts(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, assistantIDs []string) ([]wisdomChild, int, int, error) {
	if len(assistantIDs) == 0 {
		return nil, 0, 0, nil
	}
	var keys []wisdomChild
	var batch []*store.Resource
	for _, aid := range assistantIDs {
		id := aid
		pager := qconnect.NewListAIPromptsPaginator(client, &qconnect.ListAIPromptsInput{AssistantId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return nil, 0, 0, fmt.Errorf("qconnect:ListAIPrompts %s: %w", aid, perr)
			}
			for _, p := range out.AiPromptSummaries {
				arn := sv(p.AiPromptArn)
				if arn == "" {
					continue
				}
				pid := sv(p.AiPromptId)
				keys = append(keys, wisdomChild{parent: aid, id: pid})
				label := sv(p.Name)
				if label == "" {
					label = pid
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomAIPrompt, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "wisdom ai-prompts")
	return keys, t, i, err
}

func scanWisdomAIPromptVersions(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, keys []wisdomChild) (int, int, error) {
	if len(keys) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, k := range keys {
		aid := k.parent
		pid := k.id
		pager := qconnect.NewListAIPromptVersionsPaginator(client, &qconnect.ListAIPromptVersionsInput{AssistantId: &aid, AiPromptId: &pid})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("qconnect:ListAIPromptVersions %s/%s: %w", k.parent, k.id, perr)
			}
			for _, v := range out.AiPromptVersionSummaries {
				if v.AiPromptSummary == nil {
					continue
				}
				arn := sv(v.AiPromptSummary.AiPromptArn)
				if arn == "" {
					continue
				}
				ver := int64(0)
				if v.VersionNumber != nil {
					ver = *v.VersionNumber
				}
				vlabel := fmt.Sprintf("v%d", ver)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomAIPromptVersion, NativeID: fmt.Sprintf("%s:%d", arn, ver),
					Name: &vlabel, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "wisdom ai-prompt-versions")
}

func scanWisdomMessageTemplates(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, kbIDs []string) ([]wisdomChild, int, int, error) {
	if len(kbIDs) == 0 {
		return nil, 0, 0, nil
	}
	var keys []wisdomChild
	var batch []*store.Resource
	for _, kbID := range kbIDs {
		id := kbID
		pager := qconnect.NewListMessageTemplatesPaginator(client, &qconnect.ListMessageTemplatesInput{KnowledgeBaseId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return nil, 0, 0, fmt.Errorf("qconnect:ListMessageTemplates %s: %w", kbID, perr)
			}
			for _, m := range out.MessageTemplateSummaries {
				arn := sv(m.MessageTemplateArn)
				if arn == "" {
					continue
				}
				mid := sv(m.MessageTemplateId)
				keys = append(keys, wisdomChild{parent: kbID, id: mid})
				label := sv(m.Name)
				if label == "" {
					label = mid
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomMessageTemplate, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
				})
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "wisdom message-templates")
	return keys, t, i, err
}

func scanWisdomMessageTemplateVersions(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, keys []wisdomChild) (int, int, error) {
	if len(keys) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, k := range keys {
		kbid := k.parent
		mid := k.id
		pager := qconnect.NewListMessageTemplateVersionsPaginator(client, &qconnect.ListMessageTemplateVersionsInput{KnowledgeBaseId: &kbid, MessageTemplateId: &mid})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("qconnect:ListMessageTemplateVersions %s/%s: %w", k.parent, k.id, perr)
			}
			for _, v := range out.MessageTemplateVersionSummaries {
				arn := sv(v.MessageTemplateArn)
				if arn == "" {
					continue
				}
				ver := int64(0)
				if v.VersionNumber != nil {
					ver = *v.VersionNumber
				}
				vlabel := sv(v.Name)
				if vlabel == "" {
					vlabel = fmt.Sprintf("v%d", ver)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomMessageTemplateVersion, NativeID: fmt.Sprintf("%s:%d", arn, ver),
					Name: &vlabel, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "wisdom message-template-versions")
}

func scanWisdomQuickResponses(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, kbIDs []string) (int, int, error) {
	if len(kbIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, kbID := range kbIDs {
		id := kbID
		pager := qconnect.NewListQuickResponsesPaginator(client, &qconnect.ListQuickResponsesInput{KnowledgeBaseId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("qconnect:ListQuickResponses %s: %w", kbID, perr)
			}
			for _, q := range out.QuickResponseSummaries {
				arn := sv(q.QuickResponseArn)
				if arn == "" {
					continue
				}
				label := sv(q.Name)
				if label == "" {
					label = arn
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomQuickResponse, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "wisdom quick-responses")
}

// scanWisdomContents fans out ListContents per knowledge base and returns
// (knowledgeBaseId, contentId) keys for the content-association fan-out.
func scanWisdomContents(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, kbIDs []string) ([]wisdomChild, int, int, error) {
	if len(kbIDs) == 0 {
		return nil, 0, 0, nil
	}
	var keys []wisdomChild
	var batch []*store.Resource
	for _, kbID := range kbIDs {
		id := kbID
		pager := qconnect.NewListContentsPaginator(client, &qconnect.ListContentsInput{KnowledgeBaseId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return nil, 0, 0, fmt.Errorf("qconnect:ListContents %s: %w", kbID, perr)
			}
			for _, c := range out.ContentSummaries {
				arn := sv(c.ContentArn)
				if arn == "" {
					continue
				}
				cid := sv(c.ContentId)
				keys = append(keys, wisdomChild{parent: kbID, id: cid})
				label := sv(c.Name)
				if label == "" {
					label = cid
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomContent, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "wisdom contents")
	return keys, t, i, err
}

func scanWisdomContentAssociations(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, keys []wisdomChild) (int, int, error) {
	if len(keys) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, k := range keys {
		kbID := k.parent
		cID := k.id
		pager := qconnect.NewListContentAssociationsPaginator(client, &qconnect.ListContentAssociationsInput{KnowledgeBaseId: &kbID, ContentId: &cID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("qconnect:ListContentAssociations %s/%s: %w", k.parent, k.id, perr)
			}
			for _, a := range out.ContentAssociationSummaries {
				arn := sv(a.ContentAssociationArn)
				if arn == "" {
					continue
				}
				label := sv(a.ContentAssociationId)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWisdomContentAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "wisdom content-associations")
}
