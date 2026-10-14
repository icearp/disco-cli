package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// An ACME external account binding's HMAC key is returned only by
// GetAcmeExternalAccountBindingCredentials, which disco never calls; the
// ListAcmeExternalAccountBindings element carries no key material, so the
// binding needs no Redact rule. An ACME account carries the thumbprint of its
// public key, not the key, and a domain validation's ResourceRecord is the DNS
// record published for validation, so neither needs one either.
func init() {
	registerType(restype.Descriptor{Type: TypeACMCertificate, Service: "acm"})
	registerType(restype.Descriptor{Type: TypeACMAccount, Service: "acm", Managed: true})
	registerType(restype.Descriptor{Type: TypeACMAcmeEndpoint, Service: "acm"})
	registerType(restype.Descriptor{Type: TypeACMAcmeExternalAccountBinding, Service: "acm"})
	registerType(restype.Descriptor{Type: TypeACMAcmeAccount, Service: "acm"})
	registerType(restype.Descriptor{Type: TypeACMAcmeDomainValidation, Service: "acm"})
	registerService(serviceEntry{
		name: "aws:acm",
		fn:   scanACM,
	})
}

// acmAPI is the narrow set of ACM operations called by scanACM's phases.
type acmAPI interface {
	ListCertificates(context.Context, *acm.ListCertificatesInput, ...func(*acm.Options)) (*acm.ListCertificatesOutput, error)
	DescribeCertificate(context.Context, *acm.DescribeCertificateInput, ...func(*acm.Options)) (*acm.DescribeCertificateOutput, error)
	ListTagsForCertificate(context.Context, *acm.ListTagsForCertificateInput, ...func(*acm.Options)) (*acm.ListTagsForCertificateOutput, error)
	GetAccountConfiguration(context.Context, *acm.GetAccountConfigurationInput, ...func(*acm.Options)) (*acm.GetAccountConfigurationOutput, error)
	ListAcmeEndpoints(context.Context, *acm.ListAcmeEndpointsInput, ...func(*acm.Options)) (*acm.ListAcmeEndpointsOutput, error)
	ListAcmeExternalAccountBindings(context.Context, *acm.ListAcmeExternalAccountBindingsInput, ...func(*acm.Options)) (*acm.ListAcmeExternalAccountBindingsOutput, error)
	ListAcmeAccounts(context.Context, *acm.ListAcmeAccountsInput, ...func(*acm.Options)) (*acm.ListAcmeAccountsOutput, error)
	ListAcmeDomainValidations(context.Context, *acm.ListAcmeDomainValidationsInput, ...func(*acm.Options)) (*acm.ListAcmeDomainValidationsOutput, error)
}

