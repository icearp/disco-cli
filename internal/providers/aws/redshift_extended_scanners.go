package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
)

// redshiftExtAPI lists ops for the seven extended Redshift resource types.
// All seven Describe* ops expose Marker pagination but only some have
// SDK-generated Paginator helpers; the ones missing paginators (EndpointAuth)
// use a manual Marker loop.
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
	pager := redshift.NewDescribeClusterParameterGroupsPaginator(client, &redshift.DescribeClusterParameterGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "redshift:DescribeClusterParameterGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("redshift:DescribeClusterParameterGroups: %w", perr)
		}
		for _, p := range out.ParameterGroups {
			name := sv(p.ParameterGroupName)
			if name == "" {
				continue
			}
			arn := redshiftARN(region, acct.ID, "parametergroup", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftClusterParameterGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift cluster-parameter-groups")
}

func scanRedshiftEndpointAccess(ctx context.Context, client redshiftExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeEndpointAccessPaginator(client, &redshift.DescribeEndpointAccessInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "redshift:DescribeEndpointAccess", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("redshift:DescribeEndpointAccess: %w", perr)
		}
		for _, e := range out.EndpointAccessList {
			name := sv(e.EndpointName)
			if name == "" {
				continue
			}
			arn := redshiftARN(region, acct.ID, "endpointaccess", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftEndpointAccess, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift endpoint-access")
}

// scanRedshiftEndpointAuthorization uses manual Marker pagination (no SDK paginator helper).
func scanRedshiftEndpointAuthorization(ctx context.Context, client redshiftExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	input := &redshift.DescribeEndpointAuthorizationInput{}
	var batch []*store.Resource
	for {
		out, err := client.DescribeEndpointAuthorization(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "redshift:DescribeEndpointAuthorization", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("redshift:DescribeEndpointAuthorization: %w", err)
		}
		for _, a := range out.EndpointAuthorizationList {
			grantor := sv(a.Grantor)
			grantee := sv(a.Grantee)
			cluster := sv(a.ClusterIdentifier)
			if grantor == "" || grantee == "" || cluster == "" {
				continue
			}
			arn := redshiftARN(region, acct.ID, "endpointauthorization", fmt.Sprintf("%s/%s/%s", cluster, grantor, grantee))
			label := fmt.Sprintf("%s→%s", grantor, grantee)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftEndpointAuthorization, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.Marker == nil || *out.Marker == "" {
			break
		}
		input.Marker = out.Marker
	}
	return upsertBatch(st, batch, "redshift endpoint-authorizations")
}

func scanRedshiftEventSubscriptions(ctx context.Context, client redshiftExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeEventSubscriptionsPaginator(client, &redshift.DescribeEventSubscriptionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "redshift:DescribeEventSubscriptions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("redshift:DescribeEventSubscriptions: %w", perr)
		}
		for _, e := range out.EventSubscriptionsList {
			id := sv(e.CustSubscriptionId)
			if id == "" {
				continue
			}
			arn := redshiftARN(region, acct.ID, "eventsubscription", id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftEventSubscription, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift event-subscriptions")
}

func scanRedshiftIntegrations(ctx context.Context, client redshiftExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeIntegrationsPaginator(client, &redshift.DescribeIntegrationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "redshift:DescribeIntegrations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("redshift:DescribeIntegrations: %w", perr)
		}
		for _, integration := range out.Integrations {
			arn := sv(integration.IntegrationArn)
			if arn == "" {
				continue
			}
			label := sv(integration.IntegrationName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftIntegration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(integration), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift integrations")
}

func scanRedshiftScheduledActions(ctx context.Context, client redshiftExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeScheduledActionsPaginator(client, &redshift.DescribeScheduledActionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "redshift:DescribeScheduledActions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("redshift:DescribeScheduledActions: %w", perr)
		}
		for _, s := range out.ScheduledActions {
			name := sv(s.ScheduledActionName)
			if name == "" {
				continue
			}
			arn := redshiftARN(region, acct.ID, "scheduledaction", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftScheduledAction, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift scheduled-actions")
}
