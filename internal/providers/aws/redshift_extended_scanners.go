package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/redshift"
	redshifttypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/icearp/disco-cli/store"
)

// redshiftExtAPI lists the ops of scanRedshift's extended list phases.
type redshiftExtAPI interface {
	DescribeClusterParameterGroups(context.Context, *redshift.DescribeClusterParameterGroupsInput, ...func(*redshift.Options)) (*redshift.DescribeClusterParameterGroupsOutput, error)
	DescribeEndpointAccess(context.Context, *redshift.DescribeEndpointAccessInput, ...func(*redshift.Options)) (*redshift.DescribeEndpointAccessOutput, error)
	DescribeEndpointAuthorization(context.Context, *redshift.DescribeEndpointAuthorizationInput, ...func(*redshift.Options)) (*redshift.DescribeEndpointAuthorizationOutput, error)
	DescribeEventSubscriptions(context.Context, *redshift.DescribeEventSubscriptionsInput, ...func(*redshift.Options)) (*redshift.DescribeEventSubscriptionsOutput, error)
	DescribeIntegrations(context.Context, *redshift.DescribeIntegrationsInput, ...func(*redshift.Options)) (*redshift.DescribeIntegrationsOutput, error)
	DescribeScheduledActions(context.Context, *redshift.DescribeScheduledActionsInput, ...func(*redshift.Options)) (*redshift.DescribeScheduledActionsOutput, error)
}

func redshiftARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:redshift:%s:%s:%s:%s", region, acct, kind, id)
}

func scanRedshiftClusterParameterGroups(ctx context.Context, client redshiftExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeClusterParameterGroups", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeClusterParameterGroupsOutput, error) {
			return client.DescribeClusterParameterGroups(ctx, &redshift.DescribeClusterParameterGroupsInput{Marker: marker})
		},
		func(o *redshift.DescribeClusterParameterGroupsOutput) ([]redshifttypes.ClusterParameterGroup, *string) {
			return o.ParameterGroups, o.Marker
		},
		func(p redshifttypes.ClusterParameterGroup) *store.Resource {
			name := sv(p.ParameterGroupName)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftClusterParameterGroup, NativeID: redshiftIDARN(region, acct.ID, "parametergroup", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(p), TagsJSON: kvTagsJSON(p.Tags, redshiftTagKV),
				DiscoveredBy: scanID,
			}
		}, "cluster parameter groups", "ParameterGroupName")
	return total, inserted, err
}

func scanRedshiftEndpointAccess(ctx context.Context, client redshiftExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeEndpointAccess", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeEndpointAccessOutput, error) {
			return client.DescribeEndpointAccess(ctx, &redshift.DescribeEndpointAccessInput{Marker: marker})
		},
		func(o *redshift.DescribeEndpointAccessOutput) ([]redshifttypes.EndpointAccess, *string) {
			return o.EndpointAccessList, o.Marker
		},
		func(e redshifttypes.EndpointAccess) *store.Resource {
			name := sv(e.EndpointName)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftEndpointAccess, NativeID: redshiftIDARN(region, acct.ID, "endpointaccess", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			}
		}, "endpoint accesses", "EndpointName")
	return total, inserted, err
}

// scanRedshiftEndpointAuthorization keys an authorization by its cluster,
// grantor and grantee; one missing any of the three is warned about.
func scanRedshiftEndpointAuthorization(ctx context.Context, client redshiftExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeEndpointAuthorization", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeEndpointAuthorizationOutput, error) {
			return client.DescribeEndpointAuthorization(ctx, &redshift.DescribeEndpointAuthorizationInput{Marker: marker})
		},
		func(o *redshift.DescribeEndpointAuthorizationOutput) ([]redshifttypes.EndpointAuthorization, *string) {
			return o.EndpointAuthorizationList, o.Marker
		},
		func(a redshifttypes.EndpointAuthorization) *store.Resource {
			grantor, grantee, cluster := sv(a.Grantor), sv(a.Grantee), sv(a.ClusterIdentifier)
			label := fmt.Sprintf("%s→%s", grantor, grantee)
			var arn string
			if grantor != "" && grantee != "" && cluster != "" {
				arn = redshiftARN(region, acct.ID, "endpointauthorization", fmt.Sprintf("%s/%s/%s", cluster, grantor, grantee))
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftEndpointAuthorization, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}
		}, "endpoint authorizations", "ClusterIdentifier, Grantor or Grantee")
	return total, inserted, err
}

func scanRedshiftEventSubscriptions(ctx context.Context, client redshiftExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeEventSubscriptions", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeEventSubscriptionsOutput, error) {
			return client.DescribeEventSubscriptions(ctx, &redshift.DescribeEventSubscriptionsInput{Marker: marker})
		},
		func(o *redshift.DescribeEventSubscriptionsOutput) ([]redshifttypes.EventSubscription, *string) {
			return o.EventSubscriptionsList, o.Marker
		},
		func(e redshifttypes.EventSubscription) *store.Resource {
			id := sv(e.CustSubscriptionId)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftEventSubscription, NativeID: redshiftIDARN(region, acct.ID, "eventsubscription", id),
				Name: &id, Region: &region, AttributesJSON: mustJSON(e), TagsJSON: kvTagsJSON(e.Tags, redshiftTagKV),
				DiscoveredBy: scanID,
			}
		}, "event subscriptions", "CustSubscriptionId")
	return total, inserted, err
}

func scanRedshiftIntegrations(ctx context.Context, client redshiftExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeIntegrations", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeIntegrationsOutput, error) {
			return client.DescribeIntegrations(ctx, &redshift.DescribeIntegrationsInput{Marker: marker})
		},
		func(o *redshift.DescribeIntegrationsOutput) ([]redshifttypes.Integration, *string) {
			return o.Integrations, o.Marker
		},
		func(integration redshifttypes.Integration) *store.Resource {
			arn := sv(integration.IntegrationArn)
			label := sv(integration.IntegrationName)
			if label == "" {
				label = arn
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftIntegration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(integration),
				TagsJSON: kvTagsJSON(integration.Tags, redshiftTagKV), DiscoveredBy: scanID,
			}
		}, "integrations", "IntegrationArn")
	return total, inserted, err
}

func scanRedshiftScheduledActions(ctx context.Context, client redshiftExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	_, total, inserted, err := redshiftList(ctx, st, acct, region, "redshift:DescribeScheduledActions", redshiftListCodes{},
		func(ctx context.Context, marker *string) (*redshift.DescribeScheduledActionsOutput, error) {
			return client.DescribeScheduledActions(ctx, &redshift.DescribeScheduledActionsInput{Marker: marker})
		},
		func(o *redshift.DescribeScheduledActionsOutput) ([]redshifttypes.ScheduledAction, *string) {
			return o.ScheduledActions, o.Marker
		},
		func(s redshifttypes.ScheduledAction) *store.Resource {
			name := sv(s.ScheduledActionName)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftScheduledAction, NativeID: redshiftIDARN(region, acct.ID, "scheduledaction", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}
		}, "scheduled actions", "ScheduledActionName")
	return total, inserted, err
}
