package aws

import (
	"context"
	"slices"
	"strings"
	"testing"

	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// An account scan of two regions lists the dashboards once, in CloudWatch's
// once-region, so a second region whose GetDashboard and dashboard tag reads
// fail cannot overwrite the rows: each keeps its body and tags, under the
// global region.
func TestScanAccount_CloudWatchDashboardsReadOnce(t *testing.T) {
	const second = "eu-west-1"
	st := newTestStore(t)
	calls := newCWCalls()
	h := ignoreCtx(cwHandlers(calls))
	h["DescribeRegions"] = func(context.Context, any) (any, error) {
		return &ec2.DescribeRegionsOutput{Regions: []ec2types.Region{{RegionName: ptrStr(testRegion)}, {RegionName: ptrStr(second)}}}, nil
	}
	throttledInSecond := func(op string, fails func(in any) bool) {
		next := h[op]
		h[op] = func(ctx context.Context, in any) (any, error) {
			if awsmw.GetRegion(ctx) == second && fails(in) {
				return nil, cwThrottle
			}
			return next(ctx, in)
		}
	}
	throttledInSecond("GetDashboard", func(any) bool { return true })
	throttledInSecond("ListTagsForResource", func(in any) bool {
		return strings.Contains(sv(in.(*cloudwatch.ListTagsForResourceInput).ResourceARN), ":dashboard/")
	})
	var listRegions parentCalls
	listDashboards := h["ListDashboards"]
	h["ListDashboards"] = func(ctx context.Context, in any) (any, error) {
		listRegions.add(awsmw.GetRegion(ctx))
		return listDashboards(ctx, in)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(h, cwDenied)
	acct.Regions = []string{testRegion, second}

	scanAccount(context.Background(), acct, []string{"aws:cloudwatch", "aws:cloudwatch-dashboards"}, false, st, testScanID)

	if got := listRegions.distinct(); !slices.Equal(got, []string{testRegion}) {
		t.Errorf("ListDashboards called in %v; want only [%s]", got, testRegion)
	}
	if n := calls.count("ListDashboards"); n != 2 {
		t.Errorf("ListDashboards calls = %d; want 2, one per page of one listing", n)
	}
	k := cwKindOf(t, TypeCloudWatchDashboard)
	rows := rowsByNativeID(t, st, TypeCloudWatchDashboard)
	if len(rows) != 2 {
		t.Fatalf("dashboard rows = %v; want 2", nativeIDs(rows))
	}
	for _, suffix := range []string{"tagged", "bare"} {
		r := rows[k.arn(suffix)]
		if sv(r.Region) != "global" {
			t.Errorf("%s Region = %q; want global", k.arn(suffix), sv(r.Region))
		}
		assertJSONEqual(t, k.arn(suffix)+" attrs", r.AttributesJSON, cwWantAttrs(k, suffix))
	}
	assertTagsMap(t, rows[k.arn("tagged")].TagsJSON, map[string]string{"type": TypeCloudWatchDashboard})
}
