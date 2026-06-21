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
		name: "azure:microsoft.eventgrid",
		fn:   scanEventGrid,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridTopic},
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridSystemTopic},
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridDomain},
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridEventSubscription},
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridNamespace, Leaf: true},
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridPartnerConfiguration, Leaf: true},
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridPartnerNamespace, Leaf: true},
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridPartnerRegistration, Leaf: true},
			{Service: "microsoft.eventgrid", DiscoType: TypeEventGridPartnerTopic, Leaf: true},
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
	return azRunPhases(
		func() (int, int, error) {
			topicClient, err := armeventgrid.NewTopicsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armeventgrid:NewTopicsClient: %w", err)
			}
			return azSimpleScan(ctx, "armeventgrid:Topics.ListBySubscription", TypeEventGridTopic, sub, st, scanID,
				topicClient.NewListBySubscriptionPager(nil),
				func(p armeventgrid.TopicsClientListBySubscriptionResponse) []*armeventgrid.Topic { return p.Value },
				func(t *armeventgrid.Topic) azTrackedBase {
					return azTrackedBase{id: sv(t.ID), name: sv(t.Name), location: sv(t.Location), tags: t.Tags, full: t}
				})
		},
		func() (int, int, error) {
			stClient, err := armeventgrid.NewSystemTopicsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armeventgrid:NewSystemTopicsClient: %w", err)
			}
			return azSimpleScan(ctx, "armeventgrid:SystemTopics.ListBySubscription", TypeEventGridSystemTopic, sub, st, scanID,
				stClient.NewListBySubscriptionPager(nil),
				func(p armeventgrid.SystemTopicsClientListBySubscriptionResponse) []*armeventgrid.SystemTopic {
					return p.Value
				},
				func(t *armeventgrid.SystemTopic) azTrackedBase {
					return azTrackedBase{id: sv(t.ID), name: sv(t.Name), location: sv(t.Location), tags: t.Tags, full: t}
				})
		},
		func() (int, int, error) {
			domClient, err := armeventgrid.NewDomainsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armeventgrid:NewDomainsClient: %w", err)
			}
			return azSimpleScan(ctx, "armeventgrid:Domains.ListBySubscription", TypeEventGridDomain, sub, st, scanID,
				domClient.NewListBySubscriptionPager(nil),
				func(p armeventgrid.DomainsClientListBySubscriptionResponse) []*armeventgrid.Domain { return p.Value },
				func(d *armeventgrid.Domain) azTrackedBase {
					return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
				})
		},
		func() (int, int, error) {
			esClient, err := armeventgrid.NewEventSubscriptionsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armeventgrid:NewEventSubscriptionsClient: %w", err)
			}
			return azPageScan(ctx, "armeventgrid:EventSubscriptions.ListGlobalBySubscription", sub, st,
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
		},
		func() (int, int, error) {
			nsClient, err := armeventgrid.NewNamespacesClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armeventgrid:NewNamespacesClient: %w", err)
			}
			return azSimpleScan(ctx, "armeventgrid:Namespaces.ListBySubscription", TypeEventGridNamespace, sub, st, scanID,
				nsClient.NewListBySubscriptionPager(nil),
				func(p armeventgrid.NamespacesClientListBySubscriptionResponse) []*armeventgrid.Namespace {
					return p.Value
				},
				func(n *armeventgrid.Namespace) azTrackedBase {
					return azTrackedBase{id: sv(n.ID), name: sv(n.Name), location: sv(n.Location), tags: n.Tags, full: n}
				})
		},
		func() (int, int, error) {
			pcClient, err := armeventgrid.NewPartnerConfigurationsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armeventgrid:NewPartnerConfigurationsClient: %w", err)
			}
			return azSimpleScan(ctx, "armeventgrid:PartnerConfigurations.ListBySubscription", TypeEventGridPartnerConfiguration, sub, st, scanID,
				pcClient.NewListBySubscriptionPager(nil),
				func(p armeventgrid.PartnerConfigurationsClientListBySubscriptionResponse) []*armeventgrid.PartnerConfiguration {
					return p.Value
				},
				func(c *armeventgrid.PartnerConfiguration) azTrackedBase {
					return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
				})
		},
		func() (int, int, error) {
			pnClient, err := armeventgrid.NewPartnerNamespacesClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armeventgrid:NewPartnerNamespacesClient: %w", err)
			}
			return azSimpleScan(ctx, "armeventgrid:PartnerNamespaces.ListBySubscription", TypeEventGridPartnerNamespace, sub, st, scanID,
				pnClient.NewListBySubscriptionPager(nil),
				func(p armeventgrid.PartnerNamespacesClientListBySubscriptionResponse) []*armeventgrid.PartnerNamespace {
					return p.Value
				},
				func(n *armeventgrid.PartnerNamespace) azTrackedBase {
					return azTrackedBase{id: sv(n.ID), name: sv(n.Name), location: sv(n.Location), tags: n.Tags, full: n}
				})
		},
		func() (int, int, error) {
			prClient, err := armeventgrid.NewPartnerRegistrationsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armeventgrid:NewPartnerRegistrationsClient: %w", err)
			}
			return azSimpleScan(ctx, "armeventgrid:PartnerRegistrations.ListBySubscription", TypeEventGridPartnerRegistration, sub, st, scanID,
				prClient.NewListBySubscriptionPager(nil),
				func(p armeventgrid.PartnerRegistrationsClientListBySubscriptionResponse) []*armeventgrid.PartnerRegistration {
					return p.Value
				},
				func(r *armeventgrid.PartnerRegistration) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			ptClient, err := armeventgrid.NewPartnerTopicsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armeventgrid:NewPartnerTopicsClient: %w", err)
			}
			return azSimpleScan(ctx, "armeventgrid:PartnerTopics.ListBySubscription", TypeEventGridPartnerTopic, sub, st, scanID,
				ptClient.NewListBySubscriptionPager(nil),
				func(p armeventgrid.PartnerTopicsClientListBySubscriptionResponse) []*armeventgrid.PartnerTopic {
					return p.Value
				},
				func(t *armeventgrid.PartnerTopic) azTrackedBase {
					return azTrackedBase{id: sv(t.ID), name: sv(t.Name), location: sv(t.Location), tags: t.Tags, full: t}
				})
		},
	)
}
