package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

func init() {
	registerService(serviceEntry{
		name: "azure:eventgrid",
		fn:   scanEventGrid,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridTopic},
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridSystemTopic},
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridDomain},
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridEventSubscription},
		},
	})
}

// scanEventGrid discovers Event Grid topics, system topics, domains, and
// global-scope event subscriptions. Per-topic / per-domain / per-system-topic
// event-subscription fan-out is deferred — the global pager covers
// subscription- and resource-group-scoped subscriptions, which is the bulk of
// graph-relevant traffic. Partner-namespace, partner-topic, partner-channel,
// CA-certificates, and namespaces (the newer pull-style EventGrid namespaces)
// deferred to follow-up iterations.
func scanEventGrid(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	topicClient, err := armeventgrid.NewTopicsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armeventgrid:NewTopicsClient: %w", err)
	}
	tt, ti, err := azSimpleScan(ctx, "armeventgrid:Topics.ListBySubscription", TypeEventGridTopic, sub, st, scanID,
		topicClient.NewListBySubscriptionPager(nil),
		func(p armeventgrid.TopicsClientListBySubscriptionResponse) []*armeventgrid.Topic { return p.Value },
		func(t *armeventgrid.Topic) azTrackedBase {
			return azTrackedBase{id: sv(t.ID), name: sv(t.Name), location: sv(t.Location), tags: t.Tags, full: t}
		})
	total += tt
	inserted += ti
	if err != nil {
		return total, inserted, err
	}

	stClient, err := armeventgrid.NewSystemTopicsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armeventgrid:NewSystemTopicsClient: %w", err)
	}
	st1, si1, err := azSimpleScan(ctx, "armeventgrid:SystemTopics.ListBySubscription", TypeEventGridSystemTopic, sub, st, scanID,
		stClient.NewListBySubscriptionPager(nil),
		func(p armeventgrid.SystemTopicsClientListBySubscriptionResponse) []*armeventgrid.SystemTopic {
			return p.Value
		},
		func(t *armeventgrid.SystemTopic) azTrackedBase {
			return azTrackedBase{id: sv(t.ID), name: sv(t.Name), location: sv(t.Location), tags: t.Tags, full: t}
		})
	total += st1
	inserted += si1
	if err != nil {
		return total, inserted, err
	}

	domClient, err := armeventgrid.NewDomainsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armeventgrid:NewDomainsClient: %w", err)
	}
	dt, di, err := azSimpleScan(ctx, "armeventgrid:Domains.ListBySubscription", TypeEventGridDomain, sub, st, scanID,
		domClient.NewListBySubscriptionPager(nil),
		func(p armeventgrid.DomainsClientListBySubscriptionResponse) []*armeventgrid.Domain { return p.Value },
		func(d *armeventgrid.Domain) azTrackedBase {
			return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
		})
	total += dt
	inserted += di
	if err != nil {
		return total, inserted, err
	}

	esClient, err := armeventgrid.NewEventSubscriptionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armeventgrid:NewEventSubscriptionsClient: %w", err)
	}
	et, ei, err := azPageScan(ctx, "armeventgrid:EventSubscriptions.ListGlobalBySubscription", sub, st,
		esClient.NewListGlobalBySubscriptionPager(nil),
		func(page armeventgrid.EventSubscriptionsClientListGlobalBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, e := range page.Value {
				if e == nil || e.ID == nil {
					continue
				}
				name := sv(e.Name)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeEventGridEventSubscription, NativeID: sv(e.ID),
					Name:           &name,
					AttributesJSON: mustJSON(e),
					DiscoveredBy:   scanID,
				})
			}
			return batch, nil
		})
	total += et
	inserted += ei
	return total, inserted, err
}
