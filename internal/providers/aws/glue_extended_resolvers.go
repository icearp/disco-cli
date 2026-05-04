package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveGlueTriggerWorkflow,
		EdgeDecl{TypeGlueTrigger, TypeGlueWorkflow, store.RelAttachedTo},
		EdgeDecl{TypeGlueTrigger, TypeGlueJob, store.RelRoutesTo},
		EdgeDecl{TypeGlueTrigger, TypeGlueCrawler, store.RelRoutesTo},
	)
	registerResolver(resolveGlueDevEndpointRefs,
		EdgeDecl{TypeGlueDevEndpoint, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeGlueDevEndpoint, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeGlueDevEndpoint, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeGlueDevEndpoint, TypeGlueSecurityConfiguration, store.RelUses},
	)
	registerResolver(resolveGlueMLTransformRefs,
		EdgeDecl{TypeGlueMLTransform, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeGlueMLTransform, TypeGlueDatabase, store.RelUses},
		EdgeDecl{TypeGlueMLTransform, TypeGlueTable, store.RelUses},
	)
	registerResolver(resolveGlueConnectionRefs,
		EdgeDecl{TypeGlueConnection, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeGlueConnection, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(resolveGlueSchemaRegistry,
		EdgeDecl{TypeGlueSchema, TypeGlueRegistry, store.RelAttachedTo},
	)
	registerResolver(resolveGlueSecurityConfigKMS,
		EdgeDecl{TypeGlueSecurityConfiguration, TypeKMSKey, store.RelUses},
	)
	registerResolver(resolveGlueDataCatalogEncryptionKMS,
		EdgeDecl{TypeGlueDataCatalogEncryptionSettings, TypeKMSKey, store.RelUses},
	)
	registerResolver(resolveGlueWorkflowGraphNodes,
		EdgeDecl{TypeGlueWorkflow, TypeGlueJob, store.RelContains},
		EdgeDecl{TypeGlueWorkflow, TypeGlueTrigger, store.RelContains},
		EdgeDecl{TypeGlueWorkflow, TypeGlueCrawler, store.RelContains},
	)
	registerResolver(resolveGlueIdentityCenterRefs,
		EdgeDecl{TypeGlueIdentityCenterConfiguration, TypeSSOInstance, store.RelUses},
	)
}

// resolveGlueTriggerWorkflow links a trigger to its workflow (WorkflowName)
// and to the jobs / crawlers fired by its Actions[].
func resolveGlueTriggerWorkflow(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueTrigger},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	wfSet, err := scannedIDSet(acct, st, TypeGlueWorkflow)
	if err != nil {
		return err
	}
	jobSet, err := scannedIDSet(acct, st, TypeGlueJob)
	if err != nil {
		return err
	}
	crawlerSet, err := scannedIDSet(acct, st, TypeGlueCrawler)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			WorkflowName *string `json:"WorkflowName"`
			Actions      []struct {
				JobName     *string `json:"JobName"`
				CrawlerName *string `json:"CrawlerName"`
			} `json:"Actions"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if name := sv(attrs.WorkflowName); name != "" {
			arn := glueResourceARN(region, acct.ID, "workflow", name)
			tgtID := store.ResourceID("aws", acct.ID, TypeGlueWorkflow, arn)
			if wfSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue-trigger→workflow: %w", err)
				}
			}
		}
		for _, a := range attrs.Actions {
			if name := sv(a.JobName); name != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeGlueJob, glueResourceARN(region, acct.ID, "job", name))
				if jobSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert glue-trigger→job: %w", err)
					}
				}
			}
			if name := sv(a.CrawlerName); name != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeGlueCrawler, glueResourceARN(region, acct.ID, "crawler", name))
				if crawlerSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert glue-trigger→crawler: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveGlueDevEndpointRefs walks each dev-endpoint's RoleArn, SubnetId,
// SecurityGroupIds[], and SecurityConfiguration name.
func resolveGlueDevEndpointRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueDevEndpoint},
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
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	scSet, err := scannedIDSet(acct, st, TypeGlueSecurityConfiguration)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RoleArn               *string  `json:"RoleArn"`
			SubnetId              *string  `json:"SubnetId"`
			SecurityGroupIds      []string `json:"SecurityGroupIds"`
			SecurityConfiguration *string  `json:"SecurityConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if arn := glueRoleARN(acct.ID, sv(attrs.RoleArn)); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue-dev-endpoint→role: %w", err)
				}
			}
		}
		if id := sv(attrs.SubnetId); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", id))
			if subnetSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue-dev-endpoint→subnet: %w", err)
				}
			}
		}
		for _, sg := range attrs.SecurityGroupIds {
			if sg == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
			if !sgSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert glue-dev-endpoint→sg: %w", err)
			}
		}
		if name := sv(attrs.SecurityConfiguration); name != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeGlueSecurityConfiguration, glueResourceARN(region, acct.ID, "securityConfiguration", name))
			if scSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue-dev-endpoint→security-config: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveGlueMLTransformRefs walks each ML transform's Role +
