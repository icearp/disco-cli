package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/acmpca"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeACMPrivateCA, Service: "acmpca", Upstream: "AWS::ACMPCA::CertificateAuthority"})
	registerType(restype.Descriptor{Type: TypeACMPCAPermission, Service: "acmpca", Upstream: "AWS::ACMPCA::Permission", Leaf: true})
	registerService(serviceEntry{
		name: "aws:acm-pca",
		fn:   scanACMPCA,
	})
}

// acmpcaAPI is the narrow set of ACM Private CA operations called by
// scanACMPCACertificateAuthorities and scanACMPCAPermissions.
type acmpcaAPI interface {
	ListCertificateAuthorities(context.Context, *acmpca.ListCertificateAuthoritiesInput, ...func(*acmpca.Options)) (*acmpca.ListCertificateAuthoritiesOutput, error)
	ListTags(context.Context, *acmpca.ListTagsInput, ...func(*acmpca.Options)) (*acmpca.ListTagsOutput, error)
	ListPermissions(context.Context, *acmpca.ListPermissionsInput, ...func(*acmpca.Options)) (*acmpca.ListPermissionsOutput, error)
}

// acmpcaPermissionNativeID synthesizes a deterministic NativeID for an ACM-PCA
// permission grant: CreatePermission issues no ARN, so uniqueness is the
// (caARN, principal) pair, encoded here.
func acmpcaPermissionNativeID(caARN, principal string) string {
	return caARN + "/permission/" + principal
}

// scanACMPCA discovers ACM Private Certificate Authorities and their
// permission grants. Phase 1: ListCertificateAuthorities returns CA metadata
// (incl. RevocationConfiguration) directly — DescribeCertificateAuthority,
// needed only for tag-less access, is skipped. Phase 2: per-CA
// ListPermissions surfaces CreatePermission grants (only `acm.amazonaws.com`
// is currently a valid principal per AWS docs).
func scanACMPCA(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := acmpca.NewFromConfig(acct.cfg, func(o *acmpca.Options) { o.Region = region })
	caTotal, caInserted, caARNs, err := scanACMPCACertificateAuthorities(ctx, client, acct, region, st, scanID)
	if err != nil {
		return caTotal, caInserted, err
	}
	pTotal, pInserted, err := scanACMPCAPermissions(ctx, client, acct, region, st, scanID, caARNs)
	if err != nil {
		return caTotal + pTotal, caInserted + pInserted, err
	}
	return caTotal + pTotal, caInserted + pInserted, nil
}

// scanACMPCACertificateAuthorities holds the testable scan body; returns the
// CA ARNs scanned for use by the Permissions phase.
func scanACMPCACertificateAuthorities(ctx context.Context, client acmpcaAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, caARNs []string, err error) {
	pager := acmpca.NewListCertificateAuthoritiesPaginator(client, &acmpca.ListCertificateAuthoritiesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, caARNs, skipIfAccessDenied(st, "acm-pca:ListCertificateAuthorities", acct.ID, region, err)
			}
			return 0, 0, caARNs, fmt.Errorf("acm-pca:ListCertificateAuthorities: %w", err)
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
			return 0, 0, caARNs, err
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, caARNs, fmt.Errorf("upsert ACM-PCA CAs: %w", err)
			}
			total += len(batch)
			inserted += n
			for _, r := range batch {
				caARNs = append(caARNs, r.NativeID)
			}
		}
	}
	return total, inserted, caARNs, nil
}

// scanACMPCAPermissions enumerates per-CA CreatePermission grants. Permission
// records have no AWS-issued ARN; NativeID synthesized via
// acmpcaPermissionNativeID. Each permission is hierarchy-closure-wired to its
// parent CA.
func scanACMPCAPermissions(ctx context.Context, client acmpcaAPI, acct *account, region string, st *store.Store, scanID string, caARNs []string) (total, inserted int, err error) {
	if len(caARNs) == 0 {
		return 0, 0, nil
	}
	type pendingPerm struct {
		caARN string
		res   *store.Resource
	}
	var (
		mu      sync.Mutex
		pending []pendingPerm
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, caARN := range caARNs {
		g.Go(func() error {
			pager := acmpca.NewListPermissionsPaginator(client, &acmpca.ListPermissionsInput{CertificateAuthorityArn: &caARN})
			for pager.HasMorePages() {
				page, perr := pager.NextPage(gctx)
				if perr != nil {
					if isAccessDenied(perr) {
						_ = skipIfAccessDenied(st, "acm-pca:ListPermissions", acct.ID, region, perr)
						return nil
					}
					return fmt.Errorf("acm-pca:ListPermissions %s: %w", caARN, perr)
				}
				for _, p := range page.Permissions {
					principal := sv(p.Principal)
					if principal == "" {
						continue
					}
					nativeID := acmpcaPermissionNativeID(caARN, principal)
					name := principal
					r := &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeACMPCAPermission,
						NativeID:       nativeID,
						Name:           &name,
						Region:         &region,
						CreatedAt:      tp(p.CreatedAt),
						AttributesJSON: mustJSON(p),
						DiscoveredBy:   scanID,
					}
					mu.Lock()
					pending = append(pending, pendingPerm{caARN: caARN, res: r})
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(pending) == 0 {
		return 0, 0, nil
	}
	batch := make([]*store.Resource, len(pending))
	for i, pp := range pending {
		batch[i] = pp.res
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert ACM-PCA permissions: %w", err)
	}
	pairs := make([][2]string, len(pending))
	for i, pp := range pending {
		parentID := store.ResourceID("aws", acct.ID, pp.caARN)
		pairs[i] = [2]string{pp.res.ID, parentID}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure acm-pca permissions: %w", err)
	}
	return len(batch), n, nil
}
