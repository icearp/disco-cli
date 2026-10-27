package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mailmanager"
	sesv1 "github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
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
	ListTagsForResource(context.Context, *sesv2.ListTagsForResourceInput, ...func(*sesv2.Options)) (*sesv2.ListTagsForResourceOutput, error)
}

const (
	sesListTagsOp     = "sesv2:ListTagsForResource"
	sesGetIdentityOp  = "sesv2:GetEmailIdentity"
	sesGetConfigSetOp = "sesv2:GetConfigurationSet"
)

// sesNotFound is the only not-found code GetEmailIdentity,
// GetConfigurationSet, GetConfigurationSetEventDestinations and
// ListTagsForResource model.
var sesNotFound = []string{"NotFoundException"}

// The endpoints the ses scan calls. SES v2 and classic SES share the email
// endpoint; a region may serve SES without Mail Manager.
const (
	sesHost = iota
	sesMailManagerHost
)

func scanSES(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sesv2.NewFromConfig(acct.cfg, func(o *sesv2.Options) { o.Region = region })
	v1client := sesv1.NewFromConfig(acct.cfg, func(o *sesv1.Options) { o.Region = region })
	mmclient := mailmanager.NewFromConfig(acct.cfg, func(o *mailmanager.Options) { o.Region = region })
	return runHostPhases(ctx, st, acct, region, "ses", sesHost, sesPhases(ctx, client, v1client, mmclient, acct, region, st, scanID))
}

