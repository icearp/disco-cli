package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	agenttypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
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

// isBedrockChildSkip reports a per-parent denial the child listings drop
// silently: the feature-gate and SCP denials scanBedrockARPolicies treats as
// silent. A parent deleted since it was listed is the
// ResourceNotFoundException every child op models, which the
// child scanners pass storeChildrenEachParent as its not-found code.
func isBedrockChildSkip(_ string, err error) bool {
	return isAccessDeniedWithMessage(err, "not authorized to invoke this API operation") ||
		isClosedToNewCustomers(err) ||
		isSCPExplicitDeny(err)
}

// bedrockChildNotFound is the code every child op models for a parent deleted
// since it was listed.
var bedrockChildNotFound = []string{"ResourceNotFoundException"}

func bedrockAgentParents(region, acctID string, agentIDs []string) []childParent {
	parents := make([]childParent, len(agentIDs))
	for i, id := range agentIDs {
		parents[i] = childParent{id: id, arn: bedrockAgentARN(region, acctID, id)}
	}
	return parents
}

// bedrockChildRow is the row of a child stored under {parentARN}/{kind}/{id}.
// An empty id gives no row; the reason names the items as what.
func bedrockChildRow(acct *account, region, scanID, typ string, p childParent, kind, id, name, status, what string, elem any) (*store.Resource, string, string) {
	if id == "" {
		return nil, what + " have an empty id", cmp.Or(name, "(one with no name)") + " of " + p.id
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: p.arn + "/" + kind + "/" + id, Region: &region,
		AttributesJSON: mustJSON(elem), DiscoveredBy: scanID,
		Name: nonEmptyPtr(name), Status: nonEmptyPtr(status),
	}, "", ""
}

// scanBedrockAgentVersions skips DRAFT: it is the agent's working copy, which
// the agent row itself already represents (same convention as the guardrail,
// prompt and automated-reasoning-policy version scanners).
func scanBedrockAgentVersions(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, agentIDs []string) (int, int, error) {
	return storeChildrenEachParent(ctx, st, acct, region, "bedrockagent:ListAgentVersions", bedrockChildNotFound, bedrockAgentParents(region, acct.ID, agentIDs), isBedrockChildSkip, nil,
		func(ctx context.Context, p childParent, token *string) ([]agenttypes.AgentVersionSummary, *string, error) {
			out, err := client.ListAgentVersions(ctx, &bedrockagent.ListAgentVersionsInput{AgentId: &p.id, NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return withoutDraft(out.AgentVersionSummaries, func(v agenttypes.AgentVersionSummary) *string { return v.AgentVersion }), out.NextToken, nil
		},
		func(p childParent, v agenttypes.AgentVersionSummary) (*store.Resource, string, string) {
			ver := sv(v.AgentVersion)
			return bedrockChildRow(acct, region, scanID, TypeBedrockAgentVersion, p, "version", ver, ver, string(v.AgentStatus), "agent versions", v)
		})
}

func scanBedrockAgentActionGroups(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, agentIDs []string) (int, int, error) {
	return storeChildrenEachParent(ctx, st, acct, region, "bedrockagent:ListAgentActionGroups", bedrockChildNotFound, bedrockAgentParents(region, acct.ID, agentIDs), isBedrockChildSkip, nil,
		func(ctx context.Context, p childParent, token *string) ([]agenttypes.ActionGroupSummary, *string, error) {
			out, err := client.ListAgentActionGroups(ctx, &bedrockagent.ListAgentActionGroupsInput{
				AgentId: &p.id, AgentVersion: sdkaws.String(bedrockDraftVersion), NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ActionGroupSummaries, out.NextToken, nil
		},
		func(p childParent, a agenttypes.ActionGroupSummary) (*store.Resource, string, string) {
			return bedrockChildRow(acct, region, scanID, TypeBedrockAgentActionGroup, p, "action-group",
				sv(a.ActionGroupId), sv(a.ActionGroupName), string(a.ActionGroupState), "action groups", a)
		})
}

func scanBedrockAgentKnowledgeBases(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, agentIDs []string) (int, int, error) {
	return storeChildrenEachParent(ctx, st, acct, region, "bedrockagent:ListAgentKnowledgeBases", bedrockChildNotFound, bedrockAgentParents(region, acct.ID, agentIDs), isBedrockChildSkip, nil,
		func(ctx context.Context, p childParent, token *string) ([]agenttypes.AgentKnowledgeBaseSummary, *string, error) {
			out, err := client.ListAgentKnowledgeBases(ctx, &bedrockagent.ListAgentKnowledgeBasesInput{
				AgentId: &p.id, AgentVersion: sdkaws.String(bedrockDraftVersion), NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.AgentKnowledgeBaseSummaries, out.NextToken, nil
		},
		func(p childParent, k agenttypes.AgentKnowledgeBaseSummary) (*store.Resource, string, string) {
			id := sv(k.KnowledgeBaseId)
			return bedrockChildRow(acct, region, scanID, TypeBedrockAgentKnowledgeBase, p, "knowledge-base",
				id, id, string(k.KnowledgeBaseState), "agent knowledge bases", k)
		})
}

func scanBedrockAgentCollaborators(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, agentIDs []string) (int, int, error) {
	return storeChildrenEachParent(ctx, st, acct, region, "bedrockagent:ListAgentCollaborators", bedrockChildNotFound, bedrockAgentParents(region, acct.ID, agentIDs), isBedrockChildSkip, nil,
		func(ctx context.Context, p childParent, token *string) ([]agenttypes.AgentCollaboratorSummary, *string, error) {
			out, err := client.ListAgentCollaborators(ctx, &bedrockagent.ListAgentCollaboratorsInput{
				AgentId: &p.id, AgentVersion: sdkaws.String(bedrockDraftVersion), NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.AgentCollaboratorSummaries, out.NextToken, nil
		},
		func(p childParent, c agenttypes.AgentCollaboratorSummary) (*store.Resource, string, string) {
			return bedrockChildRow(acct, region, scanID, TypeBedrockAgentCollaborator, p, "collaborator",
				sv(c.CollaboratorId), sv(c.CollaboratorName), "", "agent collaborators", c)
		})
}

func scanBedrockARPolicyTestCases(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, policyArns []string) (int, int, error) {
	parents := make([]childParent, len(policyArns))
	for i, arn := range policyArns {
		parents[i] = childParent{id: arn, arn: arn}
	}
	return storeChildrenEachParent(ctx, st, acct, region, "bedrock:ListAutomatedReasoningPolicyTestCases", bedrockChildNotFound, parents, isBedrockChildSkip, nil,
		func(ctx context.Context, p childParent, token *string) ([]bedrocktypes.AutomatedReasoningPolicyTestCase, *string, error) {
			out, err := client.ListAutomatedReasoningPolicyTestCases(ctx, &bedrock.ListAutomatedReasoningPolicyTestCasesInput{
				PolicyArn: &p.arn, NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.TestCases, out.NextToken, nil
		},
		func(p childParent, tc bedrocktypes.AutomatedReasoningPolicyTestCase) (*store.Resource, string, string) {
			id := sv(tc.TestCaseId)
			return bedrockChildRow(acct, region, scanID, TypeBedrockAutomatedReasoningPolicyTestCase, p, "test-case",
				id, id, "", "automated reasoning policy test cases", tc)
		})
}
