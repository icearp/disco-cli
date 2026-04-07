package aws

import (
	"fmt"
	"strings"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveRoute53Relationships) }

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
