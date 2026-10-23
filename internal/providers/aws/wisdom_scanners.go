package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/qconnect"
	qconnecttypes "github.com/aws/aws-sdk-go-v2/service/qconnect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
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

const (
	wisdomListAssistantsOp              = "qconnect:ListAssistants"
	wisdomListKnowledgeBasesOp          = "qconnect:ListKnowledgeBases"
	wisdomListAssistantAssociationsOp   = "qconnect:ListAssistantAssociations"
	wisdomListAIAgentsOp                = "qconnect:ListAIAgents"
	wisdomListAIAgentVersionsOp         = "qconnect:ListAIAgentVersions"
	wisdomListAIGuardrailsOp            = "qconnect:ListAIGuardrails"
	wisdomListAIGuardrailVersionsOp     = "qconnect:ListAIGuardrailVersions"
	wisdomListAIPromptsOp               = "qconnect:ListAIPrompts"
	wisdomListAIPromptVersionsOp        = "qconnect:ListAIPromptVersions"
	wisdomListMessageTemplatesOp        = "qconnect:ListMessageTemplates"
	wisdomListMessageTemplateVersionsOp = "qconnect:ListMessageTemplateVersions"
	wisdomListQuickResponsesOp          = "qconnect:ListQuickResponses"
	wisdomListContentsOp                = "qconnect:ListContents"
	wisdomListContentAssociationsOp     = "qconnect:ListContentAssociations"
)

// wisdomMaxResults is the documented page cap (MaxResults range 1-100) of
// every list op scanned here.
var wisdomMaxResults = sdkaws.Int32(100)

// wisdomNotFound is the error every per-parent list op here models for a
// parent deleted since it was listed.
var wisdomNotFound = []string{"ResourceNotFoundException"}

// wisdomChild is a stored child whose own listings key on its parent's id and
// its id; arn is its NativeID, which names it in warnings.
type wisdomChild struct {
	parent, id, arn string
}

func scanWisdom(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := qconnect.NewFromConfig(acct.cfg, func(o *qconnect.Options) { o.Region = region })
	return scanWisdomAll(ctx, client, acct, region, st, scanID)
}

// scanWisdomAll runs every phase and joins their errors; only a store write
// failure stops it. A failed parent listing leaves its child phases fewer
// parents to list.
func scanWisdomAll(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var phases phaseTotals
	assistantIDs, t, i, ferr := scanWisdomAssistants(ctx, client, acct, region, st, scanID)
	if phases.addPhase(wisdomListAssistantsOp, t, i, ferr) {
		return phases.result(st, acct, region)
	}
	kbIDs, t, i, ferr := scanWisdomKnowledgeBases(ctx, client, acct, region, st, scanID)
	if phases.addPhase(wisdomListKnowledgeBasesOp, t, i, ferr) {
		return phases.result(st, acct, region)
	}
	agents, t, i, ferr := scanWisdomAIAgents(ctx, client, acct, region, st, scanID, assistantIDs)
	if phases.addPhase(wisdomListAIAgentsOp, t, i, ferr) {
		return phases.result(st, acct, region)
	}
	guardrails, t, i, ferr := scanWisdomAIGuardrails(ctx, client, acct, region, st, scanID, assistantIDs)
	if phases.addPhase(wisdomListAIGuardrailsOp, t, i, ferr) {
		return phases.result(st, acct, region)
	}
	prompts, t, i, ferr := scanWisdomAIPrompts(ctx, client, acct, region, st, scanID, assistantIDs)
	if phases.addPhase(wisdomListAIPromptsOp, t, i, ferr) {
		return phases.result(st, acct, region)
	}
	templates, t, i, ferr := scanWisdomMessageTemplates(ctx, client, acct, region, st, scanID, kbIDs)
	if phases.addPhase(wisdomListMessageTemplatesOp, t, i, ferr) {
		return phases.result(st, acct, region)
	}
	contents, t, i, ferr := scanWisdomContents(ctx, client, acct, region, st, scanID, kbIDs)
	if phases.addPhase(wisdomListContentsOp, t, i, ferr) {
		return phases.result(st, acct, region)
	}
	for _, phase := range []struct {
		op string
		fn func() (int, int, error)
	}{
		{wisdomListContentAssociationsOp, func() (int, int, error) {
			return scanWisdomContentAssociations(ctx, client, acct, region, st, scanID, contents)
		}},
		{wisdomListAssistantAssociationsOp, func() (int, int, error) {
			return scanWisdomAssistantAssocs(ctx, client, acct, region, st, scanID, assistantIDs)
		}},
		{wisdomListAIAgentVersionsOp, func() (int, int, error) {
			return scanWisdomAIAgentVersions(ctx, client, acct, region, st, scanID, agents)
		}},
		{wisdomListAIGuardrailVersionsOp, func() (int, int, error) {
			return scanWisdomAIGuardrailVersions(ctx, client, acct, region, st, scanID, guardrails)
		}},
		{wisdomListAIPromptVersionsOp, func() (int, int, error) {
			return scanWisdomAIPromptVersions(ctx, client, acct, region, st, scanID, prompts)
		}},
		{wisdomListMessageTemplateVersionsOp, func() (int, int, error) {
			return scanWisdomMessageTemplateVersions(ctx, client, acct, region, st, scanID, templates)
		}},
		{wisdomListQuickResponsesOp, func() (int, int, error) {
			return scanWisdomQuickResponses(ctx, client, acct, region, st, scanID, kbIDs)
		}},
	} {
		t, i, ferr := phase.fn()
		if phases.addPhase(phase.op, t, i, ferr) {
			break
		}
	}
	return phases.result(st, acct, region)
}

