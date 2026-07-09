package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/glue"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGlueJob, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueTrigger, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueWorkflow, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueMLTransform, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueDevEndpoint, Service: "glue"})
}

// scanGlueJobs runs Job/Trigger/Workflow/MLTransform/DevEndpoint phases.
// All five share the GetXxx-paginator-or-list-then-get pattern; ARNs are
// synthesized — Glue SDK responses for these resources expose only Name/Id.
func scanGlueJobs(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanGlueJobsPhase(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueTriggers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueWorkflows(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueMLTransforms(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueDevEndpoints(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func glueResourceARN(region, accountID, kind, id string) string {
	return fmt.Sprintf("arn:aws:glue:%s:%s:%s/%s", region, accountID, kind, id)
}

func scanGlueJobsPhase(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewGetJobsPaginator(client, &glue.GetJobsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:GetJobs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:GetJobs: %w", perr)
		}
		for _, j := range out.Jobs {
			name := sv(j.Name)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueJob,
				NativeID:       glueResourceARN(region, acct.ID, "job", name),
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(j),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue jobs: %w", uerr)
	}
	return len(batch), n, nil
}

func scanGlueTriggers(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewGetTriggersPaginator(client, &glue.GetTriggersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:GetTriggers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:GetTriggers: %w", perr)
		}
		for _, t := range out.Triggers {
			name := sv(t.Name)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueTrigger,
				NativeID:       glueResourceARN(region, acct.ID, "trigger", name),
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(t),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue triggers: %w", uerr)
	}
	return len(batch), n, nil
}

// scanGlueWorkflows lists workflow names then fans-out GetWorkflow
// per-name — no GetWorkflows plural API.
func scanGlueWorkflows(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewListWorkflowsPaginator(client, &glue.ListWorkflowsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:ListWorkflows", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:ListWorkflows: %w", perr)
		}
		names = append(names, out.Workflows...)
	}
	if len(names) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, name := range names {
		n := name
		out, derr := client.GetWorkflow(ctx, &glue.GetWorkflowInput{Name: &n})
		if derr != nil {
			if isAccessDenied(derr) {
				continue
			}
			return 0, 0, fmt.Errorf("glue:GetWorkflow %s: %w", name, derr)
		}
		if out.Workflow == nil {
			continue
		}
		wname := sv(out.Workflow.Name)
		if wname == "" {
			continue
		}
		nm := wname
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeGlueWorkflow,
			NativeID:       glueResourceARN(region, acct.ID, "workflow", wname),
			Name:           &nm,
			Region:         &region,
			AttributesJSON: mustJSON(out.Workflow),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue workflows: %w", uerr)
	}
	return len(batch), n, nil
}

func scanGlueMLTransforms(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewGetMLTransformsPaginator(client, &glue.GetMLTransformsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:GetMLTransforms", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:GetMLTransforms: %w", perr)
		}
		for _, m := range out.Transforms {
			id := sv(m.TransformId)
			if id == "" {
				continue
			}
			label := sv(m.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueMLTransform,
				NativeID:       glueResourceARN(region, acct.ID, "mlTransform", id),
				Name:           &label,
				Region:         &region,
				AttributesJSON: mustJSON(m),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue ml-transforms: %w", uerr)
	}
	return len(batch), n, nil
}

func scanGlueDevEndpoints(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewGetDevEndpointsPaginator(client, &glue.GetDevEndpointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:GetDevEndpoints", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:GetDevEndpoints: %w", perr)
		}
		for _, d := range out.DevEndpoints {
			name := sv(d.EndpointName)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueDevEndpoint,
				NativeID:       glueResourceARN(region, acct.ID, "devEndpoint", name),
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue dev-endpoints: %w", uerr)
	}
	return len(batch), n, nil
}
