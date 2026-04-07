package aws

import (
	"context"
	"fmt"
	"strings"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:route53",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) error {
			return scanRoute53(ctx, acct, st, scanID)
		},
	})
}

// scanRoute53 discovers Route53 hosted zones and their record sets.
// Route53 is a global service; zones and records are not tied to a specific region.
// Zone tags are fetched in batches via ListTagsForResources (max 10 per call).
// Record sets are paginated per zone via ListResourceRecordSets.
func scanRoute53(ctx context.Context, acct *account, st *store.Store, scanID string) error {
	client := route53.NewFromConfig(acct.cfg, func(o *route53.Options) { o.Region = "us-east-1" })

	// Collect all hosted zone IDs and summaries first, then fetch tags and records.
	type zoneSummary struct {
		id   string // bare ID, e.g. "Z1234567890" (stripped of "/hostedzone/" prefix)
		arn  string // arn:aws:route53:::hostedzone/<id>
		zone route53types.HostedZone
	}
	var zones []zoneSummary

	pager := route53.NewListHostedZonesPaginator(client, &route53.ListHostedZonesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("route53:ListHostedZones", acct.ID, "global", err)
			}
			return fmt.Errorf("route53:ListHostedZones: %w", err)
		}
		for _, z := range page.HostedZones {
			bareID := strings.TrimPrefix(sv(z.Id), "/hostedzone/")
			arn := fmt.Sprintf("arn:aws:route53:::hostedzone/%s", bareID)
			zones = append(zones, zoneSummary{id: bareID, arn: arn, zone: z})
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
		if err := st.UpsertResources(zoneBatch); err != nil {
			return fmt.Errorf("upsert Route53 hosted zones: %w", err)
		}
	}

	// Scan record sets for each hosted zone.
	for _, zs := range zones {
		if err := scanRoute53RecordSets(ctx, client, acct, zs.id, zs.arn, st, scanID); err != nil {
			return err
		}
	}
	return nil
}

// scanRoute53RecordSets pages through all record sets in one hosted zone and
// upserts them. The NativeID is composed as "<zoneARN>/<type>/<name>" to produce
// a stable, unique identifier per record set within the zone.
func scanRoute53RecordSets(ctx context.Context, client *route53.Client, acct *account, zoneID, zoneARN string, st *store.Store, scanID string) error {
	pager := route53.NewListResourceRecordSetsPaginator(client, &route53.ListResourceRecordSetsInput{
		HostedZoneId: &zoneID,
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil // skip this zone's records silently
			}
			return fmt.Errorf("route53:ListResourceRecordSets (zone %s): %w", zoneID, err)
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
				Type:           TypeRoute53RecordSet,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(rr),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert Route53 record sets (zone %s): %w", zoneID, err)
			}
		}
	}
	return nil
}
