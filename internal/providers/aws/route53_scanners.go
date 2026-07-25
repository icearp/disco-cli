package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// route53API is the narrow set of Route 53 operations called by the
// scanRoute53 sub-phases.
type route53API interface {
	ListHostedZones(context.Context, *route53.ListHostedZonesInput, ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error)
	ListResourceRecordSets(context.Context, *route53.ListResourceRecordSetsInput, ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
	GetDNSSEC(context.Context, *route53.GetDNSSECInput, ...func(*route53.Options)) (*route53.GetDNSSECOutput, error)
	ListCidrCollections(context.Context, *route53.ListCidrCollectionsInput, ...func(*route53.Options)) (*route53.ListCidrCollectionsOutput, error)
	ListHealthChecks(context.Context, *route53.ListHealthChecksInput, ...func(*route53.Options)) (*route53.ListHealthChecksOutput, error)
	ListTagsForResources(context.Context, *route53.ListTagsForResourcesInput, ...func(*route53.Options)) (*route53.ListTagsForResourcesOutput, error)
	ListReusableDelegationSets(context.Context, *route53.ListReusableDelegationSetsInput, ...func(*route53.Options)) (*route53.ListReusableDelegationSetsOutput, error)
	ListQueryLoggingConfigs(context.Context, *route53.ListQueryLoggingConfigsInput, ...func(*route53.Options)) (*route53.ListQueryLoggingConfigsOutput, error)
	ListTrafficPolicies(context.Context, *route53.ListTrafficPoliciesInput, ...func(*route53.Options)) (*route53.ListTrafficPoliciesOutput, error)
	ListTrafficPolicyInstances(context.Context, *route53.ListTrafficPolicyInstancesInput, ...func(*route53.Options)) (*route53.ListTrafficPolicyInstancesOutput, error)
}

func init() {
	registerType(restype.Descriptor{Type: TypeRoute53HostedZone, Service: "route53", Upstream: "AWS::Route53::HostedZone", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRoute53RecordSet, Service: "route53", Upstream: "AWS::Route53::RecordSet"})
	registerType(restype.Descriptor{Type: TypeRoute53HealthCheck, Service: "route53", Upstream: "AWS::Route53::HealthCheck", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRoute53DNSSEC, Service: "route53", Upstream: "AWS::Route53::DNSSEC"})
	registerType(restype.Descriptor{Type: TypeRoute53KeySigningKey, Service: "route53", Upstream: "AWS::Route53::KeySigningKey"})
	registerType(restype.Descriptor{Type: TypeRoute53CIDRCollection, Service: "route53", Upstream: "AWS::Route53::CidrCollection", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRoute53DelegationSet, Service: "route53", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRoute53QueryLoggingConfig, Service: "route53"})
	registerType(restype.Descriptor{Type: TypeRoute53TrafficPolicy, Service: "route53", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRoute53TrafficPolicyInstance, Service: "route53"})
	registerService(serviceEntry{
		name:   "aws:route53",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			return scanRoute53(ctx, acct, st, scanID)
		},
	})
}

// route53ZoneSummary holds the minimal per-zone data collected during the
// ListHostedZones pass and reused by per-zone sub-scanners.
type route53ZoneSummary struct {
	id   string // bare ID, e.g. "Z1234567890" (stripped of "/hostedzone/" prefix)
	arn  string // arn:aws:route53:::hostedzone/<id>
	zone route53types.HostedZone
}

