package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/appfabric"
	aftypes "github.com/aws/aws-sdk-go-v2/service/appfabric/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppFabricAppBundle, Service: "appfabric"})
	registerType(restype.Descriptor{Type: TypeAppFabricAppAuthorization, Service: "appfabric"})
	registerType(restype.Descriptor{Type: TypeAppFabricIngestion, Service: "appfabric"})
	registerType(restype.Descriptor{Type: TypeAppFabricIngestionDestination, Service: "appfabric"})
	registerService(serviceEntry{
		name: "aws:appfabric",
		fn:   scanAppFabric,
	})
}

type appFabricAPI interface {
	ListAppBundles(context.Context, *appfabric.ListAppBundlesInput, ...func(*appfabric.Options)) (*appfabric.ListAppBundlesOutput, error)
	GetAppBundle(context.Context, *appfabric.GetAppBundleInput, ...func(*appfabric.Options)) (*appfabric.GetAppBundleOutput, error)
	ListAppAuthorizations(context.Context, *appfabric.ListAppAuthorizationsInput, ...func(*appfabric.Options)) (*appfabric.ListAppAuthorizationsOutput, error)
	ListIngestions(context.Context, *appfabric.ListIngestionsInput, ...func(*appfabric.Options)) (*appfabric.ListIngestionsOutput, error)
	ListIngestionDestinations(context.Context, *appfabric.ListIngestionDestinationsInput, ...func(*appfabric.Options)) (*appfabric.ListIngestionDestinationsOutput, error)
	GetIngestionDestination(context.Context, *appfabric.GetIngestionDestinationInput, ...func(*appfabric.Options)) (*appfabric.GetIngestionDestinationOutput, error)
}

// ingestionDestAttrs embeds the native IngestionDestination and flattens its
// nested destination union (S3 bucket / Firehose stream) into lowercase sibling
// keys the resolver can read without re-walking the union shape.
type ingestionDestAttrs struct {
	aftypes.IngestionDestination
	S3BucketName       *string `json:"s3BucketName,omitempty"`
	FirehoseStreamName *string `json:"firehoseStreamName,omitempty"`
}

// scanAppFabric walks the AppFabric hierarchy per region: app-bundles (enriched
// via GetAppBundle for the customer-managed KMS key), then per bundle its
// app-authorizations and ingestions, then per ingestion its destinations
// (enriched via GetIngestionDestination for the S3/Firehose target).
//
// App-authorizations are read from the list summary, never GetAppAuthorization,
// to avoid pulling the OAuth/API-key credential body into the store.
func scanAppFabric(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := appfabric.NewFromConfig(acct.cfg, func(o *appfabric.Options) { o.Region = region })
	var batch []*store.Resource

	bundleARNs, err := appFabricBundles(ctx, client, acct, region, st, scanID, &batch)
	if err != nil {
		return 0, 0, err
	}
	for _, bundleARN := range bundleARNs {
		if err := appFabricAuthorizations(ctx, client, acct, region, scanID, bundleARN, &batch); err != nil {
			return 0, 0, err
		}
		ingestionARNs, err := appFabricIngestions(ctx, client, acct, region, scanID, bundleARN, &batch)
		if err != nil {
			return 0, 0, err
		}
		for _, ingARN := range ingestionARNs {
			if err := appFabricDestinations(ctx, client, acct, region, scanID, bundleARN, ingARN, &batch); err != nil {
				return 0, 0, err
			}
		}
	}
	return upsertBatch(st, batch, "appfabric")
}

func appFabricBundles(ctx context.Context, client appFabricAPI, acct *account, region string, st *store.Store, scanID string, batch *[]*store.Resource) ([]string, error) {
	var arns []string
	pager := appfabric.NewListAppBundlesPaginator(client, &appfabric.ListAppBundlesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, skipIfAccessDenied(st, "appfabric:ListAppBundles", acct.ID, region, err)
			}
			return nil, fmt.Errorf("appfabric:ListAppBundles: %w", err)
		}
		for _, s := range out.AppBundleSummaryList {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			body := any(s)
			if got, gerr := client.GetAppBundle(ctx, &appfabric.GetAppBundleInput{AppBundleIdentifier: &arn}); gerr == nil && got.AppBundle != nil {
				body = *got.AppBundle
			}
			*batch = append(*batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppFabricAppBundle, NativeID: arn,
				Region: &region, AttributesJSON: mustJSON(body), DiscoveredBy: scanID,
			})
		}
	}
	return arns, nil
}

