package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/workmail"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWorkMailOrganization, Service: "workmail", Leaf: true})
	registerService(serviceEntry{
		name: "aws:workmail",
		fn:   scanWorkMail,
	})
}

type workMailAPI interface {
	ListOrganizations(context.Context, *workmail.ListOrganizationsInput, ...func(*workmail.Options)) (*workmail.ListOrganizationsOutput, error)
}

// scanWorkMail discovers WorkMail organizations. ListOrganizations has no
// paginator constructor — manual NextToken pagination. No native ARN —
// synthesized from OrganizationId.
func scanWorkMail(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := workmail.NewFromConfig(acct.cfg, func(o *workmail.Options) { o.Region = region })
	return scanWorkMailIn(ctx, client, acct, region, st, scanID)
}

func scanWorkMailIn(ctx context.Context, client workMailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListOrganizations(ctx, &workmail.ListOrganizationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workmail:ListOrganizations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workmail:ListOrganizations: %w", err)
		}
		for _, o := range out.OrganizationSummaries {
			id := sv(o.OrganizationId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workmail:%s:%s:organization/%s", region, acct.ID, id)
			label := sv(o.Alias)
			if label == "" {
				label = id
			}
			status := sv(o.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkMailOrganization, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "workmail organizations")
}
