package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveBedrockAgentRefs,
		EdgeDecl{TypeBedrockAgent, TypeBedrockGuardrail, store.RelUses},
	)
	registerResolver(
		resolveBedrockAgentAlias,
		EdgeDecl{TypeBedrockAgentAlias, TypeBedrockAgent, store.RelAttachedTo},
	)
	registerResolver(
		resolveBedrockDataSourceKB,
		EdgeDecl{TypeBedrockDataSource, TypeBedrockKnowledgeBase, store.RelAttachedTo},
	)
	registerResolver(
		resolveBedrockGuardrailVersion,
		EdgeDecl{TypeBedrockGuardrailVersion, TypeBedrockGuardrail, store.RelAttachedTo},
	)
	registerResolver(
		resolveBedrockFlowAlias,
		EdgeDecl{TypeBedrockFlowAlias, TypeBedrockFlow, store.RelAttachedTo},
	)
	registerResolver(
		resolveBedrockFlowVersion,
		EdgeDecl{TypeBedrockFlowVersion, TypeBedrockFlow, store.RelAttachedTo},
	)
	registerResolver(
		resolveBedrockPromptVersion,
		EdgeDecl{TypeBedrockPromptVersion, TypeBedrockPrompt, store.RelAttachedTo},
	)
	registerResolver(
		resolveBedrockARPolicyVersion,
		EdgeDecl{TypeBedrockAutomatedReasoningPolicyVersion, TypeBedrockAutomatedReasoningPolicy, store.RelAttachedTo},
	)
	registerResolver(
		resolveBedrockKBStorageRefs,
		EdgeDecl{TypeBedrockKnowledgeBase, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeBedrockKnowledgeBase, TypeKendraIndex, store.RelUses},
		EdgeDecl{TypeBedrockKnowledgeBase, TypeOSSCollection, store.RelUses},
		EdgeDecl{TypeBedrockKnowledgeBase, TypeNeptuneGraphGraph, store.RelUses},
		EdgeDecl{TypeBedrockKnowledgeBase, TypeRDSDBCluster, store.RelUses},
		EdgeDecl{TypeBedrockKnowledgeBase, TypeSecretsManagerSecret, store.RelUses},
		EdgeDecl{TypeBedrockKnowledgeBase, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveBedrockDataSourceRefs,
		EdgeDecl{TypeBedrockDataSource, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeBedrockDataSource, TypeKMSKey, store.RelUses},
	)
}

