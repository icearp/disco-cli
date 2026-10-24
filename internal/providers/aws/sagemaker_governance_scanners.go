package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSageMakerAction, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerContext, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerExperiment, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerExperimentTrial, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerHub, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerHubContent, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerHumanTaskUI, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerFlowDefinition, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerLineageGroup, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerAlgorithm, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerWorkforce, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerComputeQuota, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerClusterSchedulerConfig, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerEdgeDeploymentPlan, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerAIWorkloadConfig, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerMlflowApp, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerTrainingPlan, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerUltraServer, Service: "sagemaker"})
}

// sagemakerGovernanceAPI is the narrow surface for the governance / lineage /
// hub family — account-wide List ops whose summaries already carry the resource
// ARN, plus DescribeTrial (trial summary omits the parent ExperimentName the
// trial→experiment resolver needs).
type sagemakerGovernanceAPI interface {
	sagemakerTagsAPI
	ListActions(context.Context, *sagemaker.ListActionsInput, ...func(*sagemaker.Options)) (*sagemaker.ListActionsOutput, error)
	ListContexts(context.Context, *sagemaker.ListContextsInput, ...func(*sagemaker.Options)) (*sagemaker.ListContextsOutput, error)
	ListExperiments(context.Context, *sagemaker.ListExperimentsInput, ...func(*sagemaker.Options)) (*sagemaker.ListExperimentsOutput, error)
	ListTrials(context.Context, *sagemaker.ListTrialsInput, ...func(*sagemaker.Options)) (*sagemaker.ListTrialsOutput, error)
	DescribeTrial(context.Context, *sagemaker.DescribeTrialInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeTrialOutput, error)
	ListHubs(context.Context, *sagemaker.ListHubsInput, ...func(*sagemaker.Options)) (*sagemaker.ListHubsOutput, error)
	ListHubContents(context.Context, *sagemaker.ListHubContentsInput, ...func(*sagemaker.Options)) (*sagemaker.ListHubContentsOutput, error)
	ListHumanTaskUis(context.Context, *sagemaker.ListHumanTaskUisInput, ...func(*sagemaker.Options)) (*sagemaker.ListHumanTaskUisOutput, error)
	ListFlowDefinitions(context.Context, *sagemaker.ListFlowDefinitionsInput, ...func(*sagemaker.Options)) (*sagemaker.ListFlowDefinitionsOutput, error)
	ListLineageGroups(context.Context, *sagemaker.ListLineageGroupsInput, ...func(*sagemaker.Options)) (*sagemaker.ListLineageGroupsOutput, error)
	ListAlgorithms(context.Context, *sagemaker.ListAlgorithmsInput, ...func(*sagemaker.Options)) (*sagemaker.ListAlgorithmsOutput, error)
	ListWorkforces(context.Context, *sagemaker.ListWorkforcesInput, ...func(*sagemaker.Options)) (*sagemaker.ListWorkforcesOutput, error)
	ListComputeQuotas(context.Context, *sagemaker.ListComputeQuotasInput, ...func(*sagemaker.Options)) (*sagemaker.ListComputeQuotasOutput, error)
	ListClusterSchedulerConfigs(context.Context, *sagemaker.ListClusterSchedulerConfigsInput, ...func(*sagemaker.Options)) (*sagemaker.ListClusterSchedulerConfigsOutput, error)
	ListEdgeDeploymentPlans(context.Context, *sagemaker.ListEdgeDeploymentPlansInput, ...func(*sagemaker.Options)) (*sagemaker.ListEdgeDeploymentPlansOutput, error)
	ListAIWorkloadConfigs(context.Context, *sagemaker.ListAIWorkloadConfigsInput, ...func(*sagemaker.Options)) (*sagemaker.ListAIWorkloadConfigsOutput, error)
	ListMlflowApps(context.Context, *sagemaker.ListMlflowAppsInput, ...func(*sagemaker.Options)) (*sagemaker.ListMlflowAppsOutput, error)
	ListTrainingPlans(context.Context, *sagemaker.ListTrainingPlansInput, ...func(*sagemaker.Options)) (*sagemaker.ListTrainingPlansOutput, error)
	ListUltraServersByReservedCapacity(context.Context, *sagemaker.ListUltraServersByReservedCapacityInput, ...func(*sagemaker.Options)) (*sagemaker.ListUltraServersByReservedCapacityOutput, error)
}

