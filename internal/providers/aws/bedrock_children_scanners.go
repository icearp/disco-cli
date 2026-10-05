package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	"github.com/icearp/disco-cli/store"
)

// bedrockDraftVersion is the agent version the per-agent child scanners read.
// DRAFT is an agent's only mutable version: action groups, knowledge-base
// associations and collaborators are created and edited there, and every
// numbered version is an immutable snapshot of DRAFT taken when the agent was
// versioned. Reading every version would multiply these calls by the version
// count to re-read snapshots; the cost is that a numbered version whose
// children have since diverged from DRAFT is not represented.
const bedrockDraftVersion = "DRAFT"

// isBedrockChildSkip reports a per-parent error the child fan-outs drop
// silently: a parent deleted between the parent list and the child call, and the
// feature-gate and SCP denials scanBedrockARPolicies treats as silent.
func isBedrockChildSkip(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException") ||
		isAccessDeniedWithMessage(err, "not authorized to invoke this API operation") ||
		isClosedToNewCustomers(err) ||
		isSCPExplicitDeny(err)
}

func bedrockAgentParents(region, acctID string, agentIDs []string) []childParent {
	parents := make([]childParent, len(agentIDs))
	for i, id := range agentIDs {
		parents[i] = childParent{id: id, arn: bedrockAgentARN(region, acctID, id)}
	}
	return parents
}

func bedrockChildResource(acct *account, region, scanID, typ, nativeID, name, status string, elem any) *store.Resource {
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID, Region: &region,
		AttributesJSON: mustJSON(elem), DiscoveredBy: scanID,
	}
	if name != "" {
		r.Name = &name
	}
	if status != "" {
		r.Status = &status
	}
	return r
}

// scanBedrockAgentVersions skips DRAFT: it is the agent's working copy, which
// the agent row itself already represents (same convention as the guardrail,
// prompt and automated-reasoning-policy version scanners).
func scanBedrockAgentVersions(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, agentIDs []string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "bedrockagent:ListAgentVersions", bedrockAgentParents(region, acct.ID, agentIDs), isBedrockChildSkip,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := bedrockagent.NewListAgentVersionsPaginator(client, &bedrockagent.ListAgentVersionsInput{AgentId: &p.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, v := range out.AgentVersionSummaries {
					ver := sv(v.AgentVersion)
					if ver == "" || ver == bedrockDraftVersion {
						continue
					}
					rows = append(rows, bedrockChildResource(acct, region, scanID, TypeBedrockAgentVersion,
						p.arn+"/version/"+ver, ver, string(v.AgentStatus), v))
				}
			}
			return rows, nil
		})
}

func scanBedrockAgentActionGroups(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, agentIDs []string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "bedrockagent:ListAgentActionGroups", bedrockAgentParents(region, acct.ID, agentIDs), isBedrockChildSkip,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			ver := bedrockDraftVersion
			pager := bedrockagent.NewListAgentActionGroupsPaginator(client, &bedrockagent.ListAgentActionGroupsInput{AgentId: &p.id, AgentVersion: &ver})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, a := range out.ActionGroupSummaries {
					id := sv(a.ActionGroupId)
					if id == "" {
						continue
					}
					rows = append(rows, bedrockChildResource(acct, region, scanID, TypeBedrockAgentActionGroup,
						p.arn+"/action-group/"+id, sv(a.ActionGroupName), string(a.ActionGroupState), a))
				}
			}
			return rows, nil
		})
}

func scanBedrockAgentKnowledgeBases(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, agentIDs []string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "bedrockagent:ListAgentKnowledgeBases", bedrockAgentParents(region, acct.ID, agentIDs), isBedrockChildSkip,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			ver := bedrockDraftVersion
			pager := bedrockagent.NewListAgentKnowledgeBasesPaginator(client, &bedrockagent.ListAgentKnowledgeBasesInput{AgentId: &p.id, AgentVersion: &ver})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, k := range out.AgentKnowledgeBaseSummaries {
					id := sv(k.KnowledgeBaseId)
					if id == "" {
						continue
					}
					rows = append(rows, bedrockChildResource(acct, region, scanID, TypeBedrockAgentKnowledgeBase,
						p.arn+"/knowledge-base/"+id, id, string(k.KnowledgeBaseState), k))
				}
			}
			return rows, nil
		})
}

func scanBedrockAgentCollaborators(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, agentIDs []string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "bedrockagent:ListAgentCollaborators", bedrockAgentParents(region, acct.ID, agentIDs), isBedrockChildSkip,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			ver := bedrockDraftVersion
			pager := bedrockagent.NewListAgentCollaboratorsPaginator(client, &bedrockagent.ListAgentCollaboratorsInput{AgentId: &p.id, AgentVersion: &ver})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, c := range out.AgentCollaboratorSummaries {
					id := sv(c.CollaboratorId)
					if id == "" {
						continue
					}
					rows = append(rows, bedrockChildResource(acct, region, scanID, TypeBedrockAgentCollaborator,
						p.arn+"/collaborator/"+id, sv(c.CollaboratorName), "", c))
				}
			}
			return rows, nil
		})
}

func scanBedrockARPolicyTestCases(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, policyArns []string) (int, int, error) {
	parents := make([]childParent, len(policyArns))
	for i, arn := range policyArns {
		parents[i] = childParent{id: arn, arn: arn}
	}
	return childFanOut(ctx, st, acct, region, "bedrock:ListAutomatedReasoningPolicyTestCases", parents, isBedrockChildSkip,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := bedrock.NewListAutomatedReasoningPolicyTestCasesPaginator(client, &bedrock.ListAutomatedReasoningPolicyTestCasesInput{PolicyArn: &p.arn})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, tc := range out.TestCases {
					id := sv(tc.TestCaseId)
					if id == "" {
						continue
					}
					rows = append(rows, bedrockChildResource(acct, region, scanID, TypeBedrockAutomatedReasoningPolicyTestCase,
						p.arn+"/test-case/"+id, id, "", tc))
				}
			}
			return rows, nil
		})
}
