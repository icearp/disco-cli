package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		func(acct *account, st *store.Store) error {
			if err := resolveRoute53Relationships(acct, st); err != nil {
				return err
			}
			if err := resolveRoute53DNSSECRelationships(acct, st); err != nil {
				return err
			}
			if err := resolveRoute53KSKRelationships(acct, st); err != nil {
				return err
			}
			if err := resolveRoute53HealthCheckRelationships(acct, st); err != nil {
				return err
			}
			return resolveRoute53AliasRelationships(acct, st)
		},
		EdgeDecl{TypeRoute53RecordSet, TypeRoute53HostedZone, store.RelAttachedTo},
		EdgeDecl{TypeRoute53DNSSEC, TypeRoute53HostedZone, store.RelAttachedTo},
		EdgeDecl{TypeRoute53KeySigningKey, TypeRoute53DNSSEC, store.RelAttachedTo},
		EdgeDecl{TypeRoute53RecordSet, TypeRoute53HealthCheck, store.RelUses},
		EdgeDecl{TypeRoute53RecordSet, TypeELBv2LoadBalancer, store.RelUses},
		EdgeDecl{TypeRoute53RecordSet, TypeCloudFrontDistribution, store.RelUses},
		EdgeDecl{TypeRoute53RecordSet, TypeAPIGatewayDomainName, store.RelUses},
		EdgeDecl{TypeRoute53RecordSet, TypeAPIGatewayDomainNameV2, store.RelUses},
		EdgeDecl{TypeRoute53RecordSet, TypeS3Bucket, store.RelUses},
	)
}

