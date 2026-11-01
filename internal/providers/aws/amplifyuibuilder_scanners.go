package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/amplify"
	amplifytypes "github.com/aws/aws-sdk-go-v2/service/amplify/types"
	"github.com/aws/aws-sdk-go-v2/service/amplifyuibuilder"
	auibtypes "github.com/aws/aws-sdk-go-v2/service/amplifyuibuilder/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAmplifyUIBuilderComponent, Service: "amplify-ui-builder"})
	registerType(restype.Descriptor{Type: TypeAmplifyUIBuilderForm, Service: "amplify-ui-builder"})
	registerType(restype.Descriptor{Type: TypeAmplifyUIBuilderTheme, Service: "amplify-ui-builder"})
	registerService(serviceEntry{
		name: "aws:amplify-ui-builder",
		fn:   scanAmplifyUIBuilder,
	})
}

type amplifyUIBuilderAPI interface {
	ListComponents(context.Context, *amplifyuibuilder.ListComponentsInput, ...func(*amplifyuibuilder.Options)) (*amplifyuibuilder.ListComponentsOutput, error)
	ListForms(context.Context, *amplifyuibuilder.ListFormsInput, ...func(*amplifyuibuilder.Options)) (*amplifyuibuilder.ListFormsOutput, error)
	ListThemes(context.Context, *amplifyuibuilder.ListThemesInput, ...func(*amplifyuibuilder.Options)) (*amplifyuibuilder.ListThemesOutput, error)
	ListTagsForResource(context.Context, *amplifyuibuilder.ListTagsForResourceInput, ...func(*amplifyuibuilder.Options)) (*amplifyuibuilder.ListTagsForResourceOutput, error)
}

// amplifyAppEnvsAPI is the Amplify listings this scan makes to find the
// backend environments its listings key on.
type amplifyAppEnvsAPI interface {
	ListApps(context.Context, *amplify.ListAppsInput, ...func(*amplify.Options)) (*amplify.ListAppsOutput, error)
	ListBackendEnvironments(context.Context, *amplify.ListBackendEnvironmentsInput, ...func(*amplify.Options)) (*amplify.ListBackendEnvironmentsOutput, error)
}

const (
	auibTagsOp = "amplify-ui-builder:ListTagsForResource"
	// auibAppsOp is the app listing this scan makes for its per-environment
	// listings, apart from the aws:amplify scan's own.
	auibAppsOp = "amplify:ListApps(uib-fanout)"
	auibEnvsOp = "amplify:ListBackendEnvironments"
	// auibPageCap is the model's maxResults cap (1 to 100) of ListComponents,
	// ListForms and ListThemes.
	auibPageCap = 100
)

// The hosts of scanAmplifyUIBuilder's phases (runHostPhases).
const (
	auibHost = iota
	auibAmplifyHost
)

type amplifyAppEnvRef struct {
	AppID string
	Env   string
}

