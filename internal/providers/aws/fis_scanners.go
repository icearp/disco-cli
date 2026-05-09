package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/fis"
)

func init() {
	registerService(serviceEntry{
		name: "aws:fis",
		fn:   scanFIS,
		emits: []coverage.TypeDecl{
			{Service: "fis", DiscoType: TypeFISExperimentTemplate},
			{Service: "fis", DiscoType: TypeFISTargetAccountConfiguration},
		},
	})
}

type fisAPI interface {
	ListExperimentTemplates(context.Context, *fis.ListExperimentTemplatesInput, ...func(*fis.Options)) (*fis.ListExperimentTemplatesOutput, error)
	ListTargetAccountConfigurations(context.Context, *fis.ListTargetAccountConfigurationsInput, ...func(*fis.Options)) (*fis.ListTargetAccountConfigurationsOutput, error)
}

// scanFIS discovers Fault Injection Service experiment templates and
// target-account configurations (per template).
func scanFIS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := fis.NewFromConfig(acct.cfg, func(o *fis.Options) { o.Region = region })

	templates, t, i, ferr := scanFISExperimentTemplates(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanFISTargetAccountConfigs(ctx, client, templates, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

type fisTemplateRef struct {
	ID  string
	ARN string
}

func scanFISExperimentTemplates(ctx context.Context, client fisAPI, acct *account, region string, st *store.Store, scanID string) ([]fisTemplateRef, int, int, error) {
	var batch []*store.Resource
	var refs []fisTemplateRef
	var nextToken *string
	for {
		out, err := client.ListExperimentTemplates(ctx, &fis.ListExperimentTemplatesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "fis:ListExperimentTemplates", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("fis:ListExperimentTemplates: %w", err)
		}
		for _, t := range out.ExperimentTemplates {
			arn := sv(t.Arn)
			id := sv(t.Id)
			if arn == "" || id == "" {
				continue
			}
			refs = append(refs, fisTemplateRef{ID: id, ARN: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFISExperimentTemplate, NativeID: arn,
				Name: &id, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "fis experiment-templates")
	return refs, t, i, err
}

func scanFISTargetAccountConfigs(ctx context.Context, client fisAPI, templates []fisTemplateRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, tpl := range templates {
		tplID := tpl.ID
		var nextToken *string
		for {
			out, err := client.ListTargetAccountConfigurations(ctx, &fis.ListTargetAccountConfigurationsInput{
				ExperimentTemplateId: &tplID,
				NextToken:            nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "fis:ListTargetAccountConfigurations", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
					break
				}
				return 0, 0, fmt.Errorf("fis:ListTargetAccountConfigurations t=%s: %w", tplID, err)
			}
			for _, c := range out.TargetAccountConfigurations {
				accountID := sv(c.AccountId)
				if accountID == "" {
					continue
				}
				arn := fmt.Sprintf("%s/target-account-configuration/%s", tpl.ARN, accountID)
				label := accountID
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeFISTargetAccountConfiguration, NativeID: arn,
					Name: &label, Region: &region,
					AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "fis target-account-configurations")
}
