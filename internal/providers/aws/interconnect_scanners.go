package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/interconnect"
	smithy "github.com/aws/smithy-go"
)

func init() {
	registerService(serviceEntry{
		name: "aws:interconnect",
		fn:   scanInterconnect,
		emits: []coverage.TypeDecl{
			{Service: "interconnect", DiscoType: TypeInterconnectConnection, Leaf: true},
			{Service: "interconnect", DiscoType: TypeInterconnectEnvironment, Leaf: true},
		},
	})
}

// isInterconnectClosedToAccount reports whether err is the empty-message
// AccessDenied shape AWS returns for accounts not registered for the
// (closed-to-new-customers) Interconnect service. Real per-op IAM denials
// always carry an action-identifying message.
func isInterconnectClosedToAccount(err error) bool {
	if !isAccessDenied(err) {
		return false
	}
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	return strings.TrimSpace(ae.ErrorMessage()) == ""
}

// scanInterconnect discovers Interconnect connections.
func scanInterconnect(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := interconnect.NewFromConfig(acct.cfg, func(o *interconnect.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListConnections(ctx, &interconnect.ListConnectionsInput{NextToken: nextToken})
		if err != nil {
			if isInterconnectClosedToAccount(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "interconnect:ListConnections", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("interconnect:ListConnections: %w", err)
		}
		for _, c := range out.Connections {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInterconnectConnection, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	ct, ci, cerr := upsertBatch(st, batch, "interconnect connections")
	if cerr != nil {
		return ct, ci, cerr
	}

	et, ei, eerr := scanInterconnectEnvironments(ctx, client, acct, region, st, scanID)
	return ct + et, ci + ei, eerr
}

// scanInterconnectEnvironments enumerates the connection environments available
// to the account. The Environment shape carries no AWS-issued ARN, so the
// NativeID is synthesized from EnvironmentId.
func scanInterconnectEnvironments(ctx context.Context, client *interconnect.Client, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := interconnect.NewListEnvironmentsPaginator(client, &interconnect.ListEnvironmentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isInterconnectClosedToAccount(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "interconnect:ListEnvironments", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("interconnect:ListEnvironments: %w", perr)
		}
		for _, e := range out.Environments {
			id := sv(e.EnvironmentId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:interconnect:%s:%s:environment/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInterconnectEnvironment, NativeID: arn,
				Name: &id, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "interconnect environments")
}
