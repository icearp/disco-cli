package aws

import (
	"cmp"
	"context"

	"github.com/aws/aws-sdk-go-v2/service/rolesanywhere"
	rolesanywheretypes "github.com/aws/aws-sdk-go-v2/service/rolesanywhere/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRolesAnywhereCRL, Service: "roles-anywhere"})
	registerType(restype.Descriptor{Type: TypeRolesAnywhereProfile, Service: "roles-anywhere"})
	registerType(restype.Descriptor{Type: TypeRolesAnywhereTrustAnchor, Service: "roles-anywhere"})
	registerType(restype.Descriptor{Type: TypeRolesAnywhereSubject, Service: "roles-anywhere"})
	registerService(serviceEntry{
		name: "aws:roles-anywhere",
		fn:   scanRolesAnywhere,
	})
}

type rolesAnywhereAPI interface {
	ListCrls(context.Context, *rolesanywhere.ListCrlsInput, ...func(*rolesanywhere.Options)) (*rolesanywhere.ListCrlsOutput, error)
	ListProfiles(context.Context, *rolesanywhere.ListProfilesInput, ...func(*rolesanywhere.Options)) (*rolesanywhere.ListProfilesOutput, error)
	ListTrustAnchors(context.Context, *rolesanywhere.ListTrustAnchorsInput, ...func(*rolesanywhere.Options)) (*rolesanywhere.ListTrustAnchorsOutput, error)
	ListSubjects(context.Context, *rolesanywhere.ListSubjectsInput, ...func(*rolesanywhere.Options)) (*rolesanywhere.ListSubjectsOutput, error)
	ListTagsForResource(context.Context, *rolesanywhere.ListTagsForResourceInput, ...func(*rolesanywhere.Options)) (*rolesanywhere.ListTagsForResourceOutput, error)
}

const (
	raListCrlsOp         = "rolesanywhere:ListCrls"
	raListProfilesOp     = "rolesanywhere:ListProfiles"
	raListTrustAnchorsOp = "rolesanywhere:ListTrustAnchors"
	raListSubjectsOp     = "rolesanywhere:ListSubjects"
	raTagsOp             = "rolesanywhere:ListTagsForResource"
)

