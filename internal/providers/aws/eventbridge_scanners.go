package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerService(serviceEntry{
		name: "aws:events",
		fn:   scanEventBridge,
		emits: []coverage.TypeDecl{
			{Service: "events", DiscoType: TypeEventsEventBus},
			{Service: "events", DiscoType: TypeEventsRule},
			{Service: "events", DiscoType: TypeEventsConnection},
			{Service: "events", DiscoType: TypeEventsAPIDestination},
			{Service: "events", DiscoType: TypeEventsArchive},
			{Service: "events", DiscoType: TypeEventsEndpoint},
			{Service: "events", DiscoType: TypeEventsEventBusPolicy},
		},
	})
}

// eventbridgeAPI is the narrow set of EventBridge operations called by
// scanEventBridgeAll.
type eventbridgeAPI interface {
	ListEventBuses(context.Context, *eventbridge.ListEventBusesInput, ...func(*eventbridge.Options)) (*eventbridge.ListEventBusesOutput, error)
	ListRules(context.Context, *eventbridge.ListRulesInput, ...func(*eventbridge.Options)) (*eventbridge.ListRulesOutput, error)
	ListTargetsByRule(context.Context, *eventbridge.ListTargetsByRuleInput, ...func(*eventbridge.Options)) (*eventbridge.ListTargetsByRuleOutput, error)
	ListApiDestinations(context.Context, *eventbridge.ListApiDestinationsInput, ...func(*eventbridge.Options)) (*eventbridge.ListApiDestinationsOutput, error)
	ListConnections(context.Context, *eventbridge.ListConnectionsInput, ...func(*eventbridge.Options)) (*eventbridge.ListConnectionsOutput, error)
	DescribeConnection(context.Context, *eventbridge.DescribeConnectionInput, ...func(*eventbridge.Options)) (*eventbridge.DescribeConnectionOutput, error)
	ListArchives(context.Context, *eventbridge.ListArchivesInput, ...func(*eventbridge.Options)) (*eventbridge.ListArchivesOutput, error)
	ListEndpoints(context.Context, *eventbridge.ListEndpointsInput, ...func(*eventbridge.Options)) (*eventbridge.ListEndpointsOutput, error)
	DescribeEventBus(context.Context, *eventbridge.DescribeEventBusInput, ...func(*eventbridge.Options)) (*eventbridge.DescribeEventBusOutput, error)
}

