package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// A denied ListServices keeps the paginator's token, so a retry of the page
// loop re-sends the same request until the deadline and loses every later
// phase. A denied cluster must stop its own listing, keep the services listed
// before the denial, warn once, and leave other clusters and later phases to run.
func TestScanECS_ListServicesDenied(t *testing.T) {
	cluster2ARN := fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/cl-2", testRegion, testAccountID)
	// cl-3 is always denied, so a warning per denied cluster shows as two.
	cluster3ARN := fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/cl-3", testRegion, testAccountID)
	svc1ARN := fmt.Sprintf("arn:aws:ecs:%s:%s:service/cl-1/svc-1", testRegion, testAccountID)
	svc2ARN := fmt.Sprintf("arn:aws:ecs:%s:%s:service/cl-2/svc-2", testRegion, testAccountID)
	taskDefARN := fmt.Sprintf("arn:aws:ecs:%s:%s:task-definition/web:1", testRegion, testAccountID)

	cases := []struct {
		name string
		// deniedPage is the cl-1 ListServices page that is denied; page 1
		// returns svc-1 when the denial is on page 2.
		deniedPage   int
		wantServices []string
	}{
		{"denied on first page", 1, []string{svc2ARN}},
		{"denied on second page", 2, []string{svc1ARN, svc2ARN}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, "ecs:ListServices")

			// Keyed by cluster and page token. Every ListServices caller pages
			// sequentially, so the map needs no lock.
			calls := map[string]int{}
			h := ignoreCtx(ecsTopLevelHandlers())
			h["ListClusters"] = func(context.Context, any) (any, error) {
				return &ecs.ListClustersOutput{ClusterArns: []string{ecsTestClusterARN, cluster2ARN, cluster3ARN}}, nil
			}
			h["DescribeClusters"] = func(context.Context, any) (any, error) {
				return &ecs.DescribeClustersOutput{Clusters: []ecstypes.Cluster{
					{ClusterArn: sdkaws.String(ecsTestClusterARN), ClusterName: sdkaws.String("cl-1")},
					{ClusterArn: sdkaws.String(cluster2ARN), ClusterName: sdkaws.String("cl-2")},
					{ClusterArn: sdkaws.String(cluster3ARN), ClusterName: sdkaws.String("cl-3")},
				}}, nil
			}
			h["ListServices"] = func(ctx context.Context, in any) (any, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				req := in.(*ecs.ListServicesInput)
				cluster, token := sdkaws.ToString(req.Cluster), sdkaws.ToString(req.NextToken)
				calls[cluster+"|"+token]++
				switch cluster {
				case cluster2ARN:
					return &ecs.ListServicesOutput{ServiceArns: []string{svc2ARN}}, nil
				case cluster3ARN:
					return nil, ecsDenied
				}
				page := 1
				if token == "p2" {
					page = 2
				}
				if page == tc.deniedPage {
					return nil, ecsDenied
				}
				return &ecs.ListServicesOutput{ServiceArns: []string{svc1ARN}, NextToken: sdkaws.String("p2")}, nil
			}
			h["DescribeServices"] = func(_ context.Context, in any) (any, error) {
				out := &ecs.DescribeServicesOutput{}
				for _, arn := range in.(*ecs.DescribeServicesInput).Services {
					out.Services = append(out.Services, ecstypes.Service{ServiceArn: sdkaws.String(arn), ServiceName: sdkaws.String(arn)})
				}
				return out, nil
			}
			h["ListTaskDefinitions"] = func(context.Context, any) (any, error) {
				return &ecs.ListTaskDefinitionsOutput{TaskDefinitionArns: []string{taskDefARN}}, nil
			}
			h["DescribeTaskDefinition"] = func(context.Context, any) (any, error) {
				return &ecs.DescribeTaskDefinitionOutput{TaskDefinition: &ecstypes.TaskDefinition{
					TaskDefinitionArn: sdkaws.String(taskDefARN), Family: sdkaws.String("web"), Revision: 1,
				}}, nil
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfigCtx(h, ecsDenied)}

			// The deadline turns a retry loop into a failure instead of a hang.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _, err := scanECS(ctx, acct, testRegion, st, testScanID)
			if err != nil {
				t.Fatalf("scan: %v; want nil (the denial is a warning)", err)
			}
			for key, n := range calls {
				if n > 3 {
					t.Errorf("ListServices %s sent %d times; want at most 3", key, n)
				}
			}
			if *warnings != 1 {
				t.Errorf("ecs:ListServices warnings = %d; want 1", *warnings)
			}
			services := rowsByNativeID(t, st, TypeECSService)
			if len(services) != len(tc.wantServices) {
				t.Errorf("service rows = %d; want %d (%v)", len(services), len(tc.wantServices), tc.wantServices)
			}
			for _, arn := range tc.wantServices {
				if _, ok := services[arn]; !ok {
					t.Errorf("service row %s missing", arn)
				}
			}
			if _, ok := rowsByNativeID(t, st, TypeECSTaskDefinition)[taskDefARN]; !ok {
				t.Error("task-definition row missing: the phase after services did not run")
			}
		})
	}
}