// scanRolesAnywhere discovers IAM Roles Anywhere CRLs, profiles, trust
// anchors and subjects, each with its tags (rolesAnywhereTags). Every listing
// runs as its own phase, one at a time and in that order: a phase's error
// does not stop the later ones; a failed write and ctx's end do, and so does
// a first phase that stores nothing and fails only with NXDOMAIN, since the
// region does not serve the service (runHostPhases). Each listing is written
// a page at a time, and the tag reads' denials are one warning for the whole
// scan. No listing is sent a PageSize: the model gives it no range and
// documents no cap.
func scanRolesAnywhere(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := rolesanywhere.NewFromConfig(acct.cfg, func(o *rolesanywhere.Options) { o.Region = region })

	tags := rolesAnywhereTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "rolesanywhere", 0, []hostPhase{
		{op: raListCrlsOp, run: func(p *phaseTotals) bool {
			return scanRACRLs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: raListProfilesOp, run: func(p *phaseTotals) bool {
			return scanRAProfiles(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: raListTrustAnchorsOp, run: func(p *phaseTotals) bool {
			return scanRATrustAnchors(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: raListSubjectsOp, run: func(p *phaseTotals) bool {
			return scanRASubjects(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// rolesAnywhereTags reads a row's tags with ListTagsForResource, sent the
// row's NativeID: the ARN its listed element carries. The Service
// Reference's TagResource entry lists all four types stored here (crl,
// profile, subject, trust-anchor), each with aws:ResourceTag as a condition
// key. ImportCrl, CreateProfile and CreateTrustAnchor take Tags; subjects
// have no create op, so their tags can come only from TagResource. The op
// returns every tag at once.
// ResourceNotFoundException, its modeled not-found, drops a resource deleted
// after it was listed.
func rolesAnywhereTags(client rolesAnywhereAPI) rowTagReader {
	return rowTagReader{
		op:       raTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &rolesanywhere.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t rolesanywheretypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// raElementNouns names each type's elements in the warning about those
// listed without an ARN (arnRow).
var raElementNouns = map[string]string{
	TypeRolesAnywhereCRL:         "CRLs",
	TypeRolesAnywhereProfile:     "profiles",
	TypeRolesAnywhereTrustAnchor: "trust anchors",
	TypeRolesAnywhereSubject:     "subjects",
}

// raRow is the row of a listed element of typ, stored under its ARN and
// named name. An element without an ARN is not stored, and its listing's
// warning names it by name, or by noIdentifier without one (arnRow).
func raRow(acct *account, region, scanID, typ string, arn, name *string, element any) (*store.Resource, string, string) {
	return arnRow(raElementNouns[typ], arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: name, Region: &region,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanRACRLs stores the CRLs, each page written as soon as its rows' tags
// are read. It reports whether a write failed, after which the scan must
// stop.
func scanRACRLs(ctx context.Context, client rolesAnywhereAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, raListCrlsOp,
		func(ctx context.Context, token *string) ([]rolesanywheretypes.CrlDetail, *string, error) {
			out, err := client.ListCrls(ctx, &rolesanywhere.ListCrlsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Crls, out.NextToken, nil
		},
		func(c rolesanywheretypes.CrlDetail) (*store.Resource, string, string) {
			return raRow(acct, region, scanID, TypeRolesAnywhereCRL, c.CrlArn, c.Name, c)
		}, tags, p)
}

// scanRAProfiles stores the profiles, each page written as soon as its rows'
// tags are read. It reports whether a write failed, after which the scan
// must stop.
func scanRAProfiles(ctx context.Context, client rolesAnywhereAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, raListProfilesOp,
		func(ctx context.Context, token *string) ([]rolesanywheretypes.ProfileDetail, *string, error) {
			out, err := client.ListProfiles(ctx, &rolesanywhere.ListProfilesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Profiles, out.NextToken, nil
		},
		func(pr rolesanywheretypes.ProfileDetail) (*store.Resource, string, string) {
			return raRow(acct, region, scanID, TypeRolesAnywhereProfile, pr.ProfileArn, pr.Name, pr)
		}, tags, p)
}

// scanRATrustAnchors stores the trust anchors, each page written as soon as
// its rows' tags are read. It reports whether a write failed, after which
// the scan must stop.
func scanRATrustAnchors(ctx context.Context, client rolesAnywhereAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, raListTrustAnchorsOp,
		func(ctx context.Context, token *string) ([]rolesanywheretypes.TrustAnchorDetail, *string, error) {
			out, err := client.ListTrustAnchors(ctx, &rolesanywhere.ListTrustAnchorsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TrustAnchors, out.NextToken, nil
		},
		func(a rolesanywheretypes.TrustAnchorDetail) (*store.Resource, string, string) {
			return raRow(acct, region, scanID, TypeRolesAnywhereTrustAnchor, a.TrustAnchorArn, a.Name, a)
		}, tags, p)
}

// scanRASubjects stores the subjects, each page written as soon as its rows'
// tags are read. A subject has no name, so its row is named by its
// SubjectId, or by its ARN when the element carries no id. It reports
// whether a write failed, after which the scan must stop.
func scanRASubjects(ctx context.Context, client rolesAnywhereAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, raListSubjectsOp,
		func(ctx context.Context, token *string) ([]rolesanywheretypes.SubjectSummary, *string, error) {
			out, err := client.ListSubjects(ctx, &rolesanywhere.ListSubjectsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Subjects, out.NextToken, nil
		},
		func(s rolesanywheretypes.SubjectSummary) (*store.Resource, string, string) {
			label := cmp.Or(sv(s.SubjectId), sv(s.SubjectArn))
			return raRow(acct, region, scanID, TypeRolesAnywhereSubject, s.SubjectArn, &label, s)
		}, tags, p)
}
