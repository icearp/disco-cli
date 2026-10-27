package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	agenttypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	"github.com/icearp/disco-cli/store"
)

// scanBedrockAgentList stores the region's agents with their tags and returns
// the stored ones, the parents of the alias and per-agent child phases. An
// agent's list element carries no ARN; its NativeID is built from AgentId in
// the Service Reference's agent ARN format, and the tag read is sent it. A
// first page refused by an SCP's explicit deny ends the listing silently.
func scanBedrockAgentList(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (agents []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "bedrockagent:ListAgents", "agents", "AgentId",
		bacPages(isSCPExplicitDeny, func(ctx context.Context, token *string) ([]agenttypes.AgentSummary, *string, error) {
			out, err := client.ListAgents(ctx, &bedrockagent.ListAgentsInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AgentSummaries, out.NextToken, nil
		}),
		func(a agenttypes.AgentSummary) (*store.Resource, string, string) {
			id := sv(a.AgentId)
			if id == "" {
				return nil, "agents have an empty AgentId", cmp.Or(sv(a.AgentName), "(an agent with no name)")
			}
			return bacRowAt(acct, region, scanID, TypeBedrockAgent, bedrockAgentARN(acct.partition(), region, acct.ID, id), cmp.Or(sv(a.AgentName), id), a), "", ""
		},
		func(a agenttypes.AgentSummary) string { return sv(a.AgentId) },
		tags, p)
}

// scanBedrockAgentAliases lists the aliases of each agent the agent phase
// stored and stores them with their tags (storeEachParentTags). An
// alias's list element carries no ARN; its NativeID is built from the agent
// and alias ids in the Service Reference's agent-alias ARN format. An agent
// ResourceNotFoundException says was deleted since it was listed is skipped.
func scanBedrockAgentAliases(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, agents []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, failed := storeEachParentTags(ctx, st, acct, region, "bedrockagent:ListAgentAliases", []string{"ResourceNotFoundException"}, agents,
		func(ctx context.Context, agent string, token *string) ([]agenttypes.AgentAliasSummary, *string, error) {
			out, err := client.ListAgentAliases(ctx, &bedrockagent.ListAgentAliasesInput{AgentId: &agent, NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AgentAliasSummaries, out.NextToken, nil
		},
		func(agent string, al agenttypes.AgentAliasSummary) (*store.Resource, string, string) {
			id := sv(al.AgentAliasId)
			if id == "" {
				return nil, "agent aliases have an empty AgentAliasId", cmp.Or(sv(al.AgentAliasName), "(an alias with no name)") + " of agent " + agent
			}
			return bacRowAt(acct, region, scanID, TypeBedrockAgentAlias, bedrockAgentAliasARN(acct.partition(), region, acct.ID, agent, id), cmp.Or(sv(al.AgentAliasName), id), al), "", ""
		}, tags, p)
	return failed
}

const (
	bedrockGetKBOp = "bedrockagent:GetKnowledgeBase"
	bedrockGetDSOp = "bedrockagent:GetDataSource"
)

// scanBedrockKnowledgeBases stores the region's knowledge bases with their
// tags and returns the stored ones, the parents of the data-source phase.
// Each row is the knowledge base's GetKnowledgeBase body, which adds the
// role, embeddings and storage configuration to the list element
// (readRowDetails); its NativeID is built from KnowledgeBaseId in the
// Service Reference's knowledge-base ARN format, the form of the body's
// KnowledgeBaseArn. The tag read is sent the body's KnowledgeBaseArn, which
// names the partition the NativeID cannot (bedrockAgentARN), or the NativeID
// for a row stored as its list element, which carries no ARN. A first page
// refused by an SCP's explicit deny ends the listing silently.
func scanBedrockKnowledgeBases(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (kbs []childParent, storeWriteFailed bool) {
	const op = "bedrockagent:ListKnowledgeBases"
	var (
		ids      = map[*store.Resource]string{}
		bodyARNs = map[*store.Resource]*string{}
	)
	d := &detailedRows{
		getOp: bedrockGetKBOp, what: "knowledge base(s)", notFound: bedrockGetNotFound,
		tags: bedrockAgentTagsAt(client, func(r *store.Resource) string { return cmp.Or(*bodyARNs[r], r.NativeID) }),
	}
	total, inserted, kbs, err := storeParentListWith(ctx, st, acct, region, op, "knowledge bases", "KnowledgeBaseId",
		bacPages(isSCPExplicitDeny, func(ctx context.Context, token *string) ([]agenttypes.KnowledgeBaseSummary, *string, error) {
			out, err := client.ListKnowledgeBases(ctx, &bedrockagent.ListKnowledgeBasesInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.KnowledgeBaseSummaries, out.NextToken, nil
		}),
		func(k agenttypes.KnowledgeBaseSummary) (*store.Resource, string, string) {
			id := sv(k.KnowledgeBaseId)
			if id == "" {
				return nil, "knowledge bases have an empty KnowledgeBaseId", cmp.Or(sv(k.Name), "(a knowledge base with no name)")
			}
			r := bacRowAt(acct, region, scanID, TypeBedrockKnowledgeBase, bedrockKBARN(acct.partition(), region, acct.ID, id), cmp.Or(sv(k.Name), id), k)
			ids[r] = id
			// Filled by the concurrent Gets, each through its own pointer, so
			// the map is only read while they run.
			bodyARNs[r] = new(string)
			return r, "", ""
		},
		func(k agenttypes.KnowledgeBaseSummary) string { return sv(k.KnowledgeBaseId) },
		d.before(ctx, st, acct, region, func(ctx context.Context, r *store.Resource) (any, error) {
			out, err := client.GetKnowledgeBase(ctx, &bedrockagent.GetKnowledgeBaseInput{KnowledgeBaseId: sdkaws.String(ids[r])})
			if err != nil || out.KnowledgeBase == nil {
				return nil, err
			}
			*bodyARNs[r] = sv(out.KnowledgeBase.KnowledgeBaseArn)
			return out.KnowledgeBase, nil
		}))
	return kbs, d.addPhase(p, op, total, inserted, err)
}

// bedrockGetNotFound is the only not-found code GetKnowledgeBase and
// GetDataSource model.
var bedrockGetNotFound = []string{"ResourceNotFoundException"}

// bedrockNotDeployed wraps a ListFlows or ListPrompts page. A first page that
// fails with a persistent server error can mean the feature is not deployed
// in this region, which bedrockNotDeployedRetries keeps from burning the
// retry budget: InternalServerException, which the ops model, or
// InternalServerErrorException, which they do not; that one is an observed
// gateway code, an exception to the rule that a skip names only modeled codes
// (#14). The listing then ends with nothing listed, and the error is kept in
// *seen for warnBedrockNotDeployed: the same answer is also a real outage.
func bedrockNotDeployed[Item any](seen *error, page bacPageFunc[Item]) bacPageFunc[Item] {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, token)
		if token == nil && isAPIErrorCode(err, "InternalServerErrorException", "InternalServerException") {
			*seen = err
			return nil, nil, nil
		}
		return items, next, err
	}
}

// warnBedrockNotDeployed warns once under op about the first-page server
// error bedrockNotDeployed kept, if any.
func warnBedrockNotDeployed(st *store.Store, acct *account, region, op string, err error) {
	if err == nil {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("nothing was listed: the first page failed with a server error, which can mean the feature is not deployed in this region: %v", err),
	})
}

// bedrockNotDeployedRetries clamps the retries of ListFlows and ListPrompts:
// where the feature is not deployed they answer with a persistent 5xx, and
// the global 10-attempt budget would burn about two minutes before the call
// returns. Mirrors the quicksight pattern.
func bedrockNotDeployedRetries(o *bedrockagent.Options) { o.RetryMaxAttempts = 2 }

// scanBedrockFlows stores the region's flows with their tags and returns the
// stored ones that carry an Id, the parents of the flow-alias and
// flow-version phases. A first page refused by an SCP's explicit deny ends
// the listing silently; one failing with a server error ends it with a
// warning (bedrockNotDeployed).
func scanBedrockFlows(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (flows []childParent, storeWriteFailed bool) {
	const op = "bedrockagent:ListFlows"
	var notDeployed error
	flows, failed := storeParentListTags(ctx, st, acct, region, op, "flows", "Id",
		bacPages(isSCPExplicitDeny, bedrockNotDeployed(&notDeployed, func(ctx context.Context, token *string) ([]agenttypes.FlowSummary, *string, error) {
			out, err := client.ListFlows(ctx, &bedrockagent.ListFlowsInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)}, bedrockNotDeployedRetries)
			if err != nil {
				return nil, nil, err
			}
			return out.FlowSummaries, out.NextToken, nil
		})),
		func(f agenttypes.FlowSummary) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockFlow, "flows", f.Arn, cmp.Or(sv(f.Name), sv(f.Arn)), sv(f.Id), f)
		},
		func(f agenttypes.FlowSummary) string { return sv(f.Id) },
		tags, p)
	if !failed {
		warnBedrockNotDeployed(st, acct, region, op, notDeployed)
	}
	return flows, failed
}

