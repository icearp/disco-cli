package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveCodeGuruReviewerAssociationRefs,
		EdgeDecl{TypeCodeGuruReviewerRepositoryAssociation, TypeCodeStarConnectionsConnection, store.RelUses},
	)
}

// resolveCodeGuruReviewerAssociationRefs wires each repository-association to
// its CodeStar Connection (ConnectionArn); Bitbucket/GitHub-Enterprise/S3
// sources skip via FK-safe lookup.
func resolveCodeGuruReviewerAssociationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCodeGuruReviewerRepositoryAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	connSet, err := scannedIDSet(acct, st, TypeCodeStarConnectionsConnection)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ConnectionArn *string `json:"ConnectionArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ca := sv(attrs.ConnectionArn)
		if !strings.Contains(ca, ":codestar-connections:") {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, TypeCodeStarConnectionsConnection, ca)
		if !connSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert codeguru-reviewer→codestar-conn: %w", err)
		}
	}
	return nil
}
