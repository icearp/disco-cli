package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// An ACME external account binding's HMAC key is returned only by
// GetAcmeExternalAccountBindingCredentials, which disco never calls; the
// ListAcmeExternalAccountBindings element carries no key material, so the
// binding needs no Redact rule, and its tag read sends only its ARN. An ACME
// account carries the thumbprint of its public key, not the key, and a domain
// validation's ResourceRecord is the DNS record published for validation, so
// neither needs one either.
func init() {
	registerType(restype.Descriptor{Type: TypeACMCertificate, Service: "acm"})
	registerType(restype.Descriptor{Type: TypeACMAccount, Service: "acm", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeACMAcmeEndpoint, Service: "acm"})
	registerType(restype.Descriptor{Type: TypeACMAcmeExternalAccountBinding, Service: "acm"})
	// An ACME account has no ARN (its id is an https AccountUrl), the Service
	// Reference defines no acme-account resource and lists none under
	// TagResource, and AcmeAccountSummary carries no Tags, so no tag can be
	// added to one.
	registerType(restype.Descriptor{Type: TypeACMAcmeAccount, Service: "acm", Untaggable: true})
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
	ListTagsForResource(context.Context, *acm.ListTagsForResourceInput, ...func(*acm.Options)) (*acm.ListTagsForResourceOutput, error)
}

const (
	acmTagsOp     = "acm:ListTagsForResource"
	acmCertTagsOp = "acm:ListTagsForCertificate"
	// acmAcmePageCap is the MaxResults maximum of every ACME listing.
	acmAcmePageCap = 100
	// acmCertPageCap is the MaxItems maximum of ListCertificates.
	acmCertPageCap = 1000
)

// acmNotFound is ResourceNotFoundException, the not-found code that
// DescribeCertificate, ListTagsForCertificate and ListTagsForResource model
// for a resource deleted since it was listed, and each per-endpoint ACME
// listing models for an endpoint deleted since it was listed.
var acmNotFound = []string{"ResourceNotFoundException"}

// errACMNoCertificate is a DescribeCertificate response without the
// Certificate its row is built from; the row keeps its list element.
var errACMNoCertificate = errors.New("acm:DescribeCertificate returned no Certificate")

