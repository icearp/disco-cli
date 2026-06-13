package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveDMSEndpointRefs,
		EdgeDecl{TypeDMSEndpoint, TypeDMSCertificate, store.RelUses},
		EdgeDecl{TypeDMSEndpoint, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveDMSReplicationInstanceRefs,
		EdgeDecl{TypeDMSReplicationInstance, TypeDMSReplicationSubnetGroup, store.RelAttachedTo},
		EdgeDecl{TypeDMSReplicationInstance, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeDMSReplicationInstance, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveDMSReplicationSubnetGroupRefs,
		EdgeDecl{TypeDMSReplicationSubnetGroup, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeDMSReplicationSubnetGroup, TypeEC2Subnet, store.RelAttachedTo},
	)
	registerResolver(
		resolveDMSReplicationTaskRefs,
		EdgeDecl{TypeDMSReplicationTask, TypeDMSReplicationInstance, store.RelAttachedTo},
		EdgeDecl{TypeDMSReplicationTask, TypeDMSEndpoint, store.RelUses},
	)
	registerResolver(
		resolveDMSReplicationConfigRefs,
		EdgeDecl{TypeDMSReplicationConfig, TypeDMSEndpoint, store.RelUses},
	)
	registerResolver(
		resolveDMSDataMigrationRefs,
		EdgeDecl{TypeDMSDataMigration, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeDMSDataMigration, TypeDMSMigrationProject, store.RelAttachedTo},
	)
	registerResolver(
		resolveDMSMigrationProjectRefs,
		EdgeDecl{TypeDMSMigrationProject, TypeDMSInstanceProfile, store.RelAttachedTo},
		EdgeDecl{TypeDMSMigrationProject, TypeDMSDataProvider, store.RelUses},
	)
	registerResolver(
		resolveDMSInstanceProfileRefs,
		EdgeDecl{TypeDMSInstanceProfile, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeDMSInstanceProfile, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveDMSEventSubscriptionTopic,
		EdgeDecl{TypeDMSEventSubscription, TypeSNSTopic, store.RelRoutesTo},
	)
}

func resolveDMSEndpointRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDMSEndpoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	certSet, err := scannedIDSet(acct, st, TypeDMSCertificate)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			CertificateArn *string `json:"CertificateArn"`
			KmsKeyID       *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if arn := sv(attrs.CertificateArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeDMSCertificate, arn)
			if certSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert dms-endpoint→cert: %w", err)
				}
			}
		}
		if id, ok := kmsIdx.resolveKMSKeyID(sv(attrs.KmsKeyID), region, acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert dms-endpoint→kms: %w", err)
			}
		}
	}
	return nil
}

// dmsReplicationSubnetGroupARNFromName synthesizes the subnet group ARN
// from its identifier. DMS API exposes the bare identifier on
// ReplicationInstance; the synth shape is
// `arn:aws:dms:{region}:{acct}:subgrp:{identifier}`.
func dmsReplicationSubnetGroupARNFromName(region, acctID, name string) string {
	return fmt.Sprintf("arn:aws:dms:%s:%s:subgrp:%s", region, acctID, name)
}

func resolveDMSReplicationInstanceRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDMSReplicationInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rsgSet, err := scannedIDSet(acct, st, TypeDMSReplicationSubnetGroup)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyID               *string `json:"KmsKeyId"`
			ReplicationSubnetGroup *struct {
				ReplicationSubnetGroupIdentifier *string `json:"ReplicationSubnetGroupIdentifier"`
			} `json:"ReplicationSubnetGroup"`
			VpcSecurityGroups []struct {
				VpcSecurityGroupID *string `json:"VpcSecurityGroupId"`
			} `json:"VpcSecurityGroups"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if id, ok := kmsIdx.resolveKMSKeyID(sv(attrs.KmsKeyID), region, acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert dms-ri→kms: %w", err)
			}
		}
		if attrs.ReplicationSubnetGroup != nil {
			if name := sv(attrs.ReplicationSubnetGroup.ReplicationSubnetGroupIdentifier); name != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeDMSReplicationSubnetGroup, dmsReplicationSubnetGroupARNFromName(region, acct.ID, name))
				if rsgSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert dms-ri→rsg: %w", err)
					}
				}
			}
		}
		for _, sg := range attrs.VpcSecurityGroups {
			id := sv(sg.VpcSecurityGroupID)
			if id == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", id))
			if !sgSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert dms-ri→sg: %w", err)
			}
		}
	}
	return nil
}

func resolveDMSReplicationSubnetGroupRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDMSReplicationSubnetGroup},
		Limit: util.AllResources,
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
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VpcID   *string `json:"VpcId"`
			Subnets []struct {
				SubnetIdentifier *string `json:"SubnetIdentifier"`
			} `json:"Subnets"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if id := sv(attrs.VpcID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", id))
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert dms-rsg→vpc: %w", err)
				}
			}
		}
		for _, s := range attrs.Subnets {
			id := sv(s.SubnetIdentifier)
			if id == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", id))
			if !subnetSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert dms-rsg→subnet: %w", err)
			}
		}
	}
	return nil
}

func resolveDMSReplicationTaskRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDMSReplicationTask},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	riSet, err := scannedIDSet(acct, st, TypeDMSReplicationInstance)
	if err != nil {
		return err
	}
	epSet, err := scannedIDSet(acct, st, TypeDMSEndpoint)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ReplicationInstanceArn *string `json:"ReplicationInstanceArn"`
			SourceEndpointArn      *string `json:"SourceEndpointArn"`
			TargetEndpointArn      *string `json:"TargetEndpointArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if arn := sv(attrs.ReplicationInstanceArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeDMSReplicationInstance, arn)
			if riSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert dms-rt→ri: %w", err)
				}
			}
		}
		for _, arn := range []string{sv(attrs.SourceEndpointArn), sv(attrs.TargetEndpointArn)} {
			if arn == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeDMSEndpoint, arn)
			if !epSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert dms-rt→endpoint: %w", err)
			}
		}
	}
	return nil
}

func resolveDMSReplicationConfigRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDMSReplicationConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	epSet, err := scannedIDSet(acct, st, TypeDMSEndpoint)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SourceEndpointArn *string `json:"SourceEndpointArn"`
			TargetEndpointArn *string `json:"TargetEndpointArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, arn := range []string{sv(attrs.SourceEndpointArn), sv(attrs.TargetEndpointArn)} {
			if arn == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeDMSEndpoint, arn)
			if !epSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert dms-rc→endpoint: %w", err)
			}
		}
	}
	return nil
}

func resolveDMSDataMigrationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDMSDataMigration},
		Limit: util.AllResources,
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
	mpSet, err := scannedIDSet(acct, st, TypeDMSMigrationProject)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ServiceAccessRoleArn *string `json:"ServiceAccessRoleArn"`
			MigrationProjectArn  *string `json:"MigrationProjectArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if arn := sv(attrs.ServiceAccessRoleArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert dms-dm→role: %w", err)
				}
			}
		}
		if arn := sv(attrs.MigrationProjectArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeDMSMigrationProject, arn)
			if mpSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert dms-dm→mp: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveDMSMigrationProjectRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDMSMigrationProject},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	ipSet, err := scannedIDSet(acct, st, TypeDMSInstanceProfile)
	if err != nil {
		return err
	}
	dpSet, err := scannedIDSet(acct, st, TypeDMSDataProvider)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			InstanceProfileArn            *string `json:"InstanceProfileArn"`
			SourceDataProviderDescriptors []struct {
				DataProviderArn *string `json:"DataProviderArn"`
			} `json:"SourceDataProviderDescriptors"`
			TargetDataProviderDescriptors []struct {
				DataProviderArn *string `json:"DataProviderArn"`
			} `json:"TargetDataProviderDescriptors"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if arn := sv(attrs.InstanceProfileArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeDMSInstanceProfile, arn)
			if ipSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert dms-mp→ip: %w", err)
				}
			}
		}
		for _, d := range append(attrs.SourceDataProviderDescriptors, attrs.TargetDataProviderDescriptors...) {
			arn := sv(d.DataProviderArn)
			if arn == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeDMSDataProvider, arn)
			if !dpSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert dms-mp→dp: %w", err)
			}
		}
	}
	return nil
}

func resolveDMSInstanceProfileRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDMSInstanceProfile},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyArn          *string  `json:"KmsKeyArn"`
			VpcSecurityGroupID *string  `json:"VpcSecurityGroupId"`
			VpcSecurityGroups  []string `json:"VpcSecurityGroups"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if id, ok := kmsIdx.resolveKMSKeyID(sv(attrs.KmsKeyArn), region, acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert dms-ip→kms: %w", err)
			}
		}
		sgIDs := append([]string{}, attrs.VpcSecurityGroups...)
		if id := sv(attrs.VpcSecurityGroupID); id != "" {
			sgIDs = append(sgIDs, id)
		}
		for _, id := range sgIDs {
			if id == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", id))
			if !sgSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert dms-ip→sg: %w", err)
			}
		}
	}
	return nil
}

func resolveDMSEventSubscriptionTopic(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDMSEventSubscription},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	topicSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SnsTopicArn *string `json:"SnsTopicArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.SnsTopicArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeSNSTopic, arn)
		if !topicSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert dms-es→sns: %w", err)
		}
	}
	return nil
}
