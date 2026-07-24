package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSageMakerAction, Service: "sagemaker", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerContext, Service: "sagemaker", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerExperiment, Service: "sagemaker", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerExperimentTrial, Service: "sagemaker", Upstream: "AWS::sagemaker::experiment-trial"})
	registerType(restype.Descriptor{Type: TypeSageMakerHub, Service: "sagemaker", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerHubContent, Service: "sagemaker", Upstream: "AWS::sagemaker::hub-content"})
	registerType(restype.Descriptor{Type: TypeSageMakerHumanTaskUI, Service: "sagemaker", Upstream: "AWS::sagemaker::human-task-ui", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerFlowDefinition, Service: "sagemaker", Upstream: "AWS::sagemaker::flow-definition", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerLineageGroup, Service: "sagemaker", Upstream: "AWS::sagemaker::lineage-group", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerAlgorithm, Service: "sagemaker", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerWorkforce, Service: "sagemaker", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerComputeQuota, Service: "sagemaker", Upstream: "AWS::sagemaker::compute-quota"})
	registerType(restype.Descriptor{Type: TypeSageMakerClusterSchedulerConfig, Service: "sagemaker", Upstream: "AWS::sagemaker::cluster-scheduler-config"})
	registerType(restype.Descriptor{Type: TypeSageMakerEdgeDeploymentPlan, Service: "sagemaker", Upstream: "AWS::sagemaker::edge-deployment-plan"})
	registerType(restype.Descriptor{Type: TypeSageMakerAIWorkloadConfig, Service: "sagemaker", Upstream: "AWS::sagemaker::ai-workload-config", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerMlflowApp, Service: "sagemaker", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerTrainingPlan, Service: "sagemaker", Upstream: "AWS::sagemaker::training-plan", Leaf: true})
}

// sagemakerGovernanceAPI is the narrow surface for the governance / lineage /
// hub family — account-wide List ops whose summaries already carry the resource
// ARN, plus DescribeTrial (trial summary omits the parent ExperimentName the
// trial→experiment resolver needs).
type sagemakerGovernanceAPI interface {
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
}

// scanSageMakerGovernance runs all governance / lineage / hub phases for one
// region. Hubs scan before hub-contents — the hub-content phase fans out per
// scanned hub. Every phase tolerates AccessDenied (skip, preserve siblings).
func scanSageMakerGovernance(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func(context.Context, sagemakerGovernanceAPI, *account, string, *store.Store, string) (int, int, error){
		scanSageMakerActions,
		scanSageMakerContexts,
		scanSageMakerExperiments,
		scanSageMakerExperimentTrials,
		scanSageMakerHubs,
		scanSageMakerHubContents,
		scanSageMakerHumanTaskUis,
		scanSageMakerFlowDefinitions,
		scanSageMakerLineageGroups,
		scanSageMakerAlgorithms,
		scanSageMakerWorkforces,
		scanSageMakerComputeQuotas,
		scanSageMakerClusterSchedulerConfigs,
		scanSageMakerEdgeDeploymentPlans,
		scanSageMakerAIWorkloadConfigs,
		scanSageMakerMlflowApps,
		scanSageMakerTrainingPlans,
	} {
		t, i, ferr := phase(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// smPaginator is the shape every aws-sdk-go-v2 List*Paginator satisfies; it lets
// collectSMPages drain any SageMaker paginator generically.
type smPaginator[O any] interface {
	HasMorePages() bool
	NextPage(context.Context, ...func(*sagemaker.Options)) (*O, error)
}

// collectSMPages drains a paginator, concatenating the slice picked from each page.
func collectSMPages[O any, S any](ctx context.Context, p smPaginator[O], pick func(*O) []S) ([]S, error) {
	var out []S
	for p.HasMorePages() {
		o, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, pick(o)...)
	}
	return out, nil
}

// scanSagemakerSummaryList is the shared body for the account-wide list-only
// phases: collect summaries (collect handles pagination), build one row per
// summary storing the summary verbatim, batch-upsert. AccessDenied skips the
// whole phase. row returns (arn, name, status, createdAt) — arn empty drops the row.
func scanSagemakerSummaryList[S any](
	ctx context.Context, acct *account, region string, st *store.Store, scanID string,
	rtype, op, label string,
	collect func(context.Context) ([]S, error),
	row func(S) (arn, name, status string, created *time.Time),
) (int, int, error) {
	summaries, err := collect(ctx)
	if err != nil {
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, "sagemaker:"+op, acct.ID, region, err)
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("sagemaker:%s: %w", op, err)
	}
	batch := make([]*store.Resource, 0, len(summaries))
	for _, s := range summaries {
		arn, name, status, created := row(s)
		if arn == "" {
			continue
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
			n := name
			res.Name = &n
		}
		if status != "" {
			st2 := status
			res.Status = &st2
		}
		batch = append(batch, res)
	}
	return upsertBatch(st, batch, label)
}

func scanSageMakerActions(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerAction, "ListActions", "sagemaker actions",
		func(c context.Context) ([]smtypes.ActionSummary, error) {
			return collectSMPages(c, sagemaker.NewListActionsPaginator(client, &sagemaker.ListActionsInput{}),
				func(o *sagemaker.ListActionsOutput) []smtypes.ActionSummary { return o.ActionSummaries })
		},
		func(s smtypes.ActionSummary) (string, string, string, *time.Time) {
			return sv(s.ActionArn), sv(s.ActionName), string(s.Status), s.CreationTime
		})
}

