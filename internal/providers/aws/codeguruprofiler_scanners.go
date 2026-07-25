package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/codeguruprofiler"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeGuruProfilerProfilingGroup, Service: "code-guru-profiler", Upstream: "AWS::CodeGuruProfiler::ProfilingGroup", Leaf: true})
	registerService(serviceEntry{
		name: "aws:code-guru-profiler",
		fn:   scanCodeGuruProfiler,
	})
}

// scanCodeGuruProfiler discovers CodeGuru Profiler profiling groups.
func scanCodeGuruProfiler(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := codeguruprofiler.NewFromConfig(acct.cfg, func(o *codeguruprofiler.Options) { o.Region = region })

	includeDesc := true
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListProfilingGroups(ctx, &codeguruprofiler.ListProfilingGroupsInput{
			IncludeDescription: &includeDesc,
			NextToken:          nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codeguru-profiler:ListProfilingGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codeguru-profiler:ListProfilingGroups: %w", err)
		}
		for _, p := range out.ProfilingGroups {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeGuruProfilerProfilingGroup, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "code-guru-profiler profiling-groups")
}
