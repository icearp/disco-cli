package aws

import (
	"context"
	"errors"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codeguruprofiler"
	cgptypes "github.com/aws/aws-sdk-go-v2/service/codeguruprofiler/types"
)

// TestScanCodeGuruProfiler_ReadsTags pins that a profiling-group row carries
// the Tags of its ListProfilingGroups description, and that an untagged group
// stores none.
func TestScanCodeGuruProfiler_ReadsTags(t *testing.T) {
	const (
		tagged   = "arn:aws:codeguru-profiler:us-east-1:123456789012:profilingGroup/g1"
		untagged = "arn:aws:codeguru-profiler:us-east-1:123456789012:profilingGroup/g2"
	)
	handlers := map[string]func(in any) (any, error){
		"ListProfilingGroups": func(in any) (any, error) {
			// The service returns descriptions, which carry the tags, only
			// when asked; otherwise it returns names alone.
			if !sdkaws.ToBool(in.(*codeguruprofiler.ListProfilingGroupsInput).IncludeDescription) {
				return &codeguruprofiler.ListProfilingGroupsOutput{ProfilingGroupNames: []string{"g1", "g2"}}, nil
			}
			return &codeguruprofiler.ListProfilingGroupsOutput{ProfilingGroups: []cgptypes.ProfilingGroupDescription{
				{Arn: ptrStr(tagged), Name: ptrStr("g1"), Tags: map[string]string{"team": "perf"}},
				{Arn: ptrStr(untagged), Name: ptrStr("g2")},
			}}, nil
		},
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	total, inserted, err := scanCodeGuruProfiler(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanCodeGuruProfiler: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total=%d inserted=%d, want 2/2", total, inserted)
	}

	rows := rowsByNativeID(t, st, TypeCodeGuruProfilerProfilingGroup)
	for _, arn := range []string{tagged, untagged} {
		if _, ok := rows[arn]; !ok {
			t.Fatalf("profiling group %s not stored", arn)
		}
	}
	assertTagsMap(t, rows[tagged].TagsJSON, map[string]string{"team": "perf"})
	if got := rows[untagged].TagsJSON; got != nil {
		t.Errorf("untagged profiling group TagsJSON = %q, want nil", *got)
	}
}

// TestScanCodeGuruProfiler_LaterPageFailsKeepsRows pins that a failed
// ListProfilingGroups page after the first keeps the groups already read: a
// denial warns once and returns nil, any other error is returned.
func TestScanCodeGuruProfiler_LaterPageFailsKeepsRows(t *testing.T) {
	const first = "arn:aws:codeguru-profiler:us-east-1:123456789012:profilingGroup/g1"
	for _, c := range []struct {
		name      string
		err       error
		wantErr   func(error) bool
		wantWarns int
	}{
		{
			"denied", apiErr("AccessDeniedException", "not authorized to perform: codeguru-profiler:ListProfilingGroups"),
			func(err error) bool { return err == nil }, 1,
		},
		{
			"hard error", apiErr("ValidationException", "bad token"),
			func(err error) bool { return isAPIErrorCode(err, "ValidationException") }, 0,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			handlers := map[string]func(in any) (any, error){
				"ListProfilingGroups": func(in any) (any, error) {
					if in.(*codeguruprofiler.ListProfilingGroupsInput).NextToken != nil {
						return nil, c.err
					}
					return &codeguruprofiler.ListProfilingGroupsOutput{NextToken: ptrStr("t"), ProfilingGroups: []cgptypes.ProfilingGroupDescription{
						{Arn: ptrStr(first), Name: ptrStr("g1")},
					}}, nil
				},
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, cgpListOp)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
			total, _, err := scanCodeGuruProfiler(context.Background(), acct, testRegion, st, testScanID)
			if !c.wantErr(err) {
				t.Fatalf("err = %v, unexpected for %s", err, c.name)
			}
			if _, ok := rowsByNativeID(t, st, TypeCodeGuruProfilerProfilingGroup)[first]; total != 1 || !ok {
				t.Errorf("total=%d first-page group stored=%v, want 1/true", total, ok)
			}
			if *warns != c.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, c.wantWarns)
			}
		})
	}
}

// TestScanCodeGuruProfiler_RepeatedTokenKeepsRows pins that a listing whose
// service repeats its NextToken returns errRepeatedNextToken with the groups
// of both pages stored.
func TestScanCodeGuruProfiler_RepeatedTokenKeepsRows(t *testing.T) {
	handlers := map[string]func(in any) (any, error){
		"ListProfilingGroups": func(in any) (any, error) {
			name := "g1"
			if in.(*codeguruprofiler.ListProfilingGroupsInput).NextToken != nil {
				name = "g2"
			}
			return &codeguruprofiler.ListProfilingGroupsOutput{NextToken: ptrStr("t"), ProfilingGroups: []cgptypes.ProfilingGroupDescription{
				{Arn: ptrStr("arn:aws:codeguru-profiler:us-east-1:123456789012:profilingGroup/" + name), Name: &name},
			}}, nil
		},
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	total, _, err := scanCodeGuruProfiler(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if n := len(rowsByNativeID(t, st, TypeCodeGuruProfilerProfilingGroup)); total != 2 || n != 2 {
		t.Errorf("total=%d stored=%d, want 2 each", total, n)
	}
}
