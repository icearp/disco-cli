package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/mailmanager"
	sesv1 "github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSESEmailIdentity, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESConfigurationSet, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESConfigurationSetEventDestination, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESContactList, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESCustomVerificationEmailTemplate, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESDedicatedIPPool, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESMultiRegionEndpoint, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESTemplate, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESTenant, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESVdmAttributes, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESReceiptFilter, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESReceiptRule, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESReceiptRuleSet, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESMailManagerAddonInstance, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESMailManagerAddonSubscription, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESMailManagerAddressList, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESMailManagerArchive, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESMailManagerIngressPoint, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESMailManagerRelay, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESMailManagerRuleSet, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESMailManagerTrafficPolicy, Service: "ses"})
	registerType(restype.Descriptor{Type: TypeSESEmailIdentityCertificate, Service: "ses"})
	registerService(serviceEntry{
		name: "aws:ses",
		fn:   scanSES,
	})
}

// sesv2API is the narrow set of SES v2 operations called by the scanSES
// sub-phases.
type sesv2API interface {
	ListEmailIdentities(context.Context, *sesv2.ListEmailIdentitiesInput, ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error)
	GetEmailIdentity(context.Context, *sesv2.GetEmailIdentityInput, ...func(*sesv2.Options)) (*sesv2.GetEmailIdentityOutput, error)
	ListConfigurationSets(context.Context, *sesv2.ListConfigurationSetsInput, ...func(*sesv2.Options)) (*sesv2.ListConfigurationSetsOutput, error)
	GetConfigurationSet(context.Context, *sesv2.GetConfigurationSetInput, ...func(*sesv2.Options)) (*sesv2.GetConfigurationSetOutput, error)
	GetConfigurationSetEventDestinations(context.Context, *sesv2.GetConfigurationSetEventDestinationsInput, ...func(*sesv2.Options)) (*sesv2.GetConfigurationSetEventDestinationsOutput, error)
	ListContactLists(context.Context, *sesv2.ListContactListsInput, ...func(*sesv2.Options)) (*sesv2.ListContactListsOutput, error)
	ListCustomVerificationEmailTemplates(context.Context, *sesv2.ListCustomVerificationEmailTemplatesInput, ...func(*sesv2.Options)) (*sesv2.ListCustomVerificationEmailTemplatesOutput, error)
	ListDedicatedIpPools(context.Context, *sesv2.ListDedicatedIpPoolsInput, ...func(*sesv2.Options)) (*sesv2.ListDedicatedIpPoolsOutput, error)
	ListMultiRegionEndpoints(context.Context, *sesv2.ListMultiRegionEndpointsInput, ...func(*sesv2.Options)) (*sesv2.ListMultiRegionEndpointsOutput, error)
	ListEmailTemplates(context.Context, *sesv2.ListEmailTemplatesInput, ...func(*sesv2.Options)) (*sesv2.ListEmailTemplatesOutput, error)
	ListTenants(context.Context, *sesv2.ListTenantsInput, ...func(*sesv2.Options)) (*sesv2.ListTenantsOutput, error)
	GetAccount(context.Context, *sesv2.GetAccountInput, ...func(*sesv2.Options)) (*sesv2.GetAccountOutput, error)
	ListEmailIdentityCertificates(context.Context, *sesv2.ListEmailIdentityCertificatesInput, ...func(*sesv2.Options)) (*sesv2.ListEmailIdentityCertificatesOutput, error)
}

