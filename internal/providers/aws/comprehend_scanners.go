package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/comprehend"
)

func init() {
	registerService(serviceEntry{
		name: "aws:comprehend",
		fn:   scanComprehend,
		emits: []coverage.TypeDecl{
			{Service: "comprehend", DiscoType: TypeComprehendDocumentClassifier},
			{Service: "comprehend", DiscoType: TypeComprehendFlywheel},
		},
	})
}

type comprehendAPI interface {
	ListDocumentClassifiers(context.Context, *comprehend.ListDocumentClassifiersInput, ...func(*comprehend.Options)) (*comprehend.ListDocumentClassifiersOutput, error)
	ListFlywheels(context.Context, *comprehend.ListFlywheelsInput, ...func(*comprehend.Options)) (*comprehend.ListFlywheelsOutput, error)
}

// scanComprehend discovers Comprehend document classifiers and flywheels.
func scanComprehend(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := comprehend.NewFromConfig(acct.cfg, func(o *comprehend.Options) { o.Region = region })

	t, i, ferr := scanComprehendDocumentClassifiers(ctx, client, acct, region, st, scanID)
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
			if isAPIErrorCode(err, "InvalidRequestException") && strings.Contains(err.Error(), "UNSUPPORTED_OPERATION") {
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

func scanComprehendFlywheels(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListFlywheels(ctx, &comprehend.ListFlywheelsInput{NextToken: nextToken})
		if err != nil {
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
