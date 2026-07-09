package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/artifact"
)

func init() {
	registerType(restype.Descriptor{Type: TypeArtifactCustomerAgreement, Service: "artifact", Upstream: "AWS::artifact::customer-agreement"})
	registerType(restype.Descriptor{Type: TypeArtifactReport, Service: "artifact", Leaf: true, Managed: true})
	registerService(serviceEntry{
		name:   "aws:artifact",
		global: true,
		fn:     scanArtifact,
	})
}

type artifactAPI interface {
	ListReports(context.Context, *artifact.ListReportsInput, ...func(*artifact.Options)) (*artifact.ListReportsOutput, error)
	ListCustomerAgreements(context.Context, *artifact.ListCustomerAgreementsInput, ...func(*artifact.Options)) (*artifact.ListCustomerAgreementsOutput, error)
}

// scanArtifact discovers AWS Artifact customer agreements and reports.
// Account-global; gated to us-east-1 to avoid duplicate scans across regions.
func scanArtifact(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := artifact.NewFromConfig(acct.cfg, func(o *artifact.Options) { o.Region = region })

	caTotal, caInserted, ferr := scanArtifactCustomerAgreements(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return caTotal, caInserted, ferr
	}
	rTotal, rInserted, ferr := scanArtifactReports(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return caTotal + rTotal, caInserted + rInserted, ferr
	}
	return caTotal + rTotal, caInserted + rInserted, nil
}

func scanArtifactCustomerAgreements(ctx context.Context, client artifactAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := artifact.NewListCustomerAgreementsPaginator(client, &artifact.ListCustomerAgreementsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "artifact:ListCustomerAgreements", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("artifact:ListCustomerAgreements: %w", perr)
		}
		for _, c := range out.CustomerAgreements {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeArtifactCustomerAgreement, NativeID: arn,
				Name: &label, Region: &region, CreatedAt: tp(c.EffectiveStart),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "artifact customer-agreements")
}

// scanArtifactReports lists the AWS-published compliance report catalog —
// AWS-owned documents identical across accounts, so each row is flagged
// ManagedByProvider (hidden from default queries).
func scanArtifactReports(ctx context.Context, client artifactAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := artifact.NewListReportsPaginator(client, &artifact.ListReportsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "artifact:ListReports", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("artifact:ListReports: %w", perr)
		}
		for _, r := range out.Reports {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			label := sv(r.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeArtifactReport, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "artifact reports")
}
