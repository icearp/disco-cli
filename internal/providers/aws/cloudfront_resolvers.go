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
	if err := resolveDistributionTenants(acct, st); err != nil {
		return err
	}
	return resolveDistributionCertificates(acct, st)
}

// resolveDistributionCertificates links each distribution to its ACM
// certificate (ViewerCertificate.ACMCertificateArn). IAM server certs and
// default CloudFront certs are skipped.
func resolveDistributionCertificates(acct *account, st *store.Store) error {
	dists, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudFrontDistribution},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range dists {
		var attrs struct {
			ViewerCertificate *struct {
				ACMCertificateArn *string `json:"ACMCertificateArn"`
			} `json:"ViewerCertificate"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ViewerCertificate == nil || sv(attrs.ViewerCertificate.ACMCertificateArn) == "" {
			continue
		}
		certID := store.ResourceID("aws", acct.ID, TypeACMCertificate, *attrs.ViewerCertificate.ACMCertificateArn)
		if err := st.UpsertRelationship(r.ID, certID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloudfront-distribution→acm-cert: %w", err)
		}
	}
	return nil
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
		LambdaFunctionAssociations *struct {
			Items []struct {
				LambdaFunctionARN *string `json:"LambdaFunctionARN"`
			} `json:"Items"`
		} `json:"LambdaFunctionAssociations"`
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
			if b.LambdaFunctionAssociations != nil {
				for _, la := range b.LambdaFunctionAssociations.Items {
					arn := lambdaStripQualifier(sv(la.LambdaFunctionARN))
					if err := upsert(TypeLambdaFunction, arn); err != nil {
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
		DomainName            *string `json:"DomainName"`
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
			// S3 origin bucket: DomainName ends in .s3.amazonaws.com or .s3-*.amazonaws.com
			if domain := sv(o.DomainName); domain != "" {
				if bucket := cloudfrontS3BucketFromDomain(domain); bucket != "" {
					bucketARN := "arn:aws:s3:::" + bucket
					if err := upsert(TypeS3Bucket, bucketARN); err != nil {
						return err
					}
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

// cloudfrontS3BucketFromDomain extracts the S3 bucket name from a CloudFront
// origin DomainName if it is an S3 origin. Returns "" for non-S3 domains.
// Handles the forms:
//
//	<bucket>.s3.amazonaws.com
//	<bucket>.s3.<region>.amazonaws.com
//	<bucket>.s3-website-<region>.amazonaws.com
//	<bucket>.s3-website.<region>.amazonaws.com
func cloudfrontS3BucketFromDomain(domain string) string {
	// All S3 domain variants have ".s3" as a segment after the bucket name.
	idx := strings.Index(domain, ".s3")
	if idx <= 0 {
		return ""
	}
	rest := domain[idx:]
	if strings.HasPrefix(rest, ".s3.amazonaws.com") ||
		strings.HasPrefix(rest, ".s3-website") ||
		strings.HasPrefix(rest, ".s3.") {
		return domain[:idx]
	}
	return ""
}