func scanSageMakerContexts(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerContext, "ListContexts", "sagemaker contexts",
		func(c context.Context) ([]smtypes.ContextSummary, error) {
			return collectSMPages(c, sagemaker.NewListContextsPaginator(client, &sagemaker.ListContextsInput{}),
				func(o *sagemaker.ListContextsOutput) []smtypes.ContextSummary { return o.ContextSummaries })
		},
		func(s smtypes.ContextSummary) (string, string, string, *time.Time) {
			return sv(s.ContextArn), sv(s.ContextName), "", s.CreationTime
		})
}

func scanSageMakerExperiments(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerExperiment, "ListExperiments", "sagemaker experiments",
		func(c context.Context) ([]smtypes.ExperimentSummary, error) {
			return collectSMPages(c, sagemaker.NewListExperimentsPaginator(client, &sagemaker.ListExperimentsInput{}),
				func(o *sagemaker.ListExperimentsOutput) []smtypes.ExperimentSummary { return o.ExperimentSummaries })
		},
		func(s smtypes.ExperimentSummary) (string, string, string, *time.Time) {
			return sv(s.ExperimentArn), sv(s.ExperimentName), "", s.CreationTime
		})
}

// scanSageMakerExperimentTrials lists trials then fans out DescribeTrial — the
// trial summary omits the parent ExperimentName the resolver keys on.
func scanSageMakerExperimentTrials(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListTrialsPaginator(client, &sagemaker.ListTrialsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListTrials", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListTrials: %w", perr)
		}
		for _, t := range out.TrialSummaries {
			if t.TrialName != nil {
				names = append(names, *t.TrialName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeTrial(gctx, &sagemaker.DescribeTrialInput{TrialName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeTrial %s: %w", name, derr)
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
	}, st, "sagemaker experiment trials")
}

// scanSageMakerHubs uses manual NextToken pagination — ListHubs has no SDK paginator.
func scanSageMakerHubs(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerHub, "ListHubs", "sagemaker hubs",
		func(c context.Context) ([]smtypes.HubInfo, error) {
			var out []smtypes.HubInfo
			var token *string
			for {
				o, err := client.ListHubs(c, &sagemaker.ListHubsInput{NextToken: token})
				if err != nil {
					return nil, err
				}
				out = append(out, o.HubSummaries...)
				if o.NextToken == nil || *o.NextToken == "" {
					break
				}
				token = o.NextToken
			}
			return out, nil
		},
		func(s smtypes.HubInfo) (string, string, string, *time.Time) {
			return sv(s.HubArn), sv(s.HubName), string(s.HubStatus), s.CreationTime
		})
}

// sagemakerHubContentAttrs embeds the SDK summary (fields stay top-level) and
// adds the parent HubName, which ListHubContents takes as input but does not
// echo on the summary — the hub-content→hub resolver keys on it.
type sagemakerHubContentAttrs struct {
	smtypes.HubContentInfo
	HubName string `json:"HubName"`
}

// scanSageMakerHubContents fans out per scanned hub × {Model, Notebook,
// ModelReference}. ListHubContents requires both HubName and HubContentType and
// has no SDK paginator (manual NextToken). Per-hub×type AccessDenied skips that
// slice silently — a private hub may reject one content type.
func scanSageMakerHubContents(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	hubs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSageMakerHub},
		Limit: util.AllResources,
	})
	if err != nil {
		return 0, 0, err
	}
	contentTypes := []smtypes.HubContentType{
		smtypes.HubContentTypeModel,
		smtypes.HubContentTypeNotebook,
		smtypes.HubContentTypeModelReference,
	}
	var batch []*store.Resource
	for _, h := range hubs {
		if sv(h.Region) != region || h.Name == nil || *h.Name == "" {
			continue
		}
		hubName := *h.Name
		for _, ct := range contentTypes {
			var token *string
			for {
				out, lerr := client.ListHubContents(ctx, &sagemaker.ListHubContentsInput{
					HubName: &hubName, HubContentType: ct, NextToken: token,
				})
				if lerr != nil {
					if isAccessDenied(lerr) {
						break
					}
					return 0, 0, fmt.Errorf("sagemaker:ListHubContents %s/%s: %w", hubName, ct, lerr)
				}
				for _, c := range out.HubContentSummaries {
					arn := sv(c.HubContentArn)
					if arn == "" {
						continue
					}
					name := sv(c.HubContentName)
					status := string(c.HubContentStatus)
					batch = append(batch, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeSageMakerHubContent,
						NativeID:       arn,
						Name:           &name,
						Region:         &region,
						Status:         &status,
						CreatedAt:      tp(c.CreationTime),
						AttributesJSON: mustJSON(sagemakerHubContentAttrs{HubContentInfo: c, HubName: hubName}),
						DiscoveredBy:   scanID,
					})
				}
				if out.NextToken == nil || *out.NextToken == "" {
					break
				}
				token = out.NextToken
			}
		}
	}
	return upsertBatch(st, batch, "sagemaker hub contents")
}

