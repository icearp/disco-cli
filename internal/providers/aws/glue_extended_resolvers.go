package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveGlueTriggerWorkflow,
		EdgeDecl{TypeGlueTrigger, TypeGlueWorkflow, store.RelAttachedTo},
		EdgeDecl{TypeGlueTrigger, TypeGlueJob, store.RelRoutesTo},
		EdgeDecl{TypeGlueTrigger, TypeGlueCrawler, store.RelRoutesTo},
	)
	registerResolver(
		resolveGlueDevEndpointRefs,
		EdgeDecl{TypeGlueDevEndpoint, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeGlueDevEndpoint, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeGlueDevEndpoint, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeGlueDevEndpoint, TypeGlueSecurityConfiguration, store.RelUses},
	)
	registerResolver(
		resolveGlueMLTransformRefs,
		EdgeDecl{TypeGlueMLTransform, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeGlueMLTransform, TypeGlueDatabase, store.RelUses},
		EdgeDecl{TypeGlueMLTransform, TypeGlueTable, store.RelUses},
	)
	registerResolver(
		resolveGlueConnectionRefs,
		EdgeDecl{TypeGlueConnection, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeGlueConnection, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveGlueSchemaRegistry,
		EdgeDecl{TypeGlueSchema, TypeGlueRegistry, store.RelAttachedTo},
	)
	registerResolver(
		resolveGlueSecurityConfigKMS,
		EdgeDecl{TypeGlueSecurityConfiguration, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveGlueDataCatalogEncryptionKMS,
		EdgeDecl{TypeGlueDataCatalogEncryptionSettings, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveGlueWorkflowGraphNodes,
		EdgeDecl{TypeGlueWorkflow, TypeGlueJob, store.RelContains},
		EdgeDecl{TypeGlueWorkflow, TypeGlueTrigger, store.RelContains},
		EdgeDecl{TypeGlueWorkflow, TypeGlueCrawler, store.RelContains},
	)
	registerResolver(
		resolveGlueIdentityCenterRefs,
		EdgeDecl{TypeGlueIdentityCenterConfiguration, TypeSSOInstance, store.RelUses},
	)
}

// glueTriggerAttrs mirrors the trigger fields the resolver walks.
type glueTriggerAttrs struct {
	WorkflowName *string `json:"WorkflowName"`
	Actions      []struct {
		JobName     *string `json:"JobName"`
		CrawlerName *string `json:"CrawlerName"`
	} `json:"Actions"`
}

// resolveGlueTriggerWorkflow links a trigger to its workflow (WorkflowName)
// and to the jobs / crawlers fired by its Actions[].
func resolveGlueTriggerWorkflow(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueTrigger},
		Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
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
		var attrs glueTriggerAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitGlueTriggerWorkflowEdge(st, acct, r, region, attrs, wfSet); err != nil {
			return err
		}
		if err := emitGlueTriggerActionEdges(st, acct, r, region, attrs, jobSet, crawlerSet); err != nil {
			return err
		}
	}
	return nil
}

func emitGlueTriggerWorkflowEdge(st *store.Store, acct *account, r store.Resource, region string, attrs glueTriggerAttrs, wfSet map[string]bool) error {
	name := sv(attrs.WorkflowName)
	if name == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acct.ID, TypeGlueWorkflow, glueResourceARN(region, acct.ID, "workflow", name))
	if !wfSet[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
		return fmt.Errorf("upsert glue-trigger→workflow: %w", err)
	}
	return nil
}

func emitGlueTriggerActionEdges(st *store.Store, acct *account, r store.Resource, region string, attrs glueTriggerAttrs, jobSet, crawlerSet map[string]bool) error {
	for _, a := range attrs.Actions {
		if err := emitGlueTriggerActionEdge(st, acct, r, region, sv(a.JobName), TypeGlueJob, "job", jobSet); err != nil {
			return err
		}
		if err := emitGlueTriggerActionEdge(st, acct, r, region, sv(a.CrawlerName), TypeGlueCrawler, "crawler", crawlerSet); err != nil {
			return err
		}
	}
	return nil
}

