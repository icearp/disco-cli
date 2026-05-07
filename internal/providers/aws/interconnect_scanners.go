package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/interconnect"
	smithy "github.com/aws/smithy-go"
)

func init() {
	registerService(serviceEntry{
		name: "aws:interconnect",
		fn:   scanInterconnect,
		emits: []coverage.TypeDecl{
			{Service: "interconnect", DiscoType: TypeInterconnectConnection},
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
	return upsertBatch(st, batch, "interconnect connections")
}
