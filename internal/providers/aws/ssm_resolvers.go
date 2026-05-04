package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveSSMRelationships,
		EdgeDecl{TypeSSMParameter, TypeKMSKey, store.RelUses},
	)
	registerResolver(resolveSSMAssociationDocument,
		EdgeDecl{TypeSSMAssociation, TypeSSMDocument, store.RelUses},
	)
	registerResolver(resolveSSMMaintenanceWindowTargetParent,
		EdgeDecl{TypeSSMMaintenanceWindowTarget, TypeSSMMaintenanceWindow, store.RelAttachedTo},
	)
	registerResolver(resolveSSMMaintenanceWindowTaskRefs,
		EdgeDecl{TypeSSMMaintenanceWindowTask, TypeSSMMaintenanceWindow, store.RelAttachedTo},
		EdgeDecl{TypeSSMMaintenanceWindowTask, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSSMMaintenanceWindowTask, TypeLambdaFunction, store.RelRoutesTo},
		EdgeDecl{TypeSSMMaintenanceWindowTask, TypeSFNStateMachine, store.RelRoutesTo},
	)
	registerResolver(resolveSSMResourceDataSyncRefs,
		EdgeDecl{TypeSSMResourceDataSync, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeSSMResourceDataSync, TypeKMSKey, store.RelUses},
	)
	registerResolver(resolveSSMDocumentRequires,
		EdgeDecl{TypeSSMDocument, TypeSSMDocument, store.RelUses},
	)
}

// resolveSSMDocumentRequires walks each customer-owned SSM document's
// `Requires[]` (DocumentDescription field, populated by Phase-1 DescribeDocument
// enrichment in scanSSMAll). Each entry names a sibling SSM document by name
// or ARN; map to in-region NativeID + emit `uses` edge. FK-safe.
func resolveSSMDocumentRequires(acct *account, st *store.Store) error {
	docs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMDocument},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	docByNameRegion := map[string]string{}
	for _, d := range docs {
		if name := sv(d.Name); name != "" {
			docByNameRegion[sv(d.Region)+"|"+name] = d.ID
		}
	}
	for _, d := range docs {
		var attrs struct {
			Requires []struct {
				Name *string `json:"Name"`
			} `json:"Requires"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, req := range attrs.Requires {
			name := sv(req.Name)
			if name == "" {
				continue
			}
			// Field accepts ARN or bare name; strip any leading
			// `arn:aws:ssm:...:document/` prefix to recover bare name.
			if i := strings.Index(name, ":document/"); i >= 0 {
				name = name[i+len(":document/"):]
			}
			tgtID, ok := docByNameRegion[sv(d.Region)+"|"+name]
			if !ok || tgtID == d.ID {
				continue
			}
			if err := st.UpsertRelationship(d.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ssm document→requires: %w", err)
			}
		}
	}
	return nil
}

// resolveSSMRelationships emits edges for SecureString parameters → KMS keys.
// Alias-name references are normalized to the underlying key via the KMS index
// so the edge always points at the canonical key resource.
func resolveSSMRelationships(acct *account, st *store.Store) error {
	params, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMParameter},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	type attrs struct {
		KeyID *string `json:"KeyID"`
		Type  string  `json:"Type"`
	}
	for _, r := range params {
		var a attrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if a.Type != "SecureString" {
			continue
		}
		region := ""
		if r.Region != nil {
			region = *r.Region
		}
		keyID, ok := kmsIdx.resolveKMSKeyID(sv(a.KeyID), region, acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert ssm-parameter→kms: %w", err)
		}
	}
	return nil
}

// resolveSSMAssociationDocument links each association to the SSM document it
// runs (Name field — bare document name; build the per-region document NativeID
// to look up).
func resolveSSMAssociationDocument(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	docByNameRegion := map[string]string{}
	docs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMDocument}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, d := range docs {
		if name := sv(d.Name); name != "" {
			docByNameRegion[sv(d.Region)+"|"+name] = d.ID
		}
	}
	for _, r := range rows {
		var attrs struct {
			Name *string `json:"Name"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		name := sv(attrs.Name)
		if name == "" {
			continue
		}
		// AWS-managed documents (AWS-RunShellScript etc.) live in every region but
		// may not be scanned; FK-safe lookup skips when missing.
		tgtID, ok := docByNameRegion[sv(r.Region)+"|"+name]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert ssm association→document: %w", err)
		}
	}
	return nil
}

