package aws

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/pipes"
	pipestypes "github.com/aws/aws-sdk-go-v2/service/pipes/types"
	"github.com/icearp/disco-cli/store"
)

// TestScanPipes_ReadsTags pins that a pipe row carries the Tags DescribePipe
// returned (ListPipes' element has none), and that an untagged pipe stores none.
func TestScanPipes_ReadsTags(t *testing.T) {
	const (
		tagged   = "arn:aws:pipes:us-east-1:123456789012:pipe/p1"
		untagged = "arn:aws:pipes:us-east-1:123456789012:pipe/p2"
	)
	handlers := map[string]func(in any) (any, error){
		"ListPipes": func(any) (any, error) {
			return &pipes.ListPipesOutput{Pipes: []pipestypes.Pipe{
				{Arn: ptrStr(tagged), Name: ptrStr("p1")},
				{Arn: ptrStr(untagged), Name: ptrStr("p2")},
			}}, nil
		},
		"DescribePipe": func(in any) (any, error) {
			name := sv(in.(*pipes.DescribePipeInput).Name)
			out := &pipes.DescribePipeOutput{Name: &name}
			if name == "p1" {
				out.Tags = map[string]string{"team": "events"}
			}
			return out, nil
		},
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	total, _, err := scanPipes(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanPipes: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}

	rows := rowsByNativeID(t, st, TypePipesPipe)
	assertTagsMap(t, rows[tagged].TagsJSON, map[string]string{"team": "events"})
	if got := rows[untagged].TagsJSON; got != nil {
		t.Errorf("untagged pipe TagsJSON = %q, want nil", *got)
	}
}

// TestScanPipes_DescribeFails pins what a failed DescribePipe does to the two
// listed pipes it fails for, beside a third pipe described normally: a denial
// or a transient failure keeps each as its untagged ListPipes element with one
// warning; any other error keeps them and is returned; NotFoundException skips
// them silently.
func TestScanPipes_DescribeFails(t *testing.T) {
	const (
		okARN  = "arn:aws:pipes:us-east-1:123456789012:pipe/ok"
		badARN = "arn:aws:pipes:us-east-1:123456789012:pipe/bad1"
		bad2   = "arn:aws:pipes:us-east-1:123456789012:pipe/bad2"
	)
	cases := []struct {
		name      string
		err       error
		wantCode  string // the code the returned error carries; "" = nil error
		wantKept  bool
		wantWarns int
	}{
		{"denied", apiErr("AccessDeniedException", "not authorized to perform: pipes:DescribePipe"), "", true, 1},
		{"transient", io.EOF, "", true, 1},
		{"other error", apiErr("ValidationException", "bad pipe"), "ValidationException", true, 0},
		{"deleted since listed", apiErr("NotFoundException", "pipe not found"), "", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handlers := map[string]func(in any) (any, error){
				"ListPipes": func(any) (any, error) {
					return &pipes.ListPipesOutput{Pipes: []pipestypes.Pipe{
						{Arn: ptrStr(okARN), Name: ptrStr("ok")},
						{Arn: ptrStr(badARN), Name: ptrStr("bad1")},
						{Arn: ptrStr(bad2), Name: ptrStr("bad2")},
					}}, nil
				},
				"DescribePipe": func(in any) (any, error) {
					if name := sv(in.(*pipes.DescribePipeInput).Name); name != "ok" {
						return nil, c.err
					}
					return &pipes.DescribePipeOutput{Name: ptrStr("ok"), Tags: map[string]string{"team": "events"}}, nil
				},
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, pipesDescribeOp)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
			_, _, err := scanPipes(context.Background(), acct, testRegion, st, testScanID)
			switch {
			case c.wantCode == "" && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case c.wantCode != "" && !isAPIErrorCode(err, c.wantCode):
				t.Fatalf("err = %v, want one carrying %s", err, c.wantCode)
			}
			if *warns != c.wantWarns {
				t.Errorf("%s warnings = %d, want %d", pipesDescribeOp, *warns, c.wantWarns)
			}

			rows := rowsByNativeID(t, st, TypePipesPipe)
			assertTagsMap(t, rows[okARN].TagsJSON, map[string]string{"team": "events"})
			for _, arn := range []string{badARN, bad2} {
				row, kept := rows[arn]
				if kept != c.wantKept {
					t.Fatalf("%s stored = %v, want %v", arn, kept, c.wantKept)
				}
				if kept && row.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %q, want nil (ListPipes element has no tags)", arn, *row.TagsJSON)
				}
			}
		})
	}
}

