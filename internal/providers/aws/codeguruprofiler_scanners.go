package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/codeguruprofiler"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeGuruProfilerProfilingGroup, Service: "code-guru-profiler"})
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
		if err != nil && nextToken == nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, cgpListOp, acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("%s: %w", cgpListOp, err)
		}
		if err != nil {
			return cgpKeepListed(st, batch, acct, region, err)
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
				AttributesJSON: mustJSON(p), TagsJSON: mapTagsJSON(p.Tags), DiscoveredBy: scanID,
			})
		}
		last, terr := lastPage(nextToken, out.NextToken)
		if last {
			return upsertListed(st, batch, cgpListOp, terr)
		}
		nextToken = out.NextToken
	}
}

const cgpListOp = "codeguru-profiler:ListProfilingGroups"

// cgpKeepListed stores the profiling groups read before a later page failed.
// A denial warns once they are stored and returns nil; any other error is
// returned.
func cgpKeepListed(st *store.Store, batch []*store.Resource, acct *account, region string, err error) (int, int, error) {
	if !isAccessDenied(err) {
		return upsertListed(st, batch, cgpListOp, err)
	}
	total, inserted, uerr := upsertListed(st, batch, cgpListOp, nil)
	if uerr == nil {
		_ = skipIfAccessDenied(st, cgpListOp, acct.ID, region, fmt.Errorf(
			"profiling groups on this and later pages not stored: %w", err))
	}
	return total, inserted, uerr
}
