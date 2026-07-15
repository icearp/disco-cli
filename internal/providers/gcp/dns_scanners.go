package gcp

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/dns/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDNSManagedZone, Service: "dns", Upstream: "dns.googleapis.com/ManagedZone"})
	registerType(restype.Descriptor{Type: TypeDNSRecordSet, Service: "dns", Upstream: "dns.googleapis.com/ResourceRecordSet"})
	registerType(restype.Descriptor{Type: TypeDNSKey, Service: "dns", Leaf: true})
	registerType(restype.Descriptor{Type: TypeDNSPolicy, Service: "dns"})
	registerType(restype.Descriptor{Type: TypeDNSResponsePolicy, Service: "dns"})
	registerType(restype.Descriptor{Type: TypeDNSResponsePolicyRule, Service: "dns", Leaf: true})
	registerService(serviceEntry{
		name: "gcp:clouddns",
		fn:   scanCloudDNS,
	})
}

// maxConcurrentDNSZones caps the per-project zone fan-out for record-set
// listing. Cloud DNS quotas are per-minute project-level — keep modest.
const maxConcurrentDNSZones = 10

// scanCloudDNS discovers Cloud DNS managed zones and their children.
//  1. ManagedZones.List paginated.
//  2. Per-zone ResourceRecordSets.List + DnsKeys.List, fan-out bounded by
//     maxConcurrentDNSZones.
//  3. Policies.List — project-scoped, no zone parent.
//  4. ResponsePolicies.List, then per-response-policy ResponsePolicyRules.List.
//
// RecordSets have no API-issued resource name — synthesized NativeID is
// `{zoneNativeID}/rrsets/{type}/{name}`. Both `name` and `type` are needed:
// (name, type) is the natural key — one zone can have, e.g., www.example.com.
// as both A and AAAA record sets. DnsKey has no name field either —
// synthesized from its per-zone-unique numeric Id.
func scanCloudDNS(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := dns.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("dns client: %w", err)
	}
	return scanCloudDNSWithClient(ctx, svc, p, st, scanID)
}

func scanCloudDNSWithClient(ctx context.Context, svc *dns.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase 1: managed zones.
	type zoneRef struct {
		nativeID string
		name     string
	}
	var zones []zoneRef
	t, n, err := runPaginated(ctx, st, p, "dns:managedZones.list",
		svc.ManagedZones.List(p.ID),
		func(page *dns.ManagedZonesListResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.ManagedZones))
			for _, z := range page.ManagedZones {
				zid := fmt.Sprintf("projects/%s/managedZones/%s", p.ID, z.Name)
				zname := z.Name
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeDNSManagedZone,
					NativeID:       zid,
					Name:           &zname,
					CreatedAt:      strp(z.CreationTime),
					AttributesJSON: mustJSON(z),
					DiscoveredBy:   scanID,
				})
				zones = append(zones, zoneRef{nativeID: zid, name: z.Name})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 2: per-zone record sets + DNSSEC keys.
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentDNSZones, zones, func(gctx context.Context, z zoneRef) error {
		zoneID := store.ResourceID("gcp", p.ID, z.nativeID)

		err := svc.ResourceRecordSets.List(p.ID, z.name).Pages(gctx, func(page *dns.ResourceRecordSetsListResponse) error {
			var batch []*store.Resource
			for _, rr := range page.Rrsets {
				nativeID := fmt.Sprintf("%s/rrsets/%s/%s", z.nativeID, rr.Type, rr.Name)
				name := fmt.Sprintf("%s %s", rr.Name, rr.Type)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeDNSRecordSet,
					NativeID:       nativeID,
					Name:           &name,
					AttributesJSON: mustJSON(rr),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			rt, rn, rerr := upsertWithParent(st, batch, zoneID)
			total += rt
			inserted += rn
			return rerr
		})
		if err != nil {
			if !isPermissionDenied(err) {
				return err
			}
			if serr := skipIfDenied(st, "dns:resourceRecordSets.list", p.ID, err); serr != nil {
				return serr
			}
		}

		err = svc.DnsKeys.List(p.ID, z.name).Pages(gctx, func(page *dns.DnsKeysListResponse) error {
			var batch []*store.Resource
			for _, k := range page.DnsKeys {
				nativeID := fmt.Sprintf("%s/dnsKeys/%s", z.nativeID, k.Id)
				name := fmt.Sprintf("%s (%s)", k.Type, k.Algorithm)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeDNSKey,
					NativeID:       nativeID,
					Name:           &name,
					CreatedAt:      strp(k.CreationTime),
					AttributesJSON: mustJSON(k),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			rt, rn, rerr := upsertWithParent(st, batch, zoneID)
			total += rt
			inserted += rn
			return rerr
		})
		if err != nil {
			if isPermissionDenied(err) {
				return skipIfDenied(st, "dns:dnsKeys.list", p.ID, err)
			}
			return err
		}
		return nil
	}); err != nil {
		return total, inserted, err
	}

	// Phase 3: network policies — project-scoped, no zone parent.
	t, n, err = runPaginated(ctx, st, p, "dns:policies.list",
		svc.Policies.List(p.ID),
		func(page *dns.PoliciesListResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Policies))
			for _, pol := range page.Policies {
				name := pol.Name
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeDNSPolicy,
					NativeID:       fmt.Sprintf("projects/%s/policies/%s", p.ID, pol.Name),
					Name:           &name,
					AttributesJSON: mustJSON(pol),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 4: response policies, then per-response-policy rules.
	type rpRef struct {
		nativeID string
		name     string
	}
	var responsePolicies []rpRef
	t, n, err = runPaginated(ctx, st, p, "dns:responsePolicies.list",
		svc.ResponsePolicies.List(p.ID),
		func(page *dns.ResponsePoliciesListResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.ResponsePolicies))
			for _, rp := range page.ResponsePolicies {
				rpid := fmt.Sprintf("projects/%s/responsePolicies/%s", p.ID, rp.ResponsePolicyName)
				name := rp.ResponsePolicyName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeDNSResponsePolicy,
					NativeID:       rpid,
					Name:           &name,
					AttributesJSON: mustJSON(rp),
					DiscoveredBy:   scanID,
				})
				responsePolicies = append(responsePolicies, rpRef{nativeID: rpid, name: rp.ResponsePolicyName})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	if err := forEachItem(ctx, maxConcurrentDNSZones, responsePolicies, func(gctx context.Context, rp rpRef) error {
		rpID := store.ResourceID("gcp", p.ID, rp.nativeID)
		err := svc.ResponsePolicyRules.List(p.ID, rp.name).Pages(gctx, func(page *dns.ResponsePolicyRulesListResponse) error {
			var batch []*store.Resource
			for _, rule := range page.ResponsePolicyRules {
				nativeID := fmt.Sprintf("%s/rules/%s", rp.nativeID, rule.RuleName)
				name := rule.RuleName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeDNSResponsePolicyRule,
					NativeID:       nativeID,
					Name:           &name,
					AttributesJSON: mustJSON(rule),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			rt, rn, rerr := upsertWithParent(st, batch, rpID)
			total += rt
			inserted += rn
			return rerr
		})
		if err != nil {
			if isPermissionDenied(err) {
				return skipIfDenied(st, "dns:responsePolicyRules.list", rp.name, err)
			}
			return err
		}
		return nil
	}); err != nil {
		return total, inserted, err
	}

	return total, inserted, nil
}