// scanBedrockFlowAliases lists the aliases of each flow the flow phase stored
// and stores them with their tags (storeEachParentTags). A flow
// ResourceNotFoundException says was deleted since it was listed is skipped.
func scanBedrockFlowAliases(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, flows []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, failed := storeEachParentTags(ctx, st, acct, region, "bedrockagent:ListFlowAliases", []string{"ResourceNotFoundException"}, flows,
		func(ctx context.Context, flow string, token *string) ([]agenttypes.FlowAliasSummary, *string, error) {
			out, err := client.ListFlowAliases(ctx, &bedrockagent.ListFlowAliasesInput{FlowIdentifier: &flow, NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.FlowAliasSummaries, out.NextToken, nil
		},
		func(flow string, a agenttypes.FlowAliasSummary) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockFlowAlias, "flow aliases", a.Arn, cmp.Or(sv(a.Name), sv(a.Arn)), cmp.Or(sv(a.Id), "(an alias with no id)")+" of flow "+flow, a)
		}, tags, p)
	return failed
}

// scanBedrockFlowVersions lists the versions of each flow the flow phase
// stored, concurrently (storeTokenEachParent). A version's element carries
// the flow's ARN ("the flow that the version belongs to"; FlowArn's pattern
// admits no version), so its NativeID is that ARN and the version. A flow
// ResourceNotFoundException says was deleted since it was listed is skipped;
// a denial is warned about.
func scanBedrockFlowVersions(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, flowIDs []string) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "bedrockagent:ListFlowVersions", []string{"ResourceNotFoundException"}, flowIDs,
		func(ctx context.Context, id string, token *string) ([]agenttypes.FlowVersionSummary, *string, error) {
			out, err := client.ListFlowVersions(ctx, &bedrockagent.ListFlowVersionsInput{FlowIdentifier: &id, NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.FlowVersionSummaries, out.NextToken, nil
		},
		func(id string, v agenttypes.FlowVersionSummary) (*store.Resource, string, string) {
			return bedrockVersionRow(acct, region, scanID, TypeBedrockFlowVersion, "flow versions", v.Arn, v.Version, "flow "+id, v)
		})
}

