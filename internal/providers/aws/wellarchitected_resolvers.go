package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// waShareTypes are the Well-Architected share types; every one carries
// SharedWith and Status.
var waShareTypes = []string{
	TypeWellArchitectedWorkloadShare,
	TypeWellArchitectedLensShare,
	TypeWellArchitectedProfileShare,
	TypeWellArchitectedTemplateShare,
}

func init() {
	registerResolver(
		noopWAHierarchy,
		EdgeDecl{TypeWellArchitectedWorkload, TypeWellArchitectedLensReview, store.RelContains},
		EdgeDecl{TypeWellArchitectedWorkload, TypeWellArchitectedWorkloadShare, store.RelContains},
		EdgeDecl{TypeWellArchitectedLens, TypeWellArchitectedLensShare, store.RelContains},
		EdgeDecl{TypeWellArchitectedProfile, TypeWellArchitectedProfileShare, store.RelContains},
		EdgeDecl{TypeWellArchitectedReviewTemplate, TypeWellArchitectedTemplateShare, store.RelContains},
	)
	var grantees []EdgeDecl
	for _, share := range waShareTypes {
		grantees = append(grantees,
			EdgeDecl{share, TypeOrganizationsAccount, store.RelAttachedTo},
			EdgeDecl{share, TypeOrganizationsOU, store.RelAttachedTo},
		)
	}
	registerResolver(resolveWAShareGrantees, grantees...)
}

// noopWAHierarchy only declares the parent -> child contains edges;
// scanWellArchitected records them through childFanOut.
func noopWAHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// waActiveShareStatus are the share states in which the grantee has access: an
// accepted invitation, or an organization or OU share that finished
// associating. Every other state (a pending invitation, rejected, revoked,
// expired, failed, still associating, or a value added to the enum later) is
// not known to grant anything, and an edge would claim access that may not
// exist.
var waActiveShareStatus = map[string]bool{"ACCEPTED": true, "ASSOCIATED": true}

// resolveWAShareGrantees links each live share to the organizations account or
// OU it is shared with. Only grantees present in the scanned org tree get an
// edge; a share with an organization id, or with an account outside the
// scanned tree, gets none.
func resolveWAShareGrantees(acct *account, st *store.Store) error {
	shares, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     waShareTypes,
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(shares) == 0 {
		return nil
	}
	orgArnByID, _, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}
	for _, s := range shares {
		var attrs struct {
			SharedWith *string `json:"SharedWith"`
			Status     string  `json:"Status"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			continue
		}
		if !waActiveShareStatus[attrs.Status] {
			continue
		}
		orgARN, ok := orgArnByID[sv(attrs.SharedWith)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(s.ID, store.ResourceID("aws", acct.ID, orgARN), store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert wellarchitected share→grantee: %w", err)
		}
	}
	return nil
}
