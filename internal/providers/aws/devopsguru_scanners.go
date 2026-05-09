package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/devopsguru"
	"github.com/aws/aws-sdk-go-v2/service/devopsguru/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:devops-guru",
		fn:   scanDevOpsGuru,
		emits: []coverage.TypeDecl{
			{Service: "devops-guru", DiscoType: TypeDevOpsGuruNotificationChannel, Leaf: true},
			{Service: "devops-guru", DiscoType: TypeDevOpsGuruResourceCollection, Leaf: true},
			{Service: "devops-guru", DiscoType: TypeDevOpsGuruLogAnomalyDetectionIntegration, Leaf: true},
		},
	})
}

type devOpsGuruAPI interface {
	ListNotificationChannels(context.Context, *devopsguru.ListNotificationChannelsInput, ...func(*devopsguru.Options)) (*devopsguru.ListNotificationChannelsOutput, error)
	GetResourceCollection(context.Context, *devopsguru.GetResourceCollectionInput, ...func(*devopsguru.Options)) (*devopsguru.GetResourceCollectionOutput, error)
	DescribeServiceIntegration(context.Context, *devopsguru.DescribeServiceIntegrationInput, ...func(*devopsguru.Options)) (*devopsguru.DescribeServiceIntegrationOutput, error)
}

// scanDevOpsGuru discovers notification channels, resource-collection
// configs, and log-anomaly-detection integration. All entries are
// account+region scoped; ARNs synthesised since SDK returns IDs only.
func scanDevOpsGuru(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := devopsguru.NewFromConfig(acct.cfg, func(o *devopsguru.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDOGNotificationChannels(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDOGResourceCollections(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDOGLogAnomalyIntegration(ctx, client, acct, region, st, scanID) },
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

func scanDOGNotificationChannels(ctx context.Context, client devOpsGuruAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListNotificationChannels(ctx, &devopsguru.ListNotificationChannelsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "devops-guru:ListNotificationChannels", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("devops-guru:ListNotificationChannels: %w", err)
		}
		for _, c := range out.Channels {
			id := sv(c.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:devops-guru:%s:%s:notification-channel/%s", region, acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDevOpsGuruNotificationChannel, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "devops-guru notification-channels")
}

// scanDOGResourceCollections fetches CloudFormation- and tag-keyed
// resource collections (singletons per type per account+region).
func scanDOGResourceCollections(ctx context.Context, client devOpsGuruAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, kind := range []types.ResourceCollectionType{
		types.ResourceCollectionTypeAwsCloudFormation,
		types.ResourceCollectionTypeAwsTags,
	} {
		k := kind
		var pages []any
		var nextToken *string
		for {
			out, err := client.GetResourceCollection(ctx, &devopsguru.GetResourceCollectionInput{
				ResourceCollectionType: k,
				NextToken:              nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "devops-guru:GetResourceCollection", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ValidationException", "ResourceNotFoundException") {
					break
				}
				return 0, 0, fmt.Errorf("devops-guru:GetResourceCollection(%s): %w", k, err)
			}
			if out.ResourceCollection != nil {
				pages = append(pages, out.ResourceCollection)
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
		if len(pages) == 0 {
			continue
		}
		arn := fmt.Sprintf("arn:aws:devops-guru:%s:%s:resource-collection/%s", region, acct.ID, k)
		label := string(k)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeDevOpsGuruResourceCollection, NativeID: arn,
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(map[string]any{"Type": string(k), "Pages": pages}), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "devops-guru resource-collections")
}

// scanDOGLogAnomalyIntegration emits a singleton row when log-anomaly
// detection integration is configured (OptInStatus present in the
// service-integration response).
func scanDOGLogAnomalyIntegration(ctx context.Context, client devOpsGuruAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeServiceIntegration(ctx, &devopsguru.DescribeServiceIntegrationInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "devops-guru:DescribeServiceIntegration", acct.ID, region, err)
		}
		if isAPIErrorCode(err, "ValidationException", "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("devops-guru:DescribeServiceIntegration: %w", err)
	}
	if out.ServiceIntegration == nil || out.ServiceIntegration.LogsAnomalyDetection == nil {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:devops-guru:%s:%s:log-anomaly-detection-integration", region, acct.ID)
	label := string(out.ServiceIntegration.LogsAnomalyDetection.OptInStatus)
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeDevOpsGuruLogAnomalyDetectionIntegration, NativeID: arn,
		Name: &label, Region: &region, Status: &label,
		AttributesJSON: mustJSON(out.ServiceIntegration.LogsAnomalyDetection), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "devops-guru log-anomaly-detection-integration")
}