// scanBedrockPrompts stores the region's prompts with their tags and returns
// the stored ones that carry an Id, the parents of the prompt-version phase.
// A first page refused by an SCP's explicit deny ends the listing silently;
// one failing with a server error ends it with a warning
// (bedrockNotDeployed).
func scanBedrockPrompts(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (prompts []childParent, storeWriteFailed bool) {
	const op = "bedrockagent:ListPrompts"
	var notDeployed error
	prompts, failed := storeParentListTags(ctx, st, acct, region, op, "prompts", "Id",
		bacPages(isSCPExplicitDeny, bedrockNotDeployed(&notDeployed, func(ctx context.Context, token *string) ([]agenttypes.PromptSummary, *string, error) {
			out, err := client.ListPrompts(ctx, &bedrockagent.ListPromptsInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)}, bedrockNotDeployedRetries)
			if err != nil {
				return nil, nil, err
			}
			return out.PromptSummaries, out.NextToken, nil
		})),
		func(pr agenttypes.PromptSummary) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockPrompt, "prompts", pr.Arn, cmp.Or(sv(pr.Name), sv(pr.Arn)), sv(pr.Id), pr)
		},
		func(pr agenttypes.PromptSummary) string { return sv(pr.Id) },
		tags, p)
	if !failed {
		warnBedrockNotDeployed(st, acct, region, op, notDeployed)
	}
	return prompts, failed
}

