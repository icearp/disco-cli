package aws

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// fmsMemberTypes are the disco types of the resource types a Firewall Manager
// policy can protect, as the fms Policy.ResourceType documentation lists them
// (API Gateway stage, CloudFront distribution, ELB and ELBv2 load balancer,
// EC2 EIP, subnet, security group, network interface, instance and VPC). A
// resource set's members are resolved only against rows of these types.
// Whether a resource set accepts any other type is unverified; such a member
// gets no edge.
var fmsMemberTypes = []string{
	TypeAPIGatewayStage,
	TypeCloudFrontDistribution,
	TypeEC2EIP,
	TypeEC2Instance,
	TypeEC2NetworkInterface,
	TypeEC2SecurityGroup,
	TypeEC2Subnet,
	TypeEC2VPC,
	TypeELBClassicLoadBalancer,
	TypeELBv2LoadBalancer,
}

func init() {
	registerResolver(
		resolveFMSPolicyResourceSets,
		EdgeDecl{TypeFMSPolicy, TypeFMSResourceSet, store.RelUses},
	)
	memberEdges := make([]EdgeDecl, len(fmsMemberTypes))
	for i, t := range fmsMemberTypes {
		memberEdges[i] = EdgeDecl{t, TypeFMSResourceSet, store.RelAttachedTo}
	}
	registerMembershipResolver(resolveFMSResourceSetMembers, "fms:ListResourceSetResources", memberEdges...)
}

// fmsPolicyAttrs reads the ResourceSetIds of a policy row's GetPolicy body.
type fmsPolicyAttrs struct {
	ResourceSetIDs []string `json:"ResourceSetIds"`
}

// resolveFMSPolicyResourceSets emits policy -> resource set `uses` for every
// resource set a policy scopes itself with, when that set is stored in the
// policy's region. A row whose attributes do not decode is reported and costs
// no other row its edges.
func resolveFMSPolicyResourceSets(acct *account, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeFMSPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}
	sets, err := resourceIDSet(st, acct.ID, TypeFMSResourceSet)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range policies {
		var attrs fmsPolicyAttrs
		if err := p.UnmarshalAttributes(&attrs); err != nil {
			errs = append(errs, fmt.Errorf("decode fms policy %s: %w", p.NativeID, err))
			continue
		}
		for _, id := range attrs.ResourceSetIDs {
			to := store.ResourceID("aws", acct.ID, fmsResourceSetARN(sv(p.Region), acct.ID, id))
			if _, ok := sets[to]; !ok {
				continue
			}
			if err := st.UpsertRelationship(p.ID, to, store.RelUses, "directed", nil); err != nil {
				return errors.Join(append(errs, fmt.Errorf("upsert fms policy→resource-set: %w", err))...)
			}
		}
	}
	return errors.Join(errs...)
}

// resolveFMSResourceSetMembers emits member -> resource set `attached-to` for
// every member the scan stored as one of fmsMemberTypes. A member is found by
// its URI, an ARN, as the NativeID of a row in the account that owns it, so
// a member in an account this database never scanned gets no edge.
func resolveFMSResourceSetMembers(acct *account, st *store.Store) error {
	sets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeFMSResourceSet}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	type memberEdge struct{ from, to string }
	var (
		pending []memberEdge
		errs    []error
	)
	owners := map[string]bool{}
	for _, s := range sets {
		var attrs fmsResourceSetAttrs
		if err := s.UnmarshalAttributes(&attrs); err != nil {
			errs = append(errs, fmt.Errorf("decode fms resource set %s: %w", s.NativeID, err))
			continue
		}
		if attrs.Resources == nil {
			continue
		}
		for _, m := range *attrs.Resources {
			uri := fmsMemberNativeID(sv(m.URI))
			owner := fmsMemberOwner(sv(m.AccountId), uri)
			if uri == "" || owner == "" {
				continue
			}
			owners[owner] = true
			pending = append(pending, memberEdge{from: store.ResourceID("aws", owner, uri), to: s.ID})
		}
	}
	if len(pending) == 0 {
		return errors.Join(errs...)
	}
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountIDs: slices.Sorted(maps.Keys(owners)), Types: fmsMemberTypes,
		IncludeManaged: true, Limit: util.AllResources,
	})
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	stored := make(map[string]bool, len(rows))
	for _, r := range rows {
		stored[r.ID] = true
	}
	for _, e := range pending {
		if !stored[e.from] {
			continue
		}
		if err := st.UpsertRelationship(e.from, e.to, store.RelAttachedTo, "directed", nil); err != nil {
			return errors.Join(append(errs, fmt.Errorf("upsert fms member→resource-set: %w", err))...)
		}
	}
	return errors.Join(errs...)
}

// fmsMemberNativeID maps a member URI to the NativeID disco stores it under.
// Shield-protected EIPs are named `:eip-allocation/` by AWS's protection APIs
// while the EC2 scanner stores `:elastic-ip/` (same rewrite as
// classifyShieldProtectedResource); whether Firewall Manager answers that
// form is unverified, and the rewrite is a no-op on any other URI.
func fmsMemberNativeID(uri string) string {
	if strings.HasPrefix(uri, "arn:aws:ec2:") {
		return strings.Replace(uri, ":eip-allocation/", ":elastic-ip/", 1)
	}
	return uri
}

// fmsMemberOwner returns the member's AccountId, else the account segment of
// its ARN. AccountId is optional in the model; API Gateway stage ARNs carry
// no account, so such a member needs AccountId.
func fmsMemberOwner(accountID, arn string) string {
	if accountID != "" {
		return accountID
	}
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 || parts[0] != "arn" {
		return ""
	}
	return parts[4]
}
