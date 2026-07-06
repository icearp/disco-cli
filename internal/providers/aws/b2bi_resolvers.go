package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveB2BIProfileLogGroup,
		EdgeDecl{TypeB2BIProfile, TypeLogsLogGroup, store.RelUses},
	)
	registerResolver(
		resolveB2BIPartnershipRefs,
		EdgeDecl{TypeB2BIPartnership, TypeB2BIProfile, store.RelAttachedTo},
		EdgeDecl{TypeB2BIPartnership, TypeB2BICapability, store.RelUses},
	)
	registerResolver(
		resolveB2BICapabilityS3,
		EdgeDecl{TypeB2BICapability, TypeS3Bucket, store.RelUses},
	)
}

// resolveB2BICapabilityS3 wires each capability to the S3 buckets that hold
// its instructions documents (InstructionsDocuments[].BucketName).
func resolveB2BICapabilityS3(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeB2BICapability}, Limit: util.AllResources,
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
	for _, r := range rows {
		var attrs struct {
			InstructionsDocuments []struct {
				BucketName *string `json:"BucketName"`
			} `json:"InstructionsDocuments"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := map[string]struct{}{}
		for _, d := range attrs.InstructionsDocuments {
			b := sv(d.BucketName)
			if b == "" {
				continue
			}
			if _, ok := seen[b]; ok {
				continue
			}
			seen[b] = struct{}{}
			bARN := "arn:aws:s3:::" + b
			tgt := store.ResourceID("aws", acct.ID, TypeS3Bucket, bARN)
			if !bucketSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert b2bi capability→s3: %w", err)
			}
		}
	}
	return nil
}

// resolveB2BIPartnershipRefs wires each partnership to its trading profile
// (ProfileID) and capabilities (Capabilities[], capability IDs). Both ARNs
// synthesised per region+acct from the bare ID, using `:profile/<id>` and
// `:capability/<id>` segments.
func resolveB2BIPartnershipRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeB2BIPartnership}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	profSet, err := scannedIDSet(acct, st, TypeB2BIProfile)
	if err != nil {
		return err
	}
	capSet, err := scannedIDSet(acct, st, TypeB2BICapability)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ProfileID    *string  `json:"ProfileId"`
			Capabilities []string `json:"Capabilities"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if pid := sv(attrs.ProfileID); pid != "" {
			pARN := fmt.Sprintf("arn:aws:b2bi:%s:%s:profile/%s", region, acct.ID, pid)
			tgt := store.ResourceID("aws", acct.ID, TypeB2BIProfile, pARN)
			if profSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert b2bi partnership→profile: %w", err)
				}
			}
		}
		for _, cid := range attrs.Capabilities {
			if cid == "" {
				continue
			}
			cARN := fmt.Sprintf("arn:aws:b2bi:%s:%s:capability/%s", region, acct.ID, cid)
			tgt := store.ResourceID("aws", acct.ID, TypeB2BICapability, cARN)
			if !capSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert b2bi partnership→capability: %w", err)
			}
		}
	}
	return nil
}

// resolveB2BIProfileLogGroup wires each B2BI profile to the CloudWatch log
// group it streams logs to (LogGroupName). The SDK exposes only the bare
// log-group name; rebuild the ARN per region+acct via logGroupNativeIDFromName.
func resolveB2BIProfileLogGroup(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeB2BIProfile}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	lgSet, err := scannedIDSet(acct, st, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			LogGroupName *string `json:"LogGroupName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		name := sv(attrs.LogGroupName)
		if name == "" {
			continue
		}
		lgARN := logGroupNativeIDFromName(acct.ID, sv(r.Region), name)
		tgt := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, lgARN)
		if !lgSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert b2bi profile→log-group: %w", err)
		}
	}
	return nil
}
