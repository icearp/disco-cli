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
		resolveShieldProtectionTargets,
		EdgeDecl{TypeShieldProtection, TypeELBv2LoadBalancer, store.RelAttachedTo},
		EdgeDecl{TypeShieldProtection, TypeELBClassicLoadBalancer, store.RelAttachedTo},
		EdgeDecl{TypeShieldProtection, TypeCloudFrontDistribution, store.RelAttachedTo},
		EdgeDecl{TypeShieldProtection, TypeRoute53HostedZone, store.RelAttachedTo},
		EdgeDecl{TypeShieldProtection, TypeEC2EIP, store.RelAttachedTo},
	)
	registerResolver(
		resolveShieldProtectionGroupMembers,
		EdgeDecl{TypeShieldProtectionGroup, TypeShieldProtection, store.RelContains},
	)
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
// segment; the target types are ELBv2 and classic load balancers, CloudFront
// distribution, Route 53 hosted zone, and EC2 Elastic IP. Other shapes
// (Global Accelerator, AppSync, etc.) skip without error. FK-safe via a single
// combined target id-set query.
func resolveShieldProtectionTargets(acct *account, st *store.Store) error {
	protections, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
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

	// A row that does not decode is reported at the end so that it costs the
	// others none of their edges.
	var decodeErrs []error
	for _, p := range protections {
		var attrs shieldProtectionAttrs
		if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeShieldProtection, p.NativeID, err))
			continue
		}
		ref := sv(attrs.ResourceArn)
		if ref == "" {
			continue
		}
		targetNativeID, ok := classifyShieldProtectedResource(ref)
		if !ok {
			continue
		}
		targetID := store.ResourceID("aws", acct.ID, targetNativeID)
		if !known[targetID] {
			continue
		}
		if err := st.UpsertRelationship(p.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert shield protection→target: %w", err)
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveShieldProtectionGroupMembers emits a contains edge from each
// protection group (Pattern=ARBITRARY only) to every member protection ARN
// in its Members[] array. Pattern=ALL / BY_RESOURCE_TYPE are implicit
// memberships needing expansion against the scanned set; deferred. FK-safe
// via a protection id-set lookup.
func resolveShieldProtectionGroupMembers(acct *account, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
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

	// A row that does not decode is reported at the end so that it costs the
	// others none of their edges.
	var decodeErrs []error
	for _, g := range groups {
		var attrs shieldProtectionGroupAttrs
		if err := json.Unmarshal([]byte(g.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeShieldProtectionGroup, g.NativeID, err))
			continue
		}
		if attrs.Pattern != "ARBITRARY" {
			continue
		}
		for _, m := range attrs.Members {
			if m == "" {
				continue
			}
			memberID := store.ResourceID("aws", acct.ID, m)
			if !protSet[memberID] {
				continue
			}
			if err := st.UpsertRelationship(g.ID, memberID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert shield protection-group→protection: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// classifyShieldProtectedResource maps a Shield ResourceArn to the NativeID
// disco stores its target under. ok=false means the ARN is of no target type
// disco stores. ELBv2 and classic load balancers share the
// elasticloadbalancing ARN namespace and both store the ARN itself, so one
// case serves both. EIPs are normalised: Shield emits `:eip-allocation/`,
// disco's EC2 scanner stores `:elastic-ip/` — rewritten to match so the
// FK-safe lookup succeeds.
func classifyShieldProtectedResource(arn string) (nativeID string, ok bool) {
	switch {
	case strings.Contains(arn, ":elasticloadbalancing:") && strings.Contains(arn, ":loadbalancer/"):
		return arn, true
	case strings.Contains(arn, ":cloudfront::") && strings.Contains(arn, ":distribution/"):
		return arn, true
	case strings.Contains(arn, ":route53:::hostedzone/"):
		return arn, true
	case strings.Contains(arn, ":ec2:") && strings.Contains(arn, ":eip-allocation/"):
		return strings.Replace(arn, ":eip-allocation/", ":elastic-ip/", 1), true
	case strings.Contains(arn, ":ec2:") && strings.Contains(arn, ":elastic-ip/"):
		return arn, true
	}
	return "", false
}

func shieldProtectionTargetIDSet(acct *account, st *store.Store) (map[string]bool, error) {
	targets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
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
		Providers: []string{"aws"}, AccountID: acct.ID,
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