// scanSageMakerGovernance runs all governance / lineage / hub phases for one
// region. Hubs scan before hub-contents and training plans before UltraServers
// — those phases fan out per scanned parent. Every phase tolerates
// AccessDenied (skip, preserve siblings).
func scanSageMakerGovernance(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return runSageMakerPhases(
		sagemakerPhase{"sagemaker:ListActions", func() (int, int, error) { return scanSageMakerActions(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListContexts", func() (int, int, error) { return scanSageMakerContexts(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListExperiments", func() (int, int, error) { return scanSageMakerExperiments(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListTrials", func() (int, int, error) { return scanSageMakerExperimentTrials(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListHubs", func() (int, int, error) { return scanSageMakerHubs(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListHubContents", func() (int, int, error) { return scanSageMakerHubContents(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListHumanTaskUis", func() (int, int, error) { return scanSageMakerHumanTaskUis(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListFlowDefinitions", func() (int, int, error) { return scanSageMakerFlowDefinitions(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListLineageGroups", func() (int, int, error) { return scanSageMakerLineageGroups(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListAlgorithms", func() (int, int, error) { return scanSageMakerAlgorithms(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListWorkforces", func() (int, int, error) { return scanSageMakerWorkforces(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListComputeQuotas", func() (int, int, error) { return scanSageMakerComputeQuotas(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListClusterSchedulerConfigs", func() (int, int, error) {
			return scanSageMakerClusterSchedulerConfigs(ctx, client, acct, region, st, scanID)
		}},
		sagemakerPhase{"sagemaker:ListEdgeDeploymentPlans", func() (int, int, error) {
			return scanSageMakerEdgeDeploymentPlans(ctx, client, acct, region, st, scanID)
		}},
		sagemakerPhase{"sagemaker:ListAIWorkloadConfigs", func() (int, int, error) { return scanSageMakerAIWorkloadConfigs(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListMlflowApps", func() (int, int, error) { return scanSageMakerMlflowApps(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListTrainingPlans", func() (int, int, error) { return scanSageMakerTrainingPlans(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListUltraServersByReservedCapacity", func() (int, int, error) { return scanSageMakerUltraServers(ctx, client, acct, region, st, scanID) }},
	)
}

// scanSagemakerSummaryList is the shared body for the account-wide list-only
// phases: page the listing, build one row per summary storing the summary
// verbatim, and store the rows through put, those read before a failed page
// included. row returns (arn, name, status, createdAt); a summary with an
// empty arn is not stored, and one warning after the write covers them all.
func scanSagemakerSummaryList[S any](
	ctx context.Context, acct *account, region string, st *store.Store, scanID string,
	rtype, op string,
	page func(ctx context.Context, token *string) ([]S, *string, error),
	row func(S) (arn, name, status string, created *time.Time),
	put sagemakerPut,
) (int, int, error) {
	summaries, listErr := readTokenPages(ctx, page)
	var skips notStoredSkips
	rows := buildListedRows(summaries, func(s S) (*store.Resource, string, string) {
		arn, name, status, created := row(s)
		if arn == "" {
			return nil, "summaries have an empty ARN", cmp.Or(name, "one without a name")
		}
		res := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           rtype,
			NativeID:       arn,
			Region:         &region,
			CreatedAt:      tp(created),
			AttributesJSON: mustJSON(s),
			DiscoveredBy:   scanID,
		}
		if name != "" {
			res.Name = &name
		}
		if status != "" {
			res.Status = &status
		}
		return res, "", ""
	}, nil, &skips)
	return put(rows, listErr, func() { skips.warn(st, op, acct, region) })
}

// sagemakerChildList is one child listing of a parent: key names it in
// warnings and errors, items are those read before err (nil when every page
// was read). skipped names the items listed but not kept, as they lack the
// field their row needs; nameless marks a parent not listed at all, as it
// has no name to list it by.
type sagemakerChildList[T any] struct {
	key      string
	items    []T
	skipped  []string
	nameless bool
	err      error
}

// sagemakerChildren is what readSageMakerChildren read: items[i] is a child of
// the parent whose ResourceID is parentIDs[i]; p holds the listings' errors,
// and warn, to run only after a successful write, their warnings.
type sagemakerChildren[T any] struct {
	items     []T
	parentIDs []string
	p         phaseTotals
	warn      func()
}

// sagemakerChildSkips says, for warnings, why a child listing skipped items
// (sagemakerChildList.skipped, e.g. "aliases are empty") and what a nameless
// parent is (e.g. "stored image(s)").
type sagemakerChildSkips struct{ emptyWhy, namelessWhat string }

// readSageMakerChildren reads, with op, the child listings of every parentType
// row this scan stored in this region (an earlier phase of the same family),
// so a parent deleted before this scan is never queried. Parents are listed
// fanoutMed at a time (readEachKey), and a failed listing keeps the items of
// every other one and its own items read before it failed. Per failed
// listing:
//   - a notFound code (op's model code for a parent deleted since its phase):
//     skipped without a warning;
//   - denied: one warning covers every such listing;
//   - still transient after the SDK's retries: every such listing is one
//     error under op, which phaseErrs.join can demote;
//   - not read because ctx ended: one error under op covers every such
//     listing;
//   - any other error, an NXDOMAIN included: returned under op.
//
// Skipped items and nameless parents are warned about too. The error is
// only the store's, listing the parents.
func readSageMakerChildren[T any](
	ctx context.Context, acct *account, region string, st *store.Store, scanID string,
	parentType, op string, notFound []string, why sagemakerChildSkips,
	list func(ctx context.Context, parent store.Resource) []sagemakerChildList[T],
) (sagemakerChildren[T], error) {
	parents, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{parentType},
		Regions: []string{region}, SeenBy: scanID, Limit: util.AllResources,
	})
	if err != nil {
		return sagemakerChildren[T]{}, fmt.Errorf("%s: list %s parents: %w", op, parentType, err)
	}
	reads, stopped := readEachKey(ctx, parents, list)
	var (
		c           sagemakerChildren[T]
		denied      skipTally
		empty       skipTally
		nameless    skipTally
		transients  transientTally
		errs        []error
		interrupted bool
	)
	for i, lists := range reads {
		for _, l := range lists {
			for _, item := range l.items {
				c.items = append(c.items, item)
				c.parentIDs = append(c.parentIDs, parents[i].ID)
			}
			for _, key := range l.skipped {
				empty.skipped(key, nil)
			}
			if l.nameless {
				nameless.skipped(l.key, nil)
			}
			// The parents' phase reached the endpoint, so an NXDOMAIN now is
			// a failure of this scan, not a region gap (lateNXDOMAIN).
			switch err := lateNXDOMAIN(op, l.err); {
			case err == nil:
			case ctx.Err() != nil && errors.Is(err, ctx.Err()):
				interrupted = true
			case isAPIErrorCode(err, notFound...):
			case isAccessDenied(err):
				denied.skipped(l.key, err)
			case transients.drop(l.key, err):
			default:
				errs = append(errs, fmt.Errorf("%s %s: %w", op, l.key, err))
			}
		}
	}
	// One stop member, whether ctx ended before some listings started or
	// only during the listings already running.
	switch {
	case stopped != nil:
		errs = append(errs, fmt.Errorf("%s: %w", op, stopped))
	case interrupted:
		errs = append(errs, fmt.Errorf("%s: listings cut short: %w", op, ctx.Err()))
	}
	c.p.errs.add(op, errors.Join(errs...))
	if t := &transients.skipTally; t.count > 0 {
		c.p.errs.add(op, fmt.Errorf("%s: %d listing(s) cut short, first %s: %w", op, t.count, t.first, t.err))
	}
	c.warn = func() {
		denied.warnDeniedIn(st, op, acct, region, "listing(s) whose children were not read as they were")
		warnNotStored(st, op, acct, region, &empty, why.emptyWhy)
		sagemakerWarnNameless(st, op, acct, region, &nameless, why.namelessWhat)
	}
	return c, nil
}

// scanSageMakerChildren stores the child rows read by readSageMakerChildren,
// each recorded as contained by its parent. The listings' warnings are sent
// only after a successful write.
func scanSageMakerChildren(
	ctx context.Context, acct *account, region string, st *store.Store, scanID string,
	parentType, op string, notFound []string, why sagemakerChildSkips,
	list func(ctx context.Context, parent store.Resource) []sagemakerChildList[*store.Resource],
) (int, int, error) {
	c, err := readSageMakerChildren(ctx, acct, region, st, scanID, parentType, op, notFound, why, list)
	if err != nil {
		return 0, 0, err
	}
	total, inserted, err := upsertBatch(st, c.items, op)
	if err == nil {
		pairs := make([][2]string, len(c.items))
		for i, child := range c.items {
			pairs[i] = [2]string{child.ID, c.parentIDs[i]}
		}
		if herr := st.RecordHierarchyBatch(pairs); herr != nil {
			err = fmt.Errorf("%s: record containment: %w", op, herr)
		}
	}
	if err == nil {
		c.warn()
	}
	c.p.addPhase(op, total, inserted, err)
	return sagemakerResult(&c.p)
}

func scanSageMakerActions(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerAction, "sagemaker:ListActions",
		func(ctx context.Context, token *string) ([]smtypes.ActionSummary, *string, error) {
			out, err := client.ListActions(ctx, &sagemaker.ListActionsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ActionSummaries, out.NextToken, nil
		},
		func(s smtypes.ActionSummary) (string, string, string, *time.Time) {
			return sv(s.ActionArn), sv(s.ActionName), string(s.Status), s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListActions"))
}

func scanSageMakerContexts(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerContext, "sagemaker:ListContexts",
		func(ctx context.Context, token *string) ([]smtypes.ContextSummary, *string, error) {
			out, err := client.ListContexts(ctx, &sagemaker.ListContextsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ContextSummaries, out.NextToken, nil
		},
		func(s smtypes.ContextSummary) (string, string, string, *time.Time) {
			return sv(s.ContextArn), sv(s.ContextName), "", s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListContexts"))
}

func scanSageMakerExperiments(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerExperiment, "sagemaker:ListExperiments",
		func(ctx context.Context, token *string) ([]smtypes.ExperimentSummary, *string, error) {
			out, err := client.ListExperiments(ctx, &sagemaker.ListExperimentsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ExperimentSummaries, out.NextToken, nil
		},
		func(s smtypes.ExperimentSummary) (string, string, string, *time.Time) {
			return sv(s.ExperimentArn), sv(s.ExperimentName), "", s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListExperiments"))
}

// scanSageMakerExperimentTrials lists trials then fans out DescribeTrial — the
// trial summary omits the parent ExperimentName the resolver keys on.
func scanSageMakerExperimentTrials(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.TrialSummary]{
		listOp: "sagemaker:ListTrials", describeOp: "sagemaker:DescribeTrial", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.TrialSummary, *string, error) {
			out, err := client.ListTrials(ctx, &sagemaker.ListTrialsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TrialSummaries, out.NextToken, nil
		},
		name: func(s smtypes.TrialSummary) string { return sv(s.TrialName) },
		listed: func(s smtypes.TrialSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerExperimentTrial, s.TrialArn, s.TrialName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.TrialSummary) (*store.Resource, error) {
			name := sv(s.TrialName)
			out, err := client.DescribeTrial(ctx, &sagemaker.DescribeTrialInput{TrialName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.TrialArn)
			if arn == "" {
				return nil, nil
			}
			tname := sv(out.TrialName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerExperimentTrial,
				NativeID:       arn,
				Name:           &tname,
				Region:         &region,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListTrials"))
}

// scanSageMakerHubs pages ListHubs by hand: it has no SDK paginator.
func scanSageMakerHubs(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerHub, "sagemaker:ListHubs",
		func(ctx context.Context, token *string) ([]smtypes.HubInfo, *string, error) {
			out, err := client.ListHubs(ctx, &sagemaker.ListHubsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.HubSummaries, out.NextToken, nil
		},
		func(s smtypes.HubInfo) (string, string, string, *time.Time) {
			return sv(s.HubArn), sv(s.HubName), string(s.HubStatus), s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListHubs"))
}

// sagemakerHubContentAttrs embeds the SDK summary (fields stay top-level) and
// adds the parent HubName, which ListHubContents takes as input but does not
// echo on the summary — the hub-content→hub resolver keys on it.
type sagemakerHubContentAttrs struct {
	smtypes.HubContentInfo
	HubName string `json:"HubName"`
}

// scanSageMakerHubContents lists the contents of every hub this scan stored in
// this region (readSageMakerChildren), per content type {Model, Notebook,
// ModelReference}: ListHubContents requires both HubName and HubContentType
// and has no SDK paginator. A failed listing keeps the rows of every other one
// and its own rows read before it failed, as readSageMakerChildren says; a
// private hub may deny one content type only. The rows are not recorded as
// contained by their hub: the hub-content resolver links them by HubName.
func scanSageMakerHubContents(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "sagemaker:ListHubContents"
	contentTypes := []smtypes.HubContentType{
		smtypes.HubContentTypeModel,
		smtypes.HubContentTypeNotebook,
		smtypes.HubContentTypeModelReference,
	}
	c, err := readSageMakerChildren(ctx, acct, region, st, scanID, TypeSageMakerHub, op, []string{"ResourceNotFound"},
		sagemakerChildSkips{emptyWhy: "hub contents have an empty HubContentArn", namelessWhat: "stored hub(s)"},
		func(ctx context.Context, hub store.Resource) []sagemakerChildList[*store.Resource] {
			hubName := sv(hub.Name)
			if hubName == "" {
				return []sagemakerChildList[*store.Resource]{{key: hub.NativeID, nameless: true}}
			}
			lists := make([]sagemakerChildList[*store.Resource], len(contentTypes))
			for i, ct := range contentTypes {
				key := hubName + "/" + string(ct)
				contents, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]smtypes.HubContentInfo, *string, error) {
					out, err := client.ListHubContents(ctx, &sagemaker.ListHubContentsInput{
						HubName: &hubName, HubContentType: ct, MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token,
					})
					if err != nil {
						return nil, nil, err
					}
					return out.HubContentSummaries, out.NextToken, nil
				})
				lists[i] = sagemakerChildList[*store.Resource]{key: key, err: err}
				for _, content := range contents {
					if sv(content.HubContentArn) == "" {
						lists[i].skipped = append(lists[i].skipped, key+"/"+sv(content.HubContentName))
						continue
					}
					lists[i].items = append(lists[i].items, sagemakerHubContentRow(acct, region, scanID, hubName, content))
				}
			}
			return lists
		})
	if err != nil {
		return 0, 0, err
	}
	total, inserted, err := sagemakerStoreTagged(ctx, client, acct, region, st, op)(c.items, nil, c.warn)
	addSageMakerPhase(&c.p, op, total, inserted, err)
	return sagemakerResult(&c.p)
}

// sagemakerHubContentRow is the row of one content of hub hubName.
func sagemakerHubContentRow(acct *account, region, scanID, hubName string, c smtypes.HubContentInfo) *store.Resource {
	name := sv(c.HubContentName)
	status := string(c.HubContentStatus)
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeSageMakerHubContent,
		NativeID:       sv(c.HubContentArn),
		Name:           &name,
		Region:         &region,
		Status:         &status,
		CreatedAt:      tp(c.CreationTime),
		AttributesJSON: mustJSON(sagemakerHubContentAttrs{HubContentInfo: c, HubName: hubName}),
		DiscoveredBy:   scanID,
	}
}

func scanSageMakerHumanTaskUis(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerHumanTaskUI, "sagemaker:ListHumanTaskUis",
		func(ctx context.Context, token *string) ([]smtypes.HumanTaskUiSummary, *string, error) {
			out, err := client.ListHumanTaskUis(ctx, &sagemaker.ListHumanTaskUisInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.HumanTaskUiSummaries, out.NextToken, nil
		},
		func(s smtypes.HumanTaskUiSummary) (string, string, string, *time.Time) {
			return sv(s.HumanTaskUiArn), sv(s.HumanTaskUiName), "", s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListHumanTaskUis"))
}

func scanSageMakerFlowDefinitions(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerFlowDefinition, "sagemaker:ListFlowDefinitions",
		func(ctx context.Context, token *string) ([]smtypes.FlowDefinitionSummary, *string, error) {
			out, err := client.ListFlowDefinitions(ctx, &sagemaker.ListFlowDefinitionsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FlowDefinitionSummaries, out.NextToken, nil
		},
		func(s smtypes.FlowDefinitionSummary) (string, string, string, *time.Time) {
			return sv(s.FlowDefinitionArn), sv(s.FlowDefinitionName), string(s.FlowDefinitionStatus), s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListFlowDefinitions"))
}

func scanSageMakerLineageGroups(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerLineageGroup, "sagemaker:ListLineageGroups",
		func(ctx context.Context, token *string) ([]smtypes.LineageGroupSummary, *string, error) {
			out, err := client.ListLineageGroups(ctx, &sagemaker.ListLineageGroupsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.LineageGroupSummaries, out.NextToken, nil
		},
		func(s smtypes.LineageGroupSummary) (string, string, string, *time.Time) {
			return sv(s.LineageGroupArn), sv(s.LineageGroupName), "", s.CreationTime
		},
		// Their tags are not read: neither the Service Authorization
		// Reference's ListTags resource types nor the ListTags API reference
		// name a lineage group ARN, and SageMaker creates lineage groups
		// itself, with no tags input.
		sagemakerStoreUntagged(st, acct, region, "sagemaker:ListLineageGroups"))
}

func scanSageMakerAlgorithms(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerAlgorithm, "sagemaker:ListAlgorithms",
		func(ctx context.Context, token *string) ([]smtypes.AlgorithmSummary, *string, error) {
			out, err := client.ListAlgorithms(ctx, &sagemaker.ListAlgorithmsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AlgorithmSummaryList, out.NextToken, nil
		},
		func(s smtypes.AlgorithmSummary) (string, string, string, *time.Time) {
			return sv(s.AlgorithmArn), sv(s.AlgorithmName), string(s.AlgorithmStatus), s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListAlgorithms"))
}

func scanSageMakerWorkforces(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerWorkforce, "sagemaker:ListWorkforces",
		func(ctx context.Context, token *string) ([]smtypes.Workforce, *string, error) {
			out, err := client.ListWorkforces(ctx, &sagemaker.ListWorkforcesInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Workforces, out.NextToken, nil
		},
		func(s smtypes.Workforce) (string, string, string, *time.Time) {
			return sv(s.WorkforceArn), sv(s.WorkforceName), string(s.Status), s.CreateDate
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListWorkforces"))
}

func scanSageMakerComputeQuotas(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerComputeQuota, "sagemaker:ListComputeQuotas",
		func(ctx context.Context, token *string) ([]smtypes.ComputeQuotaSummary, *string, error) {
			out, err := client.ListComputeQuotas(ctx, &sagemaker.ListComputeQuotasInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ComputeQuotaSummaries, out.NextToken, nil
		},
		func(s smtypes.ComputeQuotaSummary) (string, string, string, *time.Time) {
			return sv(s.ComputeQuotaArn), sv(s.Name), string(s.Status), s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListComputeQuotas"))
}

func scanSageMakerClusterSchedulerConfigs(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerClusterSchedulerConfig, "sagemaker:ListClusterSchedulerConfigs",
		func(ctx context.Context, token *string) ([]smtypes.ClusterSchedulerConfigSummary, *string, error) {
			out, err := client.ListClusterSchedulerConfigs(ctx, &sagemaker.ListClusterSchedulerConfigsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ClusterSchedulerConfigSummaries, out.NextToken, nil
		},
		func(s smtypes.ClusterSchedulerConfigSummary) (string, string, string, *time.Time) {
			return sv(s.ClusterSchedulerConfigArn), sv(s.Name), string(s.Status), s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListClusterSchedulerConfigs"))
}

func scanSageMakerEdgeDeploymentPlans(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerEdgeDeploymentPlan, "sagemaker:ListEdgeDeploymentPlans",
		func(ctx context.Context, token *string) ([]smtypes.EdgeDeploymentPlanSummary, *string, error) {
			out, err := client.ListEdgeDeploymentPlans(ctx, &sagemaker.ListEdgeDeploymentPlansInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EdgeDeploymentPlanSummaries, out.NextToken, nil
		},
		func(s smtypes.EdgeDeploymentPlanSummary) (string, string, string, *time.Time) {
			return sv(s.EdgeDeploymentPlanArn), sv(s.EdgeDeploymentPlanName), "", s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListEdgeDeploymentPlans"))
}

func scanSageMakerAIWorkloadConfigs(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerAIWorkloadConfig, "sagemaker:ListAIWorkloadConfigs",
		func(ctx context.Context, token *string) ([]smtypes.AIWorkloadConfigSummary, *string, error) {
			out, err := client.ListAIWorkloadConfigs(ctx, &sagemaker.ListAIWorkloadConfigsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AIWorkloadConfigs, out.NextToken, nil
		},
		func(s smtypes.AIWorkloadConfigSummary) (string, string, string, *time.Time) {
			return sv(s.AIWorkloadConfigArn), sv(s.AIWorkloadConfigName), "", s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListAIWorkloadConfigs"))
}

func scanSageMakerMlflowApps(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerMlflowApp, "sagemaker:ListMlflowApps",
		func(ctx context.Context, token *string) ([]smtypes.MlflowAppSummary, *string, error) {
			out, err := client.ListMlflowApps(ctx, &sagemaker.ListMlflowAppsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Summaries, out.NextToken, nil
		},
		func(s smtypes.MlflowAppSummary) (string, string, string, *time.Time) {
			return sv(s.Arn), sv(s.Name), string(s.Status), s.CreationTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListMlflowApps"))
}

func scanSageMakerTrainingPlans(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerTrainingPlan, "sagemaker:ListTrainingPlans",
		func(ctx context.Context, token *string) ([]smtypes.TrainingPlanSummary, *string, error) {
			out, err := client.ListTrainingPlans(ctx, &sagemaker.ListTrainingPlansInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TrainingPlanSummaries, out.NextToken, nil
		},
		func(s smtypes.TrainingPlanSummary) (string, string, string, *time.Time) {
			// TrainingPlanSummary carries no creation timestamp — only the
			// reservation window (StartTime/EndTime).
			return sv(s.TrainingPlanArn), sv(s.TrainingPlanName), string(s.Status), s.StartTime
		},
		sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListTrainingPlans"))
}

// scanSageMakerUltraServers lists the UltraServers in each UltraServer-type
// reserved capacity of every scanned training plan. Reserved capacities have no
// List op of their own; the training-plan summary is the only place their ARNs
// appear. Instance-type capacities hold no UltraServers and are not queried.
func scanSageMakerUltraServers(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "sagemaker:ListUltraServersByReservedCapacity"
	return scanSageMakerChildren(ctx, acct, region, st, scanID, TypeSageMakerTrainingPlan, op, []string{"ResourceNotFound"},
		sagemakerChildSkips{emptyWhy: "UltraServers have an empty UltraServerId"},
		func(ctx context.Context, plan store.Resource) []sagemakerChildList[*store.Resource] {
			var attrs smtypes.TrainingPlanSummary
			if err := json.Unmarshal([]byte(plan.AttributesJSON), &attrs); err != nil {
				return []sagemakerChildList[*store.Resource]{{key: plan.NativeID, err: fmt.Errorf("decode training plan attributes: %w", err)}}
			}
			// One listing per reservation, so a denied or vanished one keeps
			// the UltraServers of its siblings in the same plan.
			var lists []sagemakerChildList[*store.Resource]
			for _, rc := range attrs.ReservedCapacitySummaries {
				rcARN := sv(rc.ReservedCapacityArn)
				if rcARN == "" || rc.ReservedCapacityType != smtypes.ReservedCapacityTypeUltraserver {
					continue
				}
				servers, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]smtypes.UltraServer, *string, error) {
					out, err := client.ListUltraServersByReservedCapacity(ctx, &sagemaker.ListUltraServersByReservedCapacityInput{
						ReservedCapacityArn: &rcARN, MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token,
					})
					if err != nil {
						return nil, nil, err
					}
					return out.UltraServers, out.NextToken, nil
				})
				l := sagemakerChildList[*store.Resource]{key: rcARN, err: err}
				for i, u := range servers {
					id := sv(u.UltraServerId)
					if id == "" {
						l.skipped = append(l.skipped, fmt.Sprintf("%s #%d", rcARN, i+1))
						continue
					}
					res := &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeSageMakerUltraServer,
						NativeID:       rcARN + "/ultraserver/" + id,
						Name:           &id,
						Region:         &region,
						AttributesJSON: mustJSON(u),
						DiscoveredBy:   scanID,
					}
					if health := string(u.HealthStatus); health != "" {
						res.Status = &health
					}
					l.items = append(l.items, res)
				}
				lists = append(lists, l)
			}
			return lists
		})
}
