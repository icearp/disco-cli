package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/dataproc/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:dataproc",
		fn:   scanDataproc,
		emits: []coverage.TypeDecl{
			{Service: "dataproc", DiscoType: TypeDataprocCluster},
			{Service: "dataproc", DiscoType: TypeDataprocAutoscalingPolicy, Leaf: true},
			{Service: "dataproc", DiscoType: TypeDataprocBatch},
			{Service: "dataproc", DiscoType: TypeDataprocSession},
			{Service: "dataproc", DiscoType: TypeDataprocSessionTemplate},
			{Service: "dataproc", DiscoType: TypeDataprocWorkflowTemplate},
			{Service: "dataproc", DiscoType: TypeDataprocJob},
		},
	})
}

// scanDataproc discovers Dataproc clusters, autoscaling policies, batches,
// sessions, session templates, workflow templates, and jobs — all in every
// enabled region of the project. Dataproc has no aggregated/wildcard
// endpoint, so every phase delegates to the per-region gcpRegionFanoutScan
// helper (region doubles as "location" for the session-based services;
// SDK doc confirms both terms name the same resource-name segment).
func scanDataproc(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := dataproc.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("dataproc client: %w", err)
	}
	return scanDataprocWithClient(ctx, svc, p, st, scanID)
}

// scanDataprocWithClient is the test seam for scanDataproc — takes the
// pre-built client directly so tests can point it at a fake server. Resolves
// the project's enabled regions once via gcpRegions, then threads the same
// list into every phase via gcpRegionFanoutScanIn — avoids 6 redundant
// Regions.List calls (one per phase) that gcpRegionFanoutScan would each
// issue independently.
func scanDataprocWithClient(ctx context.Context, svc *dataproc.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	regions, err := gcpRegions(ctx, p)
	if err != nil {
		return 0, 0, err
	}
	return scanDataprocIn(ctx, svc, p, st, scanID, regions)
}

