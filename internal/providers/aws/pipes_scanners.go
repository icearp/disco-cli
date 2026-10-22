package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/pipes"
	pipestypes "github.com/aws/aws-sdk-go-v2/service/pipes/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePipesPipe, Service: "pipes"})
	registerService(serviceEntry{
		name: "aws:pipes",
		fn:   scanPipes,
	})
}

const (
	pipesListOp     = "pipes:ListPipes"
	pipesDescribeOp = "pipes:DescribePipe"
)

// pipeReads tallies the pipes kept as their ListPipes element, for one
// warning per cause after the rows are stored.
type pipeReads struct {
	denied    skipTally
	transient transientTally
	unkeyed   skipTally
}

// scanPipes discovers EventBridge Pipes, each stored from its DescribePipe
// body, the only source of a pipe's tags (see pipeRow for a failed read). A
// first ListPipes page that fails stores nothing; a later page that fails
// keeps the pipes read before it, warning when it was denied and returning
// any other error after they are stored, as is every DescribePipe error that
// is neither a denial, a transient failure nor a not-found.
func scanPipes(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := pipes.NewFromConfig(acct.cfg, func(o *pipes.Options) { o.Region = region })

	var (
		batch      []*store.Resource
		reads      pipeReads
		errs       phaseErrs
		listDenied error
		nextToken  *string
	)
	for {
		out, err := client.ListPipes(ctx, &pipes.ListPipesInput{NextToken: nextToken})
		if err != nil && nextToken == nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, pipesListOp, acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("%s: %w", pipesListOp, err)
		}
		if err != nil {
			if isAccessDenied(err) {
				listDenied = err
			} else {
				errs.add(pipesListOp, fmt.Errorf("%s: %w", pipesListOp, err))
			}
			break
		}
		for _, p := range out.Pipes {
			row, rerr := pipeRow(ctx, client, acct, region, scanID, p, &reads)
			errs.add(pipesDescribeOp, rerr)
			if row != nil {
				batch = append(batch, row)
			}
		}
		last, terr := lastPage(nextToken, out.NextToken)
		errs.add(pipesListOp, terr)
		if last {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, uerr := upsertBatch(st, batch, "pipes")
	if uerr != nil {
		return total, inserted, errors.Join(errs.join(st, acct, region), uerr)
	}
	if listDenied != nil {
		_ = skipIfAccessDenied(st, pipesListOp, acct.ID, region, fmt.Errorf(
			"listing truncated, pipes on this and later pages not stored: %w", listDenied))
	}
	const kept = "%d pipe(s) stored as their ListPipes element, without tags, first %s: %w"
	if reads.denied.count > 0 {
		_ = skipIfAccessDenied(st, pipesDescribeOp, acct.ID, region, fmt.Errorf(kept, reads.denied.count, reads.denied.first, reads.denied.err))
	}
	warnNotStored(st, pipesListOp, acct, region, &reads.unkeyed, "pipes have an empty ARN")
	if reads.transient.count > 0 {
		_ = skipIfTransient(st, pipesDescribeOp, acct.ID, region, fmt.Errorf(kept, reads.transient.count, reads.transient.first, reads.transient.err))
	}
	return total, inserted, errs.join(st, acct, region)
}

// pipeRow builds a listed pipe's row from its DescribePipe body. A pipe
// DescribePipe no longer finds (NotFoundException, in the op's model) was
// deleted since it was listed: no row, no error. A listed pipe with no ARN has
// no NativeID: no row, tallied in reads for one warning. A pipe whose DescribePipe is
// denied, still fails transiently after the SDK's retries, or fails any other
// way gets a row from its ListPipes element instead, a different shape that
// carries no tags; a denial or transient failure is tallied in reads, any
// other error is returned beside the row.
func pipeRow(ctx context.Context, client *pipes.Client, acct *account, region, scanID string, p pipestypes.Pipe, reads *pipeReads) (*store.Resource, error) {
	arn := sv(p.Arn)
	if arn == "" {
		reads.unkeyed.skipped(sv(p.Name), nil)
		return nil, nil
	}
	attrsJSON := mustJSON(p)
	var (
		tags map[string]string
		err  error
	)
	dout, derr := client.DescribePipe(ctx, &pipes.DescribePipeInput{Name: p.Name})
	switch {
	case derr == nil:
		attrsJSON, tags = mustJSON(dout), dout.Tags
	case isAPIErrorCode(derr, "NotFoundException"):
		return nil, nil
	case isAccessDenied(derr):
		reads.denied.skipped(arn, derr)
	case reads.transient.drop(arn, derr):
	default:
		err = fmt.Errorf("%s %s: %w", pipesDescribeOp, sv(p.Name), derr)
	}
	status := string(p.CurrentState)
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypePipesPipe, NativeID: arn,
		Name: p.Name, Region: &region, Status: &status,
		AttributesJSON: attrsJSON, TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}, err
}