// resolveBedrockKBStorageRefs wires knowledge-base → IAM role + variant
// storage backend resources from GetKnowledgeBase enrichment. Backend
// dispatch on KnowledgeBaseConfiguration / StorageConfiguration variants.
func resolveBedrockKBStorageRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockKnowledgeBase}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	kendraSet, err := scannedIDSet(acct, st, TypeKendraIndex)
	if err != nil {
		return err
	}
	ossSet, err := scannedIDSet(acct, st, TypeOSSCollection)
	if err != nil {
		return err
	}
	graphSet, err := scannedIDSet(acct, st, TypeNeptuneGraphGraph)
	if err != nil {
		return err
	}
	rdsSet, err := scannedIDSet(acct, st, TypeRDSDBCluster)
	if err != nil {
		return err
	}
	secretSet, err := scannedIDSet(acct, st, TypeSecretsManagerSecret)
	if err != nil {
		return err
	}
	s3Set, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	emit := func(srcID, tgtARN, ttype string, set map[string]bool, label string) error {
		if tgtARN == "" {
			return nil
		}
		tgtID := store.ResourceID("aws", acct.ID, tgtARN)
		if !set[tgtID] {
			return nil
		}
		if err := st.UpsertRelationship(srcID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert bedrock kb→%s: %w", label, err)
		}
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			RoleArn                    *string `json:"RoleArn"`
			KnowledgeBaseConfiguration *struct {
				KendraKnowledgeBaseConfiguration *struct {
					KendraIndexArn *string `json:"KendraIndexArn"`
				} `json:"KendraKnowledgeBaseConfiguration"`
			} `json:"KnowledgeBaseConfiguration"`
			StorageConfiguration *struct {
				OpensearchServerlessConfiguration *struct {
					CollectionArn *string `json:"CollectionArn"`
				} `json:"OpensearchServerlessConfiguration"`
				NeptuneAnalyticsConfiguration *struct {
					GraphArn *string `json:"GraphArn"`
				} `json:"NeptuneAnalyticsConfiguration"`
				RdsConfiguration *struct {
					ResourceArn          *string `json:"ResourceArn"`
					CredentialsSecretArn *string `json:"CredentialsSecretArn"`
				} `json:"RdsConfiguration"`
				S3VectorsConfiguration *struct {
					VectorBucketArn *string `json:"VectorBucketArn"`
				} `json:"S3VectorsConfiguration"`
			} `json:"StorageConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := emit(r.ID, sv(attrs.RoleArn), TypeIAMRole, roleSet, "role"); err != nil {
			return err
		}
		if attrs.KnowledgeBaseConfiguration != nil && attrs.KnowledgeBaseConfiguration.KendraKnowledgeBaseConfiguration != nil {
			if err := emit(r.ID, sv(attrs.KnowledgeBaseConfiguration.KendraKnowledgeBaseConfiguration.KendraIndexArn), TypeKendraIndex, kendraSet, "kendra"); err != nil {
				return err
			}
		}
		if sc := attrs.StorageConfiguration; sc != nil {
			if sc.OpensearchServerlessConfiguration != nil {
				if err := emit(r.ID, sv(sc.OpensearchServerlessConfiguration.CollectionArn), TypeOSSCollection, ossSet, "oss"); err != nil {
					return err
				}
			}
			if sc.NeptuneAnalyticsConfiguration != nil {
				if err := emit(r.ID, sv(sc.NeptuneAnalyticsConfiguration.GraphArn), TypeNeptuneGraphGraph, graphSet, "neptune-graph"); err != nil {
					return err
				}
			}
			if sc.RdsConfiguration != nil {
				if err := emit(r.ID, sv(sc.RdsConfiguration.ResourceArn), TypeRDSDBCluster, rdsSet, "rds"); err != nil {
					return err
				}
				if err := emit(r.ID, sv(sc.RdsConfiguration.CredentialsSecretArn), TypeSecretsManagerSecret, secretSet, "secret"); err != nil {
					return err
				}
			}
			if sc.S3VectorsConfiguration != nil {
				if err := emit(r.ID, sv(sc.S3VectorsConfiguration.VectorBucketArn), TypeS3Bucket, s3Set, "s3"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// resolveBedrockDataSourceRefs wires data-source → S3 bucket
// (DataSourceConfiguration.S3Configuration.BucketArn) and KMS key
// (ServerSideEncryptionConfiguration.KmsKeyArn) from GetDataSource.
func resolveBedrockDataSourceRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockDataSource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	s3Set, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DataSourceConfiguration *struct {
				S3Configuration *struct {
					BucketArn *string `json:"BucketArn"`
				} `json:"S3Configuration"`
			} `json:"DataSourceConfiguration"`
			ServerSideEncryptionConfiguration *struct {
				KmsKeyArn *string `json:"KmsKeyArn"`
			} `json:"ServerSideEncryptionConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DataSourceConfiguration != nil && attrs.DataSourceConfiguration.S3Configuration != nil {
			if b := sv(attrs.DataSourceConfiguration.S3Configuration.BucketArn); b != "" {
				tgtID := store.ResourceID("aws", acct.ID, b)
				if s3Set[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert bedrock ds→s3: %w", err)
					}
				}
			}
		}
		if attrs.ServerSideEncryptionConfiguration != nil {
			if k := sv(attrs.ServerSideEncryptionConfiguration.KmsKeyArn); k != "" {
				if keyID, ok := kmsIdx.resolveKMSKeyID(k, sv(r.Region), acct.ID); ok {
					if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert bedrock ds→kms: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveBedrockARPolicyVersion wires automated-reasoning-policy-version to its
// parent policy. NativeID shape: `{policyArn}:{ver}`; strip from the last `:`.
func resolveBedrockARPolicyVersion(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAutomatedReasoningPolicyVersion}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	pSet, err := scannedIDSet(acct, st, TypeBedrockAutomatedReasoningPolicy)
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := strings.LastIndex(r.NativeID, ":")
		if i <= 0 {
			continue
		}
		parent := r.NativeID[:i]
		tgtID := store.ResourceID("aws", acct.ID, parent)
		if !pSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bedrock arpv→arp: %w", err)
		}
	}
	return nil
}