// sesPhases is the ses scan's phases in the order they run (runHostPhases):
// SES v2 and classic SES, then Mail Manager, then the certificate phase,
// which lists the certificates of every identity the first phase stored.
// It makes one call per identity, the likeliest to fail or be cut short
// by ctx, so it runs after every phase that makes few calls. The email
// host is the primary one: a region without it is reported unavailable,
// while one without Mail Manager skips the Mail Manager phases silently.
// The phases run one at a time, so readRowTags's own bound is the bound on
// the tag reads in flight.
func sesPhases(ctx context.Context, client sesv2API, v1client sesV1API, mmclient mailManagerAPI, acct *account, region string, st *store.Store, scanID string) []hostPhase {
	var (
		tags, mmTags = sesv2Tags(client, nil), mailManagerTags(mmclient)
		identities   []sesIdentity
		configSets   []childParent
	)
	return []hostPhase{
		{sesListIdentitiesOp, sesHost, func(p *phaseTotals) (stop bool) {
			identities, stop = scanSESEmailIdentities(ctx, client, acct, region, st, scanID, p)
			return stop
		}},
		{sesListConfigSetsOp, sesHost, func(p *phaseTotals) (stop bool) {
			configSets, stop = scanSESConfigurationSets(ctx, client, acct, region, st, scanID, p)
			return stop
		}},
		{"sesv2:ListContactLists", sesHost, func(p *phaseTotals) bool {
			return scanSESContactLists(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{"sesv2:ListCustomVerificationEmailTemplates", sesHost, func(p *phaseTotals) bool {
			return scanSESCustomVerificationTemplates(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{"sesv2:ListDedicatedIpPools", sesHost, func(p *phaseTotals) bool {
			return scanSESDedicatedIPPools(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(sesHost, "sesv2:ListMultiRegionEndpoints", func() (int, int, error) {
			return scanSESMultiRegionEndpoints(ctx, client, acct, region, st, scanID)
		}),
		{"sesv2:ListEmailTemplates", sesHost, func(p *phaseTotals) bool {
			return scanSESTemplates(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{"sesv2:ListTenants", sesHost, func(p *phaseTotals) bool {
			return scanSESTenants(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(sesHost, "sesv2:GetAccount", func() (int, int, error) {
			return scanSESVdmAttributes(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(sesHost, sesEventDestinationsOp, func() (int, int, error) {
			return scanSESEventDestinations(ctx, client, acct, region, st, scanID, childParentIDs(configSets))
		}),
		countedPhase(sesHost, "ses:ListReceiptFilters", func() (int, int, error) {
			return scanSESReceiptFilters(ctx, v1client, acct, region, st, scanID)
		}),
		countedPhase(sesHost, "ses:ListReceiptRuleSets", func() (int, int, error) {
			return scanSESReceiptRuleSets(ctx, v1client, acct, region, st, scanID)
		}),
		{"mailmanager:ListAddonInstances", sesMailManagerHost, func(p *phaseTotals) bool {
			return scanMMAddonInstances(ctx, mmclient, acct, region, st, scanID, mmTags, p)
		}},
		{"mailmanager:ListAddonSubscriptions", sesMailManagerHost, func(p *phaseTotals) bool {
			return scanMMAddonSubscriptions(ctx, mmclient, acct, region, st, scanID, mmTags, p)
		}},
		{"mailmanager:ListAddressLists", sesMailManagerHost, func(p *phaseTotals) bool {
			return scanMMAddressLists(ctx, mmclient, acct, region, st, scanID, mmTags, p)
		}},
		{mmListArchivesOp, sesMailManagerHost, func(p *phaseTotals) bool {
			return scanMMArchives(ctx, mmclient, acct, region, st, scanID, mmTags, p)
		}},
		{mmListIngressPointsOp, sesMailManagerHost, func(p *phaseTotals) bool {
			return scanMMIngressPoints(ctx, mmclient, acct, region, st, scanID, mmTags, p)
		}},
		{"mailmanager:ListRelays", sesMailManagerHost, func(p *phaseTotals) bool {
			return scanMMRelays(ctx, mmclient, acct, region, st, scanID, mmTags, p)
		}},
		{"mailmanager:ListRuleSets", sesMailManagerHost, func(p *phaseTotals) bool {
			return scanMMRuleSets(ctx, mmclient, acct, region, st, scanID, mmTags, p)
		}},
		{"mailmanager:ListTrafficPolicies", sesMailManagerHost, func(p *phaseTotals) bool {
			return scanMMTrafficPolicies(ctx, mmclient, acct, region, st, scanID, mmTags, p)
		}},
		countedPhase(sesHost, sesCertificateOp, func() (int, int, error) {
			return scanSESEmailIdentityCertificates(ctx, client, acct, region, st, scanID, identities)
		}),
	}
}

// sesEmailIdentityARN rebuilds the canonical email-identity ARN from an
// IdentityName (email or domain). SES v2 List/Get return only the bare name,
// but cross-resource edges (CloudFront authorizers, Pinpoint, IAM policy doc
// walkers) need the ARN.
func sesEmailIdentityARN(partition, region, accountID, name string) string {
	return sesARN(partition, region, accountID, "identity", name)
}

func sesConfigurationSetARN(partition, region, accountID, name string) string {
	return sesARN(partition, region, accountID, "configuration-set", name)
}

// sesv2TagsJSON is the TagsJSON of a sesv2 tag list: nil for none.
func sesv2TagsJSON(tags []sesv2types.Tag) *string {
	return kvTagsJSON(tags, func(t sesv2types.Tag) (*string, *string) { return t.Key, t.Value })
}

// sesBodyTags is what a row's Get body said about its tags, once read is
// set.
type sesBodyTags struct {
	read bool
	tags *string
}

// sesv2Tags reads a row's tags with sesv2's ListTagsForResource, sent the
// row's NativeID: every type read through it is stored under the ARN the
// Service Reference gives for a resource of the op (identity,
// configuration-set, contact-list, custom-verification-email-template,
// dedicated-ip-pool, template, tenant). The op answers with every tag at once
// (no page token). A row whose Get body was read (bodies, nil for none)
// stores that body's Tags, and the op is not called for it.
func sesv2Tags(client sesv2API, bodies map[*store.Resource]*sesBodyTags) rowTagReader {
	return rowTagReader{
		op:       sesListTagsOp,
		notFound: sesNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if b := bodies[r]; b != nil && b.read {
				return b.tags, nil
			}
			out, err := client.ListTagsForResource(ctx, &sesv2.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return sesv2TagsJSON(out.Tags), nil
		},
	}
}

// sesDetailed is detailedRows for a sesv2 listing of names whose Get body
// carries the resource's tags: a row whose Get answered stores the body's
// Tags, and one kept as its list element has them read with
// ListTagsForResource (sesv2Tags), so no row loses its tags to a failed Get.
type sesDetailed struct {
	detailedRows
	names  map[*store.Resource]string
	bodies map[*store.Resource]*sesBodyTags
}

func newSESDetailed(client sesv2API, getOp, what string) *sesDetailed {
	d := &sesDetailed{names: map[*store.Resource]string{}, bodies: map[*store.Resource]*sesBodyTags{}}
	d.detailedRows = detailedRows{getOp: getOp, what: what, notFound: sesNotFound, tags: sesv2Tags(client, d.bodies)}
	return d
}

// row records name, what the Get is sent, for r.
func (d *sesDetailed) row(r *store.Resource, name string) *store.Resource {
	d.names[r] = name
	// Filled by the concurrent Gets, each through its own pointer, so the
	// map is only read while they run.
	d.bodies[r] = &sesBodyTags{}
	return r
}

// before is the listing's beforeWrite: get reads the body and its tags of
// the resource named name.
func (d *sesDetailed) before(ctx context.Context, st *store.Store, acct *account, region string, get func(ctx context.Context, name string) (body any, tags *string, err error)) beforeWrite {
	return d.detailedRows.before(ctx, st, acct, region, func(ctx context.Context, r *store.Resource) (any, error) {
		body, tags, err := get(ctx, d.names[r])
		if err != nil {
			return nil, err
		}
		*d.bodies[r] = sesBodyTags{read: true, tags: tags}
		return body, nil
	})
}

// sesIdentity is a stored email identity, handed to the certificate phase.
type sesIdentity struct {
	childParent
	// emailAddress marks an EMAIL_ADDRESS identity, whose certificate
	// associations may omit FromAddress.
	emailAddress bool
}

const (
	sesListIdentitiesOp = "sesv2:ListEmailIdentities"
	sesListConfigSetsOp = "sesv2:ListConfigurationSets"
)

// sesIdentityPageCap is the PageSize cap (0-1000) ListEmailIdentities
// documents.
const sesIdentityPageCap int32 = 1000

// scanSESEmailIdentities stores the region's email identities with their
// tags and returns the stored ones, the parents of the certificate phase.
// Each row is the identity's GetEmailIdentity body (sesDetailed), its
// NativeID the identity ARN built from IdentityName; the list element, kept
// when the Get fails, carries the IdentityType the certificate phase needs.
func scanSESEmailIdentities(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (identities []sesIdentity, storeWriteFailed bool) {
	emailAddress := map[string]bool{}
	d := newSESDetailed(client, sesGetIdentityOp, "email identities")
	total, inserted, parents, err := storeParentListWith(ctx, st, acct, region, sesListIdentitiesOp, "email identities", "IdentityName",
		func(ctx context.Context, token *string) ([]sesv2types.IdentityInfo, *string, error) {
			out, err := client.ListEmailIdentities(ctx, &sesv2.ListEmailIdentitiesInput{NextToken: token, PageSize: sdkaws.Int32(sesIdentityPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.EmailIdentities, out.NextToken, nil
		},
		func(id sesv2types.IdentityInfo) (*store.Resource, string, string) {
			name := sv(id.IdentityName)
			if name == "" {
				return nil, "email identities have an empty IdentityName", "(an identity with no name)"
			}
			emailAddress[name] = id.IdentityType == sesv2types.IdentityTypeEmailAddress
			r := bacRowAt(acct, region, scanID, TypeSESEmailIdentity, sesEmailIdentityARN(acct.partition(), region, acct.ID, name), name, id)
			r.Status = nonEmptyPtr(string(id.VerificationStatus))
			return d.row(r, name), "", ""
		},
		func(id sesv2types.IdentityInfo) string { return sv(id.IdentityName) },
		d.before(ctx, st, acct, region, func(ctx context.Context, name string) (any, *string, error) {
			out, err := client.GetEmailIdentity(ctx, &sesv2.GetEmailIdentityInput{EmailIdentity: &name})
			if err != nil {
				return nil, nil, err
			}
			return out, sesv2TagsJSON(out.Tags), nil
		}))
	if d.addPhase(p, sesListIdentitiesOp, total, inserted, err) {
		return nil, true
	}
	for _, parent := range parents {
		identities = append(identities, sesIdentity{childParent: parent, emailAddress: emailAddress[parent.id]})
	}
	return identities, false
}

// scanSESConfigurationSets stores the region's configuration sets with their
// tags and returns the stored ones, the parents of the event-destination
// phase. Each row is the set's GetConfigurationSet body
// (flattenSESConfigurationSet, through sesDetailed). The listing names each
// set and nothing else, so a set whose Get fails is stored with that name
// under the body's own ConfigurationSetName key. The model documents no
// PageSize cap for the listing, so none is sent.
func scanSESConfigurationSets(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (sets []childParent, storeWriteFailed bool) {
	d := newSESDetailed(client, sesGetConfigSetOp, "configuration sets")
	total, inserted, sets, err := storeParentListWith(ctx, st, acct, region, sesListConfigSetsOp, "configuration sets", "ConfigurationSetName",
		func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListConfigurationSets(ctx, &sesv2.ListConfigurationSetsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfigurationSets, out.NextToken, nil
		},
		func(name string) (*store.Resource, string, string) {
			if name == "" {
				return nil, "configuration sets have an empty name", "(a configuration set with no name)"
			}
			r := bacRowAt(acct, region, scanID, TypeSESConfigurationSet, sesConfigurationSetARN(acct.partition(), region, acct.ID, name), name,
				map[string]string{"ConfigurationSetName": name})
			return d.row(r, name), "", ""
		},
		func(name string) string { return name },
		d.before(ctx, st, acct, region, func(ctx context.Context, name string) (any, *string, error) {
			out, err := client.GetConfigurationSet(ctx, &sesv2.GetConfigurationSetInput{ConfigurationSetName: &name})
			if err != nil {
				return nil, nil, err
			}
			return flattenSESConfigurationSet(out), sesv2TagsJSON(out.Tags), nil
		}))
	if d.addPhase(p, sesListConfigSetsOp, total, inserted, err) {
		return nil, true
	}
	return sets, false
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
