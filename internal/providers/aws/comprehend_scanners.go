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
	registerType(restype.Descriptor{Type: TypeComprehendEntityRecognizer, Service: "comprehend"})
	registerType(restype.Descriptor{Type: TypeComprehendDocumentClassifierEndpoint, Service: "comprehend"})
	registerType(restype.Descriptor{Type: TypeComprehendEntityRecognizerEndpoint, Service: "comprehend"})
	registerType(restype.Descriptor{Type: TypeComprehendFlywheel, Service: "comprehend"})
	registerType(restype.Descriptor{Type: TypeComprehendDataset, Service: "comprehend"})
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
	ListDatasets(context.Context, *comprehend.ListDatasetsInput, ...func(*comprehend.Options)) (*comprehend.ListDatasetsOutput, error)
}

// scanComprehend discovers Comprehend document classifiers, entity recognizers,
// real-time inference endpoints, flywheels and flywheel datasets.
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

	t, i, flywheelARNs, ferr := scanComprehendFlywheels(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanComprehendDatasets(ctx, client, acct, region, st, scanID, flywheelARNs)
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

// scanComprehendFlywheels also returns the listed flywheel ARNs so the dataset
// phase can fan out over them without listing flywheels a second time.
func scanComprehendFlywheels(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []string, error) {
	var batch []*store.Resource
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListFlywheels(ctx, &comprehend.ListFlywheelsInput{NextToken: nextToken})
		if err != nil {
			// Per-region feature gap shape Comprehend uses.
			if isAPIErrorWithMessage(err, "InvalidRequestException", "UNSUPPORTED_OPERATION") {
				return 0, 0, nil, nil
			}
			if isComprehendNotEnabled(err) {
				return 0, 0, nil, nil
			}
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "comprehend:ListFlywheels", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("comprehend:ListFlywheels: %w", err)
		}
		for _, f := range out.FlywheelSummaryList {
			arn := sv(f.FlywheelArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
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
	total, inserted, err := upsertBatch(st, batch, "comprehend flywheels")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, arns, nil
}

// scanComprehendDatasets lists each flywheel's datasets and closure-wires every
// dataset to its flywheel. ListDatasets is authorized against the flywheel
// resource (Service Reference), so it is called per flywheel ARN.
func scanComprehendDatasets(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string, flywheelARNs []string) (int, int, error) {
	var batch []*store.Resource
	var parentARNs []string
	warnedDenied := false
flywheels:
	for _, flywheelARN := range flywheelARNs {
		pager := comprehend.NewListDatasetsPaginator(client, &comprehend.ListDatasetsInput{FlywheelArn: &flywheelARN})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				// A region-wide gap fails every remaining flywheel the same way;
				// stop listing but still store what earlier flywheels returned.
				if isAPIErrorWithMessage(err, "InvalidRequestException", "UNSUPPORTED_OPERATION") || isComprehendNotEnabled(err) {
					break flywheels
				}
				if isAccessDenied(err) {
					// The action is resource-scoped, so a deny may cover only
					// some flywheels: warn once, keep listing the others.
					if !warnedDenied {
						warnedDenied = true
						_ = skipIfAccessDenied(st, "comprehend:ListDatasets", acct.ID, region, err)
					}
					break
				}
				// Flywheel deleted between ListFlywheels and ListDatasets.
				if isAPIErrorCode(err, "ResourceNotFoundException") {
					break
				}
				return 0, 0, fmt.Errorf("comprehend:ListDatasets %s: %w", flywheelARN, err)
			}
			for _, d := range out.DatasetPropertiesList {
				arn := sv(d.DatasetArn)
				if arn == "" {
					continue
				}
				r := &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeComprehendDataset, NativeID: arn,
					Name: d.DatasetName, Region: &region, CreatedAt: tp(d.CreationTime),
					AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
				}
				if status := string(d.Status); status != "" {
					r.Status = &status
				}
				batch = append(batch, r)
				parentARNs = append(parentARNs, flywheelARN)
			}
		}
	}
	total, inserted, err := upsertBatch(st, batch, "comprehend datasets")
	if err != nil {
		return 0, 0, err
	}
	pairs := make([][2]string, len(batch))
	for i, r := range batch {
		pairs[i] = [2]string{r.ID, store.ResourceID("aws", acct.ID, parentARNs[i])}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure comprehend datasets: %w", err)
	}
	return total, inserted, nil
}