func emitGlueTriggerActionEdge(st *store.Store, acct *account, r store.Resource, region, name, rtype, arnKind string, set map[string]bool) error {
	if name == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acct.ID, rtype, glueResourceARN(region, acct.ID, arnKind, name))
	if !set[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
		return fmt.Errorf("upsert glue-trigger→%s: %w", arnKind, err)
	}
	return nil
}

// glueDevEndpointAttrs mirrors the dev-endpoint fields the resolver walks.
type glueDevEndpointAttrs struct {
	RoleArn               *string  `json:"RoleArn"`
	SubnetID              *string  `json:"SubnetId"`
	SecurityGroupIDs      []string `json:"SecurityGroupIds"`
	SecurityConfiguration *string  `json:"SecurityConfiguration"`
}

// glueDevEndpointTargetSets bundles FK-safe id sets for the dev-endpoint resolver.
type glueDevEndpointTargetSets struct {
	roleSet, subnetSet, sgSet, scSet map[string]bool
}

// resolveGlueDevEndpointRefs walks each dev-endpoint's RoleArn, SubnetID,
// SecurityGroupIDs[], and SecurityConfiguration name.
func resolveGlueDevEndpointRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueDevEndpoint},
		Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	sets, err := loadGlueDevEndpointTargetSets(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs glueDevEndpointAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitGlueDevEndpointRoleEdge(st, acct, r, attrs, sets); err != nil {
			return err
		}
		if err := emitGlueDevEndpointSubnetEdge(st, acct, r, region, attrs, sets); err != nil {
			return err
		}
		if err := emitGlueDevEndpointSGEdges(st, acct, r, region, attrs, sets); err != nil {
			return err
		}
		if err := emitGlueDevEndpointSecConfigEdge(st, acct, r, region, attrs, sets); err != nil {
			return err
		}
	}
	return nil
}

func loadGlueDevEndpointTargetSets(acct *account, st *store.Store) (glueDevEndpointTargetSets, error) {
	var sets glueDevEndpointTargetSets
	var err error
	if sets.roleSet, err = scannedIDSet(acct, st, TypeIAMRole); err != nil {
		return sets, err
	}
	if sets.subnetSet, err = scannedIDSet(acct, st, TypeEC2Subnet); err != nil {
		return sets, err
	}
	if sets.sgSet, err = scannedIDSet(acct, st, TypeEC2SecurityGroup); err != nil {
		return sets, err
	}
	if sets.scSet, err = scannedIDSet(acct, st, TypeGlueSecurityConfiguration); err != nil {
		return sets, err
	}
	return sets, nil
}

func emitGlueDevEndpointRoleEdge(st *store.Store, acct *account, r store.Resource, attrs glueDevEndpointAttrs, sets glueDevEndpointTargetSets) error {
	arn := glueRoleARN(acct.ID, sv(attrs.RoleArn))
	if arn == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
	if !sets.roleSet[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
		return fmt.Errorf("upsert glue-dev-endpoint→role: %w", err)
	}
	return nil
}

func emitGlueDevEndpointSubnetEdge(st *store.Store, acct *account, r store.Resource, region string, attrs glueDevEndpointAttrs, sets glueDevEndpointTargetSets) error {
	id := sv(attrs.SubnetID)
	if id == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", id))
	if !sets.subnetSet[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
		return fmt.Errorf("upsert glue-dev-endpoint→subnet: %w", err)
	}
	return nil
}

func emitGlueDevEndpointSGEdges(st *store.Store, acct *account, r store.Resource, region string, attrs glueDevEndpointAttrs, sets glueDevEndpointTargetSets) error {
	for _, sg := range attrs.SecurityGroupIDs {
		if sg == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
		if !sets.sgSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert glue-dev-endpoint→sg: %w", err)
		}
	}
	return nil
}

