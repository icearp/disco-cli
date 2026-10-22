package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/mwaa"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMWAAEnvironment, Service: "mwaa"})
	registerService(serviceEntry{
		name: "aws:mwaa",
		fn:   scanMWAA,
	})
}

const mwaaGetEnvironmentOp = "mwaa:GetEnvironment"

// mwaaReads tallies the environments GetEnvironment could not read, for one
// warning per cause after the rows are stored.
type mwaaReads struct{ denied, noBody skipTally }

// warn reports each tally once; call it only after the environments are stored.
func (r *mwaaReads) warn(st *store.Store, acct *account, region string) {
	const lost = "%d environment(s) not stored, since ListEnvironments returns only names, first %s: %w"
	if r.denied.count > 0 {
		_ = skipIfAccessDenied(st, mwaaGetEnvironmentOp, acct.ID, region, fmt.Errorf(lost, r.denied.count, r.denied.first, r.denied.err))
	}
	warnNotStored(st, mwaaGetEnvironmentOp, acct, region, &r.noBody, "environments have no Environment or Arn in GetEnvironment")
}

// scanMWAA discovers Managed Workflows for Apache Airflow environments.
// ListEnvironments returns names only; each row is the GetEnvironment body.
// With no listed element to fall back on, an environment whose GetEnvironment
// fails is not stored. A denial or an answer without an ARN is warned once
// per cause. ResourceNotFoundException (in the op's model) means the
// environment was deleted after it was listed, so it is skipped. Any other
// error, a throttle that outlasted the SDK's retries included, is returned
// after the other environments are stored, so the scan is reported partial
// rather than the environment looking deleted.
func scanMWAA(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mwaa.NewFromConfig(acct.cfg, func(o *mwaa.Options) { o.Region = region })

	var names []string
	var nextToken *string
	for {
		out, err := client.ListEnvironments(ctx, &mwaa.ListEnvironmentsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mwaa:ListEnvironments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mwaa:ListEnvironments: %w", err)
		}
		names = append(names, out.Environments...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	var (
		batch []*store.Resource
		reads mwaaReads
		errs  []error
	)
	for _, name := range names {
		out, err := client.GetEnvironment(ctx, &mwaa.GetEnvironmentInput{Name: &name})
		switch {
		case err == nil:
		case isAPIErrorCode(err, "ResourceNotFoundException"):
			continue
		case isAccessDenied(err):
			reads.denied.skipped(name, err)
			continue
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", mwaaGetEnvironmentOp, name, err))
			continue
		}
		if out.Environment == nil || sv(out.Environment.Arn) == "" {
			reads.noBody.skipped(name, nil)
			continue
		}
		env := out.Environment
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeMWAAEnvironment, NativeID: sv(env.Arn),
			Name: env.Name, Region: &region, Status: nonEmptyPtr(string(env.Status)), TagsJSON: mapTagsJSON(env.Tags),
			AttributesJSON: mustJSON(env), DiscoveredBy: scanID,
		})
	}
	total, inserted, err = upsertBatch(st, batch, "mwaa environments")
	if err != nil {
		return total, inserted, errors.Join(errors.Join(errs...), err)
	}
	reads.warn(st, acct, region)
	return total, inserted, errors.Join(errs...)
}
