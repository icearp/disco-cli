package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveCloudDirectoryAppliedSchema,
		EdgeDecl{TypeCloudDirectoryAppliedSchema, TypeCloudDirectoryDirectory, store.RelAttachedTo},
	)
}

// resolveCloudDirectoryAppliedSchema wires each applied schema to its directory
// via the scanner-recorded DirectoryArn, FK-safe.
func resolveCloudDirectoryAppliedSchema(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloudDirectoryAppliedSchema},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dirSet, err := scannedIDSet(acct, st, TypeCloudDirectoryDirectory)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DirectoryArn string `json:"DirectoryArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DirectoryArn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeCloudDirectoryDirectory, attrs.DirectoryArn)
		if !dirSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert clouddirectory applied-schema→directory: %w", err)
		}
	}
	return nil
}
