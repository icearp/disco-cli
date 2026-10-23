package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/amplify"
	amplifytypes "github.com/aws/aws-sdk-go-v2/service/amplify/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// Basic-auth credentials are base64 "user:password", secret by definition.
// Environment variables may hold secrets or only settings, so each value is
// classified: references, booleans and ARNs are kept, anything else redacted.
// A thumbnail URL is likely a presigned S3 URL (not verified live): its
// signature would be a bearer link, and one re-minted per read would
// version-split an unchanged row. Redacting it to a constant covers both.
// BuildSpec (app, branch, auto-branch config) is marked @sensitive by the
// model but is stored on purpose: it is build configuration, not a
// credential, and redaction is for credentials. A secret written inline in
// a build spec is therefore stored unredacted.
var (
	amplifyBasicAuthRedact = redact.Rule{Path: "BasicAuthCredentials", Mode: redact.RedactScalar}
	amplifyEnvRedact       = redact.Rule{Path: "EnvironmentVariables.*", Mode: redact.RedactUnlessSafe}
)

func init() {
	registerType(restype.Descriptor{Type: TypeAmplifyApp, Service: "amplify", Redact: []redact.Rule{
		amplifyBasicAuthRedact, amplifyEnvRedact,
		{Path: "AutoBranchCreationConfig.BasicAuthCredentials", Mode: redact.RedactScalar},
		{Path: "AutoBranchCreationConfig.EnvironmentVariables.*", Mode: redact.RedactUnlessSafe},
		{Path: "ProductionBranch.ThumbnailUrl", Mode: redact.RedactScalar},
	}})
	registerType(restype.Descriptor{Type: TypeAmplifyBranch, Service: "amplify", Redact: []redact.Rule{
		amplifyBasicAuthRedact, amplifyEnvRedact,
		{Path: "ThumbnailUrl", Mode: redact.RedactScalar},
	}})
	registerType(restype.Descriptor{Type: TypeAmplifyDomain, Service: "amplify"})
	registerType(restype.Descriptor{Type: TypeAmplifyWebhooks, Service: "amplify", Redact: []redact.Rule{{Path: "WebhookUrl", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "aws:amplify",
		fn:   scanAmplify,
	})
}

type amplifyAPI interface {
	ListApps(context.Context, *amplify.ListAppsInput, ...func(*amplify.Options)) (*amplify.ListAppsOutput, error)
	ListBranches(context.Context, *amplify.ListBranchesInput, ...func(*amplify.Options)) (*amplify.ListBranchesOutput, error)
	ListDomainAssociations(context.Context, *amplify.ListDomainAssociationsInput, ...func(*amplify.Options)) (*amplify.ListDomainAssociationsOutput, error)
	ListWebhooks(context.Context, *amplify.ListWebhooksInput, ...func(*amplify.Options)) (*amplify.ListWebhooksOutput, error)
}

// The ops of scanAmplifyAll's phases, shared by each phase and the entry that
// warns under it if its error is demoted.
const (
	amplifyAppsOp     = "amplify:ListApps"
	amplifyBranchesOp = "amplify:ListBranches"
	amplifyDomainsOp  = "amplify:ListDomainAssociations"
	amplifyWebhooksOp = "amplify:ListWebhooks"
)

// The MaxResults caps the Smithy model sets: 100 on ListApps, 50 on the
// per-app ops.
const (
	amplifyAppsPageMax = 100
	amplifyPageMax     = 50
)

func scanAmplify(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := amplify.NewFromConfig(acct.cfg, func(o *amplify.Options) { o.Region = region })
	return scanAmplifyAll(ctx, client, acct, region, st, scanID)
}

// scanAmplifyAll lists the apps, then each stored app's branches, domain
// associations and webhooks, each linked under its app.
//
// Phases run in order. A phase's error does not stop the later phases; only a
// failed store write does, since every later write would fail too. A failed
// app listing leaves the child phases the apps stored before it.
func scanAmplifyAll(ctx context.Context, client amplifyAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var apps []childParent
	phases := []struct {
		op  string
		run func() (int, int, error)
	}{
		{amplifyAppsOp, func() (t, i int, err error) {
			t, i, apps, err = scanAmplifyApps(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{amplifyBranchesOp, func() (int, int, error) {
			return scanAmplifyBranches(ctx, client, acct, region, st, scanID, apps)
		}},
		{amplifyDomainsOp, func() (int, int, error) {
			return scanAmplifyDomains(ctx, client, acct, region, st, scanID, apps)
		}},
		{amplifyWebhooksOp, func() (int, int, error) {
			return scanAmplifyWebhooks(ctx, client, acct, region, st, scanID, apps)
		}},
	}
	var p phaseTotals
	for _, ph := range phases {
		t, i, err := ph.run()
		if p.addPhase(ph.op, t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

// isAmplifyDenied reports whether err denies the call: UnauthorizedException,
// which every op this file calls models as "an operation failed due to a lack
// of access" but accessDeniedCodes leaves out because other services give it
// other meanings, or a code isAccessDenied matches. Both count as one denial
// per op, so the op warns once.
func isAmplifyDenied(err error) bool {
	return isAPIErrorCode(err, "UnauthorizedException") || isAccessDenied(err)
}

// amplifyRow builds the row of an Amplify resource.
func amplifyRow(acct *account, region, scanID, rtype, nativeID, name string, attrs any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: nativeID, Name: &name, Region: &region,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	}
}

// scanAmplifyApps also returns the stored apps that carry an AppId, for the
// child phases.
func scanAmplifyApps(ctx context.Context, client amplifyAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	return storeParentListDeniedBy(ctx, st, acct, region, amplifyAppsOp, "apps", "AppId", isAmplifyDenied,
		func(ctx context.Context, token *string) ([]amplifytypes.App, *string, error) {
			out, err := client.ListApps(ctx, &amplify.ListAppsInput{NextToken: token, MaxResults: amplifyAppsPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.Apps, out.NextToken, nil
		},
		func(a amplifytypes.App) (*store.Resource, string, string) {
			if sv(a.AppArn) == "" {
				return nil, "apps have an empty AppArn", sv(a.AppId)
			}
			r := amplifyRow(acct, region, scanID, TypeAmplifyApp, *a.AppArn, sv(a.Name), a)
			r.CreatedAt = tp(a.CreateTime)
			r.TagsJSON = mapTagsJSON(a.Tags)
			return r, "", ""
		},
		func(a amplifytypes.App) string { return sv(a.AppId) })
}

// storeAmplifyChildren reads op's items for each app concurrently through
// storeTokenEachParentSkip, then links every stored child under the app it
// was read from. None of the per-app ops models a not-found code, so an app
// deleted since it was listed fails the phase. A denied app (isAmplifyDenied)
// keeps the rows read before the denial and is warned about once, naming the
// first denied app in app order, after the writes succeeded.
func storeAmplifyChildren[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, apps []childParent,
	page func(ctx context.Context, appID string, token *string) ([]Item, *string, error),
	row func(app childParent, it Item) (*store.Resource, string, string),
) (int, int, error) {
	byID := make(map[string]childParent, len(apps))
	ids := make([]string, len(apps))
	for i, a := range apps {
		byID[a.id], ids[i] = a, a.id
	}
	var (
		pairs  [][2]string
		denied skipTally
	)
	total, inserted, err := storeTokenEachParentSkip(ctx, st, acct, region, op, nil, ids, page,
		func(id string, it Item) (*store.Resource, string, string) {
			app := byID[id]
			r, why, key := row(app, it)
			if r != nil {
				pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, r.NativeID), store.ResourceID("aws", acct.ID, app.arn)})
			}
			return r, why, key
		},
		func(app string, err error) bool {
			if !isAmplifyDenied(err) {
				return false
			}
			denied.skipped(app, err)
			return true
		})
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	if herr := st.RecordHierarchyBatch(pairs); herr != nil {
		return total, inserted, errors.Join(err, fmt.Errorf("closure %s: %w", op, herr))
	}
	denied.warnDeniedIn(st, op, acct, region, "app(s)")
	return total, inserted, err
}

func scanAmplifyBranches(ctx context.Context, client amplifyAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	return storeAmplifyChildren(ctx, st, acct, region, amplifyBranchesOp, apps,
		func(ctx context.Context, appID string, token *string) ([]amplifytypes.Branch, *string, error) {
			out, err := client.ListBranches(ctx, &amplify.ListBranchesInput{AppId: &appID, NextToken: token, MaxResults: amplifyPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.Branches, out.NextToken, nil
		},
		func(app childParent, b amplifytypes.Branch) (*store.Resource, string, string) {
			if sv(b.BranchArn) == "" {
				return nil, "branches have an empty BranchArn", app.arn + "/branches/" + sv(b.BranchName)
			}
			r := amplifyRow(acct, region, scanID, TypeAmplifyBranch, *b.BranchArn, sv(b.BranchName), b)
			r.CreatedAt = tp(b.CreateTime)
			r.TagsJSON = mapTagsJSON(b.Tags)
			return r, "", ""
		})
}

func scanAmplifyDomains(ctx context.Context, client amplifyAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	return storeAmplifyChildren(ctx, st, acct, region, amplifyDomainsOp, apps,
		func(ctx context.Context, appID string, token *string) ([]amplifytypes.DomainAssociation, *string, error) {
			out, err := client.ListDomainAssociations(ctx, &amplify.ListDomainAssociationsInput{AppId: &appID, NextToken: token, MaxResults: amplifyPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.DomainAssociations, out.NextToken, nil
		},
		func(app childParent, d amplifytypes.DomainAssociation) (*store.Resource, string, string) {
			if sv(d.DomainAssociationArn) == "" {
				return nil, "domain associations have an empty DomainAssociationArn", app.arn + "/domains/" + sv(d.DomainName)
			}
			return amplifyRow(acct, region, scanID, TypeAmplifyDomain, *d.DomainAssociationArn, sv(d.DomainName), d), "", ""
		})
}

// scanAmplifyWebhooks pages ListWebhooks by hand: the SDK has no paginator
// for it.
func scanAmplifyWebhooks(ctx context.Context, client amplifyAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	return storeAmplifyChildren(ctx, st, acct, region, amplifyWebhooksOp, apps,
		func(ctx context.Context, appID string, token *string) ([]amplifytypes.Webhook, *string, error) {
			out, err := client.ListWebhooks(ctx, &amplify.ListWebhooksInput{AppId: &appID, NextToken: token, MaxResults: amplifyPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.Webhooks, out.NextToken, nil
		},
		func(app childParent, w amplifytypes.Webhook) (*store.Resource, string, string) {
			if sv(w.WebhookArn) == "" {
				return nil, "webhooks have an empty WebhookArn", app.arn + "/webhooks/" + sv(w.WebhookId)
			}
			r := amplifyRow(acct, region, scanID, TypeAmplifyWebhooks, *w.WebhookArn, sv(w.WebhookId), w)
			r.CreatedAt = tp(w.CreateTime)
			return r, "", ""
		})
}