// scanEventBridge discovers EventBridge event buses and rules in one region.
// Rules are listed per event bus and enriched with their targets via
// ListTargetsByRule (targets are stored inline in attributes for resolver use).
func scanEventBridge(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := eventbridge.NewFromConfig(acct.cfg, func(o *eventbridge.Options) { o.Region = region })
	t, i, ferr := scanEventBridgeAll(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	t, i, ferr = scanEventBridgeExtended(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanEventBridgeAll holds the testable scan body.
func scanEventBridgeAll(ctx context.Context, client eventbridgeAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	busNames, t, i, ferr := scanEventBridgeBuses(ctx, client, acct, region, st, scanID)
	total += t
	inserted += i
	if ferr != nil {
		return total, inserted, ferr
	}
	t, i, ferr = scanEventBridgeRules(ctx, client, acct, region, st, scanID, busNames)
	total += t
	inserted += i
	if ferr != nil {
		return total, inserted, ferr
	}
	t, i, ferr = scanEventBridgeConnections(ctx, client, acct, region, st, scanID)
	total += t
	inserted += i
	if ferr != nil {
		return total, inserted, ferr
	}
	t, i, ferr = scanEventBridgeAPIDestinations(ctx, client, acct, region, st, scanID)
	total += t
	inserted += i
	return total, inserted, ferr
}

func scanEventBridgeBuses(ctx context.Context, client eventbridgeAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var busNames []string
	var token *string
	for {
		out, err := client.ListEventBuses(ctx, &eventbridge.ListEventBusesInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "events:ListEventBuses", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("events:ListEventBuses: %w", err)
		}
		for _, b := range out.EventBuses {
			arn := sv(b.Arn)
			attrsJSON := mustJSON(b)
			if dout, derr := client.DescribeEventBus(ctx, &eventbridge.DescribeEventBusInput{Name: b.Name}); derr == nil {
				attrsJSON = mustJSON(dout)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEventsEventBus,
				NativeID:       arn,
				Name:           b.Name,
				Region:         &region,
				AttributesJSON: attrsJSON,
				DiscoveredBy:   scanID,
				// Name "default" is the AWS-managed default event bus
				// present in every region.
				ManagedByProvider: sv(b.Name) == "default",
			})
			busNames = append(busNames, sv(b.Name))
		}
		if out.NextToken == nil {
			break
		}
		token = out.NextToken
	}
	if len(batch) == 0 {
		return busNames, 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("upsert EventBridge buses: %w", err)
	}
	return busNames, len(batch), n, nil
}

func scanEventBridgeRules(ctx context.Context, client eventbridgeAPI, acct *account, region string, st *store.Store, scanID string, busNames []string) (int, int, error) {
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, busName := range busNames {
		g.Go(func() error {
			return collectEventBridgeRulesForBus(gctx, client, acct, region, scanID, busName, &mu, &batch)
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert EventBridge rules: %w", err)
	}
	return len(batch), n, nil
}

func collectEventBridgeRulesForBus(ctx context.Context, client eventbridgeAPI, acct *account, region, scanID, busName string, mu *sync.Mutex, batch *[]*store.Resource) error {
	type ruleWithTargets struct {
		Rule    any   `json:"Rule"`
		Targets []any `json:"Targets"`
	}
	var token *string
	for {
		rulesOut, err := client.ListRules(ctx, &eventbridge.ListRulesInput{
			EventBusName: &busName,
			NextToken:    token,
		})
		if err != nil {
			if isAccessDenied(err) {
				return nil
			}
			return fmt.Errorf("events:ListRules bus=%s: %w", busName, err)
		}
		for _, rule := range rulesOut.Rules {
			arn := sv(rule.Arn)
			status := string(rule.State)
			var targets []any
			if tOut, tErr := client.ListTargetsByRule(ctx, &eventbridge.ListTargetsByRuleInput{
				Rule:         rule.Name,
				EventBusName: &busName,
			}); tErr == nil {
				for _, t := range tOut.Targets {
					targets = append(targets, t)
				}
			}
			attrs := ruleWithTargets{Rule: rule, Targets: targets}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEventsRule,
				NativeID:       arn,
				Name:           rule.Name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(attrs),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			*batch = append(*batch, r)
			mu.Unlock()
		}
		if rulesOut.NextToken == nil {
			return nil
		}
		token = rulesOut.NextToken
	}
}

func scanEventBridgeConnections(ctx context.Context, client eventbridgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListConnections(ctx, &eventbridge.ListConnectionsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "events:ListConnections", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("events:ListConnections: %w", err)
		}
		for _, c := range out.Connections {
			arn := sv(c.ConnectionArn)
			status := string(c.ConnectionState)
			attrsJSON := mustJSON(c)
			if dout, derr := client.DescribeConnection(ctx, &eventbridge.DescribeConnectionInput{Name: c.Name}); derr == nil {
				attrsJSON = mustJSON(dout)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEventsConnection,
				NativeID:       arn,
				Name:           c.Name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: attrsJSON,
				DiscoveredBy:   scanID,
			})
		}
		if out.NextToken == nil {
			break
		}
		token = out.NextToken
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert EventBridge connections: %w", err)
	}
	return len(batch), n, nil
}

func scanEventBridgeAPIDestinations(ctx context.Context, client eventbridgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListApiDestinations(ctx, &eventbridge.ListApiDestinationsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "events:ListApiDestinations", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("events:ListApiDestinations: %w", err)
		}
		for _, d := range out.ApiDestinations {
			arn := sv(d.ApiDestinationArn)
			status := string(d.ApiDestinationState)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEventsAPIDestination,
				NativeID:       arn,
				Name:           d.Name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextToken == nil {
			break
		}
		token = out.NextToken
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert EventBridge api destinations: %w", err)
	}
	return len(batch), n, nil
}
