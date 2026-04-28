package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:events", fn: scanEventBridge}) }

// eventbridgeAPI is the narrow set of EventBridge operations called by
// scanEventBridgeAll.
type eventbridgeAPI interface {
	ListEventBuses(context.Context, *eventbridge.ListEventBusesInput, ...func(*eventbridge.Options)) (*eventbridge.ListEventBusesOutput, error)
	ListRules(context.Context, *eventbridge.ListRulesInput, ...func(*eventbridge.Options)) (*eventbridge.ListRulesOutput, error)
	ListTargetsByRule(context.Context, *eventbridge.ListTargetsByRuleInput, ...func(*eventbridge.Options)) (*eventbridge.ListTargetsByRuleOutput, error)
	ListApiDestinations(context.Context, *eventbridge.ListApiDestinationsInput, ...func(*eventbridge.Options)) (*eventbridge.ListApiDestinationsOutput, error)
	ListConnections(context.Context, *eventbridge.ListConnectionsInput, ...func(*eventbridge.Options)) (*eventbridge.ListConnectionsOutput, error)
}

// scanEventBridge discovers EventBridge event buses and rules in one region.
// Rules are listed per event bus and enriched with their targets via
// ListTargetsByRule (targets are stored inline in attributes for resolver use).
func scanEventBridge(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := eventbridge.NewFromConfig(acct.cfg, func(o *eventbridge.Options) { o.Region = region })
	return scanEventBridgeAll(ctx, client, acct, region, st, scanID)
}

// scanEventBridgeAll holds the testable scan body.
func scanEventBridgeAll(ctx context.Context, client eventbridgeAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {

	// Phase 1: event buses
	var busBatch []*store.Resource
	var busNames []string
	var busToken *string
	for {
		out, err := client.ListEventBuses(ctx, &eventbridge.ListEventBusesInput{NextToken: busToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "events:ListEventBuses", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("events:ListEventBuses: %w", err)
		}
		for _, b := range out.EventBuses {
			arn := sv(b.Arn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEventsEventBus,
				NativeID:       arn,
				Name:           b.Name,
				Region:         &region,
				AttributesJSON: mustJSON(b),
				DiscoveredBy:   scanID,
			}
			busBatch = append(busBatch, r)
			busNames = append(busNames, sv(b.Name))
		}
		if out.NextToken == nil {
			break
		}
		busToken = out.NextToken
	}
	if len(busBatch) > 0 {
		n, err := st.UpsertResources(busBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert EventBridge buses: %w", err)
		}
		total += len(busBatch)
		inserted += n
	}

	// Phase 2: rules per event bus (concurrent)
	var (
		mu        sync.Mutex
		ruleBatch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, busName := range busNames {
		g.Go(func() error {
			var ruleToken *string
			for {
				rulesOut, err := client.ListRules(gctx, &eventbridge.ListRulesInput{
					EventBusName: &busName,
					NextToken:    ruleToken,
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
					// Fetch targets and embed them in attributes for resolver use.
					type ruleWithTargets struct {
						Rule    any   `json:"Rule"`
						Targets []any `json:"Targets"`
					}
					var targets []any
					tOut, tErr := client.ListTargetsByRule(gctx, &eventbridge.ListTargetsByRuleInput{
						Rule:         rule.Name,
						EventBusName: &busName,
					})
					if tErr == nil {
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
					ruleBatch = append(ruleBatch, r)
					mu.Unlock()
				}
				if rulesOut.NextToken == nil {
					break
				}
				ruleToken = rulesOut.NextToken
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(ruleBatch) > 0 {
		n, err := st.UpsertResources(ruleBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert EventBridge rules: %w", err)
		}
		total += len(ruleBatch)
		inserted += n
	}

	// Phase 3: connections (account+region scope, manual NextToken — no paginator).
	var connBatch []*store.Resource
	var connToken *string
	for {
		out, err := client.ListConnections(ctx, &eventbridge.ListConnectionsInput{NextToken: connToken})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "events:ListConnections", acct.ID, region, err)
				break
			}
			return total, inserted, fmt.Errorf("events:ListConnections: %w", err)
		}
		for _, c := range out.Connections {
			arn := sv(c.ConnectionArn)
			status := string(c.ConnectionState)
			connBatch = append(connBatch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEventsConnection,
				NativeID:       arn,
				Name:           c.Name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextToken == nil {
			break
		}
		connToken = out.NextToken
	}
	if len(connBatch) > 0 {
		n, err := st.UpsertResources(connBatch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert EventBridge connections: %w", err)
		}
		total += len(connBatch)
		inserted += n
	}

	// Phase 4: API destinations (reference connections by ARN).
	var destBatch []*store.Resource
	var destToken *string
	for {
		out, err := client.ListApiDestinations(ctx, &eventbridge.ListApiDestinationsInput{NextToken: destToken})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "events:ListApiDestinations", acct.ID, region, err)
				break
			}
			return total, inserted, fmt.Errorf("events:ListApiDestinations: %w", err)
		}
		for _, d := range out.ApiDestinations {
			arn := sv(d.ApiDestinationArn)
			status := string(d.ApiDestinationState)
			destBatch = append(destBatch, &store.Resource{
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
		destToken = out.NextToken
	}
	if len(destBatch) > 0 {
		n, err := st.UpsertResources(destBatch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert EventBridge api destinations: %w", err)
		}
		total += len(destBatch)
		inserted += n
	}

	return total, inserted, nil
}
