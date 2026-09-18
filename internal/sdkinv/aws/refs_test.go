package aws

import (
	"slices"
	"testing"
)

// refModel builds a tiny Smithy model: one operation whose output is the
// named shape, plus whatever shapes the case needs.
func refModel(shapes map[string]*shape, output string) (*smithyModel, *shape) {
	m := &smithyModel{Shapes: shapes}
	op := &shape{Type: "operation", Output: &ref{Target: output}}
	return m, op
}

func str(name string) *member { return &member{Target: name} }

// TestRefsOf_FlatAndWrappedDetailReads: a detail read has no collection, so
// recognising only the single-structure output shape left 1,275 of them
// refless and presented 87 real resolver gaps as finished derived leaves.
func TestRefsOf_FlatAndWrappedDetailReads(t *testing.T) {
	// Flat beside a structure list, as m2's GetEnvironment answers.
	m, op := refModel(map[string]*shape{
		"ns#GetEnvironmentResponse": {Type: "structure", Members: map[string]*member{
			"name":                  str("smithy.api#String"),
			"environmentArn":        str("ns#Arn"),
			"vpcId":                 str("ns#Str"),
			"subnetIds":             str("ns#StrList"),
			"loadBalancerArn":       str("smithy.api#String"),
			"clientToken":           str("smithy.api#String"),
			"storageConfigurations": str("ns#StorageConfigList"),
		}},
		"ns#Arn":               {Type: "string"},
		"ns#Str":               {Type: "string"},
		"ns#StrList":           {Type: "list", Member: str("ns#Str")},
		"ns#StorageConfigList": {Type: "list", Member: str("ns#StorageConfig")},
		"ns#StorageConfig":     {Type: "structure", Members: map[string]*member{"efsId": str("ns#Str")}},
	}, "ns#GetEnvironmentResponse")
	want := []string{"efsId", "loadBalancerArn", "subnetIds", "vpcId"}
	if got := refsOf(m, op, "environment"); !slices.Equal(got, want) {
		t.Errorf("flat detail read refs = %v, want %v", got, want)
	}

	// Wrapped beside bookkeeping, as DescribeDashboard answers.
	m, op = refModel(map[string]*shape{
		"ns#DescribeDashboardResponse": {Type: "structure", Members: map[string]*member{
			"Dashboard": str("ns#Dashboard"),
			"RequestId": str("smithy.api#String"),
			"Status":    str("ns#StatusCode"),
		}},
		"ns#StatusCode": {Type: "integer"},
		"ns#Dashboard": {Type: "structure", Members: map[string]*member{
			"DashboardId": str("ns#Str"),
			"ThemeArn":    str("ns#Str"),
		}},
		"ns#Str": {Type: "string"},
	}, "ns#DescribeDashboardResponse")
	if got := refsOf(m, op, "dashboard"); !slices.Equal(got, []string{"ThemeArn"}) {
		t.Errorf("wrapped detail read refs = %v, want [ThemeArn]", got)
	}
}

// TestRefsOf_OwnIDAtDepthAndNonStringTargets: the element of
// DescribeInstances is Reservation, so the subject's own id arrives at depth
// 1; State.Name is an enum, not a reference.
func TestRefsOf_OwnIDAtDepthAndNonStringTargets(t *testing.T) {
	m, op := refModel(map[string]*shape{
		"ns#DescribeInstancesResult": {Type: "structure", Members: map[string]*member{
			"Reservations": str("ns#ReservationList"),
		}},
		"ns#ReservationList": {Type: "list", Member: str("ns#Reservation")},
		"ns#Reservation": {Type: "structure", Members: map[string]*member{
			"OwnerId":   str("ns#Str"),
			"Instances": str("ns#InstanceList"),
		}},
		"ns#InstanceList": {Type: "list", Member: str("ns#Instance")},
		"ns#Instance": {Type: "structure", Members: map[string]*member{
			"InstanceId":     str("ns#Str"),
			"SubnetId":       str("ns#Str"),
			"ClientToken":    str("smithy.api#String"),
			"AmiLaunchIndex": str("ns#Int"),
			"State":          str("ns#InstanceState"),
		}},
		"ns#InstanceState": {Type: "structure", Members: map[string]*member{"Name": str("ns#StateEnum")}},
		"ns#StateEnum":     {Type: "enum"},
		"ns#Str":           {Type: "string"},
		"ns#Int":           {Type: "integer"},
	}, "ns#DescribeInstancesResult")
	want := []string{"Instances.SubnetId", "OwnerId"}
	if got := refsOf(m, op, "instanc"); !slices.Equal(got, want) {
		t.Errorf("refs = %v, want %v", got, want)
	}
}
