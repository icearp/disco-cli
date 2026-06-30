package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveMacieClassificationJobBuckets,
		EdgeDecl{TypeMacieClassificationJob, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveMacieAllowListBucket,
		EdgeDecl{TypeMacieAllowList, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveMacieMemberOrgAccount,
		EdgeDecl{TypeMacieMember, TypeOrganizationsAccount, store.RelAttachedTo},
	)
}

// macieMemberAttrs mirrors the verbatim Member fields used by the resolver.
// PascalCase tags match mustJSON of the macie2 SDK v2 struct (AccountId).
type macieMemberAttrs struct {
	AccountID *string `json:"AccountId"`
}

// resolveMacieMemberOrgAccount emits an `attached-to` edge from each Macie
// member row to its corresponding AWS Organizations account, when the org
// tree is also scanned. FK-safe via loadOrgTargetIndex; partial-coverage
// scans (no Org tree) skip silently. Mirrors the GuardDuty + Inspector v2
// member → org-account precedent.
func resolveMacieMemberOrgAccount(acct *account, st *store.Store) error {
	members, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeMacieMember},
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
		var attrs macieMemberAttrs
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
			return fmt.Errorf("upsert macie member→org account: %w", err)
		}
	}
	return nil
}

// macieJobAttrs mirrors the verbatim DescribeClassificationJob output stored
// as AttributesJSON. PascalCase tags match mustJSON of the AWS SDK v2 struct.
type macieJobAttrs struct {
	S3JobDefinition *struct {
		BucketDefinitions []struct {
			AccountID *string  `json:"AccountID"`
			Buckets   []string `json:"Buckets"`
		} `json:"BucketDefinitions"`
	} `json:"S3JobDefinition"`
}

// macieAllowListAttrs mirrors the verbatim GetAllowList output. Criteria can
// be either an S3WordsList (S3-backed) or a Regex (no S3 ref).
type macieAllowListAttrs struct {
	Criteria *struct {
		S3WordsList *struct {
			BucketName *string `json:"BucketName"`
		} `json:"S3WordsList"`
	} `json:"Criteria"`
}

// resolveMacieClassificationJobBuckets emits a uses edge from each Macie
// classification job to every S3 bucket listed in its S3JobDefinition's
// BucketDefinitions[]. BucketCriteria (tag-condition expressions) are
// deferred — same precedent as Backup selection tag-expansion. FK-safe via a
// scanned-bucket id set; cross-account bucket refs skip silently.
func resolveMacieClassificationJobBuckets(acct *account, st *store.Store) error {
	jobs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeMacieClassificationJob},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return nil
	}

	buckets, err := scannedBucketIDSet(acct, st)
	if err != nil {
		return err
	}

	for _, j := range jobs {
		var attrs macieJobAttrs
		if err := json.Unmarshal([]byte(j.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.S3JobDefinition == nil {
			continue
		}
		for _, bd := range attrs.S3JobDefinition.BucketDefinitions {
			for _, name := range bd.Buckets {
				if name == "" {
					continue
				}
				bucketID := store.ResourceID("aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+name)
				if !buckets[bucketID] {
					continue
				}
				if err := st.UpsertRelationship(j.ID, bucketID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert macie job→bucket: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveMacieAllowListBucket emits a uses edge from each S3-backed allow
// list to the bucket hosting its words file. Regex-only allow lists carry no
// S3 reference and skip. FK-safe via the scanned-bucket id set.
func resolveMacieAllowListBucket(acct *account, st *store.Store) error {
	lists, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeMacieAllowList},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(lists) == 0 {
		return nil
	}

	buckets, err := scannedBucketIDSet(acct, st)
	if err != nil {
		return err
	}

	for _, l := range lists {
		var attrs macieAllowListAttrs
		if err := json.Unmarshal([]byte(l.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Criteria == nil || attrs.Criteria.S3WordsList == nil {
			continue
		}
		name := sv(attrs.Criteria.S3WordsList.BucketName)
		if name == "" {
			continue
		}
		bucketID := store.ResourceID("aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+name)
		if !buckets[bucketID] {
			continue
		}
		if err := st.UpsertRelationship(l.ID, bucketID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert macie allow-list→bucket: %w", err)
		}
	}
	return nil
}

func scannedBucketIDSet(acct *account, st *store.Store) (map[string]bool, error) {
	rs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeS3Bucket},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(rs))
	for _, r := range rs {
		m[r.ID] = true
	}
	return m, nil
}