// bedrockGuardrailARN rebuilds the canonical guardrail ARN.
// AgentSummary.GuardrailConfiguration carries either the bare ID or the full
// ARN — callers handle both shapes.
func bedrockGuardrailARN(region, acct, id string) string {
	return fmt.Sprintf("arn:aws:bedrock:%s:%s:guardrail/%s", region, acct, id)
}

// resolveBedrockAgentRefs links each agent to the guardrail referenced via
// AgentSummary.GuardrailConfiguration.GuardrailIdentifier (ID or ARN form).
func resolveBedrockAgentRefs(acct *account, st *store.Store) error {
	agents, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgent},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	guardSet, err := scannedIDSet(acct, st, TypeBedrockGuardrail)
	if err != nil {
		return err
	}
	for _, r := range agents {
		var attrs struct {
			GuardrailConfiguration *struct {
				GuardrailIdentifier *string `json:"GuardrailIdentifier"`
			} `json:"GuardrailConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.GuardrailConfiguration == nil || sv(attrs.GuardrailConfiguration.GuardrailIdentifier) == "" {
			continue
		}
		ref := sv(attrs.GuardrailConfiguration.GuardrailIdentifier)
		// Handle bare-ID and full-ARN forms; build the canonical ARN.
		gARN := ref
		if !strings.HasPrefix(ref, "arn:") {
			gARN = bedrockGuardrailARN(sv(r.Region), acct.ID, ref)
		}
		gID := store.ResourceID("aws", acct.ID, gARN)
		if !guardSet[gID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, gID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert bedrock-agent→guardrail: %w", err)
		}
	}
	return nil
}

// resolveBedrockAgentAlias links each agent-alias row to its parent agent.
// Alias NativeID shape: arn:aws:bedrock:{r}:{a}:agent-alias/{agentID}/{aliasID}.
// Strip trailing /{aliasID} and swap segment to recover the agent ARN.
func resolveBedrockAgentAlias(acct *account, st *store.Store) error {
	aliases, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentAlias},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	agentSet, err := scannedIDSet(acct, st, TypeBedrockAgent)
	if err != nil {
		return err
	}
	for _, r := range aliases {
		// Drop trailing /{aliasID}, then swap agent-alias→agent to recover
		// the parent ARN (see shape above).
		idx := strings.LastIndex(r.NativeID, "/")
		if idx < 0 {
			continue
		}
		head := r.NativeID[:idx] // arn:...:agent-alias/{agentID}
		agentARN := strings.Replace(head, ":agent-alias/", ":agent/", 1)
		agentID := store.ResourceID("aws", acct.ID, agentARN)
		if !agentSet[agentID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, agentID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bedrock-agent-alias→agent: %w", err)
		}
	}
	return nil
}

// resolveBedrockDataSourceKB links each data-source to its knowledge-base
// via the KnowledgeBaseID field on DataSourceSummary.
func resolveBedrockDataSourceKB(acct *account, st *store.Store) error {
	dss, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockDataSource},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	kbSet, err := scannedIDSet(acct, st, TypeBedrockKnowledgeBase)
	if err != nil {
		return err
	}
	for _, r := range dss {
		var attrs struct {
			KnowledgeBaseID *string `json:"KnowledgeBaseId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		kb := sv(attrs.KnowledgeBaseID)
		if kb == "" {
			continue
		}
		kbID := store.ResourceID("aws", acct.ID,
			bedrockKBARN(sv(r.Region), acct.ID, kb))

		if !kbSet[kbID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, kbID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bedrock-data-source→kb: %w", err)
		}
	}
	return nil
}

