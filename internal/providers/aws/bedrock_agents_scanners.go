package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
)

// scanBedrockAgents covers Agent/AgentAlias, KnowledgeBase/DataSource,
// Flow/FlowAlias/FlowVersion, Prompt/PromptVersion. ARNs synthesized
// where SDK summaries return only IDs.
func scanBedrockAgents(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	agentIDs, t, i, ferr := scanBedrockAgentList(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanBedrockAgentAliases(ctx, client, acct, region, st, scanID, agentIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	kbIDs, t, i, ferr := scanBedrockKnowledgeBases(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanBedrockDataSources(ctx, client, acct, region, st, scanID, kbIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	flowIDs, t, i, ferr := scanBedrockFlows(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanBedrockFlowAliases(ctx, client, acct, region, st, scanID, flowIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanBedrockFlowVersions(ctx, client, acct, region, st, scanID, flowIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	promptIDs, t, i, ferr := scanBedrockPrompts(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanBedrockPromptVersions(ctx, client, acct, region, st, scanID, promptIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	return total, inserted, nil
}

func bedrockAgentARN(region, acct, agentID string) string {
	return fmt.Sprintf("arn:aws:bedrock:%s:%s:agent/%s", region, acct, agentID)
}

func bedrockAgentAliasARN(region, acct, agentID, aliasID string) string {
	return fmt.Sprintf("arn:aws:bedrock:%s:%s:agent-alias/%s/%s", region, acct, agentID, aliasID)
}

func bedrockKBARN(region, acct, kbID string) string {
	return fmt.Sprintf("arn:aws:bedrock:%s:%s:knowledge-base/%s", region, acct, kbID)
}

func bedrockDataSourceARN(region, acct, kbID, dsID string) string {
	return fmt.Sprintf("arn:aws:bedrock:%s:%s:knowledge-base/%s/data-source/%s", region, acct, kbID, dsID)
}

func scanBedrockAgentList(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bedrockagent.NewListAgentsPaginator(client, &bedrockagent.ListAgentsInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isSCPExplicitDeny(perr) {
				return nil, 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagent:ListAgents", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("bedrockagent:ListAgents: %w", perr)
		}
		for _, a := range out.AgentSummaries {
			id := sv(a.AgentId)
			if id == "" {
				continue
			}
			ids = append(ids, id)
			label := sv(a.AgentName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgent, NativeID: bedrockAgentARN(region, acct.ID, id),
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	if len(batch) == 0 {
		return ids, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return ids, 0, 0, fmt.Errorf("upsert bedrock agents: %w", uerr)
	}
	return ids, len(batch), n, nil
}

func scanBedrockAgentAliases(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, agentIDs []string) (int, int, error) {
	if len(agentIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, agentID := range agentIDs {
		aid := agentID
		pager := bedrockagent.NewListAgentAliasesPaginator(client, &bedrockagent.ListAgentAliasesInput{AgentId: &aid})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrockagent:ListAgentAliases %s: %w", agentID, perr)
			}
			for _, al := range out.AgentAliasSummaries {
				id := sv(al.AgentAliasId)
				if id == "" {
					continue
				}
				label := sv(al.AgentAliasName)
				if label == "" {
					label = id
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockAgentAlias, NativeID: bedrockAgentAliasARN(region, acct.ID, agentID, id),
					Name: &label, Region: &region, AttributesJSON: mustJSON(al), DiscoveredBy: scanID,
				})
			}
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert bedrock agent-aliases: %w", uerr)
	}
	return len(batch), n, nil
}

func scanBedrockKnowledgeBases(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bedrockagent.NewListKnowledgeBasesPaginator(client, &bedrockagent.ListKnowledgeBasesInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isSCPExplicitDeny(perr) {
				return nil, 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagent:ListKnowledgeBases", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("bedrockagent:ListKnowledgeBases: %w", perr)
		}
		for _, k := range out.KnowledgeBaseSummaries {
			id := sv(k.KnowledgeBaseId)
			if id == "" {
				continue
			}
			ids = append(ids, id)
			label := sv(k.Name)
			if label == "" {
				label = id
			}
			// Enrich via GetKnowledgeBase — adds RoleArn,
			// KnowledgeBaseConfiguration variant (vector/SQL/Kendra),
			// StorageConfiguration backend refs (S3/RDS/OSS/Pinecone/etc).
			detail, derr := client.GetKnowledgeBase(ctx, &bedrockagent.GetKnowledgeBaseInput{KnowledgeBaseId: &id})
			attrs := mustJSON(k)
			if derr == nil && detail != nil && detail.KnowledgeBase != nil {
				attrs = mustJSON(detail.KnowledgeBase)
			} else if derr != nil && isAccessDenied(derr) {
				_ = skipIfAccessDenied(st, "bedrockagent:GetKnowledgeBase", acct.ID, region, derr)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockKnowledgeBase, NativeID: bedrockKBARN(region, acct.ID, id),
				Name: &label, Region: &region, AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
	}
	if len(batch) == 0 {
		return ids, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return ids, 0, 0, fmt.Errorf("upsert bedrock knowledge-bases: %w", uerr)
	}
	return ids, len(batch), n, nil
}

func scanBedrockDataSources(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, kbIDs []string) (int, int, error) {
	if len(kbIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, kbID := range kbIDs {
		kid := kbID
		pager := bedrockagent.NewListDataSourcesPaginator(client, &bedrockagent.ListDataSourcesInput{KnowledgeBaseId: &kid})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrockagent:ListDataSources %s: %w", kbID, perr)
			}
			for _, d := range out.DataSourceSummaries {
				id := sv(d.DataSourceId)
				if id == "" {
					continue
				}
				label := sv(d.Name)
				if label == "" {
					label = id
				}
				// Enrich via GetDataSource — adds DataSourceConfiguration
				// (S3/Confluence/Salesforce/SharePoint/Web variants) +
				// ServerSideEncryptionConfiguration.KmsKeyArn.
				detail, derr := client.GetDataSource(ctx, &bedrockagent.GetDataSourceInput{
					KnowledgeBaseId: &kid, DataSourceId: &id,
				})
				attrs := mustJSON(d)
				if derr == nil && detail != nil && detail.DataSource != nil {
					attrs = mustJSON(detail.DataSource)
				} else if derr != nil && isAccessDenied(derr) {
					_ = skipIfAccessDenied(st, "bedrockagent:GetDataSource", acct.ID, region, derr)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockDataSource, NativeID: bedrockDataSourceARN(region, acct.ID, kbID, id),
					Name: &label, Region: &region, AttributesJSON: attrs, DiscoveredBy: scanID,
				})
			}
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert bedrock data-sources: %w", uerr)
	}
	return len(batch), n, nil
}

func scanBedrockFlows(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bedrockagent.NewListFlowsPaginator(client, &bedrockagent.ListFlowsInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		// Clamp retries: ListFlows returns persistent InternalServerError where
		// Flows isn't deployed; global 10-attempt budget burns ~2min before
		// surfacing. Mirrors the quicksight pattern.
		out, perr := pager.NextPage(ctx, func(o *bedrockagent.Options) {
			o.RetryMaxAttempts = 2
		})
		if perr != nil {
			if isAPIErrorCode(perr, "InternalServerErrorException", "InternalServerException") {
				return nil, 0, 0, nil
			}
			if isSCPExplicitDeny(perr) {
				return nil, 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagent:ListFlows", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("bedrockagent:ListFlows: %w", perr)
		}
		for _, f := range out.FlowSummaries {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			ids = append(ids, sv(f.Id))
			label := sv(f.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockFlow, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	if len(batch) == 0 {
		return ids, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return ids, 0, 0, fmt.Errorf("upsert bedrock flows: %w", uerr)
	}
	return ids, len(batch), n, nil
}

func scanBedrockFlowAliases(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, flowIDs []string) (int, int, error) {
	if len(flowIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, flowID := range flowIDs {
		fid := flowID
		pager := bedrockagent.NewListFlowAliasesPaginator(client, &bedrockagent.ListFlowAliasesInput{FlowIdentifier: &fid})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrockagent:ListFlowAliases %s: %w", flowID, perr)
			}
			for _, a := range out.FlowAliasSummaries {
				arn := sv(a.Arn)
				if arn == "" {
					continue
				}
				label := sv(a.Name)
				if label == "" {
					label = arn
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockFlowAlias, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert bedrock flow-aliases: %w", uerr)
	}
	return len(batch), n, nil
}

func scanBedrockFlowVersions(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, flowIDs []string) (int, int, error) {
	if len(flowIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, flowID := range flowIDs {
		fid := flowID
		pager := bedrockagent.NewListFlowVersionsPaginator(client, &bedrockagent.ListFlowVersionsInput{FlowIdentifier: &fid})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrockagent:ListFlowVersions %s: %w", flowID, perr)
			}
			for _, v := range out.FlowVersionSummaries {
				arn := sv(v.Arn)
				if arn == "" {
					continue
				}
				ver := sv(v.Version)
				label := ver
				if label == "" {
					label = arn
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockFlowVersion, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert bedrock flow-versions: %w", uerr)
	}
	return len(batch), n, nil
}

func scanBedrockPrompts(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bedrockagent.NewListPromptsPaginator(client, &bedrockagent.ListPromptsInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		// Same persistent-5xx shape as ListFlows in regions where Bedrock
		// Agents prompts isn't deployed. Clamp + soft-skip.
		out, perr := pager.NextPage(ctx, func(o *bedrockagent.Options) {
			o.RetryMaxAttempts = 2
		})
		if perr != nil {
			if isAPIErrorCode(perr, "InternalServerErrorException", "InternalServerException") {
				return nil, 0, 0, nil
			}
			if isSCPExplicitDeny(perr) {
				return nil, 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagent:ListPrompts", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("bedrockagent:ListPrompts: %w", perr)
		}
		for _, p := range out.PromptSummaries {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			ids = append(ids, sv(p.Id))
			label := sv(p.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockPrompt, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	if len(batch) == 0 {
		return ids, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return ids, 0, 0, fmt.Errorf("upsert bedrock prompts: %w", uerr)
	}
	return ids, len(batch), n, nil
}

// scanBedrockPromptVersions calls ListPrompts again per-promptID — when
// PromptIdentifier is set, the response returns versions of that prompt.
func scanBedrockPromptVersions(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, promptIDs []string) (int, int, error) {
	if len(promptIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, pid := range promptIDs {
		id := pid
		pager := bedrockagent.NewListPromptsPaginator(client, &bedrockagent.ListPromptsInput{PromptIdentifier: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrockagent:ListPrompts(versions) %s: %w", pid, perr)
			}
			for _, p := range out.PromptSummaries {
				arn := sv(p.Arn)
				if arn == "" {
					continue
				}
				ver := sv(p.Version)
				if ver == "" || ver == "DRAFT" {
					continue
				}
				vlabel := ver
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockPromptVersion, NativeID: arn + ":" + ver,
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
		return 0, 0, fmt.Errorf("upsert bedrock prompt-versions: %w", uerr)
	}
	return len(batch), n, nil
}