func scanSageMakerHumanTaskUis(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerHumanTaskUI, "ListHumanTaskUis", "sagemaker human task uis",
		func(c context.Context) ([]smtypes.HumanTaskUiSummary, error) {
			return collectSMPages(c, sagemaker.NewListHumanTaskUisPaginator(client, &sagemaker.ListHumanTaskUisInput{}),
				func(o *sagemaker.ListHumanTaskUisOutput) []smtypes.HumanTaskUiSummary { return o.HumanTaskUiSummaries })
		},
		func(s smtypes.HumanTaskUiSummary) (string, string, string, *time.Time) {
			return sv(s.HumanTaskUiArn), sv(s.HumanTaskUiName), "", s.CreationTime
		})
}

func scanSageMakerFlowDefinitions(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerFlowDefinition, "ListFlowDefinitions", "sagemaker flow definitions",
		func(c context.Context) ([]smtypes.FlowDefinitionSummary, error) {
			return collectSMPages(c, sagemaker.NewListFlowDefinitionsPaginator(client, &sagemaker.ListFlowDefinitionsInput{}),
				func(o *sagemaker.ListFlowDefinitionsOutput) []smtypes.FlowDefinitionSummary {
					return o.FlowDefinitionSummaries
				})
		},
		func(s smtypes.FlowDefinitionSummary) (string, string, string, *time.Time) {
			return sv(s.FlowDefinitionArn), sv(s.FlowDefinitionName), string(s.FlowDefinitionStatus), s.CreationTime
		})
}

func scanSageMakerLineageGroups(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerLineageGroup, "ListLineageGroups", "sagemaker lineage groups",
		func(c context.Context) ([]smtypes.LineageGroupSummary, error) {
			return collectSMPages(c, sagemaker.NewListLineageGroupsPaginator(client, &sagemaker.ListLineageGroupsInput{}),
				func(o *sagemaker.ListLineageGroupsOutput) []smtypes.LineageGroupSummary {
					return o.LineageGroupSummaries
				})
		},
		func(s smtypes.LineageGroupSummary) (string, string, string, *time.Time) {
			return sv(s.LineageGroupArn), sv(s.LineageGroupName), "", s.CreationTime
		})
}

func scanSageMakerAlgorithms(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerAlgorithm, "ListAlgorithms", "sagemaker algorithms",
		func(c context.Context) ([]smtypes.AlgorithmSummary, error) {
			return collectSMPages(c, sagemaker.NewListAlgorithmsPaginator(client, &sagemaker.ListAlgorithmsInput{}),
				func(o *sagemaker.ListAlgorithmsOutput) []smtypes.AlgorithmSummary { return o.AlgorithmSummaryList })
		},
		func(s smtypes.AlgorithmSummary) (string, string, string, *time.Time) {
			return sv(s.AlgorithmArn), sv(s.AlgorithmName), string(s.AlgorithmStatus), s.CreationTime
		})
}

func scanSageMakerWorkforces(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerWorkforce, "ListWorkforces", "sagemaker workforces",
		func(c context.Context) ([]smtypes.Workforce, error) {
			return collectSMPages(c, sagemaker.NewListWorkforcesPaginator(client, &sagemaker.ListWorkforcesInput{}),
				func(o *sagemaker.ListWorkforcesOutput) []smtypes.Workforce { return o.Workforces })
		},
		func(s smtypes.Workforce) (string, string, string, *time.Time) {
			return sv(s.WorkforceArn), sv(s.WorkforceName), string(s.Status), s.CreateDate
		})
}