// scanDataprocIn is the testable core of scanDataprocWithClient: takes a
// pre-resolved region slice instead of calling gcpRegions, so tests can
// inject an arbitrary region list without faking the Compute regions API.
func scanDataprocIn(ctx context.Context, svc *dataproc.Service, p *project, st *store.Store, scanID string, regions []string) (total, inserted int, err error) {
	t, n, err := gcpRegionFanoutScanIn(
		ctx, p, st, fanoutMed, regions, "dataproc:clusters.list",
		func(region string) pager[dataproc.ListClustersResponse] {
			return svc.Projects.Regions.Clusters.List(p.ID, region)
		},
		func(page *dataproc.ListClustersResponse) []*dataproc.Cluster { return page.Clusters },
		func(c *dataproc.Cluster, region string) *store.Resource {
			if c == nil || c.ClusterName == "" {
				return nil
			}
			name := c.ClusterName
			reg := region
			return &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeDataprocCluster,
				NativeID:       fmt.Sprintf("projects/%s/regions/%s/clusters/%s", p.ID, reg, c.ClusterName),
				Name:           &name,
				Region:         &reg,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			}
		},
	)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = gcpRegionFanoutScanIn(
		ctx, p, st, fanoutMed, regions, "dataproc:autoscalingPolicies.list",
		func(region string) pager[dataproc.ListAutoscalingPoliciesResponse] {
			return svc.Projects.Regions.AutoscalingPolicies.List(fmt.Sprintf("projects/%s/regions/%s", p.ID, region))
		},
		func(page *dataproc.ListAutoscalingPoliciesResponse) []*dataproc.AutoscalingPolicy {
			return page.Policies
		},
		func(ap *dataproc.AutoscalingPolicy, region string) *store.Resource {
			if ap == nil || ap.Name == "" {
				return nil
			}
			name := lastSegment(ap.Name)
			reg := region
			return &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeDataprocAutoscalingPolicy,
				NativeID:       ap.Name,
				Name:           &name,
				Region:         &reg,
				AttributesJSON: mustJSON(ap),
				DiscoveredBy:   scanID,
			}
		},
	)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = gcpRegionFanoutScanIn(
		ctx, p, st, fanoutMed, regions, "dataproc:batches.list",
		func(region string) pager[dataproc.ListBatchesResponse] {
			return svc.Projects.Locations.Batches.List(fmt.Sprintf("projects/%s/locations/%s", p.ID, region))
		},
		func(page *dataproc.ListBatchesResponse) []*dataproc.Batch { return page.Batches },
		func(b *dataproc.Batch, region string) *store.Resource {
			if b == nil || b.Name == "" {
				return nil
			}
			name := lastSegment(b.Name)
			reg := region
			return &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeDataprocBatch,
				NativeID:       b.Name,
				Name:           &name,
				Region:         &reg,
				Status:         strp(b.State),
				CreatedAt:      strp(b.CreateTime),
				AttributesJSON: mustJSON(b),
				DiscoveredBy:   scanID,
			}
		},
	)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = gcpRegionFanoutScanIn(
		ctx, p, st, fanoutMed, regions, "dataproc:sessions.list",
		func(region string) pager[dataproc.ListSessionsResponse] {
			return svc.Projects.Locations.Sessions.List(fmt.Sprintf("projects/%s/locations/%s", p.ID, region))
		},
		func(page *dataproc.ListSessionsResponse) []*dataproc.Session { return page.Sessions },
		func(s *dataproc.Session, region string) *store.Resource {
			if s == nil || s.Name == "" {
				return nil
			}
			name := lastSegment(s.Name)
			reg := region
			return &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeDataprocSession,
				NativeID:       s.Name,
				Name:           &name,
				Region:         &reg,
				Status:         strp(s.State),
				CreatedAt:      strp(s.CreateTime),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			}
		},
	)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = gcpRegionFanoutScanIn(
		ctx, p, st, fanoutMed, regions, "dataproc:sessionTemplates.list",
		func(region string) pager[dataproc.ListSessionTemplatesResponse] {
			return svc.Projects.Locations.SessionTemplates.List(fmt.Sprintf("projects/%s/locations/%s", p.ID, region))
		},
		func(page *dataproc.ListSessionTemplatesResponse) []*dataproc.SessionTemplate {
			return page.SessionTemplates
		},
		func(tmpl *dataproc.SessionTemplate, region string) *store.Resource {
			if tmpl == nil || tmpl.Name == "" {
				return nil
			}
			name := lastSegment(tmpl.Name)
			reg := region
			return &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeDataprocSessionTemplate,
				NativeID:       tmpl.Name,
				Name:           &name,
				Region:         &reg,
				CreatedAt:      strp(tmpl.CreateTime),
				AttributesJSON: mustJSON(tmpl),
				DiscoveredBy:   scanID,
			}
		},
	)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = gcpRegionFanoutScanIn(
		ctx, p, st, fanoutMed, regions, "dataproc:workflowTemplates.list",
		func(region string) pager[dataproc.ListWorkflowTemplatesResponse] {
			return svc.Projects.Regions.WorkflowTemplates.List(fmt.Sprintf("projects/%s/regions/%s", p.ID, region))
		},
		func(page *dataproc.ListWorkflowTemplatesResponse) []*dataproc.WorkflowTemplate { return page.Templates },
		func(wt *dataproc.WorkflowTemplate, region string) *store.Resource {
			if wt == nil || wt.Name == "" {
				return nil
			}
			name := lastSegment(wt.Name)
			reg := region
			return &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeDataprocWorkflowTemplate,
				NativeID:       wt.Name,
				Name:           &name,
				Region:         &reg,
				CreatedAt:      strp(wt.CreateTime),
				AttributesJSON: mustJSON(wt),
				DiscoveredBy:   scanID,
			}
		},
	)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = gcpRegionFanoutScanIn(
		ctx, p, st, fanoutMed, regions, "dataproc:jobs.list",
		func(region string) pager[dataproc.ListJobsResponse] {
			return svc.Projects.Regions.Jobs.List(p.ID, region)
		},
		func(page *dataproc.ListJobsResponse) []*dataproc.Job { return page.Jobs },
		func(j *dataproc.Job, region string) *store.Resource {
			if j == nil || j.Reference == nil || j.Reference.JobId == "" {
				return nil
			}
			name := j.Reference.JobId
			reg := region
			var status *string
			if j.Status != nil {
				status = strp(j.Status.State)
			}
			return &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeDataprocJob,
				NativeID:       fmt.Sprintf("projects/%s/regions/%s/jobs/%s", p.ID, reg, j.Reference.JobId),
				Name:           &name,
				Region:         &reg,
				Status:         status,
				AttributesJSON: mustJSON(j),
				DiscoveredBy:   scanID,
			}
		},
	)
	total += t
	inserted += n
	return total, inserted, err
}

// fanoutMed is the per-region concurrency cap for region fan-out scanners.
// Tuned to match GCP's typical regional-API quotas (~100 req/s per region)
// while keeping total in-flight call count modest.
const fanoutMed = 10
