package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/pcaconnectorscep"
)

func init() {
	registerService(serviceEntry{
		name: "aws:pca-connector-scep",
		fn:   scanPCAConnectorSCEP,
		emits: []coverage.TypeDecl{
			{Service: "pca-connector-scep", DiscoType: TypePCAConnectorSCEPConnector},
			{Service: "pca-connector-scep", DiscoType: TypePCAConnectorSCEPChallenge},
		},
	})
}

type pcaConnectorSCEPAPI interface {
	ListConnectors(context.Context, *pcaconnectorscep.ListConnectorsInput, ...func(*pcaconnectorscep.Options)) (*pcaconnectorscep.ListConnectorsOutput, error)
	ListChallengeMetadata(context.Context, *pcaconnectorscep.ListChallengeMetadataInput, ...func(*pcaconnectorscep.Options)) (*pcaconnectorscep.ListChallengeMetadataOutput, error)
}

// scanPCAConnectorSCEP discovers Private CA Connector for SCEP connectors
// and per-connector challenges.
func scanPCAConnectorSCEP(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := pcaconnectorscep.NewFromConfig(acct.cfg, func(o *pcaconnectorscep.Options) { o.Region = region })

	connARNs, t, i, ferr := scanPCASCEPConnectors(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanPCASCEPChallenges(ctx, client, connARNs, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanPCASCEPConnectors(ctx context.Context, client pcaConnectorSCEPAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListConnectors(ctx, &pcaconnectorscep.ListConnectorsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "pca-connector-scep:ListConnectors", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("pca-connector-scep:ListConnectors: %w", err)
		}
		for _, c := range out.Connectors {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			status := string(c.Status)
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCAConnectorSCEPConnector, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "pca-connector-scep connectors")
	return arns, t, i, err
}

func scanPCASCEPChallenges(ctx context.Context, client pcaConnectorSCEPAPI, connARNs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, c := range connARNs {
		connArn := c
		var nextToken *string
		for {
			out, err := client.ListChallengeMetadata(ctx, &pcaconnectorscep.ListChallengeMetadataInput{
				ConnectorArn: &connArn,
				NextToken:    nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "pca-connector-scep:ListChallengeMetadata", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
					break
				}
				return 0, 0, fmt.Errorf("pca-connector-scep:ListChallengeMetadata c=%s: %w", connArn, err)
			}
			for _, ch := range out.Challenges {
				arn := sv(ch.Arn)
				if arn == "" {
					continue
				}
				label := arn
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypePCAConnectorSCEPChallenge, NativeID: arn,
					Name: &label, Region: &region,
					AttributesJSON: mustJSON(ch), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "pca-connector-scep challenges")
}
