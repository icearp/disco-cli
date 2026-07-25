package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/comprehend"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeComprehendDocumentClassifier, Service: "comprehend"})
	registerType(restype.Descriptor{Type: TypeComprehendEntityRecognizer, Service: "comprehend", Upstream: "AWS::comprehend::entity-recognizer"})
	registerType(restype.Descriptor{Type: TypeComprehendDocumentClassifierEndpoint, Service: "comprehend", Upstream: "AWS::comprehend::document-classifier-endpoint"})
	registerType(restype.Descriptor{Type: TypeComprehendEntityRecognizerEndpoint, Service: "comprehend", Upstream: "AWS::comprehend::entity-recognizer-endpoint"})
	registerType(restype.Descriptor{Type: TypeComprehendFlywheel, Service: "comprehend"})
	registerService(serviceEntry{
		name: "aws:comprehend",
		fn:   scanComprehend,
	})
}

// isComprehendNotEnabled reports the NotAuthorizedException Comprehend returns
// for an account not subscribed to its custom-model surface in this region
// ("Your account is not authorized to make this call."). NotAuthorizedException
// is in accessDeniedCodes, so without this the phase records an IAM-style
// warning on every scan of every such region.
//
// Per-phase and not markServiceUnavailable: ListFlywheels succeeds in the same
// regions where the three custom-model ops fail, so Comprehend itself IS served
// there — marking the whole service region-unavailable would blank a working
// scanner.
func isComprehendNotEnabled(err error) bool {
	return isAPIErrorWithMessage(err, "NotAuthorizedException", "account is not authorized to make this call")
}

type comprehendAPI interface {
	ListDocumentClassifiers(context.Context, *comprehend.ListDocumentClassifiersInput, ...func(*comprehend.Options)) (*comprehend.ListDocumentClassifiersOutput, error)
	ListEntityRecognizers(context.Context, *comprehend.ListEntityRecognizersInput, ...func(*comprehend.Options)) (*comprehend.ListEntityRecognizersOutput, error)
	ListEndpoints(context.Context, *comprehend.ListEndpointsInput, ...func(*comprehend.Options)) (*comprehend.ListEndpointsOutput, error)
	ListFlywheels(context.Context, *comprehend.ListFlywheelsInput, ...func(*comprehend.Options)) (*comprehend.ListFlywheelsOutput, error)
}

// scanComprehend discovers Comprehend document classifiers, entity recognizers,
// real-time inference endpoints and flywheels.
func scanComprehend(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := comprehend.NewFromConfig(acct.cfg, func(o *comprehend.Options) { o.Region = region })

	t, i, ferr := scanComprehendDocumentClassifiers(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanComprehendEntityRecognizers(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanComprehendEndpoints(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanComprehendFlywheels(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanComprehendDocumentClassifiers(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListDocumentClassifiers(ctx, &comprehend.ListDocumentClassifiersInput{NextToken: nextToken})
		if err != nil {
			// Per-region feature gap shape Comprehend uses.
			if isAPIErrorWithMessage(err, "InvalidRequestException", "UNSUPPORTED_OPERATION") {
				return 0, 0, nil
			}
			if isComprehendNotEnabled(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "comprehend:ListDocumentClassifiers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("comprehend:ListDocumentClassifiers: %w", err)
		}
		for _, c := range out.DocumentClassifierPropertiesList {
			arn := sv(c.DocumentClassifierArn)
			if arn == "" {
				continue
			}
			status := string(c.Status)
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeComprehendDocumentClassifier, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "comprehend document-classifiers")
}

func scanComprehendEntityRecognizers(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListEntityRecognizers(ctx, &comprehend.ListEntityRecognizersInput{NextToken: nextToken})
		if err != nil {
			if isAPIErrorWithMessage(err, "InvalidRequestException", "UNSUPPORTED_OPERATION") {
				return 0, 0, nil
			}
			if isComprehendNotEnabled(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "comprehend:ListEntityRecognizers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("comprehend:ListEntityRecognizers: %w", err)
		}
		for _, e := range out.EntityRecognizerPropertiesList {
			arn := sv(e.EntityRecognizerArn)
			if arn == "" {
				continue
			}
			status := string(e.Status)
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeComprehendEntityRecognizer, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "comprehend entity-recognizers")
}

// scanComprehendEndpoints lists real-time inference endpoints and splits them by
// the model they front: a ModelArn containing ":entity-recognizer/" is an
// entity-recognizer-endpoint, otherwise a document-classifier-endpoint.
func scanComprehendEndpoints(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListEndpoints(ctx, &comprehend.ListEndpointsInput{NextToken: nextToken})
		if err != nil {
			if isAPIErrorWithMessage(err, "InvalidRequestException", "UNSUPPORTED_OPERATION") {
				return 0, 0, nil
			}
			if isComprehendNotEnabled(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "comprehend:ListEndpoints", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("comprehend:ListEndpoints: %w", err)
		}
		for _, e := range out.EndpointPropertiesList {
			arn := sv(e.EndpointArn)
			if arn == "" {
				continue
			}
			etype := TypeComprehendDocumentClassifierEndpoint
			if strings.Contains(sv(e.ModelArn), ":entity-recognizer/") {
				etype = TypeComprehendEntityRecognizerEndpoint
			}
			status := string(e.Status)
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: etype, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "comprehend endpoints")
}

func scanComprehendFlywheels(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListFlywheels(ctx, &comprehend.ListFlywheelsInput{NextToken: nextToken})
		if err != nil {
			// Per-region feature gap shape Comprehend uses.
			if isAPIErrorWithMessage(err, "InvalidRequestException", "UNSUPPORTED_OPERATION") {
				return 0, 0, nil
			}
			if isComprehendNotEnabled(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "comprehend:ListFlywheels", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("comprehend:ListFlywheels: %w", err)
		}
		for _, f := range out.FlywheelSummaryList {
			arn := sv(f.FlywheelArn)
			if arn == "" {
				continue
			}
			status := string(f.Status)
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeComprehendFlywheel, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "comprehend flywheels")
}
