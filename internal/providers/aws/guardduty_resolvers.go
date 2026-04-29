package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveGuardDutyRelationships)
	registerResolver(resolveGuardDutyMemberOrgAccount)
}

// guardDutyMemberAttrs mirrors the verbatim Member fields used by the
// resolver. PascalCase tags match `mustJSON(guarddutytypes.Member)`.
type guardDutyMemberAttrs struct {
	AccountID *string `json:"AccountID"`
}

// resolveGuardDutyMemberOrgAccount emits an `attached-to` edge from each
// GuardDuty member row to its corresponding AWS Organizations account when
// the org tree is also scanned. FK-safe via loadOrgTargetIndex; partial-
// coverage scans (no Org tree) skip silently. Mirrors the Inspector v2 +
// Detective + SSO assignment → org-account precedent.
func resolveGuardDutyMemberOrgAccount(acct *account, st *store.Store) error {
	members, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeGuardDutyMember},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}
	orgArnByID, _, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}
	if len(orgArnByID) == 0 {
		return nil
	}
	for _, m := range members {
		var attrs guardDutyMemberAttrs
		if err := json.Unmarshal([]byte(m.AttributesJSON), &attrs); err != nil {
			continue
		}
		accountID := sv(attrs.AccountID)
		if accountID == "" {
			continue
		}
		orgARN, ok := orgArnByID[accountID]
		if !ok {
			continue
		}
		orgID := store.ResourceID("aws", acct.ID, TypeOrganizationsAccount, orgARN)
		if err := st.UpsertRelationship(m.ID, orgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert guardduty member→org account: %w", err)
		}
	}
	return nil
}

// resolveGuardDutyRelationships emits IPSet→S3 bucket edges when the
// Location is an S3 URL. detector→{filter,ipset} containment is recorded
// by guardduty_scanners.go via RecordHierarchyBatch; the unified
// closure writer emits the matching `contains` row to relationships.
func resolveGuardDutyRelationships(acct *account, st *store.Store) error {
	return resolveGuardDutyIPSetLocation(acct, st)
}

// resolveGuardDutyIPSetLocation parses the Location URL of each IPSet and
// emits a uses edge to the S3 bucket when recognisable.
func resolveGuardDutyIPSetLocation(acct *account, st *store.Store) error {
	ipsets, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGuardDutyIPSet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range ipsets {
		var attrs struct {
			Location *string `json:"Location"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		bucket := parseS3Bucket(sv(attrs.Location))
		if bucket == "" {
			continue
		}
		bucketID := store.ResourceID("aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucket)
		if err := st.UpsertRelationship(r.ID, bucketID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert guardduty-ipset→s3: %w", err)
		}
	}
	return nil
}

// parseS3Bucket extracts the bucket name from common S3 URL forms. Returns
// empty string if the input is not recognisable.
func parseS3Bucket(loc string) string {
	if loc == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(loc, "s3://"):
		rest := strings.TrimPrefix(loc, "s3://")
		if i := strings.Index(rest, "/"); i > 0 {
			return rest[:i]
		}
		return rest
	case strings.HasPrefix(loc, "https://"):
		// virtual-hosted: https://bucket.s3.amazonaws.com/key
		// path-style:     https://s3.amazonaws.com/bucket/key
		rest := strings.TrimPrefix(loc, "https://")
		host := rest
		if i := strings.Index(rest, "/"); i >= 0 {
			host = rest[:i]
		}
		if strings.HasPrefix(host, "s3.") || strings.HasPrefix(host, "s3-") {
			// path-style
			if i := strings.Index(rest, "/"); i >= 0 {
				path := rest[i+1:]
				if j := strings.Index(path, "/"); j > 0 {
					return path[:j]
				}
				return path
			}
			return ""
		}
		if i := strings.Index(host, ".s3"); i > 0 {
			return host[:i]
		}
	}
	return ""
}