// A denied DescribePipe warns that the pipe was stored as its list element
// only once it was: a failed write returns the store error and no warning.
func TestScanPipes_KeptPipeWarnsOnlyOnceStored(t *testing.T) {
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, pipesDescribeOp)
	handlers := map[string]func(in any) (any, error){
		"ListPipes": func(any) (any, error) {
			return &pipes.ListPipesOutput{Pipes: []pipestypes.Pipe{
				{Arn: ptrStr("arn:aws:pipes:us-east-1:123456789012:pipe/p1"), Name: ptrStr("p1")},
			}}, nil
		},
		"DescribePipe": func(any) (any, error) {
			closeStore()
			return nil, apiErr("AccessDeniedException", "not authorized to perform: pipes:DescribePipe")
		},
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	_, _, err := scanPipes(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want the store write failure", err)
	}
	if *warns != 0 {
		t.Errorf("%s warnings = %d, want 0: the pipe was never stored", pipesDescribeOp, *warns)
	}
}

// TestScanPipes_LaterListPageFails pins that a ListPipes page after the first
// that fails keeps the pipes read before it: a denial warns once and returns
// nil, any other error, or a repeated NextToken, is returned.
func TestScanPipes_LaterListPageFails(t *testing.T) {
	const page1 = "arn:aws:pipes:us-east-1:123456789012:pipe/p1"
	cases := []struct {
		name      string
		page2     func() (any, error)
		wantErr   func(error) bool
		wantWarns int
	}{
		{
			"denied",
			func() (any, error) {
				return nil, apiErr("AccessDeniedException", "not authorized to perform: pipes:ListPipes")
			},
			func(err error) bool { return err == nil },
			1,
		},
		{
			"hard error",
			func() (any, error) { return nil, apiErr("ValidationException", "bad token") },
			func(err error) bool { return isAPIErrorCode(err, "ValidationException") },
			0,
		},
		{
			"repeated token",
			func() (any, error) { return &pipes.ListPipesOutput{NextToken: ptrStr("t2")}, nil },
			func(err error) bool { return errors.Is(err, errRepeatedNextToken) },
			0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handlers := map[string]func(in any) (any, error){
				"ListPipes": func(in any) (any, error) {
					if in.(*pipes.ListPipesInput).NextToken == nil {
						return &pipes.ListPipesOutput{NextToken: ptrStr("t2"), Pipes: []pipestypes.Pipe{
							{Arn: ptrStr(page1), Name: ptrStr("p1")},
						}}, nil
					}
					return c.page2()
				},
				"DescribePipe": func(any) (any, error) {
					return &pipes.DescribePipeOutput{Name: ptrStr("p1"), Tags: map[string]string{"team": "events"}}, nil
				},
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, pipesListOp)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
			total, _, err := scanPipes(context.Background(), acct, testRegion, st, testScanID)
			if !c.wantErr(err) {
				t.Fatalf("err = %v, unexpected for %s", err, c.name)
			}
			if total != 1 {
				t.Errorf("total = %d, want 1", total)
			}
			if *warns != c.wantWarns {
				t.Errorf("%s warnings = %d, want %d", pipesListOp, *warns, c.wantWarns)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypePipesPipe)[page1].TagsJSON, map[string]string{"team": "events"})
		})
	}
}

// TestScanPipes_EmptyARNWarns pins that a listed pipe with no ARN is not
// stored and is reported once, beside a keyed pipe stored normally.
func TestScanPipes_EmptyARNWarns(t *testing.T) {
	const keyed = "arn:aws:pipes:us-east-1:123456789012:pipe/p1"
	handlers := map[string]func(in any) (any, error){
		"ListPipes": func(any) (any, error) {
			return &pipes.ListPipesOutput{Pipes: []pipestypes.Pipe{
				{Arn: ptrStr(keyed), Name: ptrStr("p1")},
				{Name: ptrStr("p2")},
			}}, nil
		},
		"DescribePipe": func(in any) (any, error) {
			return &pipes.DescribePipeOutput{Name: in.(*pipes.DescribePipeInput).Name}, nil
		},
	}
	st := newTestStore(t)
	warns := countOpWarnings(st, pipesListOp)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	total, _, err := scanPipes(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanPipes: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d, want 1", pipesListOp, *warns)
	}
	rows := rowsByNativeID(t, st, TypePipesPipe)
	if _, ok := rows[keyed]; !ok || len(rows) != 1 {
		t.Errorf("stored %d pipes, want only %s", len(rows), keyed)
	}
}