// scanRoute53 discovers Route53 hosted zones, DNSSEC configs, key-signing keys,
// record sets, CIDR collections, and health checks. Route53 is a global
// service — resources aren't tied to any region.
func scanRoute53(ctx context.Context, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	client := route53.NewFromConfig(acct.cfg, func(o *route53.Options) { o.Region = "us-east-1" })

	// Collect all hosted zone IDs and summaries first, then fetch tags and records.
	var zones []route53ZoneSummary

	pager := route53.NewListHostedZonesPaginator(client, &route53.ListHostedZonesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53:ListHostedZones", acct.ID, "global", err)
			}
			return 0, 0, fmt.Errorf("route53:ListHostedZones: %w", err)
		}
		for _, z := range page.HostedZones {
			bareID := strings.TrimPrefix(sv(z.Id), "/hostedzone/")
			arn := fmt.Sprintf("arn:aws:route53:::hostedzone/%s", bareID)
			zones = append(zones, route53ZoneSummary{id: bareID, arn: arn, zone: z})
		}
	}

	// Fetch zone tags in batches of 10 (API maximum).
	const tagBatch = 10
	tagsByID := make(map[string]*string, len(zones))
	for i := 0; i < len(zones); i += tagBatch {
		end := min(i+tagBatch, len(zones))
		ids := make([]string, 0, end-i)
		for _, zs := range zones[i:end] {
			ids = append(ids, zs.id)
		}
		out, err := client.ListTagsForResources(ctx, &route53.ListTagsForResourcesInput{
			ResourceType: route53types.TagResourceTypeHostedzone,
			ResourceIds:  ids,
		})
		if err == nil {
			for _, rts := range out.ResourceTagSets {
				if rts.ResourceId != nil {
					tagsByID[*rts.ResourceId] = awsTagsJSON(rts.Tags)
				}
			}
		}
		// Tags are best-effort; continue even if the call fails.
	}

	// Upsert hosted zones.
	var zoneBatch []*store.Resource
	for _, zs := range zones {
		z := zs.zone
		// Zone name has a trailing dot (e.g. "example.com.") — strip it for display.
		name := strings.TrimSuffix(sv(z.Name), ".")
		r := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Region:         regionGlobal,
			Type:           TypeRoute53HostedZone,
			NativeID:       zs.arn,
			Name:           &name,
			AttributesJSON: mustJSON(z),
			TagsJSON:       tagsByID[zs.id],
			DiscoveredBy:   scanID,
		}
		zoneBatch = append(zoneBatch, r)
	}
	if len(zoneBatch) > 0 {
		n, err := st.UpsertResources(zoneBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Route53 hosted zones: %w", err)
		}
		total += len(zoneBatch)
		inserted += n
	}

	// Scan DNSSEC configs and key-signing keys for each zone (one GetDNSSEC call per zone).
	t, n, err := scanRoute53DNSSEC(ctx, client, acct, zones, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Scan record sets for each hosted zone.
	for _, zs := range zones {
		t, n, err := scanRoute53RecordSets(ctx, client, acct, zs.id, zs.arn, st, scanID)
		total += t
		inserted += n
		if err != nil {
			return total, inserted, err
		}
	}

	// Scan CIDR collections (account-level, not per-zone).
	t, n, err = scanRoute53CIDRCollections(ctx, client, acct, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Scan health checks (account-level).
	t, n, err = scanRoute53HealthChecks(ctx, client, acct, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Account-level: reusable delegation sets, query-logging configs, traffic
	// policies and traffic-policy instances.
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanRoute53DelegationSets(ctx, client, acct, st, scanID) },
		func() (int, int, error) { return scanRoute53QueryLoggingConfigs(ctx, client, acct, st, scanID) },
		func() (int, int, error) { return scanRoute53TrafficPolicies(ctx, client, acct, st, scanID) },
		func() (int, int, error) { return scanRoute53TrafficPolicyInstances(ctx, client, acct, st, scanID) },
	} {
		t, n, perr := phase()
		total += t
		inserted += n
		if perr != nil {
			return total, inserted, perr
		}
	}
	return total, inserted, nil
}

// scanRoute53DelegationSets lists reusable delegation sets (no paginator; manual
// Marker pagination). NativeID is synthesized — Route53 ids are bare.
func scanRoute53DelegationSets(ctx context.Context, client route53API, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var marker *string
	for {
		out, err := client.ListReusableDelegationSets(ctx, &route53.ListReusableDelegationSetsInput{Marker: marker})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53:ListReusableDelegationSets", acct.ID, "global", err)
			}
			return 0, 0, fmt.Errorf("route53:ListReusableDelegationSets: %w", err)
		}
		for i := range out.DelegationSets {
			d := &out.DelegationSets[i]
			id := strings.TrimPrefix(sv(d.Id), "/delegationset/")
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:route53:::delegationset/%s", id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Region: regionGlobal, Type: TypeRoute53DelegationSet, NativeID: arn,
				Name: &label, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if !out.IsTruncated || out.NextMarker == nil {
			break
		}
		marker = out.NextMarker
	}
	return upsertBatch(st, batch, "route53 delegation-sets")
}

