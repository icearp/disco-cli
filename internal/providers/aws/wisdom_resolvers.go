package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveWisdomAssistantChildren,
		EdgeDecl{TypeWisdomAssistantAssociation, TypeWisdomAssistant, store.RelAttachedTo},
		EdgeDecl{TypeWisdomAIAgent, TypeWisdomAssistant, store.RelAttachedTo},
		EdgeDecl{TypeWisdomAIGuardrail, TypeWisdomAssistant, store.RelAttachedTo},
		EdgeDecl{TypeWisdomAIPrompt, TypeWisdomAssistant, store.RelAttachedTo},
	)
	registerResolver(
		resolveWisdomKnowledgeBaseChildren,
		EdgeDecl{TypeWisdomMessageTemplate, TypeWisdomKnowledgeBase, store.RelAttachedTo},
		EdgeDecl{TypeWisdomQuickResponse, TypeWisdomKnowledgeBase, store.RelAttachedTo},
	)
	registerResolver(
		resolveWisdomVersionParent,
		EdgeDecl{TypeWisdomAIAgentVersion, TypeWisdomAIAgent, store.RelAttachedTo},
		EdgeDecl{TypeWisdomAIGuardrailVersion, TypeWisdomAIGuardrail, store.RelAttachedTo},
		EdgeDecl{TypeWisdomAIPromptVersion, TypeWisdomAIPrompt, store.RelAttachedTo},
		EdgeDecl{TypeWisdomMessageTemplateVersion, TypeWisdomMessageTemplate, store.RelAttachedTo},
	)
	registerResolver(
		resolveWisdomAssistantAssociationKnowledgeBase,
		EdgeDecl{TypeWisdomAssistantAssociation, TypeWisdomKnowledgeBase, store.RelUses},
	)
	registerResolver(
		resolveWisdomKMSRefs,
		EdgeDecl{TypeWisdomAssistant, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeWisdomKnowledgeBase, TypeKMSKey, store.RelUses},
	)
}

// resolveWisdomKMSRefs wires assistant + knowledge-base to their customer
// managed KMS key (ServerSideEncryptionConfiguration.KmsKeyID).
func resolveWisdomKMSRefs(acct *account, st *store.Store) error {
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, rtype := range []string{TypeWisdomAssistant, TypeWisdomKnowledgeBase} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{rtype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				ServerSideEncryptionConfiguration *struct {
					KmsKeyID *string `json:"KmsKeyId"`
				} `json:"ServerSideEncryptionConfiguration"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			if attrs.ServerSideEncryptionConfiguration == nil {
				continue
			}
			ref := sv(attrs.ServerSideEncryptionConfiguration.KmsKeyID)
			if ref == "" {
				continue
			}
			if keyID, ok := kmsIdx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert wisdom %s→kms: %w", rtype, err)
				}
			}
		}
	}
	return nil
}

func resolveWisdomChildToParentByArnField(acct *account, st *store.Store, ctype, parentType, fieldName, label string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{ctype},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	parentSet, err := scannedIDSet(acct, st, parentType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		raw, ok := attrs[fieldName]
		if !ok {
			continue
		}
		var arn string
		if err := json.Unmarshal(raw, &arn); err != nil || arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, parentType, arn)
		if !parentSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert wisdom-%s→%s: %w", label, parentType, err)
		}
	}
	return nil
}

// resolveWisdomAssistantChildren wires assistant-association, ai-agent,
// ai-guardrail, ai-prompt → parent assistant via AssistantArn attr.
func resolveWisdomAssistantChildren(acct *account, st *store.Store) error {
	for _, ctype := range []string{
		TypeWisdomAssistantAssociation,
		TypeWisdomAIAgent,
		TypeWisdomAIGuardrail,
		TypeWisdomAIPrompt,
	} {
		if err := resolveWisdomChildToParentByArnField(acct, st, ctype, TypeWisdomAssistant, "AssistantArn", strings.TrimPrefix(ctype, "aws:wisdom:")); err != nil {
			return err
		}
	}
	return nil
}

// resolveWisdomKnowledgeBaseChildren wires message-template + quick-response
// → knowledge-base via KnowledgeBaseArn attr.
func resolveWisdomKnowledgeBaseChildren(acct *account, st *store.Store) error {
	for _, ctype := range []string{TypeWisdomMessageTemplate, TypeWisdomQuickResponse} {
		if err := resolveWisdomChildToParentByArnField(acct, st, ctype, TypeWisdomKnowledgeBase, "KnowledgeBaseArn", strings.TrimPrefix(ctype, "aws:wisdom:")); err != nil {
			return err
		}
	}
	return nil
}

// wisdomVersionParentARN strips a `:NN` numeric version suffix from a Wisdom
// version NativeID, recovering the unversioned parent ARN. Returns "" when
// the input has no trailing `:digits` segment.
func wisdomVersionParentARN(arn string) string {
	i := strings.LastIndexByte(arn, ':')
	if i <= 0 {
		return ""
	}
	tail := arn[i+1:]
	if tail == "" {
		return ""
	}
	for _, c := range tail {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return arn[:i]
}

// resolveWisdomVersionParent wires every *-version row to its unversioned
// parent by stripping the trailing `:NN` segment from NativeID.
func resolveWisdomVersionParent(acct *account, st *store.Store) error {
	pairs := []struct {
		ctype, ptype string
	}{
		{TypeWisdomAIAgentVersion, TypeWisdomAIAgent},
		{TypeWisdomAIGuardrailVersion, TypeWisdomAIGuardrail},
		{TypeWisdomAIPromptVersion, TypeWisdomAIPrompt},
		{TypeWisdomMessageTemplateVersion, TypeWisdomMessageTemplate},
	}
	for _, p := range pairs {
		parentSet, err := scannedIDSet(acct, st, p.ptype)
		if err != nil {
			return err
		}
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{p.ctype},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := wisdomVersionParentARN(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, p.ptype, parent)
			if !parentSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert wisdom-%s→%s: %w", p.ctype, p.ptype, err)
			}
		}
	}
	return nil
}

// resolveWisdomAssistantAssociationKnowledgeBase emits a uses edge from each
// assistant-association whose AssociationData points at a knowledge-base.
// SDK shape: `AssociationData.KnowledgeBaseAssociation.KnowledgeBaseArn`.
func resolveWisdomAssistantAssociationKnowledgeBase(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeWisdomAssistantAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kbSet, err := scannedIDSet(acct, st, TypeWisdomKnowledgeBase)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AssociationData *struct {
				KnowledgeBaseAssociation *struct {
					KnowledgeBaseArn *string `json:"KnowledgeBaseArn"`
				} `json:"KnowledgeBaseAssociation"`
			} `json:"AssociationData"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.AssociationData == nil || attrs.AssociationData.KnowledgeBaseAssociation == nil {
			continue
		}
		arn := sv(attrs.AssociationData.KnowledgeBaseAssociation.KnowledgeBaseArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeWisdomKnowledgeBase, arn)
		if !kbSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert wisdom-aa→kb: %w", err)
		}
	}
	return nil
}