// wisdomRow builds the row of a listed Wisdom element.
func wisdomRow(acct *account, region, scanID, rtype, nativeID, name string, element any, tags *string) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: nativeID, Name: &name, Region: &region,
		AttributesJSON: mustJSON(element), TagsJSON: tags, DiscoveredBy: scanID,
	}
}

// wisdomChildrenByARN returns the ARNs of children, which their own listings
// run under, and the children by ARN.
func wisdomChildrenByARN(children []wisdomChild) ([]string, map[string]wisdomChild) {
	arns := make([]string, 0, len(children))
	byARN := make(map[string]wisdomChild, len(children))
	for _, c := range children {
		arns = append(arns, c.arn)
		byARN[c.arn] = c
	}
	return arns, byARN
}

// wisdomVersionNativeID is "<arn>:<version>", or "" with the reason the
// version cannot be stored.
func wisdomVersionNativeID(kind, arn string, version *int64) (nativeID, why string) {
	if arn == "" {
		return "", kind + " versions have an empty ARN (first names their parent)"
	}
	if version == nil {
		return "", kind + " versions have no VersionNumber (first names their parent)"
	}
	return fmt.Sprintf("%s:%d", arn, *version), ""
}

// scanWisdomAssistants stores the assistants and returns the ids of those
// stored, for the per-assistant phases.
func scanWisdomAssistants(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var (
		ids       []string
		noChildID skipTally
	)
	t, i, err := storeTokenList(ctx, st, acct, region, wisdomListAssistantsOp,
		func(ctx context.Context, token *string) ([]qconnecttypes.AssistantSummary, *string, error) {
			out, err := client.ListAssistants(ctx, &qconnect.ListAssistantsInput{NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.AssistantSummaries, out.NextToken, nil
		},
		func(a qconnecttypes.AssistantSummary) (*store.Resource, string, string) {
			arn, id := sv(a.AssistantArn), sv(a.AssistantId)
			if arn == "" {
				return nil, "assistants have an empty AssistantArn, and their children were not read either", id
			}
			if id == "" {
				noChildID.skipped(arn, nil)
			} else {
				ids = append(ids, id)
			}
			return wisdomRow(acct, region, scanID, TypeWisdomAssistant, arn, cmp.Or(sv(a.Name), id), a, mapTagsJSON(a.Tags)), "", ""
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		warnNoChildID(st, wisdomListAssistantsOp, acct, region, &noChildID, "assistant(s)", "AssistantId")
	}
	return ids, t, i, err
}

// scanWisdomKnowledgeBases stores the knowledge bases and returns the ids of
// those stored, for the per-knowledge-base phases.
func scanWisdomKnowledgeBases(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var (
		ids       []string
		noChildID skipTally
	)
	t, i, err := storeTokenList(ctx, st, acct, region, wisdomListKnowledgeBasesOp,
		func(ctx context.Context, token *string) ([]qconnecttypes.KnowledgeBaseSummary, *string, error) {
			out, err := client.ListKnowledgeBases(ctx, &qconnect.ListKnowledgeBasesInput{NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.KnowledgeBaseSummaries, out.NextToken, nil
		},
		func(k qconnecttypes.KnowledgeBaseSummary) (*store.Resource, string, string) {
			arn, id := sv(k.KnowledgeBaseArn), sv(k.KnowledgeBaseId)
			if arn == "" {
				return nil, "knowledge bases have an empty KnowledgeBaseArn, and their children were not read either", id
			}
			if id == "" {
				noChildID.skipped(arn, nil)
			} else {
				ids = append(ids, id)
			}
			return wisdomRow(acct, region, scanID, TypeWisdomKnowledgeBase, arn, cmp.Or(sv(k.Name), id), k, mapTagsJSON(k.Tags)), "", ""
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		warnNoChildID(st, wisdomListKnowledgeBasesOp, acct, region, &noChildID, "knowledge base(s)", "KnowledgeBaseId")
	}
	return ids, t, i, err
}

func scanWisdomAssistantAssocs(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, assistantIDs []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, wisdomListAssistantAssociationsOp, wisdomNotFound, assistantIDs,
		func(ctx context.Context, aid string, token *string) ([]qconnecttypes.AssistantAssociationSummary, *string, error) {
			out, err := client.ListAssistantAssociations(ctx, &qconnect.ListAssistantAssociationsInput{AssistantId: &aid, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.AssistantAssociationSummaries, out.NextToken, nil
		},
		func(aid string, a qconnecttypes.AssistantAssociationSummary) (*store.Resource, string, string) {
			arn := sv(a.AssistantAssociationArn)
			if arn == "" {
				return nil, "assistant associations have an empty AssistantAssociationArn (first names their assistant)", aid
			}
			return wisdomRow(acct, region, scanID, TypeWisdomAssistantAssociation, arn, sv(a.AssistantAssociationId), a, mapTagsJSON(a.Tags)), "", ""
		})
}

// scanWisdomAIAgents stores the AI agents of each assistant and returns those
// stored with an id, for the version phase.
func scanWisdomAIAgents(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, assistantIDs []string) ([]wisdomChild, int, int, error) {
	var (
		agents    []wisdomChild
		noChildID skipTally
	)
	t, i, err := storeTokenPerParent(ctx, st, acct, region, wisdomListAIAgentsOp, wisdomNotFound, assistantIDs,
		func(ctx context.Context, aid string, token *string) ([]qconnecttypes.AIAgentSummary, *string, error) {
			out, err := client.ListAIAgents(ctx, &qconnect.ListAIAgentsInput{AssistantId: &aid, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.AiAgentSummaries, out.NextToken, nil
		},
		func(aid string, a qconnecttypes.AIAgentSummary) (*store.Resource, string, string) {
			arn, id := sv(a.AiAgentArn), sv(a.AiAgentId)
			if arn == "" {
				return nil, "AI agents have an empty AiAgentArn, and their versions were not read either (first names their assistant)", aid
			}
			if id == "" {
				noChildID.skipped(arn, nil)
			} else {
				agents = append(agents, wisdomChild{parent: aid, id: id, arn: arn})
			}
			return wisdomRow(acct, region, scanID, TypeWisdomAIAgent, arn, cmp.Or(sv(a.Name), id), a, mapTagsJSON(a.Tags)), "", ""
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		warnNoChildID(st, wisdomListAIAgentsOp, acct, region, &noChildID, "AI agent(s)", "AiAgentId")
	}
	return agents, t, i, err
}

// scanWisdomAIAgentVersions lists the versions of each AI agent. A version's
// AiAgentSummary carries Tags, but a version has no ARN of its own and no API
// tags one (CreateAIAgentVersion takes no tags; TagResource names no version
// resource), so the row stores none.
func scanWisdomAIAgentVersions(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, agents []wisdomChild) (int, int, error) {
	arns, byARN := wisdomChildrenByARN(agents)
	return storeTokenPerParent(ctx, st, acct, region, wisdomListAIAgentVersionsOp, wisdomNotFound, arns,
		func(ctx context.Context, arn string, token *string) ([]qconnecttypes.AIAgentVersionSummary, *string, error) {
			k := byARN[arn]
			out, err := client.ListAIAgentVersions(ctx, &qconnect.ListAIAgentVersionsInput{AssistantId: &k.parent, AiAgentId: &k.id, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.AiAgentVersionSummaries, out.NextToken, nil
		},
		func(parentARN string, v qconnecttypes.AIAgentVersionSummary) (*store.Resource, string, string) {
			var arn string
			if v.AiAgentSummary != nil {
				arn = sv(v.AiAgentSummary.AiAgentArn)
			}
			nativeID, why := wisdomVersionNativeID("AI agent", arn, v.VersionNumber)
			if nativeID == "" {
				return nil, why, parentARN
			}
			return wisdomRow(acct, region, scanID, TypeWisdomAIAgentVersion, nativeID, fmt.Sprintf("v%d", *v.VersionNumber), v, nil), "", ""
		})
}

// scanWisdomAIGuardrails stores the AI guardrails of each assistant and
// returns those stored with an id, for the version phase.
func scanWisdomAIGuardrails(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, assistantIDs []string) ([]wisdomChild, int, int, error) {
	var (
		guardrails []wisdomChild
		noChildID  skipTally
	)
	t, i, err := storeTokenPerParent(ctx, st, acct, region, wisdomListAIGuardrailsOp, wisdomNotFound, assistantIDs,
		func(ctx context.Context, aid string, token *string) ([]qconnecttypes.AIGuardrailSummary, *string, error) {
			out, err := client.ListAIGuardrails(ctx, &qconnect.ListAIGuardrailsInput{AssistantId: &aid, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.AiGuardrailSummaries, out.NextToken, nil
		},
		func(aid string, g qconnecttypes.AIGuardrailSummary) (*store.Resource, string, string) {
			arn, id := sv(g.AiGuardrailArn), sv(g.AiGuardrailId)
			if arn == "" {
				return nil, "AI guardrails have an empty AiGuardrailArn, and their versions were not read either (first names their assistant)", aid
			}
			if id == "" {
				noChildID.skipped(arn, nil)
			} else {
				guardrails = append(guardrails, wisdomChild{parent: aid, id: id, arn: arn})
			}
			return wisdomRow(acct, region, scanID, TypeWisdomAIGuardrail, arn, cmp.Or(sv(g.Name), id), g, mapTagsJSON(g.Tags)), "", ""
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		warnNoChildID(st, wisdomListAIGuardrailsOp, acct, region, &noChildID, "AI guardrail(s)", "AiGuardrailId")
	}
	return guardrails, t, i, err
}

// scanWisdomAIGuardrailVersions lists the versions of each AI guardrail; the
// row stores no tags, for the reason scanWisdomAIAgentVersions gives.
func scanWisdomAIGuardrailVersions(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, guardrails []wisdomChild) (int, int, error) {
	arns, byARN := wisdomChildrenByARN(guardrails)
	return storeTokenPerParent(ctx, st, acct, region, wisdomListAIGuardrailVersionsOp, wisdomNotFound, arns,
		func(ctx context.Context, arn string, token *string) ([]qconnecttypes.AIGuardrailVersionSummary, *string, error) {
			k := byARN[arn]
			out, err := client.ListAIGuardrailVersions(ctx, &qconnect.ListAIGuardrailVersionsInput{AssistantId: &k.parent, AiGuardrailId: &k.id, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.AiGuardrailVersionSummaries, out.NextToken, nil
		},
		func(parentARN string, v qconnecttypes.AIGuardrailVersionSummary) (*store.Resource, string, string) {
			var arn string
			if v.AiGuardrailSummary != nil {
				arn = sv(v.AiGuardrailSummary.AiGuardrailArn)
			}
			nativeID, why := wisdomVersionNativeID("AI guardrail", arn, v.VersionNumber)
			if nativeID == "" {
				return nil, why, parentARN
			}
			return wisdomRow(acct, region, scanID, TypeWisdomAIGuardrailVersion, nativeID, fmt.Sprintf("v%d", *v.VersionNumber), v, nil), "", ""
		})
}

// scanWisdomAIPrompts stores the AI prompts of each assistant and returns
// those stored with an id, for the version phase.
func scanWisdomAIPrompts(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, assistantIDs []string) ([]wisdomChild, int, int, error) {
	var (
		prompts   []wisdomChild
		noChildID skipTally
	)
	t, i, err := storeTokenPerParent(ctx, st, acct, region, wisdomListAIPromptsOp, wisdomNotFound, assistantIDs,
		func(ctx context.Context, aid string, token *string) ([]qconnecttypes.AIPromptSummary, *string, error) {
			out, err := client.ListAIPrompts(ctx, &qconnect.ListAIPromptsInput{AssistantId: &aid, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.AiPromptSummaries, out.NextToken, nil
		},
		func(aid string, p qconnecttypes.AIPromptSummary) (*store.Resource, string, string) {
			arn, id := sv(p.AiPromptArn), sv(p.AiPromptId)
			if arn == "" {
				return nil, "AI prompts have an empty AiPromptArn, and their versions were not read either (first names their assistant)", aid
			}
			if id == "" {
				noChildID.skipped(arn, nil)
			} else {
				prompts = append(prompts, wisdomChild{parent: aid, id: id, arn: arn})
			}
			return wisdomRow(acct, region, scanID, TypeWisdomAIPrompt, arn, cmp.Or(sv(p.Name), id), p, mapTagsJSON(p.Tags)), "", ""
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		warnNoChildID(st, wisdomListAIPromptsOp, acct, region, &noChildID, "AI prompt(s)", "AiPromptId")
	}
	return prompts, t, i, err
}

// scanWisdomAIPromptVersions lists the versions of each AI prompt; the row
// stores no tags, for the reason scanWisdomAIAgentVersions gives.
func scanWisdomAIPromptVersions(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, prompts []wisdomChild) (int, int, error) {
	arns, byARN := wisdomChildrenByARN(prompts)
	return storeTokenPerParent(ctx, st, acct, region, wisdomListAIPromptVersionsOp, wisdomNotFound, arns,
		func(ctx context.Context, arn string, token *string) ([]qconnecttypes.AIPromptVersionSummary, *string, error) {
			k := byARN[arn]
			out, err := client.ListAIPromptVersions(ctx, &qconnect.ListAIPromptVersionsInput{AssistantId: &k.parent, AiPromptId: &k.id, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.AiPromptVersionSummaries, out.NextToken, nil
		},
		func(parentARN string, v qconnecttypes.AIPromptVersionSummary) (*store.Resource, string, string) {
			var arn string
			if v.AiPromptSummary != nil {
				arn = sv(v.AiPromptSummary.AiPromptArn)
			}
			nativeID, why := wisdomVersionNativeID("AI prompt", arn, v.VersionNumber)
			if nativeID == "" {
				return nil, why, parentARN
			}
			return wisdomRow(acct, region, scanID, TypeWisdomAIPromptVersion, nativeID, fmt.Sprintf("v%d", *v.VersionNumber), v, nil), "", ""
		})
}

// scanWisdomMessageTemplates stores the message templates of each knowledge
// base and returns those stored with an id, for the version phase.
func scanWisdomMessageTemplates(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, kbIDs []string) ([]wisdomChild, int, int, error) {
	var (
		templates []wisdomChild
		noChildID skipTally
	)
	t, i, err := storeTokenPerParent(ctx, st, acct, region, wisdomListMessageTemplatesOp, wisdomNotFound, kbIDs,
		func(ctx context.Context, kbID string, token *string) ([]qconnecttypes.MessageTemplateSummary, *string, error) {
			out, err := client.ListMessageTemplates(ctx, &qconnect.ListMessageTemplatesInput{KnowledgeBaseId: &kbID, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.MessageTemplateSummaries, out.NextToken, nil
		},
		func(kbID string, m qconnecttypes.MessageTemplateSummary) (*store.Resource, string, string) {
			arn, id := sv(m.MessageTemplateArn), sv(m.MessageTemplateId)
			if arn == "" {
				return nil, "message templates have an empty MessageTemplateArn, and their versions were not read either (first names their knowledge base)", kbID
			}
			if id == "" {
				noChildID.skipped(arn, nil)
			} else {
				templates = append(templates, wisdomChild{parent: kbID, id: id, arn: arn})
			}
			return wisdomRow(acct, region, scanID, TypeWisdomMessageTemplate, arn, cmp.Or(sv(m.Name), id), m, mapTagsJSON(m.Tags)), "", ""
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		warnNoChildID(st, wisdomListMessageTemplatesOp, acct, region, &noChildID, "message template(s)", "MessageTemplateId")
	}
	return templates, t, i, err
}

// scanWisdomMessageTemplateVersions lists the versions of each message
// template. MessageTemplateVersionSummary carries no tags.
func scanWisdomMessageTemplateVersions(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, templates []wisdomChild) (int, int, error) {
	arns, byARN := wisdomChildrenByARN(templates)
	return storeTokenPerParent(ctx, st, acct, region, wisdomListMessageTemplateVersionsOp, wisdomNotFound, arns,
		func(ctx context.Context, arn string, token *string) ([]qconnecttypes.MessageTemplateVersionSummary, *string, error) {
			k := byARN[arn]
			out, err := client.ListMessageTemplateVersions(ctx, &qconnect.ListMessageTemplateVersionsInput{KnowledgeBaseId: &k.parent, MessageTemplateId: &k.id, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.MessageTemplateVersionSummaries, out.NextToken, nil
		},
		func(parentARN string, v qconnecttypes.MessageTemplateVersionSummary) (*store.Resource, string, string) {
			nativeID, why := wisdomVersionNativeID("message template", sv(v.MessageTemplateArn), v.VersionNumber)
			if nativeID == "" {
				return nil, why, parentARN
			}
			return wisdomRow(acct, region, scanID, TypeWisdomMessageTemplateVersion, nativeID, cmp.Or(sv(v.Name), fmt.Sprintf("v%d", *v.VersionNumber)), v, nil), "", ""
		})
}

func scanWisdomQuickResponses(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, kbIDs []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, wisdomListQuickResponsesOp, wisdomNotFound, kbIDs,
		func(ctx context.Context, kbID string, token *string) ([]qconnecttypes.QuickResponseSummary, *string, error) {
			out, err := client.ListQuickResponses(ctx, &qconnect.ListQuickResponsesInput{KnowledgeBaseId: &kbID, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.QuickResponseSummaries, out.NextToken, nil
		},
		func(kbID string, q qconnecttypes.QuickResponseSummary) (*store.Resource, string, string) {
			arn := sv(q.QuickResponseArn)
			if arn == "" {
				return nil, "quick responses have an empty QuickResponseArn (first names their knowledge base)", kbID
			}
			return wisdomRow(acct, region, scanID, TypeWisdomQuickResponse, arn, cmp.Or(sv(q.Name), arn), q, mapTagsJSON(q.Tags)), "", ""
		})
}

// scanWisdomContents stores the contents of each knowledge base and returns
// those stored with an id, for the content-association phase.
func scanWisdomContents(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, kbIDs []string) ([]wisdomChild, int, int, error) {
	var (
		contents  []wisdomChild
		noChildID skipTally
	)
	t, i, err := storeTokenPerParent(ctx, st, acct, region, wisdomListContentsOp, wisdomNotFound, kbIDs,
		func(ctx context.Context, kbID string, token *string) ([]qconnecttypes.ContentSummary, *string, error) {
			out, err := client.ListContents(ctx, &qconnect.ListContentsInput{KnowledgeBaseId: &kbID, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.ContentSummaries, out.NextToken, nil
		},
		func(kbID string, c qconnecttypes.ContentSummary) (*store.Resource, string, string) {
			arn, id := sv(c.ContentArn), sv(c.ContentId)
			if arn == "" {
				return nil, "contents have an empty ContentArn, and their associations were not read either (first names their knowledge base)", kbID
			}
			if id == "" {
				noChildID.skipped(arn, nil)
			} else {
				contents = append(contents, wisdomChild{parent: kbID, id: id, arn: arn})
			}
			return wisdomRow(acct, region, scanID, TypeWisdomContent, arn, cmp.Or(sv(c.Name), id), c, mapTagsJSON(c.Tags)), "", ""
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		warnNoChildID(st, wisdomListContentsOp, acct, region, &noChildID, "content(s)", "ContentId")
	}
	return contents, t, i, err
}

func scanWisdomContentAssociations(ctx context.Context, client wisdomAPI, acct *account, region string, st *store.Store, scanID string, contents []wisdomChild) (int, int, error) {
	arns, byARN := wisdomChildrenByARN(contents)
	return storeTokenPerParent(ctx, st, acct, region, wisdomListContentAssociationsOp, wisdomNotFound, arns,
		func(ctx context.Context, arn string, token *string) ([]qconnecttypes.ContentAssociationSummary, *string, error) {
			k := byARN[arn]
			out, err := client.ListContentAssociations(ctx, &qconnect.ListContentAssociationsInput{KnowledgeBaseId: &k.parent, ContentId: &k.id, NextToken: token, MaxResults: wisdomMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.ContentAssociationSummaries, out.NextToken, nil
		},
		func(contentARN string, a qconnecttypes.ContentAssociationSummary) (*store.Resource, string, string) {
			arn := sv(a.ContentAssociationArn)
			if arn == "" {
				return nil, "content associations have an empty ContentAssociationArn (first names their content)", contentARN
			}
			return wisdomRow(acct, region, scanID, TypeWisdomContentAssociation, arn, sv(a.ContentAssociationId), a, mapTagsJSON(a.Tags)), "", ""
		})
}