// scanACM discovers ACM certificates, the account's expiry-events config, and
// ACME endpoints with their external account bindings, ACME accounts and
// domain validations in one region. A certificate's own domain validation
// state is an attribute of the certificate row, not a resource.
//
// Phases run in order and stop at the first hard error, returning the counts
// each phase reports, the failing phase's included. The ACME phases are the newest
// API surface and run last, so their failure cannot cost the certificates or
// the account config.
func scanACM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := acm.NewFromConfig(acct.cfg, func(o *acm.Options) { o.Region = region })
	var endpoints []childParent
	// Each child phase calls its list function directly, not through a shared
	// function-valued helper: the coverage pairing walker follows direct calls
	// only, so a function value would leave the op labels unanchored.
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanACMCertificates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanACMAccountConfig(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			t, i, endpoints, err = scanACMAcmeEndpoints(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) {
			return childFanOut(ctx, st, acct, region, "acm:ListAcmeExternalAccountBindings", endpoints, isACMNotFound,
				func(ctx context.Context, p childParent) ([]*store.Resource, error) {
					return listACMAcmeExternalAccountBindings(ctx, client, acct, region, scanID, p)
				})
		},
		func() (int, int, error) {
			return childFanOut(ctx, st, acct, region, "acm:ListAcmeAccounts", endpoints, isACMNotFound,
				func(ctx context.Context, p childParent) ([]*store.Resource, error) {
					return listACMAcmeAccounts(ctx, client, acct, region, scanID, p)
				})
		},
		func() (int, int, error) {
			return childFanOut(ctx, st, acct, region, "acm:ListAcmeDomainValidations", endpoints, isACMNotFound,
				func(ctx context.Context, p childParent) ([]*store.Resource, error) {
					return listACMAcmeDomainValidations(ctx, client, acct, region, scanID, p)
				})
		},
	} {
		t, i, perr := phase()
		if perr != nil {
			return total + t, inserted + i, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// isACMNotFound matches an ACME endpoint deleted between its list call and
// the call that lists its children.
func isACMNotFound(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// scanACMAcmeEndpoints also returns the stored endpoints for the
// endpoint-child phases. A denial on a later page warns once and
// keeps the endpoints already listed.
func scanACMAcmeEndpoints(ctx context.Context, client acmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var batch []*store.Resource
	var parents []childParent
	pager := acm.NewListAcmeEndpointsPaginator(client, &acm.ListAcmeEndpointsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if !isAccessDenied(err) {
				return 0, 0, nil, fmt.Errorf("acm:ListAcmeEndpoints: %w", err)
			}
			_ = skipIfAccessDenied(st, "acm:ListAcmeEndpoints", acct.ID, region, err)
			break
		}
		for _, e := range out.AcmeEndpoints {
			arn := sv(e.AcmeEndpointArn)
			if arn == "" {
				continue
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeACMAcmeEndpoint, NativeID: arn, Region: &region,
				CreatedAt:      tp(e.CreatedAt),
				AttributesJSON: mustJSON(flattenACMAcmeEndpoint(e)), DiscoveredBy: scanID,
			}
			if s := string(e.Status); s != "" {
				r.Status = &s
			}
			batch = append(batch, r)
			parents = append(parents, childParent{id: arn, arn: arn})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "acm acme-endpoints")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, parents, nil
}

// acmAcmeEndpointAttrs shadows the endpoint's CertificateAuthority union,
// which marshals as an unlabelled {"Value": ...}, with one key per known
// member so the stored row names the issuing authority kind. A member newer
// than the pinned SDK is kept under UnknownMember with its tag and its value,
// which is []byte and so stored base64-encoded.
type acmAcmeEndpointAttrs struct {
	acmtypes.AcmeEndpointSummary
	CertificateAuthority acmCertificateAuthorityAttrs
}

type acmCertificateAuthorityAttrs struct {
	PublicCertificateAuthority *acmtypes.PublicCertificateAuthority `json:",omitempty"`
	UnknownMember              *acmtypes.UnknownUnionMember         `json:",omitempty"`
}

func flattenACMAcmeEndpoint(e acmtypes.AcmeEndpointSummary) acmAcmeEndpointAttrs {
	out := acmAcmeEndpointAttrs{AcmeEndpointSummary: e}
	switch ca := e.CertificateAuthority.(type) {
	case *acmtypes.CertificateAuthorityMemberPublicCertificateAuthority:
		out.CertificateAuthority.PublicCertificateAuthority = &ca.Value
	case *acmtypes.UnknownUnionMember:
		out.CertificateAuthority.UnknownMember = ca
	}
	return out
}

// listACMAcmeExternalAccountBindings lists the external account bindings of
// one ACME endpoint. A binding keeps its own ARN, so it is the NativeID. The
// API models no binding status; Status is "revoked" once RevokedAt is set and
// unset otherwise, since an unrevoked binding may have expired. Expiry stays
// in ExpiresAt and is not compared with the scan clock.
func listACMAcmeExternalAccountBindings(ctx context.Context, client acmAPI, acct *account, region, scanID string, endpoint childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := acm.NewListAcmeExternalAccountBindingsPaginator(client, &acm.ListAcmeExternalAccountBindingsInput{AcmeEndpointArn: &endpoint.arn})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, b := range out.ExternalAccountBindings {
			arn := sv(b.AcmeExternalAccountBindingArn)
			if arn == "" {
				continue
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeACMAcmeExternalAccountBinding, NativeID: arn, Region: &region,
				CreatedAt:      tp(b.CreatedAt),
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			}
			if b.RevokedAt != nil {
				revoked := "revoked"
				r.Status = &revoked
			}
			rows = append(rows, r)
		}
	}
	return rows, nil
}

// acmAcmeAccountNativeID is an ACME account's identity. The account has no ARN
// (the Service Reference defines no acme-account resource), and the ACM API
// addresses it by endpoint ARN and AccountUrl together (DescribeAcmeAccount,
// RevokeAcmeAccount), so the id carries both: an AccountUrl reused under two
// endpoints stays two accounts. The acme-account segment differs from the
// endpoint's ARN-issued child segments (acme-external-account-binding,
// acme-domain-validation).
func acmAcmeAccountNativeID(endpointARN, accountURL string) string {
	return endpointARN + "/acme-account/" + accountURL
}

// listACMAcmeAccounts lists the ACME accounts registered with one ACME
// endpoint. The list element has every field DescribeAcmeAccount returns, so
// no describe call follows.
func listACMAcmeAccounts(ctx context.Context, client acmAPI, acct *account, region, scanID string, endpoint childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := acm.NewListAcmeAccountsPaginator(client, &acm.ListAcmeAccountsInput{AcmeEndpointArn: &endpoint.arn})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, a := range out.AcmeAccounts {
			url := sv(a.AccountUrl)
			if url == "" {
				continue
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeACMAcmeAccount, NativeID: acmAcmeAccountNativeID(endpoint.arn, url), Region: &region,
				CreatedAt:      tp(a.CreatedAt),
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}
			if s := string(a.Status); s != "" {
				r.Status = &s
			}
			rows = append(rows, r)
		}
	}
	return rows, nil
}

// acmAcmeDomainValidationAttrs shadows the PrevalidationDetails union the same
// way acmAcmeEndpointAttrs shadows CertificateAuthority.
type acmAcmeDomainValidationAttrs struct {
	acmtypes.AcmeDomainValidationSummary
	PrevalidationDetails acmPrevalidationDetailsAttrs
}

type acmPrevalidationDetailsAttrs struct {
	DNSPrevalidation *acmtypes.DnsPrevalidationDetails `json:"DnsPrevalidation,omitempty"`
	UnknownMember    *acmtypes.UnknownUnionMember      `json:",omitempty"`
}

func flattenACMAcmeDomainValidation(v acmtypes.AcmeDomainValidationSummary) acmAcmeDomainValidationAttrs {
	out := acmAcmeDomainValidationAttrs{AcmeDomainValidationSummary: v}
	switch d := v.PrevalidationDetails.(type) {
	case *acmtypes.PrevalidationDetailsMemberDnsPrevalidation:
		out.PrevalidationDetails.DNSPrevalidation = &d.Value
	case *acmtypes.UnknownUnionMember:
		out.PrevalidationDetails.UnknownMember = d
	}
	return out
}

// listACMAcmeDomainValidations lists the domain validations of one ACME
// endpoint, keyed by their own ARN.
func listACMAcmeDomainValidations(ctx context.Context, client acmAPI, acct *account, region, scanID string, endpoint childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := acm.NewListAcmeDomainValidationsPaginator(client, &acm.ListAcmeDomainValidationsInput{AcmeEndpointArn: &endpoint.arn})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range out.AcmeDomainValidations {
			arn := sv(v.AcmeDomainValidationArn)
			if arn == "" {
				continue
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeACMAcmeDomainValidation, NativeID: arn, Name: v.DomainName, Region: &region,
				CreatedAt:      tp(v.CreatedAt),
				AttributesJSON: mustJSON(flattenACMAcmeDomainValidation(v)), DiscoveredBy: scanID,
			}
			if s := string(v.Status); s != "" {
				r.Status = &s
			}
			rows = append(rows, r)
		}
	}
	return rows, nil
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