// scanACM discovers ACM certificates, the account's expiry-events config, and
// ACME endpoints with their external account bindings, ACME accounts and
// domain validations in one region. A certificate's own domain validation
// state is an attribute of the certificate row, not a resource.
//
// Every listing runs as its own phase, one at a time: a phase's error does not
// stop the later ones; a failed write and ctx's end do (runHostPhases). The
// ACME phases are the newest API surface and run last; the per-endpoint
// phases read the endpoints stored this scan. Certificates have their tags
// read with ListTagsForCertificate; ACME endpoints, external account bindings
// and domain validations with ListTagsForResource, whose denials are one
// warning for the whole scan. ACME accounts have no ARN and no tag operation.
func scanACM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := acm.NewFromConfig(acct.cfg, func(o *acm.Options) { o.Region = region })
	tags := acmResourceTags(client).sharingDenials()
	var endpoints []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "acm", 0, []hostPhase{
		{op: "acm:ListCertificates", run: func(p *phaseTotals) bool {
			return scanACMCertificates(ctx, client, acct, region, st, scanID, p)
		}},
		countedPhase(0, "acm:GetAccountConfiguration", func() (int, int, error) {
			return scanACMAccountConfig(ctx, client, acct, region, st, scanID)
		}),
		{op: "acm:ListAcmeEndpoints", run: func(p *phaseTotals) (failed bool) {
			endpoints, failed = scanACMAcmeEndpoints(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "acm:ListAcmeExternalAccountBindings", run: func(p *phaseTotals) bool {
			return scanACMAcmeExternalAccountBindings(ctx, client, acct, region, st, scanID, endpoints, tags, p)
		}},
		countedPhase(0, "acm:ListAcmeAccounts", func() (int, int, error) {
			return scanACMAcmeAccounts(ctx, client, acct, region, st, scanID, endpoints)
		}),
		{op: "acm:ListAcmeDomainValidations", run: func(p *phaseTotals) bool {
			return scanACMAcmeDomainValidations(ctx, client, acct, region, st, scanID, endpoints, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// acmCertificateTags reads a certificate's tags with ListTagsForCertificate,
// sent the row's NativeID, the CertificateArn ListCertificates returned. The
// op is not paginated.
func acmCertificateTags(client acmAPI) rowTagReader {
	return rowTagReader{
		op:       acmCertTagsOp,
		notFound: acmNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForCertificate(ctx, &acm.ListTagsForCertificateInput{CertificateArn: &arn})
			if err != nil {
				return nil, err
			}
			return awsTagsJSON(out.Tags), nil
		},
	}
}

// acmResourceTags reads the tags of an ACME endpoint, external account
// binding or domain validation with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The Service Reference lists those
// three resources under ListTagsForResource and TagResource, and lists no
// ACME account resource. The op is not paginated.
func acmResourceTags(client acmAPI) rowTagReader {
	return rowTagReader{
		op:       acmTagsOp,
		notFound: acmNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &acm.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return awsTagsJSON(out.Tags), nil
		},
	}
}

// acmUnderEndpoint names, in a warning, an element of endpoint's listing
// that has no identifier.
func acmUnderEndpoint(endpoint string) string {
	return noIdentifier + " under " + endpoint
}

// scanACMAcmeEndpoints stores the ACME endpoints with their tags, a page at a
// time, and returns the stored ones, the parents of the per-endpoint phases.
// A denial on a later page warns once and keeps the endpoints already listed.
// It reports whether a write failed, after which the scan must stop.
func scanACMAcmeEndpoints(ctx context.Context, client acmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "acm:ListAcmeEndpoints", "ACME endpoints", "AcmeEndpointArn",
		func(ctx context.Context, token *string) ([]acmtypes.AcmeEndpointSummary, *string, error) {
			out, err := client.ListAcmeEndpoints(ctx, &acm.ListAcmeEndpointsInput{MaxResults: sdkaws.Int32(acmAcmePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AcmeEndpoints, out.NextToken, nil
		},
		func(e acmtypes.AcmeEndpointSummary) (*store.Resource, string, string) {
			return arnRow("ACME endpoints", e.AcmeEndpointArn, e.EndpointUrl, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeACMAcmeEndpoint, NativeID: sv(e.AcmeEndpointArn), Region: &region,
				Status: nonEmptyPtr(string(e.Status)), CreatedAt: tp(e.CreatedAt),
				AttributesJSON: mustJSON(flattenACMAcmeEndpoint(e)), DiscoveredBy: scanID,
			})
		},
		func(e acmtypes.AcmeEndpointSummary) string { return sv(e.AcmeEndpointArn) },
		tags, p)
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

// scanACMAcmeExternalAccountBindings stores the external account bindings of
// each stored ACME endpoint with their tags, each under its endpoint
// (storeEachParentTagsUnder); one endpoint's failure costs the others none of
// theirs. A binding keeps its own ARN, so it is the NativeID. The API models
// no binding status; Status is "revoked" once RevokedAt is set and unset
// otherwise, since an unrevoked binding may have expired. Expiry stays in
// ExpiresAt and is not compared with the scan clock. It reports whether a
// write failed, after which the scan must stop.
func scanACMAcmeExternalAccountBindings(ctx context.Context, client acmAPI, acct *account, region string, st *store.Store, scanID string, endpoints []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentTagsUnder(ctx, st, acct, region, "acm:ListAcmeExternalAccountBindings", acmNotFound, endpoints,
		func(ctx context.Context, endpoint string, token *string) ([]acmtypes.AcmeExternalAccountBindingSummary, *string, error) {
			out, err := client.ListAcmeExternalAccountBindings(ctx, &acm.ListAcmeExternalAccountBindingsInput{
				AcmeEndpointArn: &endpoint, MaxResults: sdkaws.Int32(acmAcmePageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ExternalAccountBindings, out.NextToken, nil
		},
		func(endpoint string, b acmtypes.AcmeExternalAccountBindingSummary) (*store.Resource, string, string) {
			arn := sv(b.AcmeExternalAccountBindingArn)
			if arn == "" {
				return nil, "ACME external account bindings have no ARN", acmUnderEndpoint(endpoint)
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
			return r, "", ""
		}, tags, p)
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

// scanACMAcmeAccounts stores the ACME accounts registered with each stored
// ACME endpoint, each under its endpoint (storeChildrenEachParent); one
// endpoint's failure costs the others none of theirs. The list element has
// every field DescribeAcmeAccount returns, so no describe call follows. An
// account has no ARN and no tag operation, so no tags are read.
func scanACMAcmeAccounts(ctx context.Context, client acmAPI, acct *account, region string, st *store.Store, scanID string, endpoints []childParent) (int, int, error) {
	return storeChildrenEachParent(ctx, st, acct, region, "acm:ListAcmeAccounts", acmNotFound, endpoints, nil, nil,
		func(ctx context.Context, endpoint childParent, token *string) ([]acmtypes.AcmeAccountSummary, *string, error) {
			out, err := client.ListAcmeAccounts(ctx, &acm.ListAcmeAccountsInput{
				AcmeEndpointArn: &endpoint.arn, MaxResults: sdkaws.Int32(acmAcmePageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.AcmeAccounts, out.NextToken, nil
		},
		func(endpoint childParent, a acmtypes.AcmeAccountSummary) (*store.Resource, string, string) {
			url := sv(a.AccountUrl)
			if url == "" {
				return nil, "ACME accounts have no AccountUrl", acmUnderEndpoint(endpoint.arn)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeACMAcmeAccount, NativeID: acmAcmeAccountNativeID(endpoint.arn, url), Region: &region,
				Status: nonEmptyPtr(string(a.Status)), CreatedAt: tp(a.CreatedAt),
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		})
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

// scanACMAcmeDomainValidations stores the domain validations of each stored
// ACME endpoint with their tags, keyed by their own ARN, each under its
// endpoint (storeEachParentTagsUnder); one endpoint's failure costs the
// others none of theirs. It reports whether a write failed, after which the
// scan must stop.
func scanACMAcmeDomainValidations(ctx context.Context, client acmAPI, acct *account, region string, st *store.Store, scanID string, endpoints []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentTagsUnder(ctx, st, acct, region, "acm:ListAcmeDomainValidations", acmNotFound, endpoints,
		func(ctx context.Context, endpoint string, token *string) ([]acmtypes.AcmeDomainValidationSummary, *string, error) {
			out, err := client.ListAcmeDomainValidations(ctx, &acm.ListAcmeDomainValidationsInput{
				AcmeEndpointArn: &endpoint, MaxResults: sdkaws.Int32(acmAcmePageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.AcmeDomainValidations, out.NextToken, nil
		},
		func(endpoint string, v acmtypes.AcmeDomainValidationSummary) (*store.Resource, string, string) {
			arn := sv(v.AcmeDomainValidationArn)
			if arn == "" {
				return nil, "ACME domain validations have no ARN", acmUnderEndpoint(endpoint)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeACMAcmeDomainValidation, NativeID: arn, Name: v.DomainName, Region: &region,
				Status: nonEmptyPtr(string(v.Status)), CreatedAt: tp(v.CreatedAt),
				AttributesJSON: mustJSON(flattenACMAcmeDomainValidation(v)), DiscoveredBy: scanID,
			}, "", ""
		}, tags, p)
}

// scanACMAccountConfig discovers the per-(account, region) ACM expiry-events
// config as a single aws:acm:account row. NativeID synthesized:
// arn:{p}:acm:{r}:{a}:account.
func scanACMAccountConfig(ctx context.Context, client acmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetAccountConfiguration(ctx, &acm.GetAccountConfigurationInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "acm:GetAccountConfiguration", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("acm:GetAccountConfiguration: %w", err)
	}
	arn := arnFor(acct.partition(), "acm", region, acct.ID, "account")
	name := "account"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeACMAccount, NativeID: arn,
		Name: &name, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "acm account-config")
}

// scanACMCertificates stores the certificates a page at a time, each row's
// attributes replaced with its DescribeCertificate body, then its tags read
// (acmCertificateTags). A certificate DescribeCertificate or
// ListTagsForCertificate no longer finds was deleted since the listing and is
// dropped; any other Describe failure keeps the CertificateSummary as the row
// (rowDetailReader). Name, Status and CreatedAt come from the
// CertificateSummary. It reports whether a write failed, after which the scan
// must stop.
//
// ListCertificates by default returns only RSA_1024 and RSA_2048 certificates
// whose key pair origin is AWS_MANAGED or CUSTOMER_PROVIDED, so every page
// asks for every key type and every origin (ACME included) the pinned SDK
// knows. The origin list allows 1 to 3 values, as many as the enum has;
// TestScanACM_ListCertificatesFilters pins both lists, so an SDK bump that
// adds a value fails it until the bound is checked again. The documentation
// also ties the default to certificates with at least one domain and does not
// say whether these filters lift that, so a certificate without a domain may
// still be left out.
func scanACMCertificates(ctx context.Context, client acmAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) bool {
	steps := tokenPageSteps{details: rowDetailReader{
		op: "acm:DescribeCertificate", what: "certificate(s)", notFound: acmNotFound,
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			arn := r.NativeID
			out, err := client.DescribeCertificate(ctx, &acm.DescribeCertificateInput{CertificateArn: &arn})
			if err != nil {
				return nil, err
			}
			if out.Certificate == nil {
				return nil, errACMNoCertificate
			}
			return out.Certificate, nil
		},
	}}
	return storeTokenPagesTagsWith(ctx, st, acct, region, "acm:ListCertificates",
		func(ctx context.Context, token *string) ([]acmtypes.CertificateSummary, *string, error) {
			out, err := client.ListCertificates(ctx, &acm.ListCertificatesInput{
				CertificateKeyPairOrigins: acmtypes.CertificateKeyPairOrigin("").Values(),
				Includes:                  &acmtypes.Filters{KeyTypes: acmtypes.KeyAlgorithm("").Values()},
				MaxItems:                  sdkaws.Int32(acmCertPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.CertificateSummaryList, out.NextToken, nil
		},
		func(s acmtypes.CertificateSummary) (*store.Resource, string, string) {
			name := sv(s.DomainName)
			status := string(s.Status)
			return arnRow("certificates", s.CertificateArn, s.DomainName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeACMCertificate, NativeID: sv(s.CertificateArn),
				Name: &name, Region: &region, Status: &status,
				CreatedAt:      tp(s.CreatedAt),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}, steps, acmCertificateTags(client), p)
}
