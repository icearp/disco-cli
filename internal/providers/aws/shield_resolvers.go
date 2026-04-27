package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveShieldProtectionTargets)
	registerResolver(resolveShieldProtectionGroupMembers)
}

// shieldProtectionAttrs mirrors the verbatim ListProtections entry stored as
// AttributesJSON. Tags are PascalCase to match mustJSON of the AWS SDK v2
// types.Protection struct (no json tags).
type shieldProtectionAttrs struct {
	ResourceArn *string `json:"ResourceArn"`
}

// shieldProtectionGroupAttrs mirrors the verbatim ListProtectionGroups entry.
// Pattern values are upper-case (ARBITRARY, ALL, BY_RESOURCE_TYPE).
type shieldProtectionGroupAttrs struct {
	Pattern string   `json:"Pattern"`
	Members []string `json:"Members"`
}

// resolveShieldProtectionTargets emits an attached-to edge from each Shield
// protection to the resource it protects. ResourceArn is classified by ARN
// segment; the four target types currently scanned are ELBv2 load balancer,
// CloudFront distribution, Route 53 hosted zone, and EC2 Elastic IP. Other
// shapes (Global Accelerator, AppSync, etc.) skip without error. FK-safe via
// a single combined target id-set query.
func resolveShieldProtectionTargets(acct *account, st *store.Store) error {
	protections, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeShieldProtection},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(protections) == 0 {
		return nil
	}

	known, err := shieldProtectionTargetIDSet(acct, st)
	if err != nil {
		return err
	}

	for _, p := range protections {
		var attrs shieldProtectionAttrs
		if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil {
			continue
		}
		ref := sv(attrs.ResourceArn)
		if ref == "" {
			continue
		}
		targetType, targetNativeID, ok := classifyShieldProtectedResource(ref)
		if !ok {
			continue
		}
		targetID := store.ResourceID("aws", acct.ID, targetType, targetNativeID)
		if !known[targetID] {
			continue
		}
		if err := st.UpsertRelationship(p.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert shield protection→target: %w", err)
		}
	}
	return nil
}

// resolveShieldProtectionGroupMembers emits a contains edge from each
// protection group (Pattern=ARBITRARY only) to every member protection ARN
// listed in its Members[] array. Pattern=ALL and Pattern=BY_RESOURCE_TYPE are
// implicit memberships that would require expanding against the scanned
// protection set; deferred. FK-safe via a protection id-set lookup.
func resolveShieldProtectionGroupMembers(acct *account, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeShieldProtectionGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}

	protSet, err := shieldProtectionIDSet(acct, st)
	if err != nil {
		return err
	}

	for _, g := range groups {
		var attrs shieldProtectionGroupAttrs
		if err := json.Unmarshal([]byte(g.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Pattern != "ARBITRARY" {
			continue
		}
		for _, m := range attrs.Members {
			if m == "" {
				continue
			}
			memberID := store.ResourceID("aws", acct.ID, TypeShieldProtection, m)
			if !protSet[memberID] {
				continue
			}
			if err := st.UpsertRelationship(g.ID, memberID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert shield protection-group→protection: %w", err)
			}
		}
	}
	return nil
}

// classifyShieldProtectedResource maps a Shield ResourceArn to (disco type,
// canonical NativeID, ok). Returns ok=false when the ARN does not match any
// scanned target type. EIPs receive a small normalisation: Shield emits
// `:eip-allocation/` while disco's EC2 scanner stores `:elastic-ip/` — rewrite
// to match the scanner's NativeID shape so the FK-safe lookup succeeds.
func classifyShieldProtectedResource(arn string) (rtype, nativeID string, ok bool) {
	switch {
	case strings.Contains(arn, ":elasticloadbalancingv2:") && strings.Contains(arn, ":loadbalancer/"):
		return TypeELBv2LoadBalancer, arn, true
	case strings.Contains(arn, ":elasticloadbalancing:") && strings.Contains(arn, ":loadbalancer/"):
		// Classic ELB protections — historically rare, but Shield supports them.
		return TypeELBClassicLoadBalancer, arn, true
	case strings.Contains(arn, ":cloudfront::") && strings.Contains(arn, ":distribution/"):
		return TypeCloudFrontDistribution, arn, true
	case strings.Contains(arn, ":route53:::hostedzone/"):
		return TypeRoute53HostedZone, arn, true
	case strings.Contains(arn, ":ec2:") && strings.Contains(arn, ":eip-allocation/"):
		return TypeEC2EIP, strings.Replace(arn, ":eip-allocation/", ":elastic-ip/", 1), true
	case strings.Contains(arn, ":ec2:") && strings.Contains(arn, ":elastic-ip/"):
		return TypeEC2EIP, arn, true
	}
	return "", "", false
}

func shieldProtectionTargetIDSet(acct *account, st *store.Store) (map[string]bool, error) {
	targets, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{
			TypeELBv2LoadBalancer,
			TypeELBClassicLoadBalancer,
			TypeCloudFrontDistribution,
			TypeRoute53HostedZone,
			TypeEC2EIP,
		},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(targets))
	for _, r := range targets {
		m[r.ID] = true
	}
	return m, nil
}

func shieldProtectionIDSet(acct *account, st *store.Store) (map[string]bool, error) {
	targets, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeShieldProtection},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(targets))
	for _, r := range targets {
		m[r.ID] = true
	}
	return m, nil
}