func emitGlueDevEndpointSecConfigEdge(st *store.Store, acct *account, r store.Resource, region string, attrs glueDevEndpointAttrs, sets glueDevEndpointTargetSets) error {
	name := sv(attrs.SecurityConfiguration)
	if name == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acct.ID, TypeGlueSecurityConfiguration, glueResourceARN(region, acct.ID, "securityConfiguration", name))
	if !sets.scSet[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert glue-dev-endpoint→security-config: %w", err)
	}
	return nil
}

// glueMLTAttrs mirrors the ML-transform fields the resolver walks.
type glueMLTAttrs struct {
	Role              *string `json:"Role"`
	InputRecordTables []struct {
		DatabaseName *string `json:"DatabaseName"`
		TableName    *string `json:"TableName"`
	} `json:"InputRecordTables"`
}

// resolveGlueMLTransformRefs walks each ML transform's Role +
// InputRecordTables[] (database/table refs).
func resolveGlueMLTransformRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueMLTransform},
		Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
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
		var attrs glueMLTAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitGlueMLTRoleEdge(st, acct, r, attrs, roleSet); err != nil {
			return err
		}
		if err := emitGlueMLTTableEdges(st, acct, r, region, attrs, dbSet, tblSet); err != nil {
			return err
		}
	}
	return nil
}

func emitGlueMLTRoleEdge(st *store.Store, acct *account, r store.Resource, attrs glueMLTAttrs, roleSet map[string]bool) error {
	arn := glueRoleARN(acct.ID, sv(attrs.Role))
	if arn == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
	if !roleSet[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
		return fmt.Errorf("upsert glue-mlt→role: %w", err)
	}
	return nil
}

func emitGlueMLTTableEdges(st *store.Store, acct *account, r store.Resource, region string, attrs glueMLTAttrs, dbSet, tblSet map[string]bool) error {
	for _, t := range attrs.InputRecordTables {
		db := sv(t.DatabaseName)
		tbl := sv(t.TableName)
		if db == "" {
			continue
		}
		dbID := store.ResourceID("aws", acct.ID, TypeGlueDatabase, glueResourceARN(region, acct.ID, "database", db))
		if dbSet[dbID] {
			if err := st.UpsertRelationship(r.ID, dbID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert glue-mlt→database: %w", err)
			}
		}
		if tbl == "" {
			continue
		}
		tblID := store.ResourceID("aws", acct.ID, TypeGlueTable, glueResourceARN(region, acct.ID, "table", db+"/"+tbl))
		if !tblSet[tblID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tblID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert glue-mlt→table: %w", err)
		}
	}
	return nil
}

// resolveGlueConnectionRefs walks each connection's
// PhysicalConnectionRequirements.{SubnetID, SecurityGroupIDList[]}.
func resolveGlueConnectionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueConnection},
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
				SubnetID            *string  `json:"SubnetId"`
				SecurityGroupIDList []string `json:"SecurityGroupIdList"`
			} `json:"PhysicalConnectionRequirements"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.PhysicalConnectionRequirements == nil {
			continue
		}
		region := sv(r.Region)
		if id := sv(attrs.PhysicalConnectionRequirements.SubnetID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", id))
			if subnetSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue-conn→subnet: %w", err)
				}
			}
		}
		for _, sg := range attrs.PhysicalConnectionRequirements.SecurityGroupIDList {
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
// `RegistryID.RegistryArn` (or RegistryName fallback).
func resolveGlueSchemaRegistry(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueSchema},
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
			RegistryID *struct {
				RegistryName *string `json:"RegistryName"`
				RegistryArn  *string `json:"RegistryArn"`
			} `json:"RegistryId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.RegistryID == nil {
			continue
		}
		region := sv(r.Region)
		arn := sv(attrs.RegistryID.RegistryArn)
		if arn == "" {
			if name := sv(attrs.RegistryID.RegistryName); name != "" {
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

// glueSecConfigAttrs mirrors the security-config encryption sub-blocks.
type glueSecConfigAttrs struct {
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

// resolveGlueSecurityConfigKMS walks each SecurityConfiguration's
// EncryptionConfiguration sub-blocks and emits KMS edges.
func resolveGlueSecurityConfigKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueSecurityConfiguration},
		Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs glueSecConfigAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.EncryptionConfiguration == nil {
			continue
		}
		if err := emitGlueSecConfigKMSEdges(st, acct, r, attrs, kmsIdx); err != nil {
			return err
		}
	}
	return nil
}

