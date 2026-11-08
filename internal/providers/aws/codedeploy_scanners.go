package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/codedeploy"
	cdtypes "github.com/aws/aws-sdk-go-v2/service/codedeploy/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeDeployApplication, Service: "codedeploy"})
	registerType(restype.Descriptor{Type: TypeCodeDeployDeploymentGroup, Service: "codedeploy"})
	registerType(restype.Descriptor{Type: TypeCodeDeployDeploymentConfig, Service: "codedeploy", Untaggable: true})
	registerService(serviceEntry{
		name: "aws:codedeploy",
		fn:   scanCodeDeploy,
	})
}

type codeDeployAPI interface {
	ListApplications(context.Context, *codedeploy.ListApplicationsInput, ...func(*codedeploy.Options)) (*codedeploy.ListApplicationsOutput, error)
	BatchGetApplications(context.Context, *codedeploy.BatchGetApplicationsInput, ...func(*codedeploy.Options)) (*codedeploy.BatchGetApplicationsOutput, error)
	ListDeploymentGroups(context.Context, *codedeploy.ListDeploymentGroupsInput, ...func(*codedeploy.Options)) (*codedeploy.ListDeploymentGroupsOutput, error)
	BatchGetDeploymentGroups(context.Context, *codedeploy.BatchGetDeploymentGroupsInput, ...func(*codedeploy.Options)) (*codedeploy.BatchGetDeploymentGroupsOutput, error)
	ListDeploymentConfigs(context.Context, *codedeploy.ListDeploymentConfigsInput, ...func(*codedeploy.Options)) (*codedeploy.ListDeploymentConfigsOutput, error)
	GetDeploymentConfig(context.Context, *codedeploy.GetDeploymentConfigInput, ...func(*codedeploy.Options)) (*codedeploy.GetDeploymentConfigOutput, error)
	ListTagsForResource(context.Context, *codedeploy.ListTagsForResourceInput, ...func(*codedeploy.Options)) (*codedeploy.ListTagsForResourceOutput, error)
}

const (
	cdListAppsOp    = "codedeploy:ListApplications"
	cdGetAppsOp     = "codedeploy:BatchGetApplications"
	cdListGroupsOp  = "codedeploy:ListDeploymentGroups"
	cdGetGroupsOp   = "codedeploy:BatchGetDeploymentGroups"
	cdListConfigsOp = "codedeploy:ListDeploymentConfigs"
	cdTagsOp        = "codedeploy:ListTagsForResource"
	// cdBatchCap is the most names one BatchGetApplications or
	// BatchGetDeploymentGroups call takes: the limit the model's
	// BatchLimitExceededException names.
	cdBatchCap = 100
)

// cdAppGone is the code ListDeploymentGroups and BatchGetDeploymentGroups
// model for an application that does not exist: one deleted after it was
// listed.
var cdAppGone = []string{"ApplicationDoesNotExistException"}

