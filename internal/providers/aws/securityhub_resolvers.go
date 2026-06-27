package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveSecurityHubProductSubscriptions,
		EdgeDecl{TypeSecurityHubProductSubscription, TypeGuardDutyDetector, store.RelUses},
		EdgeDecl{TypeSecurityHubProductSubscription, TypeConfigRecorder, store.RelUses},
		EdgeDecl{TypeSecurityHubProductSubscription, TypeMacieSession, store.RelUses},
	)
}

// resolveSecurityHubProductSubscriptions emits a uses edge from each Security
// Hub product subscription to the upstream finding source already scanned in
// the same (account, region). Subscription ARN shape:
//
//	arn:aws:securityhub:{region}:{acct}:product-subscription/{vendor}/{product}
//
// AWS-vendor products map to disco scanner types as follows:
//
//	aws/guardduty            → aws:guardduty:detector  (any in region)
//	aws/macie                → aws:macie:session       (singleton, NativeID known)
//	aws/config               → aws:config:recorder     (any in region)
//
// Self (aws/securityhub) skipped — no edge. Third-party vendors and AWS
// products without a corresponding scanner (Inspector, IAM Access Analyzer,
// Firewall Manager, Detective, etc.) skip silently. FK-safe via per-type id
// sets keyed on resource ID.
func resolveSecurityHubProductSubscriptions(acct *account, st *store.Store) error {
	subs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeSecurityHubProductSubscription},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return nil
	}

	// Pre-build per-type id-sets. Each set maps "{region}\x00{resourceID}"
	// → bool so the per-region lookup below stays O(1) without scanning the
	// whole list per subscription.
	detectorByRegion, err := scannedIDsByRegion(acct, st, TypeGuardDutyDetector)
	if err != nil {
		return err
	}
	recorderByRegion, err := scannedIDsByRegion(acct, st, TypeConfigRecorder)
	if err != nil {
		return err
	}
	macieSet, err := scannedIDSet(acct, st, TypeMacieSession)
	if err != nil {
		return err
	}

	for _, s := range subs {
		region := sv(s.Region)
		vendor, product, ok := parseSecurityHubProductSubscriptionARN(s.NativeID)
		if !ok || vendor != "aws" {
			continue
		}
		var targets []string
		switch product {
		case "securityhub":
			continue
		case "guardduty":
			targets = detectorByRegion[region]
		case "config":
			targets = recorderByRegion[region]
		case "macie":
			tid := store.ResourceID("aws", acct.ID, TypeMacieSession, macieSessionNativeID(acct.ID, region))
			if macieSet[tid] {
				targets = []string{tid}
			}
		default:
			continue
		}
		for _, t := range targets {
			if err := st.UpsertRelationship(s.ID, t, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert securityhub product-subscription→source: %w", err)
			}
		}
	}
	return nil
}

// parseSecurityHubProductSubscriptionARN extracts vendor + product from the
// trailing path segment of a product-subscription ARN. Returns ok=false on any
// shape mismatch.
func parseSecurityHubProductSubscriptionARN(arn string) (vendor, product string, ok bool) {
	_, tail, ok := strings.Cut(arn, ":product-subscription/")
	if !ok {
		return "", "", false
	}
	v, p, ok := strings.Cut(tail, "/")
	if !ok || v == "" || p == "" {
		return "", "", false
	}
	return v, p, true
}

// scannedIDsByRegion builds a region → []resourceID map for one type, scoped
// to acct. Used when a target service has no fixed NativeID per region (e.g.
// guardduty detector, config recorder may have multiple instances).
func scannedIDsByRegion(acct *account, st *store.Store, rtype string) (map[string][]string, error) {
	rs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{rtype},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string][]string)
	for _, r := range rs {
		region := sv(r.Region)
		m[region] = append(m[region], r.ID)
	}
	return m, nil
}

// scannedIDSet builds an id-set for one type, scoped to acct.
func scannedIDSet(acct *account, st *store.Store, rtype string) (map[string]bool, error) {
	rs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{rtype},
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
