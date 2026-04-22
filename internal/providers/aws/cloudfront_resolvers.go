package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveCloudFrontRelationships)
}

// resolveCloudFrontRelationships runs all CloudFront sub-resolvers.
func resolveCloudFrontRelationships(acct *account, st *store.Store) error {
	if err := resolveDistributionPolicies(acct, st); err != nil {
		return err
	}
	if err := resolveDistributionOrigins(acct, st); err != nil {
		return err
	}
	return resolveDistributionTenants(acct, st)
}

// resolveDistributionPolicies emits "uses" edges from each distribution to the
// cache, origin-request, response-headers, realtime-log, key-group, and
// CloudFront Function resources it references in its behavior configs.
//
// All IDs come from the stored DistributionSummary AttributesJSON. Both the
// DefaultCacheBehavior and each entry in CacheBehaviors are inspected;
// duplicate target IDs are deduplicated per distribution.
func resolveDistributionPolicies(acct *account, st *store.Store) error {
	dists, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudFrontDistribution},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	// Minimal shape of a DistributionSummary covering only the fields we need.
	type behavior struct {
		CachePolicyId           *string `json:"CachePolicyId"`
		OriginRequestPolicyId   *string `json:"OriginRequestPolicyId"`
		ResponseHeadersPolicyId *string `json:"ResponseHeadersPolicyId"`
		RealtimeLogConfigArn    *string `json:"RealtimeLogConfigArn"`
		TrustedKeyGroups        *struct {
			Items []string `json:"Items"`
		} `json:"TrustedKeyGroups"`
		FunctionAssociations *struct {
			Items []struct {
				FunctionARN *string `json:"FunctionARN"`
			} `json:"Items"`
		} `json:"FunctionAssociations"`
	}
	type distSummary struct {
		DefaultCacheBehavior *behavior `json:"DefaultCacheBehavior"`
		CacheBehaviors       *struct {
			Items []behavior `json:"Items"`
		} `json:"CacheBehaviors"`
	}

	for _, d := range dists {
		var attrs distSummary
		if err := json.Unmarshal([]byte(d.AttributesJSON), &attrs); err != nil {
			continue
		}

		// Collect all behaviors (default + path-specific).
		var behaviors []behavior
		if attrs.DefaultCacheBehavior != nil {
			behaviors = append(behaviors, *attrs.DefaultCacheBehavior)
		}
		if attrs.CacheBehaviors != nil {
			behaviors = append(behaviors, attrs.CacheBehaviors.Items...)
		}

		// seen prevents duplicate relationship upserts within a single distribution.
		seen := make(map[string]bool)

		upsert := func(targetType, nativeID string) error {
			if nativeID == "" {
				return nil
			}
			targetID := store.ResourceID("aws", acct.ID, targetType, nativeID)
			if seen[targetID] {
				return nil
			}
			seen[targetID] = true
			if err := st.UpsertRelationship(d.ID, targetID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert cloudfront distribution→%s relationship: %w", targetType, err)
			}
			return nil
		}

		for _, b := range behaviors {
			if err := upsert(TypeCloudFrontCachePolicy, sv(b.CachePolicyId)); err != nil {
				return err
			}
			if err := upsert(TypeCloudFrontOriginRequestPolicy, sv(b.OriginRequestPolicyId)); err != nil {
				return err
			}
			if err := upsert(TypeCloudFrontResponseHeadersPolicy, sv(b.ResponseHeadersPolicyId)); err != nil {
				return err
			}
			if err := upsert(TypeCloudFrontRealtimeLogConfig, sv(b.RealtimeLogConfigArn)); err != nil {
				return err
			}
			if b.TrustedKeyGroups != nil {
				for _, kgID := range b.TrustedKeyGroups.Items {
					if err := upsert(TypeCloudFrontKeyGroup, kgID); err != nil {
						return err
					}
				}
			}
			if b.FunctionAssociations != nil {
				for _, fa := range b.FunctionAssociations.Items {
					if err := upsert(TypeCloudFrontFunction, sv(fa.FunctionARN)); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// resolveDistributionOrigins emits "uses" edges from each distribution to the
// origin access controls and origin access identities (OAIs) used by its origins.
func resolveDistributionOrigins(acct *account, st *store.Store) error {
	dists, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudFrontDistribution},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	type origin struct {
		OriginAccessControlId *string `json:"OriginAccessControlId"`
		S3OriginConfig        *struct {
			// Format: "origin-access-identity/cloudfront/<ID>" or empty string.
			OriginAccessIdentity *string `json:"OriginAccessIdentity"`
		} `json:"S3OriginConfig"`
	}
	type distSummary struct {
		Origins *struct {
			Items []origin `json:"Items"`
		} `json:"Origins"`
	}

	for _, d := range dists {
		var attrs distSummary
		if err := json.Unmarshal([]byte(d.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Origins == nil {
			continue
		}

		seen := make(map[string]bool)

		upsert := func(targetType, nativeID string) error {
			if nativeID == "" {
				return nil
			}
			targetID := store.ResourceID("aws", acct.ID, targetType, nativeID)
			if seen[targetID] {
				return nil
			}
			seen[targetID] = true
			if err := st.UpsertRelationship(d.ID, targetID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert cloudfront distribution→%s relationship: %w", targetType, err)
			}
			return nil
		}

		for _, o := range attrs.Origins.Items {
			if err := upsert(TypeCloudFrontOriginAccessControl, sv(o.OriginAccessControlId)); err != nil {
				return err
			}
			if o.S3OriginConfig != nil {
				oaiRef := sv(o.S3OriginConfig.OriginAccessIdentity)
				// Strip "origin-access-identity/cloudfront/" prefix to get the raw OAI ID.
				oaiID := strings.TrimPrefix(oaiRef, "origin-access-identity/cloudfront/")
				if err := upsert(TypeCloudFrontOAI, oaiID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// resolveDistributionTenants emits "uses" edges from each distribution tenant
// to its parent distribution. The DistributionId field on the tenant is used to
// reconstruct the distribution ARN (the NativeID used by the distribution scanner).
func resolveDistributionTenants(acct *account, st *store.Store) error {
	tenants, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudFrontDistributionTenant},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	for _, t := range tenants {
		var attrs struct {
			DistributionId *string `json:"DistributionId"`
		}
		if err := json.Unmarshal([]byte(t.AttributesJSON), &attrs); err != nil {
			continue
		}
		distID := sv(attrs.DistributionId)
		if distID == "" {
			continue
		}
		// Construct the distribution ARN from the distribution ID.
		distARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution/%s", acct.ID, distID)
		targetID := store.ResourceID("aws", acct.ID, TypeCloudFrontDistribution, distARN)
		if err := st.UpsertRelationship(t.ID, targetID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloudfront tenant→distribution relationship: %w", err)
		}
	}
	return nil
}