func scanSageMakerComputeQuotas(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerComputeQuota, "ListComputeQuotas", "sagemaker compute quotas",
		func(c context.Context) ([]smtypes.ComputeQuotaSummary, error) {
			return collectSMPages(c, sagemaker.NewListComputeQuotasPaginator(client, &sagemaker.ListComputeQuotasInput{}),
				func(o *sagemaker.ListComputeQuotasOutput) []smtypes.ComputeQuotaSummary {
					return o.ComputeQuotaSummaries
				})
		},
		func(s smtypes.ComputeQuotaSummary) (string, string, string, *time.Time) {
			return sv(s.ComputeQuotaArn), sv(s.Name), string(s.Status), s.CreationTime
		})
}

func scanSageMakerClusterSchedulerConfigs(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerClusterSchedulerConfig, "ListClusterSchedulerConfigs", "sagemaker cluster scheduler configs",
		func(c context.Context) ([]smtypes.ClusterSchedulerConfigSummary, error) {
			return collectSMPages(c, sagemaker.NewListClusterSchedulerConfigsPaginator(client, &sagemaker.ListClusterSchedulerConfigsInput{}),
				func(o *sagemaker.ListClusterSchedulerConfigsOutput) []smtypes.ClusterSchedulerConfigSummary {
					return o.ClusterSchedulerConfigSummaries
				})
		},
		func(s smtypes.ClusterSchedulerConfigSummary) (string, string, string, *time.Time) {
			return sv(s.ClusterSchedulerConfigArn), sv(s.Name), string(s.Status), s.CreationTime
		})
}

func scanSageMakerEdgeDeploymentPlans(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerEdgeDeploymentPlan, "ListEdgeDeploymentPlans", "sagemaker edge deployment plans",
		func(c context.Context) ([]smtypes.EdgeDeploymentPlanSummary, error) {
			return collectSMPages(c, sagemaker.NewListEdgeDeploymentPlansPaginator(client, &sagemaker.ListEdgeDeploymentPlansInput{}),
				func(o *sagemaker.ListEdgeDeploymentPlansOutput) []smtypes.EdgeDeploymentPlanSummary {
					return o.EdgeDeploymentPlanSummaries
				})
		},
		func(s smtypes.EdgeDeploymentPlanSummary) (string, string, string, *time.Time) {
			return sv(s.EdgeDeploymentPlanArn), sv(s.EdgeDeploymentPlanName), "", s.CreationTime
		})
}

func scanSageMakerAIWorkloadConfigs(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerAIWorkloadConfig, "ListAIWorkloadConfigs", "sagemaker ai workload configs",
		func(c context.Context) ([]smtypes.AIWorkloadConfigSummary, error) {
			return collectSMPages(c, sagemaker.NewListAIWorkloadConfigsPaginator(client, &sagemaker.ListAIWorkloadConfigsInput{}),
				func(o *sagemaker.ListAIWorkloadConfigsOutput) []smtypes.AIWorkloadConfigSummary {
					return o.AIWorkloadConfigs
				})
		},
		func(s smtypes.AIWorkloadConfigSummary) (string, string, string, *time.Time) {
			return sv(s.AIWorkloadConfigArn), sv(s.AIWorkloadConfigName), "", s.CreationTime
		})
}

func scanSageMakerMlflowApps(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerMlflowApp, "ListMlflowApps", "sagemaker mlflow apps",
		func(c context.Context) ([]smtypes.MlflowAppSummary, error) {
			return collectSMPages(c, sagemaker.NewListMlflowAppsPaginator(client, &sagemaker.ListMlflowAppsInput{}),
				func(o *sagemaker.ListMlflowAppsOutput) []smtypes.MlflowAppSummary { return o.Summaries })
		},
		func(s smtypes.MlflowAppSummary) (string, string, string, *time.Time) {
			return sv(s.Arn), sv(s.Name), string(s.Status), s.CreationTime
		})
}

func scanSageMakerTrainingPlans(ctx context.Context, client sagemakerGovernanceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerTrainingPlan, "ListTrainingPlans", "sagemaker training plans",
		func(c context.Context) ([]smtypes.TrainingPlanSummary, error) {
			return collectSMPages(c, sagemaker.NewListTrainingPlansPaginator(client, &sagemaker.ListTrainingPlansInput{}),
				func(o *sagemaker.ListTrainingPlansOutput) []smtypes.TrainingPlanSummary {
					return o.TrainingPlanSummaries
				})
		},
		func(s smtypes.TrainingPlanSummary) (string, string, string, *time.Time) {
			// TrainingPlanSummary carries no creation timestamp — only the
			// reservation window (StartTime/EndTime).
			return sv(s.TrainingPlanArn), sv(s.TrainingPlanName), string(s.Status), s.StartTime
		})
}