// InputRecordTables[] (database/table refs).
func resolveGlueMLTransformRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueMLTransform},
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
	dbSet, err := scannedIDSet(acct, st, TypeGlueDatabase)
	if err != nil {
		return err
	}
	tblSet, err := scannedIDSet(acct, st, TypeGlueTable)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Role              *string `json:"Role"`
			InputRecordTables []struct {
				DatabaseName *string `json:"DatabaseName"`
				TableName    *string `json:"TableName"`
			} `json:"InputRecordTables"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if arn := glueRoleARN(acct.ID, sv(attrs.Role)); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue-mlt→role: %w", err)
				}
			}
		}
		for _, t := range attrs.InputRecordTables {
			db := sv(t.DatabaseName)
			tbl := sv(t.TableName)
			if db != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeGlueDatabase, glueResourceARN(region, acct.ID, "database", db))
				if dbSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert glue-mlt→database: %w", err)
					}
				}
			}
			if db != "" && tbl != "" {
				tblARN := glueResourceARN(region, acct.ID, "table", db+"/"+tbl)
				tgtID := store.ResourceID("aws", acct.ID, TypeGlueTable, tblARN)
				if tblSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert glue-mlt→table: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveGlueConnectionRefs walks each connection's
// PhysicalConnectionRequirements.{SubnetId, SecurityGroupIdList[]}.
func resolveGlueConnectionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueConnection},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			PhysicalConnectionRequirements *struct {
				SubnetId            *string  `json:"SubnetId"`
				SecurityGroupIdList []string `json:"SecurityGroupIdList"`
			} `json:"PhysicalConnectionRequirements"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.PhysicalConnectionRequirements == nil {
			continue
		}
		region := sv(r.Region)
		if id := sv(attrs.PhysicalConnectionRequirements.SubnetId); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", id))
			if subnetSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue-conn→subnet: %w", err)
				}
			}
		}
		for _, sg := range attrs.PhysicalConnectionRequirements.SecurityGroupIdList {
			if sg == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
			if !sgSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert glue-conn→sg: %w", err)
			}
		}
	}
	return nil
}

// resolveGlueSchemaRegistry links each schema to its parent registry via
// `RegistryId.RegistryArn` (or RegistryName fallback).
func resolveGlueSchemaRegistry(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueSchema},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	regSet, err := scannedIDSet(acct, st, TypeGlueRegistry)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RegistryId *struct {
				RegistryName *string `json:"RegistryName"`
				RegistryArn  *string `json:"RegistryArn"`
			} `json:"RegistryId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.RegistryId == nil {
			continue
		}
		region := sv(r.Region)
		arn := sv(attrs.RegistryId.RegistryArn)
		if arn == "" {
			if name := sv(attrs.RegistryId.RegistryName); name != "" {
				arn = glueResourceARN(region, acct.ID, "registry", name)
			}
		}
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeGlueRegistry, arn)
		if !regSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert glue-schema→registry: %w", err)
		}
	}
	return nil
}

// resolveGlueSecurityConfigKMS walks each SecurityConfiguration's
// EncryptionConfiguration sub-blocks and emits KMS edges.
func resolveGlueSecurityConfigKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueSecurityConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			EncryptionConfiguration *struct {
				S3Encryption []struct {
					KmsKeyArn *string `json:"KmsKeyArn"`
				} `json:"S3Encryption"`
				CloudWatchEncryption *struct {
					KmsKeyArn *string `json:"KmsKeyArn"`
				} `json:"CloudWatchEncryption"`
				JobBookmarksEncryption *struct {
					KmsKeyArn *string `json:"KmsKeyArn"`
				} `json:"JobBookmarksEncryption"`
			} `json:"EncryptionConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.EncryptionConfiguration == nil {
			continue
		}
		region := sv(r.Region)
		seen := map[string]bool{}
		emit := func(ref string) error {
			if ref == "" {
				return nil
			}
			id, ok := kmsIdx.resolveKMSKeyID(ref, region, acct.ID)
			if !ok || seen[id] {
				return nil
			}
			seen[id] = true
			return st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil)
		}
		for _, s3 := range attrs.EncryptionConfiguration.S3Encryption {
			if err := emit(sv(s3.KmsKeyArn)); err != nil {
				return fmt.Errorf("upsert glue-sc→kms (s3): %w", err)
			}
		}
		if c := attrs.EncryptionConfiguration.CloudWatchEncryption; c != nil {
			if err := emit(sv(c.KmsKeyArn)); err != nil {
				return fmt.Errorf("upsert glue-sc→kms (cw): %w", err)
			}
		}
		if j := attrs.EncryptionConfiguration.JobBookmarksEncryption; j != nil {
			if err := emit(sv(j.KmsKeyArn)); err != nil {
				return fmt.Errorf("upsert glue-sc→kms (jb): %w", err)
			}
		}
	}
	return nil
}