// resolveBedrockGuardrailVersion links each guardrail-version row to its
// parent guardrail. NativeID shape: {guardrailARN}:{version}; strip the
// :{version} suffix to recover the parent ARN.
func resolveBedrockGuardrailVersion(acct *account, st *store.Store) error {
	return resolveBedrockVersionParent(acct, st,
		TypeBedrockGuardrailVersion, TypeBedrockGuardrail, "bedrock-guardrail-version→guardrail")
}

// resolveBedrockPromptVersion mirrors guardrail-version: NativeID is
// {promptARN}:{version}; strip suffix to find parent prompt.
func resolveBedrockPromptVersion(acct *account, st *store.Store) error {
	return resolveBedrockVersionParent(acct, st,
		TypeBedrockPromptVersion, TypeBedrockPrompt, "bedrock-prompt-version→prompt")
}

// resolveBedrockVersionParent is the shared body for guardrail-version and
// prompt-version — both types use a {parentARN}:{version} NativeID.
func resolveBedrockVersionParent(acct *account, st *store.Store, childType, parentType, label string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{childType},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	parentSet, err := scannedIDSet(acct, st, parentType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		// Trim the final ":<version>" suffix — ARNs have 5 colons before the
		// resource segment (arn:aws:svc:region:acct:resource), so the
		// version delimiter is the 6th (last) colon.
		idx := strings.LastIndex(r.NativeID, ":")
		if idx < 0 {
			continue
		}
		parentARN := r.NativeID[:idx]
		parentID := store.ResourceID("aws", acct.ID, parentARN)
		if !parentSet[parentID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, parentID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert %s: %w", label, err)
		}
	}
	return nil
}

// loadBedrockFlowIDIndex builds {flow-id → flow-row-ID} so flow-alias and
// flow-version resolvers can look up the parent flow without re-deriving
// its ARN. FlowSummary.ID is the SDK's logical id; the row's NativeID is
// the ARN.
func loadBedrockFlowIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	flows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockFlow},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(flows))
	for _, f := range flows {
		var attrs struct {
			ID *string `json:"Id"`
		}
		if json.Unmarshal([]byte(f.AttributesJSON), &attrs) != nil {
			continue
		}
		if id := sv(attrs.ID); id != "" {
			idx[id] = f.ID
		}
	}
	return idx, nil
}

// resolveBedrockFlowAlias links flow-alias → flow via FlowID on
// FlowAliasSummary.
func resolveBedrockFlowAlias(acct *account, st *store.Store) error {
	idx, err := loadBedrockFlowIDIndex(acct, st)
	if err != nil {
		return err
	}
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockFlowAlias},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			FlowID *string `json:"FlowId"`
		}
		if json.Unmarshal([]byte(r.AttributesJSON), &attrs) != nil {
			continue
		}
		fid := sv(attrs.FlowID)
		if fid == "" {
			continue
		}
		flowRowID, ok := idx[fid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, flowRowID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bedrock-flow-alias→flow: %w", err)
		}
	}
	return nil
}

// resolveBedrockFlowVersion links flow-version → flow. FlowVersionSummary
// has only ID (the parent flow's id) — same lookup as flow-alias but on
// the ID key rather than FlowID.
func resolveBedrockFlowVersion(acct *account, st *store.Store) error {
	idx, err := loadBedrockFlowIDIndex(acct, st)
	if err != nil {
		return err
	}
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockFlowVersion},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ID *string `json:"Id"`
		}
		if json.Unmarshal([]byte(r.AttributesJSON), &attrs) != nil {
			continue
		}
		fid := sv(attrs.ID)
		if fid == "" {
			continue
		}
		flowRowID, ok := idx[fid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, flowRowID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bedrock-flow-version→flow: %w", err)
		}
	}
	return nil
}
