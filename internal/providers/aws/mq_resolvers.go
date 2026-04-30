package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveMQRelationships) }

// resolveMQRelationships emits broker → KMS / security-group / subnet /
// configuration edges. ConfigurationId.Id (broker side) maps to Configuration.Id
// (configuration side) — build (acct, region, configId) → configuration ARN
// index from scanned configurations to recover the ARN-keyed NativeID.
func resolveMQRelationships(acct *account, st *store.Store) error {
	brokers, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMQBroker},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(brokers) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	sgIDs, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	subnetIDs, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	configIdxByID, err := mqConfigIndex(acct, st)
	if err != nil {
		return err
	}

	type encOpts struct {
		KmsKeyID *string `json:"KmsKeyId"`
	}
	type cfgID struct {
		ID *string `json:"Id"`
	}
	type cfgs struct {
		Current *cfgID `json:"Current"`
	}
	type attrs struct {
		BrokerArn         *string  `json:"BrokerArn"`
		EncryptionOptions *encOpts `json:"EncryptionOptions"`
		Configurations    *cfgs    `json:"Configurations"`
		SecurityGroups    []string `json:"SecurityGroups"`
		SubnetIDs         []string `json:"SubnetIds"`
	}
	for _, b := range brokers {
		var a attrs
		if err := json.Unmarshal([]byte(b.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(b.Region)
		if a.EncryptionOptions != nil {
			if keyID, ok := kmsIdx.resolveKMSKeyID(sv(a.EncryptionOptions.KmsKeyID), region, acct.ID); ok {
				if err := st.UpsertRelationship(b.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert mq-broker→kms: %w", err)
				}
			}
		}
		for _, sgID := range a.SecurityGroups {
			if sgID == "" {
				continue
			}
			sgARN := ec2ARN(region, acct.ID, "security-group", sgID)
			id := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sgARN)
			if _, ok := sgIDs[id]; ok {
				if err := st.UpsertRelationship(b.ID, id, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert mq-broker→sg: %w", err)
				}
			}
		}
		for _, subnetID := range a.SubnetIDs {
			if subnetID == "" {
				continue
			}
			subnetARN := ec2ARN(region, acct.ID, "subnet", subnetID)
			id := store.ResourceID("aws", acct.ID, TypeEC2Subnet, subnetARN)
			if _, ok := subnetIDs[id]; ok {
				if err := st.UpsertRelationship(b.ID, id, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert mq-broker→subnet: %w", err)
				}
			}
		}
		if a.Configurations != nil && a.Configurations.Current != nil {
			cfgID := sv(a.Configurations.Current.ID)
			if cfgARN, ok := configIdxByID[cfgID]; ok {
				targetID := store.ResourceID("aws", acct.ID, TypeMQConfiguration, cfgARN)
				if err := st.UpsertRelationship(b.ID, targetID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert mq-broker→config: %w", err)
				}
			}
		}
	}
	return nil
}

// mqConfigIndex maps Configuration.Id (the AWS-issued opaque ID brokers refer
// to) → Configuration.Arn for every scanned MQ configuration in the account.
// Brokers reference configurations by Id; the store keys by Arn.
func mqConfigIndex(acct *account, st *store.Store) (map[string]string, error) {
	configs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMQConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(configs))
	type cfgAttrs struct {
		ID  *string `json:"Id"`
		Arn *string `json:"Arn"`
	}
	for _, c := range configs {
		var a cfgAttrs
		if err := json.Unmarshal([]byte(c.AttributesJSON), &a); err != nil {
			continue
		}
		if id := sv(a.ID); id != "" {
			idx[id] = sv(a.Arn)
		}
	}
	return idx, nil
}
