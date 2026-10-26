package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGlueCrawler, Service: "glue"})
	// GetConnections is sent HidePassword, but ENCRYPTED_PASSWORD, the Kafka
	// key and keystore passwords and a custom connector's properties still
	// come back, so each property is judged by its key name and shape: the
	// properties name their secrets (PASSWORD, *_PASSWORD, SECRET_ID), and a
	// URL carrying credentials is caught by its shape. The
	// AuthenticationConfiguration SecretArn and KmsKeyArn are references.
	// SECRET_ID (a secret's name) and KAFKA_SASL_GSSAPI_KEYTAB (the keytab's
	// S3 location) are pointers but are replaced too: their key names match
	// the classifier ("secret", "gssAPIKEYtab"), and no rule can exempt one
	// key of a map that a wildcard rule judges, since every rule applies.
	registerType(restype.Descriptor{Type: TypeGlueConnection, Service: "glue", Redact: glueIfSensitive(
		"ConnectionProperties.*",
		"SparkProperties.*",
		"AthenaProperties.*",
		"PythonProperties.*",
		"AuthenticationConfiguration.OAuth2Properties.TokenUrlParametersMap.*",
		// The key name TokenUrl contains "token", so this always replaces the
		// URL: no rule mode judges a value by its shape alone under a
		// secret-looking key.
		"AuthenticationConfiguration.OAuth2Properties.TokenUrl",
	)})
	registerType(restype.Descriptor{Type: TypeGlueClassifier, Service: "glue"})
}

func scanGlueCrawlers(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "glue:GetCrawlers",
		func(ctx context.Context, token *string) ([]gluetypes.Crawler, *string, error) {
			out, err := client.GetCrawlers(ctx, &glue.GetCrawlersInput{NextToken: token, MaxResults: sdkaws.Int32(gluePageSize)})
			if err != nil {
				return nil, nil, err
			}
			return out.Crawlers, out.NextToken, nil
		},
		func(c gluetypes.Crawler) (*store.Resource, string, string) {
			return glueNamedRow(acct, region, scanID, TypeGlueCrawler, "crawler", "crawlers", sv(c.Name), "", c)
		}, tags, p)
}

// scanGlueConnections stores the account's connections with their tags.
// HidePassword keeps the connection's password out of the response, and so
// out of the stored attributes: without it Glue returns the password to a
// caller allowed to decrypt it.
func scanGlueConnections(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "glue:GetConnections",
		func(ctx context.Context, token *string) ([]gluetypes.Connection, *string, error) {
			out, err := client.GetConnections(ctx, &glue.GetConnectionsInput{NextToken: token, MaxResults: sdkaws.Int32(gluePageSize), HidePassword: true})
			if err != nil {
				return nil, nil, err
			}
			return out.ConnectionList, out.NextToken, nil
		},
		func(c gluetypes.Connection) (*store.Resource, string, string) {
			return glueNamedRow(acct, region, scanID, TypeGlueConnection, "connection", "connections", sv(c.Name), "", c)
		}, tags, p)
}

// glueClassifierName extracts the name from a Classifier tagged union — one of
// CsvClassifier/GrokClassifier/JsonClassifier/XMLClassifier is non-nil per SDK row.
func glueClassifierName(c gluetypes.Classifier) string {
	switch {
	case c.CsvClassifier != nil:
		return sv(c.CsvClassifier.Name)
	case c.GrokClassifier != nil:
		return sv(c.GrokClassifier.Name)
	case c.JsonClassifier != nil:
		return sv(c.JsonClassifier.Name)
	case c.XMLClassifier != nil:
		return sv(c.XMLClassifier.Name)
	}
	return ""
}

func scanGlueClassifiers(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewGetClassifiersPaginator(client, &glue.GetClassifiersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:GetClassifiers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:GetClassifiers: %w", perr)
		}
		for _, c := range out.Classifiers {
			name := glueClassifierName(c)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueClassifier,
				NativeID:       glueResourceARN(acct.partition(), region, acct.ID, "classifier", name),
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue classifiers: %w", uerr)
	}
	return len(batch), n, nil
}
