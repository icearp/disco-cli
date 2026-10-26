package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveSecurityHubProductSubscriptions,
		EdgeDecl{TypeSecurityHubProductSubscription, TypeGuardDutyDetector, store.RelUses},
		EdgeDecl{TypeSecurityHubProductSubscription, TypeConfigRecorder, store.RelUses},
		EdgeDecl{TypeSecurityHubProductSubscription, TypeMacieSession, store.RelUses},
	)
	registerResolver(
		noopSecurityHubHierarchy,
		EdgeDecl{TypeSecurityHubHub, TypeSecurityHubInsight, store.RelContains},
		EdgeDecl{TypeSecurityHubHub, TypeSecurityHubStandardsSubscription, store.RelContains},
		EdgeDecl{TypeSecurityHubHub, TypeSecurityHubProductSubscription, store.RelContains},
		EdgeDecl{TypeSecurityHubHub, TypeSecurityHubActionTarget, store.RelContains},
		EdgeDecl{TypeSecurityHubHub, TypeSecurityHubMember, store.RelContains},
		EdgeDecl{TypeSecurityHubSecurityControl, TypeSecurityHubStandardsControlAssociation, store.RelContains},
	)
	registerResolver(
		resolveSecurityHubMemberOrgAccount,
		EdgeDecl{TypeSecurityHubMember, TypeOrganizationsAccount, store.RelAttachedTo},
	)
	registerResolver(
		resolveSecurityHubControlAssociationSubscriptions,
		EdgeDecl{TypeSecurityHubStandardsControlAssociation, TypeSecurityHubStandardsSubscription, store.RelAttachedTo},
	)
}

// noopSecurityHubHierarchy only carries EdgeDecl metadata: the scanner records
// a hub→child contains pair for every child type upsertSecurityHubChildren
// stores, and a control→association pair for every standards-control
// association, itself.
func noopSecurityHubHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveSecurityHubMemberOrgAccount emits an attached-to edge from each
// Security Hub member row to its Organizations account when the org tree was
// scanned; with no org tree it emits nothing. A row whose attributes do not
// parse is reported in the returned error after the remaining rows are wired.
func resolveSecurityHubMemberOrgAccount(acct *account, st *store.Store) error {
	members, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeSecurityHubMember}, Limit: util.AllResources,
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
	var bad []error
	for _, m := range members {
		var attrs struct {
			AccountID *string `json:"AccountId"`
		}
		if err := json.Unmarshal([]byte(m.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("securityhub member %s attributes: %w", m.NativeID, err))
			continue
		}
		orgARN, ok := orgArnByID[sv(attrs.AccountID)]
		if !ok {
			continue
		}
		orgID := store.ResourceID("aws", acct.ID, orgARN)
		if err := st.UpsertRelationship(m.ID, orgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert securityhub member→org account: %w", err)
		}
	}
	return errors.Join(bad...)
}

// resolveSecurityHubControlAssociationSubscriptions emits an attached-to edge
// from each standards-control association to the account's subscription to
// that standard in the same region, matched on StandardsArn. An association
// whose subscription was not stored gets no edge. A row whose attributes do
// not parse is reported in the returned error after the remaining rows are
// wired.
func resolveSecurityHubControlAssociationSubscriptions(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeSecurityHubStandardsControlAssociation}, Limit: util.AllResources,
	})
	if err != nil || len(assocs) == 0 {
		return err
	}
	subs, err := listAccountRows(acct, st, TypeSecurityHubStandardsSubscription)
	if err != nil {
		return err
	}
	type standardsRef struct {
		StandardsArn *string `json:"StandardsArn"`
	}
	var bad []error
	subByStandard := make(map[string]string, len(subs))
	for _, s := range subs {
		var attrs standardsRef
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("securityhub standards subscription %s attributes: %w", s.NativeID, err))
			continue
		}
		if arn := sv(attrs.StandardsArn); arn != "" {
			subByStandard[sv(s.Region)+"\x00"+arn] = s.ID
		}
	}
	for _, a := range assocs {
		var attrs standardsRef
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("securityhub standards-control association %s attributes: %w", a.NativeID, err))
			continue
		}
		subID, ok := subByStandard[sv(a.Region)+"\x00"+sv(attrs.StandardsArn)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(a.ID, subID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert securityhub standards-control association→subscription: %w", err)
		}
	}
	return errors.Join(bad...)
}

// resolveSecurityHubProductSubscriptions emits a uses edge from each Security
// Hub product subscription to the upstream finding source already scanned in
// the same (account, region). Subscription ARN shape:
//
//	arn:aws:securityhub:{region}:{acct}:product-subscription/{vendor}/{product}
//
// AWS-vendor products map to disco scanner types:
//
//	aws/guardduty            → aws:guardduty:detector  (any in region)
//	aws/macie                → aws:macie:session       (singleton, NativeID known)
//	aws/config               → aws:config:configuration-recorder     (any in region)
//
// Self (aws/securityhub) skipped. Third-party vendors and AWS products
// without a scanner (Inspector, IAM Access Analyzer, Firewall Manager,
// Detective, etc.) skip silently. FK-safe via per-type id sets keyed on
// resource ID.
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
			tid := store.ResourceID("aws", acct.ID, macieSessionNativeIDIn(acct.partition(), acct.ID, region))
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

// listAccountRows lists every row of one type in acct.
func listAccountRows(acct *account, st *store.Store, rtype string) ([]store.Resource, error) {
	return st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype},
		Limit: util.AllResources,
	})
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
