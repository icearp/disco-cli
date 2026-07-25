package aws

import (
	"context"
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/dax"
)

// stubDAX returns a canned error from every Describe op. Each scanner phase
// calls exactly one of them, so a single err field covers all three.
type stubDAX struct{ err error }

func (s stubDAX) DescribeClusters(context.Context, *dax.DescribeClustersInput, ...func(*dax.Options)) (*dax.DescribeClustersOutput, error) {
	return nil, s.err
}

func (s stubDAX) DescribeParameterGroups(context.Context, *dax.DescribeParameterGroupsInput, ...func(*dax.Options)) (*dax.DescribeParameterGroupsOutput, error) {
	return nil, s.err
}

func (s stubDAX) DescribeSubnetGroups(context.Context, *dax.DescribeSubnetGroupsInput, ...func(*dax.Options)) (*dax.DescribeSubnetGroupsOutput, error) {
	return nil, s.err
}

// Regions without the DAX V3 control plane reject every Describe op with
// InvalidParameterValueException "Access Denied to API Version: DAX_V3" — a
// per-region availability gap, not a scan failure. DescribeClusters has always
// guarded it; parameter-groups and subnet-groups did not, so the error escaped
// as a hard scan error (3 of the 4 errors in the all-region scan). All three
// phases must now silent-skip: zero rows, no warning, no error.
func TestScanDAXPhases_SilentSkipOnDAXV3Gap(t *testing.T) {
	daxV3 := apiErr("InvalidParameterValueException", "Access Denied to API Version: DAX_V3")

	phases := map[string]func(context.Context, daxAPI, *account, string, *store.Store, string) (int, int, error){
		"clusters":         scanDAXClusters,
		"parameter-groups": scanDAXParameterGroups,
		"subnet-groups":    scanDAXSubnetGroups,
	}

	for name, phase := range phases {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warned := false
			st.OnWarn = func(store.ScanWarning) { warned = true }

			total, inserted, err := phase(
				context.Background(), stubDAX{err: daxV3}, newTestAccount(testAccountID), "ca-central-1", st, testScanID)
			if err != nil {
				t.Fatalf("DAX_V3 gap must not surface an error, got %v", err)
			}
			if total != 0 || inserted != 0 {
				t.Errorf("want (0,0), got (%d,%d)", total, inserted)
			}
			if warned {
				t.Error("per-region availability gap must not record a scan warning")
			}
		})
	}
}

// The guard keys on the message, not the code alone: an unrelated
// InvalidParameterValueException is a real failure and must still abort the
// phase, or a genuine malformed-input bug would be silently swallowed.
func TestScanDAXPhases_UnrelatedInvalidParameterStillErrors(t *testing.T) {
	other := apiErr("InvalidParameterValueException", "Invalid subnet group name")

	phases := map[string]func(context.Context, daxAPI, *account, string, *store.Store, string) (int, int, error){
		"clusters":         scanDAXClusters,
		"parameter-groups": scanDAXParameterGroups,
		"subnet-groups":    scanDAXSubnetGroups,
	}

	for name, phase := range phases {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)

			if _, _, err := phase(
				context.Background(), stubDAX{err: other}, newTestAccount(testAccountID), "ca-central-1", st, testScanID); err == nil {
				t.Fatal("unrelated InvalidParameterValueException must surface as an error")
			}
		})
	}
}