// scanAmplifyUIBuilder discovers the UI components, forms and themes of each
// (Amplify app, backend environment) of one region. The apps and their
// backend environments are listed here, because the aws:amplify scan runs
// concurrently with this one. Every listing runs as its own phase, one at a
// time: a phase's error does not stop the later ones; a failed write and
// ctx's end do; a region whose component listing finds no endpoint runs no
// form or theme listing (runHostPhases). Every type has its tags read per row
// (auibTags), and the tag denials of the whole scan are one warning.
func scanAmplifyUIBuilder(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	uib := amplifyuibuilder.NewFromConfig(acct.cfg, func(o *amplifyuibuilder.Options) { o.Region = region })
	amp := amplify.NewFromConfig(acct.cfg, func(o *amplify.Options) { o.Region = region })
	tags := auibTags(uib).sharingDenials()
	var envs []amplifyAppEnvRef
	total, inserted, err := runHostPhases(ctx, st, acct, region, "amplify-ui-builder", auibHost, []hostPhase{
		{op: auibAppsOp, host: auibAmplifyHost, run: func(p *phaseTotals) bool {
			envs = loadAmplifyAppEnvs(ctx, amp, acct, region, st, p)
			return false
		}},
		{op: "amplify-ui-builder:ListComponents", host: auibHost, run: func(p *phaseTotals) bool {
			return scanAUIBComponents(ctx, uib, envs, acct, region, st, scanID, tags, p)
		}},
		{op: "amplify-ui-builder:ListForms", host: auibHost, run: func(p *phaseTotals) bool {
			return scanAUIBForms(ctx, uib, envs, acct, region, st, scanID, tags, p)
		}},
		{op: "amplify-ui-builder:ListThemes", host: auibHost, run: func(p *phaseTotals) bool {
			return scanAUIBThemes(ctx, uib, envs, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// auibTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, its ARN; the op answers with every tag at once (no page token).
// The Service Reference lists ComponentResource, FormResource and
// ThemeResource under ListTagsForResource, each ARN in the shape auibARN
// builds. ResourceNotFoundException, the op's modeled not-found, drops a
// resource deleted after it was listed; the model documents it as "does not
// exist, or access was denied", and which of the two a listed row would get
// is unverified live. UnauthorizedException, the op's modeled denial, which
// isAccessDenied does not match, is warned about as a denial. Every row read
// here is this account's own: its ARN is built in this account.
func auibTags(client amplifyUIBuilderAPI) rowTagReader {
	return rowTagReader{
		op:       auibTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		denied:   []string{"UnauthorizedException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &amplifyuibuilder.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// auibARN is the ARN of the UI Builder entity id of app appID's backend
// environment env, in the Service Reference's shape
// app/{AppId}/environment/{EnvironmentName}/{kind}/{Id}, kind being
// components, forms or themes.
func auibARN(partition, region, accountID, appID, env, kind, id string) string {
	return arnFor(partition, "amplifyuibuilder", region, accountID, "app/"+appID+"/environment/"+env+"/"+kind+"/"+id)
}

// loadAmplifyAppEnvs lists the region's Amplify apps, then each app's backend
// environments, the apps concurrently, and returns the environments read,
// those of the pages read before a failure included. The listings' errors go
// to p, each under its op; one app's failure costs the others none of their
// environments. A denial of either listing is warned about once rather than
// returned, and an app or environment listed without its id is warned about,
// as its UI Builder resources cannot be listed. An app or environment listed
// twice (a repeated NextToken hands its page over again) is kept once, so
// that its resources are not listed, tag-read and counted twice. An NXDOMAIN
// on the first ListApps page is passed on as is: runHostPhases reads it as
// the Amplify host being absent from the region and drops it, that host not
// being the scan's primary one. Once a page was served, even an empty one,
// the endpoint resolved, so a later NXDOMAIN is a failure of this scan
// (lateNXDOMAIN).
//
// ListBackendEnvironments is documented as available only to Amplify Gen 1
// apps, and BadRequestException is the only refusal its model lists that
// fits an app it does not serve. A Gen 2 app has no backend environments and
// so no UI Builder (Studio) resources, so that code on an app's first page
// reads as an app with no environments. On a later page the op already served
// the app, so it is returned. Which code the service sends for a Gen 2 app
// is unverified live. The op models no not-found code, so an app deleted
// after it was listed fails its listing with whatever code the service sends,
// and that error is returned.
func loadAmplifyAppEnvs(ctx context.Context, client amplifyAppEnvsAPI, acct *account, region string, st *store.Store, p *phaseTotals) []amplifyAppEnvRef {
	denied := listDenial{isDenied: isAmplifyDenied, unread: "apps on later pages and their UI Builder resources"}
	served := false
	apps, err := readTokenPages(ctx, deniedPages(&denied, func(ctx context.Context, token *string) ([]amplifytypes.App, *string, error) {
		out, err := client.ListApps(ctx, &amplify.ListAppsInput{NextToken: token, MaxResults: amplifyAppsPageMax})
		if err != nil {
			return nil, nil, err
		}
		served = true
		return out.Apps, out.NextToken, nil
	}))
	if served {
		err = lateNXDOMAIN(auibAppsOp, err)
	}
	denied.warn(st, auibAppsOp, acct, region)
	p.addPhase(auibAppsOp, 0, 0, err)

	var (
		appIDs  []string
		seenApp = map[string]bool{}
		noID    skipTally
	)
	for _, a := range apps {
		id := sv(a.AppId)
		if id == "" {
			noID.skipped(cmp.Or(sv(a.Name), sv(a.AppArn), noIdentifier), nil)
			continue
		}
		if !seenApp[id] {
			seenApp[id] = true
			appIDs = append(appIDs, id)
		}
	}
	warnAUIBUnread(st, auibAppsOp, acct, region, &noID, "apps", "AppId")

	notGen1 := func(err error) bool { return isAPIErrorCode(err, "BadRequestException") }
	reads, stopped := readEachKey(ctx, appIDs, func(ctx context.Context, appID string) parentItems[amplifytypes.BackendEnvironment] {
		items, err := readTokenPages(ctx, firstPageGapPages(notGen1, func(ctx context.Context, token *string) ([]amplifytypes.BackendEnvironment, *string, error) {
			out, err := client.ListBackendEnvironments(ctx, &amplify.ListBackendEnvironmentsInput{AppId: &appID, NextToken: token, MaxResults: amplifyPageMax})
			if err != nil {
				return nil, nil, lateNXDOMAIN(auibEnvsOp, err)
			}
			return out.BackendEnvironments, out.NextToken, nil
		}))
		return parentItems[amplifytypes.BackendEnvironment]{items, err}
	})
	var (
		envs    []amplifyAppEnvRef
		seenEnv = map[amplifyAppEnvRef]bool{}
		noName  skipTally
	)
	out := parentOutcomes{op: auibEnvsOp, isDenied: isAmplifyDenied}
	for i, read := range reads {
		for _, be := range read.items {
			ref := amplifyAppEnvRef{AppID: appIDs[i], Env: sv(be.EnvironmentName)}
			if ref.Env == "" {
				noName.skipped(cmp.Or(sv(be.BackendEnvironmentArn), noIdentifier), nil)
				continue
			}
			if !seenEnv[ref] {
				seenEnv[ref] = true
				envs = append(envs, ref)
			}
		}
		out.parentErr(appIDs[i], len(read.items) > 0, read.err)
	}
	out.stopped(stopped)
	out.warn(st, acct, region)
	warnAUIBUnread(st, auibEnvsOp, acct, region, &noName, "backend environments", "EnvironmentName")
	p.addPhase(auibEnvsOp, 0, 0, errors.Join(out.errs...))
	return envs
}

// warnAUIBUnread warns once for the what listed with an empty field, whose
// UI Builder resources were therefore not listed.
func warnAUIBUnread(st *store.Store, op string, acct *account, region string, t *skipTally, what, field string) {
	if t.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d %s listed with an empty %s, so their UI Builder resources were not read; first %s", t.count, what, field, t.first),
	})
}

// scanAUIBEntities stores the entities op lists for each backend environment
// with their tags, the environments read concurrently and each page written
// as soon as its tags are read (storeEachParentPagesTags). One environment's
// failure does not stop the others, and the pages written before it are
// kept. The listings model no not-found code, so an environment deleted after
// it was listed fails its listing with whatever code the service sends, and
// that error is returned. Each row is stored under auibARN built from the
// environment it was listed in, kind (components, forms or themes) naming
// both the ARN's segment and, in a warning, the entities; an entity listed
// without an Id is not stored. It reports whether a write failed.
func scanAUIBEntities[Item any](
	ctx context.Context, st *store.Store, acct *account, region, scanID, op, typ, kind string,
	envs []amplifyAppEnvRef, tags rowTagReader, p *phaseTotals,
	list func(ctx context.Context, e amplifyAppEnvRef, token *string) ([]Item, *string, error),
	idName func(Item) (id, name *string),
) (storeWriteFailed bool) {
	parents := make([]childParent, len(envs))
	byKey := make(map[string]amplifyAppEnvRef, len(envs))
	for i, e := range envs {
		key := e.AppID + "/" + e.Env
		parents[i] = childParent{id: key}
		byKey[key] = e
	}
	return storeEachParentPagesTags(ctx, st, acct, region, op, nil, parents,
		func(ctx context.Context, key string, token *string) ([]Item, *string, error) {
			return list(ctx, byKey[key], token)
		},
		func(key string, it Item) (*store.Resource, string, string) {
			id, name := idName(it)
			if sv(id) == "" {
				return nil, kind + " have an empty Id", cmp.Or(sv(name), noIdentifier) + " in " + key
			}
			e := byKey[key]
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: typ, NativeID: auibARN(acct.partition(), region, acct.ID, e.AppID, e.Env, kind, sv(id)),
				Name: name, Region: &region, AttributesJSON: mustJSON(it), DiscoveredBy: scanID,
			}, "", ""
		}, tags, p)
}

func scanAUIBComponents(ctx context.Context, client amplifyUIBuilderAPI, envs []amplifyAppEnvRef, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return scanAUIBEntities(ctx, st, acct, region, scanID, "amplify-ui-builder:ListComponents", TypeAmplifyUIBuilderComponent, "components", envs, tags, p,
		func(ctx context.Context, e amplifyAppEnvRef, token *string) ([]auibtypes.ComponentSummary, *string, error) {
			out, err := client.ListComponents(ctx, &amplifyuibuilder.ListComponentsInput{AppId: &e.AppID, EnvironmentName: &e.Env, MaxResults: sdkaws.Int32(auibPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Entities, out.NextToken, nil
		},
		func(c auibtypes.ComponentSummary) (*string, *string) { return c.Id, c.Name })
}

func scanAUIBForms(ctx context.Context, client amplifyUIBuilderAPI, envs []amplifyAppEnvRef, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return scanAUIBEntities(ctx, st, acct, region, scanID, "amplify-ui-builder:ListForms", TypeAmplifyUIBuilderForm, "forms", envs, tags, p,
		func(ctx context.Context, e amplifyAppEnvRef, token *string) ([]auibtypes.FormSummary, *string, error) {
			out, err := client.ListForms(ctx, &amplifyuibuilder.ListFormsInput{AppId: &e.AppID, EnvironmentName: &e.Env, MaxResults: sdkaws.Int32(auibPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Entities, out.NextToken, nil
		},
		func(f auibtypes.FormSummary) (*string, *string) { return f.Id, f.Name })
}

func scanAUIBThemes(ctx context.Context, client amplifyUIBuilderAPI, envs []amplifyAppEnvRef, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return scanAUIBEntities(ctx, st, acct, region, scanID, "amplify-ui-builder:ListThemes", TypeAmplifyUIBuilderTheme, "themes", envs, tags, p,
		func(ctx context.Context, e amplifyAppEnvRef, token *string) ([]auibtypes.ThemeSummary, *string, error) {
			out, err := client.ListThemes(ctx, &amplifyuibuilder.ListThemesInput{AppId: &e.AppID, EnvironmentName: &e.Env, MaxResults: sdkaws.Int32(auibPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Entities, out.NextToken, nil
		},
		func(t auibtypes.ThemeSummary) (*string, *string) { return t.Id, t.Name })
}