// scanBedrockPromptVersions lists the versions of each prompt the prompt
// phase stored (ListPrompts with PromptIdentifier set) and stores them with
// their tags (storeEachParentTags). Each is stored under its own ARN, which
// the Service Reference gives as prompt/${PromptId}:${PromptVersion}.
// PromptSummary.Arn is "the prompt or the prompt version", so an element
// whose Arn already ends in ":"+Version is that ARN, and one whose Arn is the
// prompt's gets ":"+Version appended: both build the same documented ARN.
// The DRAFT listed beside the versions is the prompt itself and is dropped.
// An element with no Arn or no Version is warned about, not stored. The tag
// read is sent that ARN, which the tag op's pattern accepts. A prompt
// ResourceNotFoundException says was deleted since it was listed is skipped.
func scanBedrockPromptVersions(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, prompts []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, failed := storeEachParentTags(ctx, st, acct, region, "bedrockagent:ListPrompts", []string{"ResourceNotFoundException"}, prompts,
		func(ctx context.Context, id string, token *string) ([]agenttypes.PromptSummary, *string, error) {
			out, err := client.ListPrompts(ctx, &bedrockagent.ListPromptsInput{PromptIdentifier: &id, NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return withoutDraft(out.PromptSummaries, func(pr agenttypes.PromptSummary) *string { return pr.Version }), out.NextToken, nil
		},
		func(id string, pr agenttypes.PromptSummary) (*store.Resource, string, string) {
			arn, ver := sv(pr.Arn), sv(pr.Version)
			key := cmp.Or(ver, "(a version with no number)") + " of prompt " + id
			switch {
			case arn == "":
				return nil, "prompt versions have an empty Arn", key
			case ver == "":
				return nil, "prompt versions have an empty Version", key
			}
			return bacRowAt(acct, region, scanID, TypeBedrockPromptVersion, bedrockPromptVersionARN(arn, ver), ver, pr), "", ""
		}, tags, p)
	return failed
}

// bedrockPromptVersionARN is the version's ARN, prompt/${PromptId}:${PromptVersion},
// from a list element's Arn, which names either the version or its prompt.
func bedrockPromptVersionARN(arn, ver string) string {
	if strings.HasSuffix(arn, ":"+ver) {
		return arn
	}
	return arn + ":" + ver
}

func bedrockAgentARN(partition, region, acct, agentID string) string {
	return arnFor(partition, "bedrock", region, acct, "agent/"+agentID)
}

func bedrockAgentAliasARN(partition, region, acct, agentID, aliasID string) string {
	return arnFor(partition, "bedrock", region, acct, "agent-alias/"+agentID+"/"+aliasID)
}

func bedrockKBARN(partition, region, acct, kbID string) string {
	return arnFor(partition, "bedrock", region, acct, "knowledge-base/"+kbID)
}

func bedrockDataSourceARN(partition, region, acct, kbID, dsID string) string {
	return bedrockKBARN(partition, region, acct, kbID) + "/data-source/" + dsID
}

// scanBedrockDataSources lists the data sources of each knowledge base the
// knowledge-base phase stored, concurrently (readEachKey), and stores each
// as its GetDataSource body, which adds the connector configuration and the
// encryption key to the list element (readRowDetails). A data source's
// element carries no ARN; its NativeID is built from the two ids in the
// Service Reference's data-source ARN format. A knowledge base
// ResourceNotFoundException says was deleted since it was listed is skipped;
// a denial is warned about. Counts and errors go to p.
func scanBedrockDataSources(ctx context.Context, client bedrockAgentAPI, acct *account, region string, st *store.Store, scanID string, kbIDs []string, p *phaseTotals) (storeWriteFailed bool) {
	const op = "bedrockagent:ListDataSources"
	reads, stopped := readEachKey(ctx, kbIDs, func(ctx context.Context, kb string) parentItems[agenttypes.DataSourceSummary] {
		return readParentPages(ctx, kb, func(ctx context.Context, kb string, token *string) ([]agenttypes.DataSourceSummary, *string, error) {
			out, err := client.ListDataSources(ctx, &bedrockagent.ListDataSourcesInput{KnowledgeBaseId: &kb, NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.DataSourceSummaries, out.NextToken, nil
		})
	})
	out := parentOutcomes{op: op, notFound: []string{"ResourceNotFoundException"}}
	keys := map[*store.Resource][2]string{}
	for i, read := range reads {
		addParentRows(&out, kbIDs[i], read, func(kb string, d agenttypes.DataSourceSummary) (*store.Resource, string, string) {
			id := sv(d.DataSourceId)
			if id == "" {
				return nil, "data sources have an empty DataSourceId", cmp.Or(sv(d.Name), "(a data source with no name)") + " of knowledge base " + kb
			}
			r := bacRowAt(acct, region, scanID, TypeBedrockDataSource, bedrockDataSourceARN(acct.partition(), region, acct.ID, kb, id), cmp.Or(sv(d.Name), id), d)
			keys[r] = [2]string{kb, id}
			return r, "", ""
		})
	}
	out.stopped(stopped)
	var (
		warn            func()
		hard, transient error
	)
	out.rows, warn, hard, transient = readRowDetails(ctx, st, acct, region, bedrockGetDSOp, "data source(s)", bedrockGetNotFound, out.rows,
		func(ctx context.Context, r *store.Resource) (any, error) {
			k := keys[r]
			got, err := client.GetDataSource(ctx, &bedrockagent.GetDataSourceInput{KnowledgeBaseId: &k[0], DataSourceId: &k[1]})
			if err != nil || got.DataSource == nil {
				return nil, err
			}
			return got.DataSource, nil
		})
	total, inserted, err := out.store(st, acct, region)
	if errors.Is(err, store.ErrStoreWrite) {
		return p.addPhase(op, total, inserted, errors.Join(err, hard, transient))
	}
	warn()
	p.errs.add(bedrockGetDSOp, hard)
	p.errs.add(bedrockGetDSOp, transient)
	return p.addPhase(op, total, inserted, err)
}