func appFabricAuthorizations(ctx context.Context, client appFabricAPI, acct *account, region, scanID, bundleARN string, batch *[]*store.Resource) error {
	pager := appfabric.NewListAppAuthorizationsPaginator(client, &appfabric.ListAppAuthorizationsInput{AppBundleIdentifier: &bundleARN})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil
			}
			return fmt.Errorf("appfabric:ListAppAuthorizations: %w", err)
		}
		for _, s := range out.AppAuthorizationSummaryList {
			arn := sv(s.AppAuthorizationArn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			*batch = append(*batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppFabricAppAuthorization, NativeID: arn,
				Name: s.App, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return nil
}

func appFabricIngestions(ctx context.Context, client appFabricAPI, acct *account, region, scanID, bundleARN string, batch *[]*store.Resource) ([]string, error) {
	var arns []string
	pager := appfabric.NewListIngestionsPaginator(client, &appfabric.ListIngestionsInput{AppBundleIdentifier: &bundleARN})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return arns, nil
			}
			return nil, fmt.Errorf("appfabric:ListIngestions: %w", err)
		}
		for _, s := range out.Ingestions {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			status := string(s.State)
			*batch = append(*batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppFabricIngestion, NativeID: arn,
				Name: s.App, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return arns, nil
}

func appFabricDestinations(ctx context.Context, client appFabricAPI, acct *account, region, scanID, bundleARN, ingARN string, batch *[]*store.Resource) error {
	pager := appfabric.NewListIngestionDestinationsPaginator(client, &appfabric.ListIngestionDestinationsInput{
		AppBundleIdentifier: &bundleARN, IngestionIdentifier: &ingARN,
	})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil
			}
			return fmt.Errorf("appfabric:ListIngestionDestinations: %w", err)
		}
		for _, s := range out.IngestionDestinations {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			attrs := appFabricDestAttrs(ctx, client, bundleARN, ingARN, arn)
			*batch = append(*batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppFabricIngestionDestination, NativeID: arn,
				Region: &region, AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
			})
		}
	}
	return nil
}

// appFabricDestAttrs enriches a destination via GetIngestionDestination and
// flattens the S3/Firehose target out of the nested destination union.
func appFabricDestAttrs(ctx context.Context, client appFabricAPI, bundleARN, ingARN, destARN string) any {
	got, err := client.GetIngestionDestination(ctx, &appfabric.GetIngestionDestinationInput{
		AppBundleIdentifier: &bundleARN, IngestionIdentifier: &ingARN, IngestionDestinationIdentifier: &destARN,
	})
	if err != nil || got.IngestionDestination == nil {
		return map[string]string{"Arn": destARN}
	}
	return flattenIngestionDest(got.IngestionDestination)
}

// flattenIngestionDest embeds the native destination body and lifts the S3
// bucket / Firehose stream out of the nested union into flat sibling keys
// (union interfaces aren't JSON-rehydratable; the resolver reads only these
// flattened fields).
func flattenIngestionDest(dest *aftypes.IngestionDestination) ingestionDestAttrs {
	out := ingestionDestAttrs{IngestionDestination: *dest}
	if cfg, ok := dest.DestinationConfiguration.(*aftypes.DestinationConfigurationMemberAuditLog); ok {
		switch dst := cfg.Value.Destination.(type) {
		case *aftypes.DestinationMemberS3Bucket:
			out.S3BucketName = dst.Value.BucketName
		case *aftypes.DestinationMemberFirehoseStream:
			out.FirehoseStreamName = dst.Value.StreamName
		}
	}
	return out
}
