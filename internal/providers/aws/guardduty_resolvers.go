package aws

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveGuardDutyRelationships,
		EdgeDecl{TypeGuardDutyIPSet, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveGuardDutyMemberOrgAccount,
		EdgeDecl{TypeGuardDutyMember, TypeOrganizationsAccount, store.RelAttachedTo},
	)
	registerResolver(
		noopGuardDutyHierarchy,
		EdgeDecl{TypeGuardDutyDetector, TypeGuardDutyFilter, store.RelContains},
		EdgeDecl{TypeGuardDutyDetector, TypeGuardDutyIPSet, store.RelContains},
		EdgeDecl{TypeGuardDutyDetector, TypeGuardDutyMember, store.RelContains},
		EdgeDecl{TypeGuardDutyDetector, TypeGuardDutyOrganizationConfiguration, store.RelContains},
	)
}

// noopGuardDutyHierarchy emits nothing: the detector → filter / ipset contains
// edges are written at scan time by storeGDDetectorSets and the detector →
// member edges by scanGDMembers (storeChildrenEachParent), both via
// RecordHierarchyBatch, and the detector → organization-configuration edge by
// scanGDOrganizationConfigurations through childFanOut. It is registered only so
// those edges are declared to coverage tooling.
func noopGuardDutyHierarchy(_ *account, _ *store.Store) error {
	return nil
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
		Providers: []string{"aws"},
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
		orgID := store.ResourceID("aws", acct.ID, orgARN)
		if err := st.UpsertRelationship(m.ID, orgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert guardduty member→org account: %w", err)
		}
	}
	return nil
}

// resolveGuardDutyRelationships emits IPSet→S3 bucket edges when Location is
// an S3 URL. detector→{filter,ipset} containment is recorded by
// guardduty_scanners.go via RecordHierarchyBatch; the unified closure writer
// emits the matching `contains` row.
func resolveGuardDutyRelationships(acct *account, st *store.Store) error {
	return resolveGuardDutyIPSetLocation(acct, st)
}

// resolveGuardDutyIPSetLocation parses the Location URL of each IPSet and
// emits a uses edge to the S3 bucket when recognisable.
func resolveGuardDutyIPSetLocation(acct *account, st *store.Store) error {
	ipsets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGuardDutyIPSet},
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
		bucket := parseS3Bucket(acct.partition(), sv(attrs.Location))
		if bucket == "" {
			continue
		}
		bucketID := store.ResourceID("aws", acct.ID, s3BucketARN(acct.partition(), bucket))
		if err := st.UpsertRelationship(r.ID, bucketID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert guardduty-ipset→s3: %w", err)
		}
	}
	return nil
}

// parseS3Bucket extracts the bucket name from an s3:// URL or an https URL
// whose host is an S3 endpoint of partition (s3HostBucket), virtual-hosted or
// path-style. Returns "" for any other input, including an https URL of
// another partition's endpoint.
func parseS3Bucket(partition, loc string) string {
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
		u, err := url.Parse(loc)
		if err != nil {
			return ""
		}
		bucket, ok := s3HostBucket(partition, u.Hostname())
		if !ok {
			return ""
		}
		if bucket != "" {
			return bucket
		}
		bucket, _, _ = strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
		return bucket
	}
	return ""
}
