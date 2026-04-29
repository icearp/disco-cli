package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/acmpca"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerService(serviceEntry{
		name: "aws:acm-pca",
		fn:   scanACMPCA,
		emits: []coverage.TypeDecl{
			{Service: "acmpca", DiscoType: TypeACMPrivateCA},
		},
	})
}

// acmpcaAPI is the narrow set of ACM Private CA operations called by
// scanACMPCACertificateAuthorities.
type acmpcaAPI interface {
	ListCertificateAuthorities(context.Context, *acmpca.ListCertificateAuthoritiesInput, ...func(*acmpca.Options)) (*acmpca.ListCertificateAuthoritiesOutput, error)
	ListTags(context.Context, *acmpca.ListTagsInput, ...func(*acmpca.Options)) (*acmpca.ListTagsOutput, error)
}

// scanACMPCA discovers ACM Private Certificate Authorities. ListCertificateAuthorities
// returns CA metadata including RevocationConfiguration; DescribeCertificateAuthority
// is only needed for tag-less access, so skip it and use list output directly.
func scanACMPCA(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := acmpca.NewFromConfig(acct.cfg, func(o *acmpca.Options) { o.Region = region })
	return scanACMPCACertificateAuthorities(ctx, client, acct, region, st, scanID)
}

// scanACMPCACertificateAuthorities holds the testable scan body.
func scanACMPCACertificateAuthorities(ctx context.Context, client acmpcaAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := acmpca.NewListCertificateAuthoritiesPaginator(client, &acmpca.ListCertificateAuthoritiesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "acm-pca:ListCertificateAuthorities", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("acm-pca:ListCertificateAuthorities: %w", err)
		}
		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, ca := range page.CertificateAuthorities {
			g.Go(func() error {
				arn := sv(ca.Arn)
				status := string(ca.Status)
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeACMPrivateCA,
					NativeID:       arn,
					Region:         &region,
					Status:         &status,
					CreatedAt:      tp(ca.CreatedAt),
					AttributesJSON: mustJSON(ca),
					DiscoveredBy:   scanID,
				}
				if tagsOut, tErr := client.ListTags(gctx, &acmpca.ListTagsInput{CertificateAuthorityArn: &arn}); tErr == nil && len(tagsOut.Tags) > 0 {
					m := make(map[string]string, len(tagsOut.Tags))
					for _, t := range tagsOut.Tags {
						if t.Key != nil && t.Value != nil {
							m[*t.Key] = *t.Value
						}
					}
					r.TagsJSON = mapTagsJSON(m)
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
				return 0, 0, fmt.Errorf("upsert ACM-PCA CAs: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
