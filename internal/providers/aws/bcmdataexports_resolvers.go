package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveBCMDataExportsRelationships,
		EdgeDecl{TypeBCMDataExportsExport, TypeS3Bucket, store.RelUses},
	)
}

// bcmExportAttrs mirrors verbatim Export.DestinationConfigurations.S3Destination.
type bcmExportAttrs struct {
	DestinationConfigurations *struct {
		S3Destination *struct {
			S3Bucket *string `json:"S3Bucket"`
		} `json:"S3Destination"`
	} `json:"DestinationConfigurations"`
}

// resolveBCMDataExportsRelationships emits export → S3 bucket (uses)
// edges. Cross-account destinations skip silently via FK-safe id set.
func resolveBCMDataExportsRelationships(acct *account, st *store.Store) error {
	exports, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeBCMDataExportsExport},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(exports) == 0 {
		return nil
	}
	bucketIDs, err := resourceIDSet(st, acct.ID, TypeS3Bucket)
	if err != nil {
		return err
	}
	for _, e := range exports {
		var attrs bcmExportAttrs
		if err := json.Unmarshal([]byte(e.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DestinationConfigurations == nil || attrs.DestinationConfigurations.S3Destination == nil {
			continue
		}
		bucket := sv(attrs.DestinationConfigurations.S3Destination.S3Bucket)
		if bucket == "" {
			continue
		}
		bARN := fmt.Sprintf("arn:aws:s3:::%s", bucket)
		bID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bARN)
		if _, ok := bucketIDs[bID]; !ok {
			continue
		}
		if err := st.UpsertRelationship(e.ID, bID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert bcmdataexports-export→s3: %w", err)
		}
	}
	return nil
}
