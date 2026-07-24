package aws

import (
	"context"
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/applicationautoscaling"
	aastypes "github.com/aws/aws-sdk-go-v2/service/applicationautoscaling/types"
)

// stubApplicationAutoScaling is a deterministic in-memory replacement for
// applicationAutoScalingAPI used by the scanner tests. Both methods key
// canned responses by ServiceNamespace so per-namespace fan-out is
// exercised.
type stubApplicationAutoScaling struct {
	targetsByNS  map[aastypes.ServiceNamespace]*applicationautoscaling.DescribeScalableTargetsOutput
	policiesByNS map[aastypes.ServiceNamespace]*applicationautoscaling.DescribeScalingPoliciesOutput
}

func (s *stubApplicationAutoScaling) DescribeScalableTargets(_ context.Context, in *applicationautoscaling.DescribeScalableTargetsInput, _ ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalableTargetsOutput, error) {
	if out, ok := s.targetsByNS[in.ServiceNamespace]; ok {
		return out, nil
	}
	return &applicationautoscaling.DescribeScalableTargetsOutput{}, nil
}

func (s *stubApplicationAutoScaling) DescribeScalingPolicies(_ context.Context, in *applicationautoscaling.DescribeScalingPoliciesInput, _ ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalingPoliciesOutput, error) {
	if out, ok := s.policiesByNS[in.ServiceNamespace]; ok {
		return out, nil
	}
	return &applicationautoscaling.DescribeScalingPoliciesOutput{}, nil
}

func TestApplicationAutoScalingScalableTargetNativeID(t *testing.T) {
	got := applicationAutoScalingScalableTargetNativeID("us-east-1", "111111111111", "ecs", "service/cluster/svc", "ecs:service:DesiredCount")
	want := "arn:aws:application-autoscaling:us-east-1:111111111111:scalable-target/ecs/service/cluster/svc/ecs:service:DesiredCount"
	if got != want {
		t.Errorf("native id = %q, want %q", got, want)
	}
}

func TestScanApplicationAutoScalingScalableTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	rid := "service/cluster/svc"
	dim := aastypes.ScalableDimension("ecs:service:DesiredCount")
	stub := &stubApplicationAutoScaling{
		targetsByNS: map[aastypes.ServiceNamespace]*applicationautoscaling.DescribeScalableTargetsOutput{
			aastypes.ServiceNamespaceEcs: {ScalableTargets: []aastypes.ScalableTarget{{
				ResourceId:        &rid,
				ScalableDimension: dim,
			}}},
		},
	}
	total, inserted, err := scanApplicationAutoScalingScalableTargets(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanApplicationAutoScalingScalableTargets: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	want := applicationAutoScalingScalableTargetNativeID(region, acct.ID, "ecs", rid, string(dim))
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationAutoScalingScalableTarget}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != want {
		t.Errorf("rows=%+v, want one row with NativeID %s", rows, want)
	}
}

func TestScanApplicationAutoScalingScalingPolicies(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	arn := "arn:aws:autoscaling:us-east-1:123456789012:scalingPolicy:abc:resource/ecs/service/cluster/svc:policyName/foo"
	name := "foo"
	stub := &stubApplicationAutoScaling{
		policiesByNS: map[aastypes.ServiceNamespace]*applicationautoscaling.DescribeScalingPoliciesOutput{
			aastypes.ServiceNamespaceEcs: {ScalingPolicies: []aastypes.ScalingPolicy{{
				PolicyARN:  &arn,
				PolicyName: &name,
			}}},
		},
	}
	total, inserted, err := scanApplicationAutoScalingScalingPolicies(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanApplicationAutoScalingScalingPolicies: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationAutoScalingScalingPolicy}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != arn {
		t.Errorf("rows=%+v, want one row with NativeID %s", rows, arn)
	}
}
