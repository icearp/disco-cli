package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGlueJob, Service: "glue", Redact: glueJobRedact()})
	registerType(restype.Descriptor{Type: TypeGlueTrigger, Service: "glue", Redact: glueIfSensitive("Actions[*].Arguments.*")})
	registerType(restype.Descriptor{Type: TypeGlueWorkflow, Service: "glue", Redact: glueIfSensitive(
		"DefaultRunProperties.*",
		"LastRun.WorkflowRunProperties.*",
		"Graph.Nodes[*].TriggerDetails.Trigger.Actions[*].Arguments.*",
		"Graph.Nodes[*].JobDetails.JobRuns[*].Arguments.*",
		"LastRun.Graph.Nodes[*].TriggerDetails.Trigger.Actions[*].Arguments.*",
		"LastRun.Graph.Nodes[*].JobDetails.JobRuns[*].Arguments.*",
	)})
	registerType(restype.Descriptor{Type: TypeGlueMLTransform, Service: "glue", Redact: glueIfSensitive("InputRecordTables[*].AdditionalOptions.*")})
	// PublicKey and PublicKeys are SSH public keys, not secrets, so they are kept.
	registerType(restype.Descriptor{Type: TypeGlueDevEndpoint, Service: "glue", Redact: glueIfSensitive("Arguments.*")})
}

// glueIfSensitive judges each value under paths by its key name and shape
// (redact.RedactIfSensitive): Glue's argument and option maps hold settings
// ("--job-language", "--TempDir") under keys the user chooses, so a secret
// passed in one ("--password", "secretKey") is caught by its key name or by a
// credential shape in its value. A secret under a key that does not say so,
// in a value of no credential shape, is kept.
func glueIfSensitive(paths ...string) []redact.Rule {
	rules := make([]redact.Rule, len(paths))
	for i, p := range paths {
		rules[i] = redact.Rule{Path: p, Mode: redact.RedactIfSensitive}
	}
	return rules
}

// glueJobRedact covers the job's arguments, its source-control token and
// every string-map option of its visual ETL nodes (CodeGenConfigurationNodes,
// keyed by node id), where a connector's options can carry a password.
// Free-text code and SQL (CustomCode.Code, SparkSQL.SqlQuery,
// JDBCConnectorSource.Query, the Redshift and Snowflake nodes'
// Data.SampleQuery) are not judged: a credential written into them is kept,
// a best-effort residual.
// AuthToken holds a personal access token, or a Secrets Manager reference
// when AuthStrategy is AWS_SECRETS_MANAGER, so only a reference is kept (its
// key name already marks it as a secret).
func glueJobRedact() []redact.Rule {
	paths := []string{"DefaultArguments.*", "NonOverridableArguments.*"}
	for _, node := range []string{
		"CatalogDeltaSource.AdditionalDeltaOptions",
		"CatalogHudiSource.AdditionalHudiOptions",
		"CatalogIcebergSource.AdditionalIcebergOptions",
		"ConnectorDataSource.Data",
		"ConnectorDataTarget.Data",
		"EvaluateDataQualityMultiFrame.AdditionalDataSources",
		"EvaluateDataQualityMultiFrame.AdditionalOptions",
		"JDBCConnectorTarget.AdditionalOptions",
		"Recipe.RecipeSteps[*].Action.Parameters",
		"S3CatalogDeltaSource.AdditionalDeltaOptions",
		"S3CatalogHudiSource.AdditionalHudiOptions",
		"S3CatalogIcebergSource.AdditionalIcebergOptions",
		"S3DeltaCatalogTarget.AdditionalOptions",
		"S3DeltaDirectTarget.AdditionalOptions",
		"S3DeltaSource.AdditionalDeltaOptions",
		"S3HudiCatalogTarget.AdditionalOptions",
		"S3HudiDirectTarget.AdditionalOptions",
		"S3HudiSource.AdditionalHudiOptions",
		"S3IcebergCatalogTarget.AdditionalOptions",
		"S3IcebergDirectTarget.AdditionalOptions",
		"SnowflakeSource.Data.AdditionalOptions",
		"SnowflakeTarget.Data.AdditionalOptions",
		"SparkConnectorSource.AdditionalOptions",
		"SparkConnectorTarget.AdditionalOptions",
	} {
		paths = append(paths, "CodeGenConfigurationNodes.*."+node+".*")
	}
	rules := glueIfSensitive(paths...)
	for _, node := range []string{"AmazonRedshiftSource", "AmazonRedshiftTarget"} {
		rules = append(rules, redact.Rule{Path: "CodeGenConfigurationNodes.*." + node + ".Data.AdvancedOptions[*].Value", Mode: redact.RedactIfSensitive, KeyField: "Key"})
	}
	return append(rules, redact.Rule{Path: "SourceControlDetails.AuthToken", Mode: redact.RedactUnlessSafe})
}

// The job, trigger, workflow, ML transform and dev endpoint elements expose
// only a name or id, so their NativeIDs are built in each type's documented
// ARN format (glueResourceARN).

func scanGlueJobsPhase(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "glue:GetJobs",
		func(ctx context.Context, token *string) ([]gluetypes.Job, *string, error) {
			out, err := client.GetJobs(ctx, &glue.GetJobsInput{NextToken: token, MaxResults: sdkaws.Int32(gluePageSize)})
			if err != nil {
				return nil, nil, err
			}
			return out.Jobs, out.NextToken, nil
		},
		func(j gluetypes.Job) (*store.Resource, string, string) {
			return glueNamedRow(acct, region, scanID, TypeGlueJob, "job", "jobs", sv(j.Name), "", j)
		}, tags, p)
}