// resolveRoute53AliasRelationships links record sets with AliasTarget.DNSName
// to the AWS backend fronted by that DNS (ELBv2 LB, CloudFront distribution,
// APIGW custom domain v1/v2). Emits `uses` edges. Alias records whose DNS
// doesn't match any scanned backend are skipped — avoids phantom edges.
//
// S3-website aliases handled separately: the AliasTarget.DNSName is a region
// endpoint shared by every website-enabled bucket in that region (e.g.
// `s3-website-us-east-1.amazonaws.com`), so it can't disambiguate which
// bucket. S3 website hosting requires the bucket name to exactly match the
// record FQDN, so when the alias DNS is recognized as an S3-website endpoint
// the resolver pivots to looking up the bucket by record-set name.
func resolveRoute53AliasRelationships(acct *account, st *store.Store) error {
	index, err := buildAliasBackendIndex(acct, st)
	if err != nil {
		return err
	}
	bucketByName, err := buildS3BucketNameIndex(acct, st)
	if err != nil {
		return err
	}
	if len(index) == 0 && len(bucketByName) == 0 {
		return nil
	}
	records, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRoute53RecordSet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range records {
		var attrs struct {
			Name        *string `json:"Name"`
			AliasTarget *struct {
				DNSName *string `json:"DNSName"`
			} `json:"AliasTarget"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.AliasTarget == nil {
			continue
		}
		key := normalizeAliasDNS(sv(attrs.AliasTarget.DNSName))
		if key == "" {
			continue
		}
		// S3-website endpoints: bucket-name match against scanned buckets.
		if isS3WebsiteEndpoint(key) {
			recordName := normalizeAliasDNS(sv(attrs.Name))
			if recordName == "" {
				continue
			}
			if bucketID, ok := bucketByName[recordName]; ok {
				if err := st.UpsertRelationship(r.ID, bucketID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert route53 record-set→s3-website bucket: %w", err)
				}
			}
			continue
		}
		backendID, ok := index[key]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, backendID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert route53 record-set→alias-backend: %w", err)
		}
	}
	return nil
}

// isS3WebsiteEndpoint reports whether dns is an S3 static-website hosting
// regional endpoint. Two shapes coexist: legacy `s3-website-<region>` (older
// regions like us-east-1) and modern `s3-website.<region>` (newer regions
// like ap-east-1, eu-south-1). Both are normalized via lowercase + trailing
// dot strip prior to this check.
func isS3WebsiteEndpoint(dns string) bool {
	return strings.HasPrefix(dns, "s3-website-") || strings.HasPrefix(dns, "s3-website.")
}

// buildS3BucketNameIndex maps lowercased bucket name → bucket resource ID
// for every scanned S3 bucket in the account. Bucket NativeID is
// `arn:aws:s3:::<name>`, so the name is the suffix after the prefix. S3
// bucket names are globally unique and case-folded by AWS, so the lowercase
// is just for matching against record FQDNs (which the resolver also lowers).
func buildS3BucketNameIndex(acct *account, st *store.Store) (map[string]string, error) {
	const arnPrefix = "arn:aws:s3:::"
	rs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeS3Bucket},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	index := make(map[string]string, len(rs))
	for _, r := range rs {
		if !strings.HasPrefix(r.NativeID, arnPrefix) {
			continue
		}
		name := strings.ToLower(r.NativeID[len(arnPrefix):])
		if name == "" {
			continue
		}
		index[name] = r.ID
	}
	return index, nil
}

// normalizeAliasDNS lower-cases, strips trailing dot, and strips a leading
// "dualstack." prefix (Route53 alias records commonly carry it on ELB
// targets even when the LB's own DNSName attribute does not).
func normalizeAliasDNS(s string) string {
	s = strings.ToLower(strings.TrimSuffix(s, "."))
	s = strings.TrimPrefix(s, "dualstack.")
	return s
}

// buildAliasBackendIndex maps normalized backend DNS names → backend
// resource ID for every alias-target candidate in this account: ELBv2 LBs,
// CloudFront distributions, APIGW custom domains (v1 + v2).
func buildAliasBackendIndex(acct *account, st *store.Store) (map[string]string, error) {
	index := map[string]string{}

	// ELBv2 load balancers — scanner wraps response as {"lb": <LB>, "type": "..."}.
	lbs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeELBv2LoadBalancer},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range lbs {
		var attrs struct {
			LB struct {
				DNSName *string `json:"DNSName"`
			} `json:"lb"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if k := normalizeAliasDNS(sv(attrs.LB.DNSName)); k != "" {
			index[k] = r.ID
		}
	}

	// CloudFront distributions — top-level DomainName.
	dists, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudFrontDistribution},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range dists {
		var attrs struct {
			DomainName *string `json:"DomainName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if k := normalizeAliasDNS(sv(attrs.DomainName)); k != "" {
			index[k] = r.ID
		}
	}

	// APIGW v1 custom domains — DistributionDomainName (edge-optimized) +
	// RegionalDomainName (regional). Either may be set.
	v1, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayDomainName},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range v1 {
		var attrs struct {
			DistributionDomainName *string `json:"DistributionDomainName"`
			RegionalDomainName     *string `json:"RegionalDomainName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, dns := range []string{sv(attrs.DistributionDomainName), sv(attrs.RegionalDomainName)} {
			if k := normalizeAliasDNS(dns); k != "" {
				index[k] = r.ID
			}
		}
	}

	// APIGW v2 custom domains — DomainNameConfigurations[].APIGatewayDomainName.
	v2, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayDomainNameV2},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range v2 {
		var attrs struct {
			DomainNameConfigurations []struct {
				APIGatewayDomainName *string `json:"APIGatewayDomainName"`
			} `json:"DomainNameConfigurations"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, c := range attrs.DomainNameConfigurations {
			if k := normalizeAliasDNS(sv(c.APIGatewayDomainName)); k != "" {
				index[k] = r.ID
			}
		}
	}

	return index, nil
}

// resolveRoute53Relationships links each record set to its hosted zone.
// The record set NativeID is "<zoneARN>/<type>/<name>", so the zone ARN
// is the prefix up to the second slash after the ARN segment.
func resolveRoute53Relationships(acct *account, st *store.Store) error {
	records, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRoute53RecordSet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range records {
		// NativeID format: "arn:aws:route53:::hostedzone/<id>/<TYPE>/<name>"
		// The zone ARN is everything before the second-to-last slash pair.
		// Specifically: split on "/" after the ARN prefix to isolate zoneID.
		zoneARN := recordSetZoneARN(r.NativeID)
		if zoneARN == "" {
			continue
		}
		zoneID := store.ResourceID("aws", acct.ID, TypeRoute53HostedZone, zoneARN)
		if err := st.UpsertRelationship(r.ID, zoneID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert route53 record-set→hosted-zone: %w", err)
		}
	}
	return nil
}

// recordSetZoneARN extracts the hosted zone ARN from a record set NativeID.
// NativeID format: "arn:aws:route53:::hostedzone/<zoneID>/<TYPE>/<name>"
// Zone ARN format: "arn:aws:route53:::hostedzone/<zoneID>"
func recordSetZoneARN(nativeID string) string {
	// The zone ARN prefix is "arn:aws:route53:::hostedzone/<id>".
	// Split off the <TYPE>/<name> suffix by finding the index of the type segment.
	const prefix = "arn:aws:route53:::hostedzone/"
	if !strings.HasPrefix(nativeID, prefix) {
		return ""
	}
	// After the prefix: "<zoneID>/<TYPE>/<name>"
	rest := nativeID[len(prefix):]
	// The zoneID is the first segment before the first slash.
	zoneID, _, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	return prefix + zoneID
}

// resolveRoute53DNSSECRelationships links each DNSSEC resource to its hosted zone.
// NativeID format: "arn:aws:route53:::hostedzone/<zoneID>/dnssec"
func resolveRoute53DNSSECRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRoute53DNSSEC},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		zoneARN := dnssecZoneARN(r.NativeID)
		if zoneARN == "" {
			continue
		}
		zoneID := store.ResourceID("aws", acct.ID, TypeRoute53HostedZone, zoneARN)
		if err := st.UpsertRelationship(r.ID, zoneID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert route53 dnssec→hosted-zone: %w", err)
		}
	}
	return nil
}

// dnssecZoneARN extracts the hosted zone ARN from a DNSSEC NativeID.
// NativeID format: "arn:aws:route53:::hostedzone/<zoneID>/dnssec"
// Zone ARN format: "arn:aws:route53:::hostedzone/<zoneID>"
func dnssecZoneARN(nativeID string) string {
	const suffix = "/dnssec"
	if !strings.HasSuffix(nativeID, suffix) {
		return ""
	}
	candidate := nativeID[:len(nativeID)-len(suffix)]
	const prefix = "arn:aws:route53:::hostedzone/"
	if !strings.HasPrefix(candidate, prefix) {
		return ""
	}
	// Zone ID must be a single path segment (no extra slashes).
	if strings.Contains(candidate[len(prefix):], "/") {
		return ""
	}
	return candidate
}

// resolveRoute53KSKRelationships links each key-signing key to its DNSSEC resource.
// NativeID format: "arn:aws:route53:::hostedzone/<zoneID>/ksk/<name>"
func resolveRoute53KSKRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRoute53KeySigningKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		dnssecNativeID := kskDNSSECNativeID(r.NativeID)
		if dnssecNativeID == "" {
			continue
		}
		dnssecID := store.ResourceID("aws", acct.ID, TypeRoute53DNSSEC, dnssecNativeID)
		if err := st.UpsertRelationship(r.ID, dnssecID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert route53 ksk→dnssec: %w", err)
		}
	}
	return nil
}

// kskDNSSECNativeID derives the DNSSEC NativeID from a KSK NativeID.
// KSK NativeID:    "arn:aws:route53:::hostedzone/<zoneID>/ksk/<name>"
// DNSSEC NativeID: "arn:aws:route53:::hostedzone/<zoneID>/dnssec"
func kskDNSSECNativeID(nativeID string) string {
	const prefix = "arn:aws:route53:::hostedzone/"
	if !strings.HasPrefix(nativeID, prefix) {
		return ""
	}
	// rest = "<zoneID>/ksk/<name>"
	rest := nativeID[len(prefix):]
	zoneID, after, ok := strings.Cut(rest, "/")
	if !ok || zoneID == "" {
		return ""
	}
	// after must begin with "ksk/" to be a valid KSK NativeID.
	if !strings.HasPrefix(after, "ksk/") {
		return ""
	}
	return prefix + zoneID + "/dnssec"
}

// resolveRoute53HealthCheckRelationships links each record set that references
// a health check to that health check via a "uses" relationship.
// HealthCheckID is extracted from the record set's AttributesJSON.
func resolveRoute53HealthCheckRelationships(acct *account, st *store.Store) error {
	records, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRoute53RecordSet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range records {
		var attrs struct {
			HealthCheckID *string `json:"HealthCheckID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		hcID := sv(attrs.HealthCheckID)
		if hcID == "" {
			continue
		}
		hcNativeID := fmt.Sprintf("arn:aws:route53:::healthcheck/%s", hcID)
		hcResID := store.ResourceID("aws", acct.ID, TypeRoute53HealthCheck, hcNativeID)
		if err := st.UpsertRelationship(r.ID, hcResID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert route53 record-set→health-check: %w", err)
		}
	}
	return nil
}
