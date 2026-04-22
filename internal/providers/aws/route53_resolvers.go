package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(func(acct *account, st *store.Store) error {
		if err := resolveRoute53Relationships(acct, st); err != nil {
			return err
		}
		if err := resolveRoute53DNSSECRelationships(acct, st); err != nil {
			return err
		}
		if err := resolveRoute53KSKRelationships(acct, st); err != nil {
			return err
		}
		return resolveRoute53HealthCheckRelationships(acct, st)
	})
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
// HealthCheckId is extracted from the record set's AttributesJSON.
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
			HealthCheckId *string `json:"HealthCheckId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		hcID := sv(attrs.HealthCheckId)
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