// resolveSSMMaintenanceWindowTargetParent wires each mw-target to its parent
// maintenance-window via WindowId.
func resolveSSMMaintenanceWindowTargetParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMMaintenanceWindowTarget}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	mwSet, err := scannedIDSet(acct, st, TypeSSMMaintenanceWindow)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			WindowId *string `json:"WindowId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		wid := sv(attrs.WindowId)
		if wid == "" {
			continue
		}
		region := sv(r.Region)
		mwARN := fmt.Sprintf("arn:aws:ssm:%s:%s:maintenancewindow/%s", region, acct.ID, wid)
		tgtID := store.ResourceID("aws", acct.ID, TypeSSMMaintenanceWindow, mwARN)
		if !mwSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ssm mw-target→mw: %w", err)
		}
	}
	return nil
}

// resolveSSMMaintenanceWindowTaskRefs walks each mw-task's WindowId,
// ServiceRoleArn, and TaskArn (dispatched on Type for LAMBDA / STEP_FUNCTIONS).
// RUN_COMMAND / AUTOMATION TaskArns are document names without per-region
// resolution context — skip those rather than synthesize a wrong document
// edge.
func resolveSSMMaintenanceWindowTaskRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMMaintenanceWindowTask}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	mwSet, err := scannedIDSet(acct, st, TypeSSMMaintenanceWindow)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	lambdaSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	sfnSet, err := scannedIDSet(acct, st, TypeSFNStateMachine)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			WindowId       *string `json:"WindowId"`
			ServiceRoleArn *string `json:"ServiceRoleArn"`
			TaskArn        *string `json:"TaskArn"`
			Type           string  `json:"Type"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if wid := sv(attrs.WindowId); wid != "" {
			mwARN := fmt.Sprintf("arn:aws:ssm:%s:%s:maintenancewindow/%s", region, acct.ID, wid)
			tgtID := store.ResourceID("aws", acct.ID, TypeSSMMaintenanceWindow, mwARN)
			if mwSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ssm mw-task→mw: %w", err)
				}
			}
		}
		if ra := sv(attrs.ServiceRoleArn); ra != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, ra)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert ssm mw-task→role: %w", err)
				}
			}
		}
		taskARN := sv(attrs.TaskArn)
		if taskARN == "" || !strings.HasPrefix(taskARN, "arn:") {
			continue
		}
		switch attrs.Type {
		case "LAMBDA":
			tgtID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, taskARN)
			if lambdaSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ssm mw-task→lambda: %w", err)
				}
			}
		case "STEP_FUNCTIONS":
			tgtID := store.ResourceID("aws", acct.ID, TypeSFNStateMachine, taskARN)
			if sfnSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ssm mw-task→sfn: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveSSMResourceDataSyncRefs wires each resource-data-sync to its
// destination S3 bucket and (optional) KMS key
// (S3Destination.{BucketName, AWSKMSKeyARN}).
func resolveSSMResourceDataSyncRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMResourceDataSync}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			S3Destination *struct {
				BucketName   *string `json:"BucketName"`
				AWSKMSKeyARN *string `json:"AWSKMSKeyARN"`
			} `json:"S3Destination"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.S3Destination == nil {
			continue
		}
		if name := sv(attrs.S3Destination.BucketName); name != "" {
			bArn := "arn:aws:s3:::" + name
			tgtID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bArn)
			if bucketSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ssm rds→s3: %w", err)
				}
			}
		}
		if ref := sv(attrs.S3Destination.AWSKMSKeyARN); ref != "" {
			if id, ok := kmsIdx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ssm rds→kms: %w", err)
				}
			}
		}
	}
	return nil
}