// scanRoute53QueryLoggingConfigs lists query-logging configs (paginator).
// NativeID synthesized from the bare config Id.
func scanRoute53QueryLoggingConfigs(ctx context.Context, client route53API, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := route53.NewListQueryLoggingConfigsPaginator(client, &route53.ListQueryLoggingConfigsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53:ListQueryLoggingConfigs", acct.ID, "global", err)
			}
			return 0, 0, fmt.Errorf("route53:ListQueryLoggingConfigs: %w", err)
		}
		for i := range page.QueryLoggingConfigs {
			c := &page.QueryLoggingConfigs[i]
			id := sv(c.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:route53:::queryloggingconfig/%s", id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Region: regionGlobal, Type: TypeRoute53QueryLoggingConfig, NativeID: arn,
				Name: &label, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53 query-logging-configs")
}

// scanRoute53TrafficPolicies lists traffic policies (no paginator; manual
// TrafficPolicyIdMarker pagination).
func scanRoute53TrafficPolicies(ctx context.Context, client route53API, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var marker *string
	for {
		out, err := client.ListTrafficPolicies(ctx, &route53.ListTrafficPoliciesInput{TrafficPolicyIdMarker: marker})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53:ListTrafficPolicies", acct.ID, "global", err)
			}
			return 0, 0, fmt.Errorf("route53:ListTrafficPolicies: %w", err)
		}
		for i := range out.TrafficPolicySummaries {
			p := &out.TrafficPolicySummaries[i]
			id := sv(p.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:route53:::trafficpolicy/%s", id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Region: regionGlobal, Type: TypeRoute53TrafficPolicy, NativeID: arn,
				Name: p.Name, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if !out.IsTruncated || out.TrafficPolicyIdMarker == nil {
			break
		}
		marker = out.TrafficPolicyIdMarker
	}
	return upsertBatch(st, batch, "route53 traffic-policies")
}

// scanRoute53TrafficPolicyInstances lists traffic-policy instances (no
// paginator; manual three-marker pagination).
func scanRoute53TrafficPolicyInstances(ctx context.Context, client route53API, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var zoneMarker, nameMarker *string
	var typeMarker route53types.RRType
	for {
		out, err := client.ListTrafficPolicyInstances(ctx, &route53.ListTrafficPolicyInstancesInput{
			HostedZoneIdMarker:              zoneMarker,
			TrafficPolicyInstanceNameMarker: nameMarker,
			TrafficPolicyInstanceTypeMarker: typeMarker,
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53:ListTrafficPolicyInstances", acct.ID, "global", err)
			}
			return 0, 0, fmt.Errorf("route53:ListTrafficPolicyInstances: %w", err)
		}
		for i := range out.TrafficPolicyInstances {
			ti := &out.TrafficPolicyInstances[i]
			id := sv(ti.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:route53:::trafficpolicyinstance/%s", id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Region: regionGlobal, Type: TypeRoute53TrafficPolicyInstance, NativeID: arn,
				Name: ti.Name, AttributesJSON: mustJSON(ti), DiscoveredBy: scanID,
			})
		}
		if !out.IsTruncated {
			break
		}
		zoneMarker, nameMarker, typeMarker = out.HostedZoneIdMarker, out.TrafficPolicyInstanceNameMarker, out.TrafficPolicyInstanceTypeMarker
	}
	return upsertBatch(st, batch, "route53 traffic-policy-instances")
}

