package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/acm"
)

func init() {
	registerType(restype.Descriptor{Type: TypeACMCertificate, Service: "acm", Upstream: "AWS::CertificateManager::Certificate"})
	registerType(restype.Descriptor{Type: TypeACMAccount, Service: "acm", Upstream: "AWS::CertificateManager::Account", Leaf: true, Managed: true})
	registerService(serviceEntry{
		name: "aws:acm",
		fn:   scanACM,
	})
}

// acmAPI is the narrow set of ACM operations called by scanACMCertificates.
type acmAPI interface {
	ListCertificates(context.Context, *acm.ListCertificatesInput, ...func(*acm.Options)) (*acm.ListCertificatesOutput, error)
	DescribeCertificate(context.Context, *acm.DescribeCertificateInput, ...func(*acm.Options)) (*acm.DescribeCertificateOutput, error)
	ListTagsForCertificate(context.Context, *acm.ListTagsForCertificateInput, ...func(*acm.Options)) (*acm.ListTagsForCertificateOutput, error)
	GetAccountConfiguration(context.Context, *acm.GetAccountConfigurationInput, ...func(*acm.Options)) (*acm.GetAccountConfigurationOutput, error)
}

// scanACM discovers ACM certificates in one region. ListCertificates returns
// ARNs + minimal metadata; DescribeCertificate runs concurrently per cert for
// full detail (SubjectAlternativeNames, CertificateAuthorityArn,
// DomainValidationOptions, etc.); tags come from ListTagsForCertificate.
func scanACM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := acm.NewFromConfig(acct.cfg, func(o *acm.Options) { o.Region = region })
	t1, i1, err := scanACMCertificates(ctx, client, acct, region, st, scanID)
	if err != nil {
		return t1, i1, err
	}
	t2, i2, err := scanACMAccountConfig(ctx, client, acct, region, st, scanID)
	if err != nil {
		return t1, i1, err
	}
	return t1 + t2, i1 + i2, nil
}

// scanACMAccountConfig discovers the per-(account, region) ACM expiry-events
// config as a single aws:acm:account row. NativeID synthesized:
// arn:aws:acm:{r}:{a}:account.
func scanACMAccountConfig(ctx context.Context, client acmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetAccountConfiguration(ctx, &acm.GetAccountConfigurationInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "acm:GetAccountConfiguration", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("acm:GetAccountConfiguration: %w", err)
	}
	arn := fmt.Sprintf("arn:aws:acm:%s:%s:account", region, acct.ID)
	name := "account"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeACMAccount, NativeID: arn,
		Name: &name, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "acm account-config")
}

// scanACMCertificates holds the testable scan body.
func scanACMCertificates(ctx context.Context, client acmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := acm.NewListCertificatesPaginator(client, &acm.ListCertificatesInput{})
	return pageScanConcurrent(ctx, "acm:ListCertificates", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*acm.ListCertificatesOutput, error) { return p.NextPage(c) },
		func(o *acm.ListCertificatesOutput) []string {
			out := make([]string, 0, len(o.CertificateSummaryList))
			for _, s := range o.CertificateSummaryList {
				out = append(out, sv(s.CertificateArn))
			}
			return out
		},
		func(gctx context.Context, arn string) (*store.Resource, error) {
			desc, err := client.DescribeCertificate(gctx, &acm.DescribeCertificateInput{CertificateArn: &arn})
			if err != nil {
				if isAccessDenied(err) {
					return nil, nil
				}
				return nil, fmt.Errorf("acm:DescribeCertificate %s: %w", arn, err)
			}
			cert := desc.Certificate
			name := sv(cert.DomainName)
			status := string(cert.Status)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeACMCertificate,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(cert.CreatedAt),
				AttributesJSON: mustJSON(cert),
				DiscoveredBy:   scanID,
			}
			if tagsOut, tErr := client.ListTagsForCertificate(gctx, &acm.ListTagsForCertificateInput{CertificateArn: &arn}); tErr == nil {
				r.TagsJSON = awsTagsJSON(tagsOut.Tags)
			}
			return r, nil
		}, 0)
}
