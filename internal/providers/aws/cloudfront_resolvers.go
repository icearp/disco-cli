package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveCloudFrontRelationships,
		EdgeDecl{TypeCloudFrontDistribution, TypeACMCertificate, store.RelUses},
		EdgeDecl{TypeCloudFrontDistribution, TypeCloudFrontCachePolicy, store.RelUses},
		EdgeDecl{TypeCloudFrontDistribution, TypeCloudFrontOriginRequestPolicy, store.RelUses},
		EdgeDecl{TypeCloudFrontDistribution, TypeCloudFrontResponseHeadersPolicy, store.RelUses},
		EdgeDecl{TypeCloudFrontDistribution, TypeCloudFrontRealtimeLogConfig, store.RelUses},
		EdgeDecl{TypeCloudFrontDistribution, TypeCloudFrontKeyGroup, store.RelUses},
		EdgeDecl{TypeCloudFrontDistribution, TypeCloudFrontFunction, store.RelUses},
		EdgeDecl{TypeCloudFrontDistribution, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeCloudFrontDistribution, TypeCloudFrontOriginAccessControl, store.RelUses},
		EdgeDecl{TypeCloudFrontDistribution, TypeCloudFrontOAI, store.RelUses},
		EdgeDecl{TypeCloudFrontDistribution, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeCloudFrontDistributionTenant, TypeCloudFrontDistribution, store.RelUses},
		EdgeDecl{TypeCloudFrontKeyGroup, TypeCloudFrontPublicKey, store.RelUses},
		EdgeDecl{TypeCloudFrontRealtimeLogConfig, TypeKinesisStream, store.RelUses},
		EdgeDecl{TypeCloudFrontRealtimeLogConfig, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeCloudFrontStreamingDistribution, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeCloudFrontMonitoringSubscription, TypeCloudFrontDistribution, store.RelAttachedTo},
		EdgeDecl{TypeCloudFrontConnectionGroup, TypeCloudFrontAnycastIPList, store.RelUses},
	)
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
	if err := resolveDistributionCertificates(acct, st); err != nil {
		return err
	}
	if err := resolveCloudFrontKeyGroupPublicKeys(acct, st); err != nil {
		return err
	}
	if err := resolveCloudFrontRealtimeLogConfigTargets(acct, st); err != nil {
		return err
	}
	if err := resolveCloudFrontStreamingDistributionOrigins(acct, st); err != nil {
		return err
	}
	if err := resolveCloudFrontMonitoringSubscriptionParent(acct, st); err != nil {
		return err
	}
	return resolveCloudFrontConnectionGroupAnycast(acct, st)
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
		CachePolicyID           *string `json:"CachePolicyID"`
		OriginRequestPolicyID   *string `json:"OriginRequestPolicyID"`
		ResponseHeadersPolicyID *string `json:"ResponseHeadersPolicyID"`
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
			if err := upsert(TypeCloudFrontCachePolicy, sv(b.CachePolicyID)); err != nil {
				return err
			}
			if err := upsert(TypeCloudFrontOriginRequestPolicy, sv(b.OriginRequestPolicyID)); err != nil {
				return err
			}
			if err := upsert(TypeCloudFrontResponseHeadersPolicy, sv(b.ResponseHeadersPolicyID)); err != nil {
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
		OriginAccessControlID *string `json:"OriginAccessControlID"`
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
			if err := upsert(TypeCloudFrontOriginAccessControl, sv(o.OriginAccessControlID)); err != nil {
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
// to its parent distribution. The DistributionID field on the tenant is used to
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
			DistributionID *string `json:"DistributionID"`
		}
		if err := json.Unmarshal([]byte(t.AttributesJSON), &attrs); err != nil {
			continue
		}
		distID := sv(attrs.DistributionID)
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

// resolveCloudFrontKeyGroupPublicKeys emits "uses" edges from each key group to
// the public keys it lists in KeyGroupConfig.Items[]. FK-safe via scannedIDSet.
func resolveCloudFrontKeyGroupPublicKeys(acct *account, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudFrontKeyGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}
	pkSet, err := scannedIDSet(acct, st, TypeCloudFrontPublicKey)
	if err != nil {
		return err
	}
	// KeyGroup attrs are stored as the wrapping summary `{KeyGroup: {Id, KeyGroupConfig: {Items: [...]}}}`.
	for _, g := range groups {
		var attrs struct {
			KeyGroup *struct {
				KeyGroupConfig *struct {
					Items []string `json:"Items"`
				} `json:"KeyGroupConfig"`
			} `json:"KeyGroup"`
		}
		if err := json.Unmarshal([]byte(g.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.KeyGroup == nil || attrs.KeyGroup.KeyGroupConfig == nil {
			continue
		}
		for _, pkID := range attrs.KeyGroup.KeyGroupConfig.Items {
			targetID := store.ResourceID("aws", acct.ID, TypeCloudFrontPublicKey, pkID)
			if !pkSet[targetID] {
				continue
			}
			if err := st.UpsertRelationship(g.ID, targetID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert cloudfront key-group→public-key: %w", err)
			}
		}
	}
	return nil
}

// resolveCloudFrontRealtimeLogConfigTargets emits edges from each realtime-log
// config to its Kinesis stream (uses) and IAM role (assumes). The scanner stores
// the raw RealtimeLogConfig with EndPoints[].KinesisStreamConfig.{StreamARN,RoleARN}.
// Both edges are FK-safe via scannedIDSet — cross-account refs silently skip.
func resolveCloudFrontRealtimeLogConfigTargets(acct *account, st *store.Store) error {
	cfgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudFrontRealtimeLogConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(cfgs) == 0 {
		return nil
	}
	streamSet, err := scannedIDSet(acct, st, TypeKinesisStream)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, c := range cfgs {
		var attrs struct {
			EndPoints []struct {
				KinesisStreamConfig *struct {
					StreamARN *string `json:"StreamARN"`
					RoleARN   *string `json:"RoleARN"`
				} `json:"KinesisStreamConfig"`
			} `json:"EndPoints"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, ep := range attrs.EndPoints {
			if ep.KinesisStreamConfig == nil {
				continue
			}
			if streamARN := sv(ep.KinesisStreamConfig.StreamARN); streamARN != "" {
				targetID := store.ResourceID("aws", acct.ID, TypeKinesisStream, streamARN)
				if streamSet[targetID] {
					if err := st.UpsertRelationship(c.ID, targetID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert cloudfront realtime-log→kinesis: %w", err)
					}
				}
			}
			if roleARN := sv(ep.KinesisStreamConfig.RoleARN); roleARN != "" {
				targetID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
				if roleSet[targetID] {
					if err := st.UpsertRelationship(c.ID, targetID, store.RelAssumes, "directed", nil); err != nil {
						return fmt.Errorf("upsert cloudfront realtime-log→iam-role: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveCloudFrontStreamingDistributionOrigins emits "uses" edges from each
// streaming distribution to the S3 bucket it serves (S3Origin.DomainName).
func resolveCloudFrontStreamingDistributionOrigins(acct *account, st *store.Store) error {
	sds, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudFrontStreamingDistribution},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(sds) == 0 {
		return nil
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	for _, sd := range sds {
		var attrs struct {
			S3Origin *struct {
				DomainName *string `json:"DomainName"`
			} `json:"S3Origin"`
		}
		if err := json.Unmarshal([]byte(sd.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.S3Origin == nil {
			continue
		}
		bucket := cloudfrontS3BucketFromDomain(sv(attrs.S3Origin.DomainName))
		if bucket == "" {
			continue
		}
		targetID := store.ResourceID("aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucket)
		if !bucketSet[targetID] {
			continue
		}
		if err := st.UpsertRelationship(sd.ID, targetID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloudfront streaming-distribution→s3: %w", err)
		}
	}
	return nil
}

// resolveCloudFrontMonitoringSubscriptionParent emits "attached-to" edges from
// each monitoring subscription to its parent distribution. The subscription's
// NativeID is the parent distribution ARN (see scanCloudFrontMonitoringSubscriptions).
func resolveCloudFrontMonitoringSubscriptionParent(acct *account, st *store.Store) error {
	subs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudFrontMonitoringSubscription},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return nil
	}
	distSet, err := scannedIDSet(acct, st, TypeCloudFrontDistribution)
	if err != nil {
		return err
	}
	for _, s := range subs {
		targetID := store.ResourceID("aws", acct.ID, TypeCloudFrontDistribution, s.NativeID)
		if !distSet[targetID] {
			continue
		}
		if err := st.UpsertRelationship(s.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloudfront monitoring-sub→distribution: %w", err)
		}
	}
	return nil
}

// resolveCloudFrontConnectionGroupAnycast emits "uses" edges from each
// connection group to the anycast IP list it references (AnycastIpListId).
// Connection group carries the bare list ID; the anycast list rows store the
// full ARN as NativeID — build a (bare-id → resourceID) lookup.
func resolveCloudFrontConnectionGroupAnycast(acct *account, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudFrontConnectionGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}
	anyLists, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudFrontAnycastIPList},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	byID := make(map[string]string, len(anyLists))
	for _, a := range anyLists {
		// ARN form: arn:aws:cloudfront::<acct>:anycast-ip-list/<id>
		if i := strings.LastIndex(a.NativeID, "/"); i >= 0 {
			byID[a.NativeID[i+1:]] = a.ID
		}
	}
	for _, g := range groups {
		var attrs struct {
			AnycastIpListId *string `json:"AnycastIpListId"`
		}
		if err := json.Unmarshal([]byte(g.AttributesJSON), &attrs); err != nil {
			continue
		}
		anycastID := sv(attrs.AnycastIpListId)
		if anycastID == "" {
			continue
		}
		targetID, ok := byID[anycastID]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(g.ID, targetID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloudfront connection-group→anycast-ip-list: %w", err)
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