// scanRoute53RecordSets pages through all record sets in one hosted zone and
// upserts them. NativeID is composed as "<zoneARN>/<type>/<name>" — stable
// and unique per record set within the zone.
func scanRoute53RecordSets(ctx context.Context, client route53API, acct *account, zoneID, zoneARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := route53.NewListResourceRecordSetsPaginator(client, &route53.ListResourceRecordSetsInput{
		HostedZoneId: &zoneID,
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, nil // skip this zone's records silently
			}
			return 0, 0, fmt.Errorf("route53:ListResourceRecordSets (zone %s): %w", zoneID, err)
		}
		var batch []*store.Resource
		for _, rr := range page.ResourceRecordSets {
			rrName := strings.TrimSuffix(sv(rr.Name), ".")
			rrType := string(rr.Type)
			// NativeID uniquely identifies this record set within the account.
			nativeID := fmt.Sprintf("%s/%s/%s", zoneARN, rrType, rrName)
			name := fmt.Sprintf("%s %s", rrType, rrName)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeRoute53RecordSet,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(rr),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Route53 record sets (zone %s): %w", zoneID, err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanRoute53DNSSEC calls GetDNSSEC once per hosted zone and upserts one
// aws:route53:dnssec resource and zero or more aws:route53:key-signing-key
// resources per zone. Both resource types come from the same API call.
func scanRoute53DNSSEC(
	ctx context.Context,
	client route53API,
	acct *account,
	zones []route53ZoneSummary,
	st *store.Store,
	scanID string,
) (total, inserted int, err error) {
	// dnssecAttrs is the shape stored in AttributesJSON for the dnssec resource.
	type dnssecAttrs struct {
		Status         *route53types.DNSSECStatus   `json:"Status"`
		KeySigningKeys []route53types.KeySigningKey `json:"KeySigningKeys"`
	}

	for _, zs := range zones {
		// GetDNSSEC is unsupported for private hosted zones; skip them.
		if zs.zone.Config != nil && zs.zone.Config.PrivateZone {
			continue
		}
		out, err := client.GetDNSSEC(ctx, &route53.GetDNSSECInput{HostedZoneId: &zs.id})
		if err != nil {
			if isAccessDenied(err) {
				continue // best-effort per zone
			}
			return 0, 0, fmt.Errorf("route53:GetDNSSEC (zone %s): %w", zs.id, err)
		}

		// Upsert the DNSSEC configuration resource for this zone.
		dnssecNativeID := fmt.Sprintf("%s/dnssec", zs.arn)
		dnssecName := fmt.Sprintf("DNSSEC %s", zs.id)
		n, err := st.UpsertResources([]*store.Resource{{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Region:         regionGlobal,
			Type:           TypeRoute53DNSSEC,
			NativeID:       dnssecNativeID,
			Name:           &dnssecName,
			AttributesJSON: mustJSON(dnssecAttrs{Status: out.Status, KeySigningKeys: out.KeySigningKeys}),
			DiscoveredBy:   scanID,
		}})
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Route53 DNSSEC (zone %s): %w", zs.id, err)
		}
		total++
		inserted += n

		// Upsert one resource per key-signing key in this zone.
		var kskBatch []*store.Resource
		for _, ksk := range out.KeySigningKeys {
			kskName := sv(ksk.Name)
			if kskName == "" {
				continue
			}
			kskNativeID := fmt.Sprintf("%s/ksk/%s", zs.arn, kskName)
			displayName := fmt.Sprintf("KSK %s", kskName)
			kskBatch = append(kskBatch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeRoute53KeySigningKey,
				NativeID:       kskNativeID,
				Name:           &displayName,
				AttributesJSON: mustJSON(ksk),
				DiscoveredBy:   scanID,
			})
		}
		if len(kskBatch) > 0 {
			n, err := st.UpsertResources(kskBatch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Route53 KSKs (zone %s): %w", zs.id, err)
			}
			total += len(kskBatch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanRoute53CIDRCollections pages through all CIDR collections in the account
// and upserts each as an aws:route53:cidr-collection resource. NativeID is
// the collection ARN (real, API-provided).
func scanRoute53CIDRCollections(
	ctx context.Context,
	client route53API,
	acct *account,
	st *store.Store,
	scanID string,
) (total, inserted int, err error) {
	pager := route53.NewListCidrCollectionsPaginator(client, &route53.ListCidrCollectionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53:ListCidrCollections", acct.ID, "global", err)
			}
			return 0, 0, fmt.Errorf("route53:ListCidrCollections: %w", err)
		}
		var batch []*store.Resource
		for i := range page.CidrCollections {
			c := &page.CidrCollections[i]
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			name := sv(c.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeRoute53CIDRCollection,
				NativeID:       arn,
				Name:           &name,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Route53 CIDR collections: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanRoute53HealthChecks pages through all health checks in the account and
// upserts each as an aws:route53:health-check resource. Tags are fetched in
// batches of 10 via ListTagsForResources (best-effort, same as zone tags).
func scanRoute53HealthChecks(
	ctx context.Context,
	client route53API,
	acct *account,
	st *store.Store,
	scanID string,
) (total, inserted int, err error) {
	// healthCheckSummary holds the bare ID and full struct for tag-batching.
	type healthCheckSummary struct {
		id string
		hc route53types.HealthCheck
	}
	var checks []healthCheckSummary

	pager := route53.NewListHealthChecksPaginator(client, &route53.ListHealthChecksInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "route53:ListHealthChecks", acct.ID, "global", err)
			}
			return 0, 0, fmt.Errorf("route53:ListHealthChecks: %w", err)
		}
		for _, hc := range page.HealthChecks {
			if hc.Id == nil {
				continue
			}
			checks = append(checks, healthCheckSummary{id: *hc.Id, hc: hc})
		}
	}

	// Batch-fetch tags (max 10 per call, same pattern as zone tags).
	const tagBatch = 10
	tagsByID := make(map[string]*string, len(checks))
	for i := 0; i < len(checks); i += tagBatch {
		end := min(i+tagBatch, len(checks))
		ids := make([]string, 0, end-i)
		for _, cs := range checks[i:end] {
			ids = append(ids, cs.id)
		}
		out, err := client.ListTagsForResources(ctx, &route53.ListTagsForResourcesInput{
			ResourceType: route53types.TagResourceTypeHealthcheck,
			ResourceIds:  ids,
		})
		if err == nil {
			for _, rts := range out.ResourceTagSets {
				if rts.ResourceId != nil {
					tagsByID[*rts.ResourceId] = awsTagsJSON(rts.Tags)
				}
			}
		}
		// Tags are best-effort; continue even if the call fails.
	}

	// Upsert health checks.
	var batch []*store.Resource
	for _, cs := range checks {
		nativeID := fmt.Sprintf("arn:aws:route53:::healthcheck/%s", cs.id)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Region:         regionGlobal,
			Type:           TypeRoute53HealthCheck,
			NativeID:       nativeID,
			AttributesJSON: mustJSON(cs.hc),
			TagsJSON:       tagsByID[cs.id],
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Route53 health checks: %w", err)
		}
		total += len(batch)
		inserted += n
	}
	return total, inserted, nil
}