func scanGlueTriggers(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "glue:GetTriggers",
		func(ctx context.Context, token *string) ([]gluetypes.Trigger, *string, error) {
			out, err := client.GetTriggers(ctx, &glue.GetTriggersInput{NextToken: token, MaxResults: sdkaws.Int32(glueOrchestrationPageSize200)})
			if err != nil {
				return nil, nil, err
			}
			return out.Triggers, out.NextToken, nil
		},
		func(t gluetypes.Trigger) (*store.Resource, string, string) {
			return glueNamedRow(acct, region, scanID, TypeGlueTrigger, "trigger", "triggers", sv(t.Name), sv(t.Id), t)
		}, tags, p)
}

// glueWorkflowRead is one workflow's GetWorkflow result.
type glueWorkflowRead struct {
	workflow *gluetypes.Workflow
	err      error
}

// scanGlueWorkflows lists the workflow names, reads each workflow with
// GetWorkflow, graph included (resolveGlueWorkflowGraphNodes reads it), and
// stores them with their tags. ListWorkflows returns names only, so a
// workflow whose GetWorkflow fails has no element to store and is not
// stored: one GetWorkflow no longer finds (EntityNotFoundException, deleted
// after it was listed) silently, a denied one with one warning once the
// write succeeded, and any other error is returned after the others are
// stored, a transient one apart so that it is demoted beside a hard error.
func scanGlueWorkflows(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "glue:GetWorkflow"
	names, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]string, *string, error) {
		out, err := client.ListWorkflows(ctx, &glue.ListWorkflowsInput{NextToken: token, MaxResults: sdkaws.Int32(glueOrchestrationPageSize25)})
		if err != nil {
			return nil, nil, err
		}
		return out.Workflows, out.NextToken, nil
	})
	reads, stopped := readEachKey(ctx, names, func(ctx context.Context, name string) glueWorkflowRead {
		out, err := client.GetWorkflow(ctx, &glue.GetWorkflowInput{Name: &name, IncludeGraph: sdkaws.Bool(true)})
		if err != nil {
			return glueWorkflowRead{err: err}
		}
		return glueWorkflowRead{workflow: out.Workflow}
	})
	var (
		rows       []*store.Resource
		skips      notStoredSkips
		denied     skipTally
		transients transientTally
		errs       []error
	)
	for i, read := range reads {
		name := names[i]
		switch err := read.err; {
		case err == nil:
			if read.workflow == nil || sv(read.workflow.Name) == "" {
				skips.skipped("workflows were read without a name", cmp.Or(name, "(a workflow listed with no name)"))
				continue
			}
			wname := sv(read.workflow.Name)
			rows = append(rows, glueRow(acct, region, scanID, TypeGlueWorkflow, glueResourceARN(region, acct.ID, "workflow", wname), wname, read.workflow))
		case isAPIErrorCode(err, "EntityNotFoundException"):
		case isAccessDenied(err):
			denied.skipped(name, err)
		case transients.drop(name, err):
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", op, name, err))
		}
	}
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: %w", op, stopped))
	}
	var transient error
	if t := &transients.skipTally; t.count > 0 {
		transient = fmt.Errorf("%s: %d workflow(s) not read, so not stored, first %s: %w", op, t.count, t.first, t.err)
	}
	hard := errors.Join(errs...)
	warn := func() {
		skips.warn(st, op, acct, region)
		if denied.count > 0 {
			_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf("%d workflow(s) not stored, first %s: %w", denied.count, denied.first, denied.err))
		}
	}
	if storeRowsTags(ctx, st, acct, region, "glue:ListWorkflows", rows, listErr, warn, tags, p) {
		p.errs.add(op, errors.Join(hard, transient))
		return true
	}
	p.errs.add(op, hard)
	p.errs.add(op, transient)
	return false
}

func scanGlueMLTransforms(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "glue:GetMLTransforms",
		func(ctx context.Context, token *string) ([]gluetypes.MLTransform, *string, error) {
			out, err := client.GetMLTransforms(ctx, &glue.GetMLTransformsInput{NextToken: token, MaxResults: sdkaws.Int32(gluePageSize)})
			if err != nil {
				return nil, nil, err
			}
			return out.Transforms, out.NextToken, nil
		},
		func(m gluetypes.MLTransform) (*store.Resource, string, string) {
			id := sv(m.TransformId)
			if id == "" {
				return nil, "ML transforms have an empty TransformId", cmp.Or(sv(m.Name), "(a transform with no name)")
			}
			return glueRow(acct, region, scanID, TypeGlueMLTransform, glueResourceARN(region, acct.ID, "mlTransform", id), cmp.Or(sv(m.Name), id), m), "", ""
		}, tags, p)
}

func scanGlueDevEndpoints(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "glue:GetDevEndpoints",
		func(ctx context.Context, token *string) ([]gluetypes.DevEndpoint, *string, error) {
			out, err := client.GetDevEndpoints(ctx, &glue.GetDevEndpointsInput{NextToken: token, MaxResults: sdkaws.Int32(gluePageSize)})
			if err != nil {
				return nil, nil, err
			}
			return out.DevEndpoints, out.NextToken, nil
		},
		func(d gluetypes.DevEndpoint) (*store.Resource, string, string) {
			return glueNamedRow(acct, region, scanID, TypeGlueDevEndpoint, "devEndpoint", "dev endpoints", sv(d.EndpointName), "", d)
		}, tags, p)
}
