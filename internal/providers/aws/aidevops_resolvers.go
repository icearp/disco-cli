package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveAidevopsRefs,
		EdgeDecl{TypeAidevopsAgentSpace, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeAidevopsService, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeAidevopsAssociations, TypeAidevopsAgentSpace, store.RelAttachedTo},
		EdgeDecl{TypeAidevopsAssociations, TypeAidevopsService, store.RelUses},
		EdgeDecl{TypeAidevopsPrivateConnection, TypeEC2VPC, store.RelAttachedTo},
	)
}

// resolveAidevopsRefs wires the AI DevOps agent graph: agent spaces and
// registered services to their KMS CMK; each association to its agent space and
// service; private connections to their VPC.
func resolveAidevopsRefs(acct *account, st *store.Store) error {
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	if err := resolveAidevopsKMS(acct, st, kmsIdx, TypeAidevopsAgentSpace); err != nil {
		return err
	}
	if err := resolveAidevopsKMS(acct, st, kmsIdx, TypeAidevopsService); err != nil {
		return err
	}
	if err := resolveAidevopsAssociations(acct, st); err != nil {
		return err
	}
	return resolveAidevopsPrivateConnVPC(acct, st)
}

func resolveAidevopsKMS(acct *account, st *store.Store, kmsIdx *kmsResolveIndex, rtype string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyArn *string `json:"KmsKeyArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.KmsKeyArn) == "" {
			continue
		}
		keyID, ok := kmsIdx.resolveKMSKeyID(sv(attrs.KmsKeyArn), sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert aidevops→kms: %w", err)
		}
	}
	return nil
}

func resolveAidevopsAssociations(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAidevopsAssociations}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	spaceSet, err := scannedIDSet(acct, st, TypeAidevopsAgentSpace)
	if err != nil {
		return err
	}
	svcSet, err := scannedIDSet(acct, st, TypeAidevopsService)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AgentSpaceID *string `json:"AgentSpaceId"`
			ServiceID    *string `json:"ServiceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if id := sv(attrs.AgentSpaceID); id != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeAidevopsAgentSpace, aidevopsARN(region, acct.ID, "agent-space", id))
			if spaceSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert aidevops-assoc→agent-space: %w", err)
				}
			}
		}
		if id := sv(attrs.ServiceID); id != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeAidevopsService, aidevopsARN(region, acct.ID, "service", id))
			if svcSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert aidevops-assoc→service: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveAidevopsPrivateConnVPC(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAidevopsPrivateConnection}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VpcID *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.VpcID) == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", sv(attrs.VpcID)))
		if !vpcSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert aidevops-private-conn→vpc: %w", err)
		}
	}
	return nil
}
