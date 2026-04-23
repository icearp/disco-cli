package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:acm", fn: scanACM}) }

// scanACM discovers ACM certificates in one region. ListCertificates returns
// ARNs + minimal metadata; DescribeCertificate is called concurrently per cert
// to fetch the full detail (SubjectAlternativeNames, CertificateAuthorityArn,
// DomainValidationOptions, etc.). Tags fetched via ListTagsForCertificate.
func scanACM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := acm.NewFromConfig(acct.cfg, func(o *acm.Options) { o.Region = region })

	pager := acm.NewListCertificatesPaginator(client, &acm.ListCertificatesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "acm:ListCertificates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("acm:ListCertificates: %w", err)
		}

		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, summary := range page.CertificateSummaryList {
			arn := sv(summary.CertificateArn)
			g.Go(func() error {
				desc, err := client.DescribeCertificate(gctx, &acm.DescribeCertificateInput{CertificateArn: &arn})
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("acm:DescribeCertificate %s: %w", arn, err)
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
				// Tags are a separate API call.
				if tagsOut, tErr := client.ListTagsForCertificate(gctx, &acm.ListTagsForCertificateInput{CertificateArn: &arn}); tErr == nil {
					r.TagsJSON = awsTagsJSON(tagsOut.Tags)
				}
				mu.Lock()
				batch = append(batch, r)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return 0, 0, err
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert ACM certificates: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
