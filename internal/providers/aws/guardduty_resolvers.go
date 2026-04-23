package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveGuardDutyRelationships) }

// resolveGuardDutyRelationships emits child→detector contains edges for
// filters and IPSets, plus IPSet→S3 bucket edges when the Location is an
// S3 URL (s3://bucket/key or https://s3[.-].../bucket/...).
func resolveGuardDutyRelationships(acct *account, st *store.Store) error {
	if err := resolveGuardDutyChildContains(acct, st, TypeGuardDutyFilter); err != nil {
		return err
	}
	if err := resolveGuardDutyChildContains(acct, st, TypeGuardDutyIPSet); err != nil {
		return err
	}
	return resolveGuardDutyIPSetLocation(acct, st)
}

// resolveGuardDutyChildContains extracts the parent detector ARN from each
// child's NativeID and emits a contains edge child → parent.
func resolveGuardDutyChildContains(acct *account, st *store.Store, childType string) error {
	children, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{childType},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range children {
		// NativeID: arn:aws:guardduty:{r}:{a}:detector/{did}/(filter|ipset)/{id}
		var marker string
		switch childType {
		case TypeGuardDutyFilter:
			marker = "/filter/"
		case TypeGuardDutyIPSet:
			marker = "/ipset/"
		}
		idx := strings.Index(r.NativeID, marker)
		if idx == -1 {
			continue
		}
		parentARN := r.NativeID[:idx]
		pid := store.ResourceID("aws", acct.ID, TypeGuardDutyDetector, parentARN)
		if err := st.UpsertRelationship(r.ID, pid, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert guardduty %s→detector: %w", childType, err)
		}
	}
	return nil
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