// scanCodeDeploy discovers applications, deployment groups (per app), and
// deployment configs (built-in CodeDeployDefault.* flagged ManagedByProvider).
// Each runs as its own phase, one at a time: a phase's error does not stop
// the later ones; a failed write and ctx's end do (runHostPhases).
// Applications and deployment groups have their tags read per row (cdTags),
// whose denials are one warning for the whole scan; deployment configs take
// no tags.
func scanCodeDeploy(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := codedeploy.NewFromConfig(acct.cfg, func(o *codedeploy.Options) { o.Region = region })
	tags := cdTags(client).sharingDenials()
	var apps []string
	total, err := runHostPhases(ctx, st, acct, region, "codedeploy", 0, []hostPhase{
		{op: cdListAppsOp, run: func(p *phaseTotals) (failed bool) {
			apps, failed = scanCDApplications(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: cdListGroupsOp, run: func(p *phaseTotals) bool {
			return scanCDDeploymentGroups(ctx, client, apps, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, cdListConfigsOp, func() (int, error) {
			return scanCDDeploymentConfigs(ctx, client, acct, region, st, scanID)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// cdTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN built from the listed names in the Service Reference's
// application and deploymentgroup ARN formats. The Service Reference lists
// exactly those two types under ListTagsForResource and TagResource, and
// CreateApplication and CreateDeploymentGroup take Tags; neither listing
// returns an ARN, and neither BatchGet body carries an ARN or tags. The op
// pages its tags with NextToken and takes no page size; every page is read,
// and a repeated token fails the read. It models no not-found error, so a
// resource deleted after it was listed fails its tag read and keeps its row
// untagged, the error returned (which code the service answers then is
// unverified).
func cdTags(client codeDeployAPI) rowTagReader {
	return rowTagReader{
		op: cdTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]cdtypes.Tag, *string, error) {
				out, err := client.ListTagsForResource(ctx, &codedeploy.ListTagsForResourceInput{ResourceArn: &arn, NextToken: token})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t cdtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// cdBatchFailure is one failed BatchGet call: the names it was sent and its
// error.
type cdBatchFailure struct {
	names []string
	err   error
}

// cdBatchMisses is what a BatchGet read of one listing did not describe: the
// names sent that no returned body names, and the failed calls.
type cdBatchMisses struct {
	missing []string
	failed  []cdBatchFailure
}

// cdGoneErr is the first failed call's error that says the parent of the
// names does not exist (cdAppGone), nil when none does.
func (m cdBatchMisses) cdGoneErr() error {
	for _, f := range m.failed {
		if isAPIErrorCode(f.err, cdAppGone...) {
			return f.err
		}
	}
	return nil
}

// cdBatch is what cdBatchGet read: the bodies returned and the misses.
type cdBatch[Info any] struct {
	infos []Info
	cdBatchMisses
}

// cdBatchGet describes names with get, cdBatchCap names a call, one call at
// a time; a failed call costs only its own names. name is a returned body's
// name: a name sent that no body names is missing. BatchGetDeploymentGroups
// models no error code for one group it cannot describe, only a free-text
// ErrorMessage, so a missing name is warned about rather than failed.
func cdBatchGet[Info any](ctx context.Context, names []string, get func(ctx context.Context, chunk []string) ([]Info, error), name func(Info) string) cdBatch[Info] {
	var b cdBatch[Info]
	for chunk := range slices.Chunk(names, cdBatchCap) {
		infos, err := get(ctx, chunk)
		if err != nil {
			b.failed = append(b.failed, cdBatchFailure{chunk, err})
			continue
		}
		got := make(map[string]bool, len(infos))
		for _, in := range infos {
			got[name(in)] = true
		}
		for _, n := range chunk {
			if !got[n] {
				b.missing = append(b.missing, n)
			}
		}
		b.infos = append(b.infos, infos...)
	}
	return b
}

// cdBatchOutcomes tallies, for one phase, what op's BatchGet reads did not
// describe, so that each kind is warned about or returned once. notFound
// holds the codes op models for a parent deleted after it was listed (nil
// for none): a call failing so had nothing left to describe, and its names
// are not tallied here; the caller reports the parent's deletion
// (cdGoneErr). what names the items in a warning, and missingWhy says why
// the missing ones were not stored.
type cdBatchOutcomes struct {
	op, what, missingWhy        string
	notFound                    []string
	missing, denied, transients skipTally
	hard                        []error
}

// tallyCDBatchMisses tallies m, each name keyed in a warning by prefix plus the name.
func (o *cdBatchOutcomes) tallyCDBatchMisses(prefix string, m cdBatchMisses) {
	for _, n := range m.missing {
		o.missing.skipped(prefix+n, nil)
	}
	for _, f := range m.failed {
		first := prefix + f.names[0]
		// The names were listed from op's endpoint, so an NXDOMAIN now is a
		// failure of this scan, not a region gap.
		switch err := lateNXDOMAIN(o.op, f.err); {
		case isAPIErrorCode(err, o.notFound...):
		case isAccessDenied(err):
			for _, n := range f.names {
				o.denied.skipped(prefix+n, err)
			}
		case isTransientNetworkError(err):
			for _, n := range f.names {
				o.transients.skipped(prefix+n, err)
			}
		default:
			o.hard = append(o.hard, fmt.Errorf("%s %d name(s), first %s: %w", o.op, len(f.names), first, err))
		}
	}
}

// cdBatchErrs returns the failed calls apart from those still transient after the
// SDK's retries, so that phaseErrs can demote the transient one beside a
// hard error.
func (o *cdBatchOutcomes) cdBatchErrs() (hard, transient error) {
	if t := &o.transients; t.count > 0 {
		transient = fmt.Errorf("%s: %d %s not read, so not stored, first %s: %w", o.op, t.count, o.what, t.first, t.err)
	}
	return errors.Join(o.hard...), transient
}

// warnCDBatchMisses warns once about the names no body came back for and once about the
// denied ones. Call it only once the rows are stored.
func (o *cdBatchOutcomes) warnCDBatchMisses(st *store.Store, acct *account, region string) {
	warnNotStored(st, o.op, acct, region, &o.missing, o.missingWhy)
	if o.denied.count > 0 {
		_ = skipIfAccessDenied(st, o.op, acct.ID, region, fmt.Errorf("%d %s not stored, first %s: %w", o.denied.count, o.what, o.denied.first, o.denied.err))
	}
}

// cdStoreRows stores rows, built from op's listing and gets' BatchGet
// bodies, with their tags read through tags. The listing ended with listErr,
// returned wrapped with op unless it is a denial (storeReadRows); listed and
// listedTransient are the listing's errors already labelled with op, the
// latter only those still transient after the SDK's retries, returned as
// they are. warn and gets' warnings run only after a successful write. gets'
// errors go to p under gets.op, and listedTransient under op, each transient
// one apart so that phaseErrs can demote it beside a hard error; after a
// failed write they are all joined into the write's error instead, so that
// no warning follows it. It reports whether the write failed, after which
// the scan must stop.
func cdStoreRows(ctx context.Context, st *store.Store, acct *account, region, op string, rows []*store.Resource, listErr, listed, listedTransient error, warn func(), gets *cdBatchOutcomes, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	var tagErrs rowTagErrs
	rows, tagWarn := tags.hook(ctx, st, acct, region, &tagErrs)(rows)
	total, err := storeReadRows(st, acct, region, op, rows, listErr, func() {
		warn()
		gets.warnCDBatchMisses(st, acct, region)
		tagWarn()
	}, listed)
	hard, transient := gets.cdBatchErrs()
	if errors.Is(err, store.ErrStoreWrite) {
		return tagErrs.addPhase(p, tags.op, op, total, errors.Join(err, hard, transient, listedTransient))
	}
	p.errs.add(gets.op, hard)
	p.errs.add(gets.op, transient)
	p.errs.add(op, listedTransient)
	return tagErrs.addPhase(p, tags.op, op, total, err)
}

// scanCDApplications stores the applications with their tags and returns
// every name listed, the parents of the deployment group phase.
// ListApplications returns names only and BatchGetApplications describes
// them (cdBatchGet); an application is stored only with its body, since the
// listing has nothing else to store. A per-region quota bounds the
// applications, so the listing is held whole. The names whose
// BatchGetApplications read failed are returned too: their groups are listed
// by name alone. Once a ListApplications page answered, an NXDOMAIN on a
// later one is a failure of this scan, not a region gap (lateNXDOMAIN), so
// the later phases still run. It reports whether the write failed, after
// which the scan must stop.
//
// BatchGetApplications models ApplicationDoesNotExistException without
// saying whether one deleted name fails the whole call; it is returned as an
// error, so an application deleted after the listing costs the names sent
// with it their rows until a rescan (unverified live).
func scanCDApplications(ctx context.Context, client codeDeployAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]string, bool) {
	served := false
	names, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]string, *string, error) {
		out, err := client.ListApplications(ctx, &codedeploy.ListApplicationsInput{NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		served = true
		return out.Applications, out.NextToken, nil
	})
	if served {
		listErr = lateNXDOMAIN(cdListAppsOp, listErr)
	}
	got := cdBatchGet(ctx, names, func(ctx context.Context, chunk []string) ([]cdtypes.ApplicationInfo, error) {
		out, err := client.BatchGetApplications(ctx, &codedeploy.BatchGetApplicationsInput{ApplicationNames: chunk})
		if err != nil {
			return nil, err
		}
		return out.ApplicationsInfo, nil
	}, func(a cdtypes.ApplicationInfo) string { return sv(a.ApplicationName) })
	gets := cdBatchOutcomes{op: cdGetAppsOp, what: "applications", missingWhy: "applications were not described by BatchGetApplications (left out of its answer or returned without a name; they may have been deleted after the listing)"}
	gets.tallyCDBatchMisses("", got.cdBatchMisses)
	var rows []*store.Resource
	for _, a := range got.infos {
		// A body without a name left the name it was sent for missing,
		// which is warned about.
		name := sv(a.ApplicationName)
		if name == "" {
			continue
		}
		rows = append(rows, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCodeDeployApplication, NativeID: codeDeployARN(acct.partition(), region, acct.ID, "application", name),
			Name: &name, Region: &region,
			AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
		})
	}
	return names, cdStoreRows(ctx, st, acct, region, cdListAppsOp, rows, listErr, nil, nil, func() {}, &gets, tags, p)
}

// cdAppGroups is what one application's deployment group reads returned:
// the error that ended its listing (nil when every page was read) and the
// BatchGetDeploymentGroups bodies of the names listed before it.
type cdAppGroups struct {
	listErr error
	got     cdBatch[cdtypes.DeploymentGroupInfo]
}

// scanCDDeploymentGroups stores each application's deployment groups with
// their tags. ListDeploymentGroups returns names only and
// BatchGetDeploymentGroups describes them (cdBatchGet). The applications are
// read concurrently (readEachKey); one application's failure costs the
// others none of theirs, and the groups it listed before a failed page are
// still described. An application deleted after it was listed
// (ApplicationDoesNotExistException, from its listing or from any of its
// BatchGetDeploymentGroups calls) is skipped silently when none of its
// groups was stored, and warned about once otherwise, since only part of
// it was stored; whatever else ended its listing is then not reported. A listing still transient after the SDK's retries is one
// error for all such applications, apart from the hard ones. Every
// application's groups, bodies included, are held until one write, as
// storeEachParentTags holds its parents' items and as this phase did before
// its tag reads; per-page writes (storeEachParentPagesTags) are for listings
// known to be too large to hold. It reports whether the write failed, after
// which the scan must stop.
func scanCDDeploymentGroups(ctx context.Context, client codeDeployAPI, apps []string, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	reads, stopped := readEachKey(ctx, apps, func(ctx context.Context, app string) cdAppGroups {
		names, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListDeploymentGroups(ctx, &codedeploy.ListDeploymentGroupsInput{ApplicationName: &app, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DeploymentGroups, out.NextToken, nil
		})
		got := cdBatchGet(ctx, names, func(ctx context.Context, chunk []string) ([]cdtypes.DeploymentGroupInfo, error) {
			out, err := client.BatchGetDeploymentGroups(ctx, &codedeploy.BatchGetDeploymentGroupsInput{ApplicationName: &app, DeploymentGroupNames: chunk})
			if err != nil {
				return nil, err
			}
			return out.DeploymentGroupsInfo, nil
		}, func(g cdtypes.DeploymentGroupInfo) string { return sv(g.DeploymentGroupName) })
		return cdAppGroups{err, got}
	})
	var listTransients transientTally
	lists := parentOutcomes{op: cdListGroupsOp, notFound: cdAppGone, skip: listTransients.drop}
	gets := cdBatchOutcomes{op: cdGetGroupsOp, what: "deployment groups", notFound: cdAppGone, missingWhy: "deployment groups were not described by BatchGetDeploymentGroups (left out of its answer or returned without a name; they may have been deleted after the listing)"}
	var rows []*store.Resource
	for i, read := range reads {
		app := apps[i]
		before := len(rows)
		for _, g := range read.got.infos {
			// A body without a name left the name it was sent for missing,
			// which is warned about.
			group := sv(g.DeploymentGroupName)
			if group == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeDeployDeploymentGroup, NativeID: codeDeployARN(acct.partition(), region, acct.ID, "deploymentgroup", app+"/"+group),
				Name: &group, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
		stored := len(rows) > before
		err := lateNXDOMAIN(cdListGroupsOp, read.listErr)
		if gone := read.got.cdGoneErr(); gone != nil {
			// The application no longer exists, so nothing its listing
			// failed to read can be read by a rescan either.
			err = gone
		}
		lists.parentErr(app, stored, err)
		gets.tallyCDBatchMisses(app+"/", read.got.cdBatchMisses)
	}
	// runHostPhases folds this into its own stop member; it is recorded so
	// that this phase never reports success for applications it did not read.
	lists.stopped(stopped)
	var listTransient error
	if t := &listTransients; t.count > 0 {
		listTransient = fmt.Errorf("%s: %d application(s) whose deployment groups were not all listed, first %s: %w", cdListGroupsOp, t.count, t.first, t.err)
	}
	return cdStoreRows(ctx, st, acct, region, cdListGroupsOp, rows, nil, errors.Join(lists.errs...), listTransient, func() { lists.warn(st, acct, region) }, &gets, tags, p)
}

func scanCDDeploymentConfigs(ctx context.Context, client codeDeployAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	var names []string
	var nextToken *string
	for {
		out, err := client.ListDeploymentConfigs(ctx, &codedeploy.ListDeploymentConfigsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, skipIfAccessDenied(st, "codedeploy:ListDeploymentConfigs", acct.ID, region, err)
			}
			return 0, fmt.Errorf("codedeploy:ListDeploymentConfigs: %w", err)
		}
		names = append(names, out.DeploymentConfigsList...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	var batch []*store.Resource
	for _, name := range names {
		nm := name
		out, err := client.GetDeploymentConfig(ctx, &codedeploy.GetDeploymentConfigInput{DeploymentConfigName: &nm})
		if err != nil {
			if isAccessDenied(err) {
				continue
			}
			return 0, fmt.Errorf("codedeploy:GetDeploymentConfig %s: %w", nm, err)
		}
		if out.DeploymentConfigInfo == nil {
			continue
		}
		arn := codeDeployARN(acct.partition(), region, acct.ID, "deploymentconfig", nm)
		managed := strings.HasPrefix(nm, "CodeDeployDefault.")
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCodeDeployDeploymentConfig, NativeID: arn,
			Name: &nm, Region: &region,
			AttributesJSON:    mustJSON(out.DeploymentConfigInfo),
			DiscoveredBy:      scanID,
			ManagedByProvider: managed,
		})
	}
	return upsertBatch(st, batch, "codedeploy deployment-configs")
}