// scanSES discovers SES v1, SES v2 and Mail Manager resources in one region.
// Phases run sequentially and the first hard error returns; phase-level
// AccessDenied is tolerated via skipIfAccessDenied without barring the other
// phases. The certificate phase runs last so its errors cannot stop an older
// phase.
func scanSES(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sesv2.NewFromConfig(acct.cfg, func(o *sesv2.Options) { o.Region = region })
	v1client := sesv1.NewFromConfig(acct.cfg, func(o *sesv1.Options) { o.Region = region })
	mmclient := mailmanager.NewFromConfig(acct.cfg, func(o *mailmanager.Options) { o.Region = region })

	t, i, identities, ferr := scanSESEmailIdentities(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	{
		t, i, ferr := scanSESConfigurationSets(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanSESExtended(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanSESReceipt(ctx, v1client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanSESMailManager(ctx, mmclient, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	t, i, ferr = scanSESEmailIdentityCertificates(ctx, client, acct, region, st, scanID, identities)
	return total + t, inserted + i, ferr
}

// sesEmailIdentityARN rebuilds the canonical email-identity ARN from an
// IdentityName (email or domain). SES v2 List/Get return only the bare name,
// but cross-resource edges (CloudFront authorizers, Pinpoint, IAM policy doc
// walkers) need the ARN.
func sesEmailIdentityARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:ses:%s:%s:identity/%s", region, accountID, name)
}

func sesConfigurationSetARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:ses:%s:%s:configuration-set/%s", region, accountID, name)
}

// sesIdentity is a stored email identity, handed to the certificate phase.
type sesIdentity struct {
	childParent
	// emailAddress marks an EMAIL_ADDRESS identity, whose certificate
	// associations may omit FromAddress.
	emailAddress bool
}

// scanSESEmailIdentities stores every listed email identity and returns the
// stored ones as parents for the certificate phase; an identity whose
// GetEmailIdentity is denied is neither stored nor returned.
func scanSESEmailIdentities(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, identities []sesIdentity, err error) {
	pager := sesv2.NewListEmailIdentitiesPaginator(client, &sesv2.ListEmailIdentitiesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sesv2:ListEmailIdentities", acct.ID, region, perr)
				return 0, 0, nil, nil
			}
			return 0, 0, nil, fmt.Errorf("sesv2:ListEmailIdentities: %w", perr)
		}
		for _, id := range out.EmailIdentities {
			if id.IdentityName != nil {
				names = append(names, *id.IdentityName)
			}
		}
	}
	if len(names) == 0 {
		return 0, 0, nil, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, nil, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.GetEmailIdentity(gctx, &sesv2.GetEmailIdentityInput{EmailIdentity: &name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sesv2:GetEmailIdentity %s: %w", name, derr)
			}
			arn := sesEmailIdentityARN(region, acct.ID, name)
			status := string(out.VerificationStatus)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSESEmailIdentity,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			identities = append(identities, sesIdentity{
				childParent:  childParent{id: name, arn: arn},
				emailAddress: out.IdentityType == sesv2types.IdentityTypeEmailAddress,
			})
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, nil, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, nil, fmt.Errorf("upsert ses email identities: %w", uerr)
	}
	return len(batch), n, identities, nil
}

func scanSESConfigurationSets(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := sesv2.NewListConfigurationSetsPaginator(client, &sesv2.ListConfigurationSetsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sesv2:ListConfigurationSets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sesv2:ListConfigurationSets: %w", perr)
		}
		names = append(names, out.ConfigurationSets...)
	}
	if len(names) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.GetConfigurationSet(gctx, &sesv2.GetConfigurationSetInput{ConfigurationSetName: &name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sesv2:GetConfigurationSet %s: %w", name, derr)
			}
			arn := sesConfigurationSetARN(region, acct.ID, name)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSESConfigurationSet,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(flattenSESConfigurationSet(out)),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses configuration sets: %w", uerr)
	}
	return len(batch), n, nil
}

// sesConfigurationSetAttrs is the GetConfigurationSet body with the
// SigningScheme union keyed by member name. Each union member marshals as an
// unlabelled {"Value": ...}, so the default scheme would store as
// {"Value":{}} and an S/MIME scheme only differ by its SignatureFormat. A
// member newer than the pinned SDK is kept under UnknownMember with its tag
// (precedent: supportPermitAttrs). A nil MessageSecurityOptions is omitted:
// sesv2 before v1.71.0 had no such field, so a configuration set without one
// stores the shape it stored before the module bump and gets no new version.
// A nil SigningScheme marshals as null, as the embedded field does.
type sesConfigurationSetAttrs struct {
	*sesv2.GetConfigurationSetOutput
	MessageSecurityOptions *sesMessageSecurityAttrs `json:",omitempty"`
}

type sesMessageSecurityAttrs struct {
	SigningScheme *sesSigningSchemeAttrs
}

type sesSigningSchemeAttrs struct {
	DefaultScheme *sesv2types.DefaultSigningScheme `json:",omitempty"`
	SmimeScheme   *sesv2types.SmimeSigningScheme   `json:",omitempty"`
	UnknownMember *sesv2types.UnknownUnionMember   `json:",omitempty"`
}

func flattenSESConfigurationSet(out *sesv2.GetConfigurationSetOutput) sesConfigurationSetAttrs {
	attrs := sesConfigurationSetAttrs{GetConfigurationSetOutput: out}
	if out.MessageSecurityOptions == nil {
		return attrs
	}
	attrs.MessageSecurityOptions = &sesMessageSecurityAttrs{}
	var scheme sesSigningSchemeAttrs
	switch m := out.MessageSecurityOptions.SigningScheme.(type) {
	case *sesv2types.SigningSchemeMemberDefaultScheme:
		scheme.DefaultScheme = &m.Value
	case *sesv2types.SigningSchemeMemberSmimeScheme:
		scheme.SmimeScheme = &m.Value
	case *sesv2types.UnknownUnionMember:
		scheme.UnknownMember = m
	default:
		return attrs
	}
	attrs.MessageSecurityOptions.SigningScheme = &scheme
	return attrs
}
