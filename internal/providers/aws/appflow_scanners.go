package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/appflow"
)

func init() {
	registerService(serviceEntry{
		name: "aws:appflow",
		fn:   scanAppFlow,
		emits: []coverage.TypeDecl{
			{Service: "appflow", DiscoType: TypeAppFlowFlow},
			{Service: "appflow", DiscoType: TypeAppFlowConnector, Leaf: true},
			{Service: "appflow", DiscoType: TypeAppFlowConnectorProfile},
		},
	})
}

// appflowAPI is the narrow surface the AppFlow scanner uses. ListFlows
// returns FlowDefinition entries with full body. ListConnectors enumerates
// custom (registered) connectors; DescribeConnectorProfiles paginates
// connector-profile detail rows.
type appflowAPI interface {
	ListFlows(context.Context, *appflow.ListFlowsInput, ...func(*appflow.Options)) (*appflow.ListFlowsOutput, error)
	ListConnectors(context.Context, *appflow.ListConnectorsInput, ...func(*appflow.Options)) (*appflow.ListConnectorsOutput, error)
	DescribeConnectorProfiles(context.Context, *appflow.DescribeConnectorProfilesInput, ...func(*appflow.Options)) (*appflow.DescribeConnectorProfilesOutput, error)
}

// appflowConnectorNativeID synthesizes the NativeID for an AppFlow Custom
// Connector. ConnectorDetail carries no ARN; the (account, region, label)
// triple uniquely identifies a registered connector.
func appflowConnectorNativeID(region, acct, label string) string {
	return fmt.Sprintf("arn:aws:appflow:%s:%s:connector/%s", region, acct, label)
}

func scanAppFlow(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := appflow.NewFromConfig(acct.cfg, func(o *appflow.Options) { o.Region = region })
	fTotal, fInserted, err := scanAppFlowFlows(ctx, client, acct, region, st, scanID)
	if err != nil {
		return fTotal, fInserted, err
	}
	cTotal, cInserted, err := scanAppFlowConnectors(ctx, client, acct, region, st, scanID)
	if err != nil {
		return fTotal + cTotal, fInserted + cInserted, err
	}
	pTotal, pInserted, err := scanAppFlowConnectorProfiles(ctx, client, acct, region, st, scanID)
	if err != nil {
		return fTotal + cTotal + pTotal, fInserted + cInserted + pInserted, err
	}
	return fTotal + cTotal + pTotal, fInserted + cInserted + pInserted, nil
}

// scanAppFlowConnectors enumerates custom (registered) connectors via
// appflow:ListConnectors. ConnectorDetail has no AWS-issued ARN; NativeID
// synthesized as arn:aws:appflow:{region}:{acct}:connector/{ConnectorLabel}.
// ListConnectors uses manual NextToken pagination (no SDK paginator helper).
func scanAppFlowConnectors(ctx context.Context, client appflowAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var nextToken *string
	for {
		out, err := client.ListConnectors(ctx, &appflow.ListConnectorsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "appflow:ListConnectors", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("appflow:ListConnectors: %w", err)
		}
		batch := make([]*store.Resource, 0, len(out.Connectors))
		for _, c := range out.Connectors {
			label := sv(c.ConnectorLabel)
			if label == "" {
				continue
			}
			nativeID := appflowConnectorNativeID(region, acct.ID, label)
			name := label
			batch = append(batch, &store.Resource{
				Provider:    "aws",
				AccountID:   acct.ID,
				AccountName: &acct.Name,
				Type:        TypeAppFlowConnector,
				NativeID:    nativeID,
				Name:        &name,
				Region:      &region,
				CreatedAt:   tp(c.RegisteredAt),
				// AWS-shipped built-in connectors carry ConnectorOwner="AWS";
				// customer-registered connectors carry the registering account ID.
				ManagedByProvider: sv(c.ConnectorOwner) == "AWS",
				AttributesJSON:    mustJSON(c),
				DiscoveredBy:      scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert appflow connectors: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return total, inserted, nil
}

// scanAppFlowConnectorProfiles enumerates connector profiles. NativeID =
// ConnectorProfileArn (real AWS-issued ARN). Paginated via SDK paginator.
func scanAppFlowConnectorProfiles(ctx context.Context, client appflowAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := appflow.NewDescribeConnectorProfilesPaginator(client, &appflow.DescribeConnectorProfilesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "appflow:DescribeConnectorProfiles", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("appflow:DescribeConnectorProfiles: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.ConnectorProfileDetails))
		for _, cp := range page.ConnectorProfileDetails {
			arn := sv(cp.ConnectorProfileArn)
			if arn == "" {
				continue
			}
			name := sv(cp.ConnectorProfileName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppFlowConnectorProfile,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(cp.CreatedAt),
				AttributesJSON: mustJSON(cp),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert appflow connector profiles: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func scanAppFlowFlows(ctx context.Context, client appflowAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := appflow.NewListFlowsPaginator(client, &appflow.ListFlowsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "appflow:ListFlows", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("appflow:ListFlows: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.Flows))
		for _, f := range page.Flows {
			arn := sv(f.FlowArn)
			if arn == "" {
				continue
			}
			name := sv(f.FlowName)
			status := string(f.FlowStatus)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppFlowFlow,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(f.CreatedAt),
				AttributesJSON: mustJSON(f),
				TagsJSON:       mapTagsJSON(f.Tags),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert appflow flows: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