func emitGlueSecConfigKMSEdges(st *store.Store, acct *account, r store.Resource, attrs glueSecConfigAttrs, kmsIdx *kmsResolveIndex) error {
	region := sv(r.Region)
	seen := map[string]bool{}
	emit := func(ref, label string) error {
		if ref == "" {
			return nil
		}
		id, ok := kmsIdx.resolveKMSKeyID(ref, region, acct.ID)
		if !ok || seen[id] {
			return nil
		}
		seen[id] = true
		if err := st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert glue-sc→kms (%s): %w", label, err)
		}
		return nil
	}
	for _, s3 := range attrs.EncryptionConfiguration.S3Encryption {
		if err := emit(sv(s3.KmsKeyArn), "s3"); err != nil {
			return err
		}
	}
	if c := attrs.EncryptionConfiguration.CloudWatchEncryption; c != nil {
		if err := emit(sv(c.KmsKeyArn), "cw"); err != nil {
			return err
		}
	}
	if j := attrs.EncryptionConfiguration.JobBookmarksEncryption; j != nil {
		if err := emit(sv(j.KmsKeyArn), "jb"); err != nil {
			return err
		}
	}
	return nil
}

// resolveGlueDataCatalogEncryptionKMS wires the per-region data-catalog
// encryption singleton to the KMS keys used for catalog and connection-password
// encryption (EncryptionAtRest.SseAwsKmsKeyID, ConnectionPasswordEncryption.AwsKmsKeyID).
func resolveGlueDataCatalogEncryptionKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueDataCatalogEncryptionSettings},
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
					SseAwsKmsKeyID *string `json:"SseAwsKmsKeyId"`
				} `json:"EncryptionAtRest"`
				ConnectionPasswordEncryption *struct {
					AwsKmsKeyID *string `json:"AwsKmsKeyId"`
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
			if err := emit(sv(e.SseAwsKmsKeyID)); err != nil {
				return fmt.Errorf("upsert glue data-catalog-encryption→kms: %w", err)
			}
		}
		if c := attrs.DataCatalogEncryptionSettings.ConnectionPasswordEncryption; c != nil {
			if err := emit(sv(c.AwsKmsKeyID)); err != nil {
				return fmt.Errorf("upsert glue data-catalog-encryption→kms: %w", err)
			}
		}
	}
	return nil
}

// glueWorkflowNodeKind dispatches Type discriminator → (rtype, arnKind, set).
type glueWorkflowNodeKind struct {
	rtype, arnKind string
	set            map[string]bool
}

// resolveGlueWorkflowGraphNodes walks each workflow's Graph.Nodes[] and emits
// contains → job/trigger/crawler by Type discriminator. The JSON shape is the
// SDK Workflow struct as marshalled by mustJSON.
func resolveGlueWorkflowGraphNodes(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueWorkflow},
		Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
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
	kinds := map[string]glueWorkflowNodeKind{
		"JOB":     {TypeGlueJob, "job", jobSet},
		"TRIGGER": {TypeGlueTrigger, "trigger", trigSet},
		"CRAWLER": {TypeGlueCrawler, "crawler", crawlSet},
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
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Graph == nil {
			continue
		}
		region := sv(r.Region)
		seen := map[string]bool{}
		for _, n := range attrs.Graph.Nodes {
			name := sv(n.Name)
			kind, ok := kinds[sv(n.Type)]
			if name == "" || !ok {
				continue
			}
			childID := store.ResourceID("aws", acct.ID, kind.rtype, glueResourceARN(region, acct.ID, kind.arnKind, name))
			if !kind.set[childID] || seen[childID] {
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueIdentityCenterConfiguration},
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
