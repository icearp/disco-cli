package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCEAnomalyMonitor, Service: "ce", Upstream: "AWS::CE::AnomalyMonitor", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCEAnomalySubscription, Service: "ce", Upstream: "AWS::CE::AnomalySubscription", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCECostCategory, Service: "ce", Upstream: "AWS::CE::CostCategory", Leaf: true})
	registerService(serviceEntry{
		name:   "aws:ce",
		global: true,
		fn:     scanCostExplorer,
	})
}

type costExplorerAPI interface {
	GetAnomalyMonitors(context.Context, *costexplorer.GetAnomalyMonitorsInput, ...func(*costexplorer.Options)) (*costexplorer.GetAnomalyMonitorsOutput, error)
	GetAnomalySubscriptions(context.Context, *costexplorer.GetAnomalySubscriptionsInput, ...func(*costexplorer.Options)) (*costexplorer.GetAnomalySubscriptionsOutput, error)
	ListCostCategoryDefinitions(context.Context, *costexplorer.ListCostCategoryDefinitionsInput, ...func(*costexplorer.Options)) (*costexplorer.ListCostCategoryDefinitionsOutput, error)
}

// scanCostExplorer discovers anomaly monitors, anomaly subscriptions, and
// cost category definitions. CE is global, accessed via us-east-1.
func scanCostExplorer(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := costexplorer.NewFromConfig(acct.cfg, func(o *costexplorer.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanCEAnomalyMonitors(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanCEAnomalySubscriptions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanCECostCategories(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanCEAnomalyMonitors(ctx context.Context, client costExplorerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.GetAnomalyMonitors(ctx, &costexplorer.GetAnomalyMonitorsInput{NextPageToken: nextToken})
		if err != nil {
			if isCostExplorerNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ce:GetAnomalyMonitors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ce:GetAnomalyMonitors: %w", err)
		}
		for _, m := range out.AnomalyMonitors {
			arn := sv(m.MonitorArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCEAnomalyMonitor, NativeID: arn,
				Name: m.MonitorName, Region: regionGlobal,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		nextToken = out.NextPageToken
	}
	return upsertBatch(st, batch, "ce anomaly-monitors")
}

func scanCEAnomalySubscriptions(ctx context.Context, client costExplorerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.GetAnomalySubscriptions(ctx, &costexplorer.GetAnomalySubscriptionsInput{NextPageToken: nextToken})
		if err != nil {
			if isCostExplorerNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ce:GetAnomalySubscriptions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ce:GetAnomalySubscriptions: %w", err)
		}
		for _, s := range out.AnomalySubscriptions {
			arn := sv(s.SubscriptionArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCEAnomalySubscription, NativeID: arn,
				Name: s.SubscriptionName, Region: regionGlobal,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		nextToken = out.NextPageToken
	}
	return upsertBatch(st, batch, "ce anomaly-subscriptions")
}

func scanCECostCategories(ctx context.Context, client costExplorerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListCostCategoryDefinitions(ctx, &costexplorer.ListCostCategoryDefinitionsInput{NextToken: nextToken})
		if err != nil {
			if isCostExplorerNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ce:ListCostCategoryDefinitions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ce:ListCostCategoryDefinitions: %w", err)
		}
		for _, c := range out.CostCategoryReferences {
			arn := sv(c.CostCategoryArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCECostCategory, NativeID: arn,
				Name: c.Name, Region: regionGlobal,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "ce cost-categories")
}