// resolveGlueDataCatalogEncryptionKMS wires the per-region data-catalog
// encryption singleton to the KMS keys used for catalog and connection-password
// encryption (EncryptionAtRest.SseAwsKmsKeyId, ConnectionPasswordEncryption.AwsKmsKeyId).
func resolveGlueDataCatalogEncryptionKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueDataCatalogEncryptionSettings},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DataCatalogEncryptionSettings *struct {
				EncryptionAtRest *struct {
					SseAwsKmsKeyId *string `json:"SseAwsKmsKeyId"`
				} `json:"EncryptionAtRest"`
				ConnectionPasswordEncryption *struct {
					AwsKmsKeyId *string `json:"AwsKmsKeyId"`
				} `json:"ConnectionPasswordEncryption"`
			} `json:"DataCatalogEncryptionSettings"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DataCatalogEncryptionSettings == nil {
			continue
		}
		region := sv(r.Region)
		seen := map[string]bool{}
		emit := func(ref string) error {
			if ref == "" {
				return nil
			}
			id, ok := idx.resolveKMSKeyID(ref, region, acct.ID)
			if !ok || seen[id] {
				return nil
			}
			seen[id] = true
			return st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil)
		}
		if e := attrs.DataCatalogEncryptionSettings.EncryptionAtRest; e != nil {
			if err := emit(sv(e.SseAwsKmsKeyId)); err != nil {
				return fmt.Errorf("upsert glue data-catalog-encryption→kms: %w", err)
			}
		}
		if c := attrs.DataCatalogEncryptionSettings.ConnectionPasswordEncryption; c != nil {
			if err := emit(sv(c.AwsKmsKeyId)); err != nil {
				return fmt.Errorf("upsert glue data-catalog-encryption→kms: %w", err)
			}
		}
	}
	return nil
}

// resolveGlueWorkflowGraphNodes walks each workflow's Graph.Nodes[] and emits
// contains → job/trigger/crawler by Type discriminator. The JSON shape is the
// SDK Workflow struct as marshalled by mustJSON.
func resolveGlueWorkflowGraphNodes(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueWorkflow},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	jobSet, err := scannedIDSet(acct, st, TypeGlueJob)
	if err != nil {
		return err
	}
	trigSet, err := scannedIDSet(acct, st, TypeGlueTrigger)
	if err != nil {
		return err
	}
	crawlSet, err := scannedIDSet(acct, st, TypeGlueCrawler)
	if err != nil {
		return err
	}
	var pairs [][2]string
	for _, r := range rows {
		var attrs struct {
			Graph *struct {
				Nodes []struct {
					Name *string `json:"Name"`
					Type *string `json:"Type"`
				} `json:"Nodes"`
			} `json:"Graph"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Graph == nil {
			continue
		}
		region := sv(r.Region)
		seen := map[string]bool{}
		for _, n := range attrs.Graph.Nodes {
			name := sv(n.Name)
			if name == "" {
				continue
			}
			var childID string
			switch sv(n.Type) {
			case "JOB":
				childID = store.ResourceID("aws", acct.ID, TypeGlueJob, glueResourceARN(region, acct.ID, "job", name))
				if !jobSet[childID] {
					childID = ""
				}
			case "TRIGGER":
				childID = store.ResourceID("aws", acct.ID, TypeGlueTrigger, glueResourceARN(region, acct.ID, "trigger", name))
				if !trigSet[childID] {
					childID = ""
				}
			case "CRAWLER":
				childID = store.ResourceID("aws", acct.ID, TypeGlueCrawler, glueResourceARN(region, acct.ID, "crawler", name))
				if !crawlSet[childID] {
					childID = ""
				}
			}
			if childID == "" || seen[childID] {
				continue
			}
			seen[childID] = true
			pairs = append(pairs, [2]string{childID, r.ID})
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	return st.RecordHierarchyBatch(pairs)
}

// resolveGlueIdentityCenterRefs links the per-region Glue Identity Center
// configuration to its parent SSO instance (InstanceArn).
func resolveGlueIdentityCenterRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueIdentityCenterConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	ssoSet, err := scannedIDSet(acct, st, TypeSSOInstance)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			InstanceArn *string `json:"InstanceArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ia := sv(attrs.InstanceArn)
		if ia == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeSSOInstance, ia)
		if !ssoSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert glue identity-center→sso-instance: %w", err)
		}
	}
	return nil
}
