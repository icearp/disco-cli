package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/icearp/disco-cli/store"
)

// scanEventBridgeExtended discovers archives, endpoints (global event-bus
// federations), and per-bus policies. Archives synth ARN; endpoints native;
// event-bus policy fans out per bus from DescribeEventBus.
func scanEventBridgeExtended(ctx context.Context, client eventbridgeAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t, i, ferr := scanEBArchives(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanEBEndpoints(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanEBEventBusPolicies(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanEBArchives(ctx context.Context, client eventbridgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListArchives(ctx, &eventbridge.ListArchivesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "events:ListArchives", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("events:ListArchives: %w", err)
		}
		for _, a := range out.Archives {
			name := sv(a.ArchiveName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:events:%s:%s:archive/%s", region, acct.ID, name)
			status := string(a.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEventsArchive, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "events archives")
}

func scanEBEndpoints(ctx context.Context, client eventbridgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListEndpoints(ctx, &eventbridge.ListEndpointsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "events:ListEndpoints", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("events:ListEndpoints: %w", err)
		}
		for _, e := range out.Endpoints {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			status := string(e.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEventsEndpoint, NativeID: arn,
				Name: e.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "events endpoints")
}

// scanEBEventBusPolicies walks ListEventBuses and calls DescribeEventBus to
// fetch the Policy attribute. Synth ARN: {busArn}/policy.
func scanEBEventBusPolicies(ctx context.Context, client eventbridgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var busNames []string
	var nextToken *string
	for {
		out, err := client.ListEventBuses(ctx, &eventbridge.ListEventBusesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "events:ListEventBuses(policy)", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("events:ListEventBuses(policy): %w", err)
		}
		for _, b := range out.EventBuses {
			if n := sv(b.Name); n != "" {
				busNames = append(busNames, n)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	var batch []*store.Resource
	for _, name := range busNames {
		nm := name
		out, err := client.DescribeEventBus(ctx, &eventbridge.DescribeEventBusInput{Name: &nm})
		if err != nil {
			if isAccessDenied(err) {
				continue
			}
			return 0, 0, fmt.Errorf("events:DescribeEventBus(policy): %w", err)
		}
		if sv(out.Policy) == "" {
			continue
		}
		busArn := sv(out.Arn)
		if busArn == "" {
			continue
		}
		arn := busArn + "/policy"
		label := nm
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeEventsEventBusPolicy, NativeID: arn,
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(map[string]string{"EventBusName": nm, "Policy": sv(out.Policy)}), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "events event-bus-policies")
}
