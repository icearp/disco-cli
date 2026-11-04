package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafkaconnect"
	kafkaconnecttypes "github.com/aws/aws-sdk-go-v2/service/kafkaconnect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeKafkaConnectConnector, Service: "kafka-connect"})
	registerType(restype.Descriptor{Type: TypeKafkaConnectCustomPlugin, Service: "kafka-connect"})
	registerType(restype.Descriptor{Type: TypeKafkaConnectWorkerConfiguration, Service: "kafka-connect"})
	registerService(serviceEntry{
		name: "aws:kafka-connect",
		fn:   scanKafkaConnect,
	})
}

type kafkaConnectAPI interface {
	ListConnectors(context.Context, *kafkaconnect.ListConnectorsInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error)
	ListCustomPlugins(context.Context, *kafkaconnect.ListCustomPluginsInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.ListCustomPluginsOutput, error)
	ListWorkerConfigurations(context.Context, *kafkaconnect.ListWorkerConfigurationsInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.ListWorkerConfigurationsOutput, error)
	ListTagsForResource(context.Context, *kafkaconnect.ListTagsForResourceInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.ListTagsForResourceOutput, error)
}

const (
	kcListConnectorsOp           = "kafkaconnect:ListConnectors"
	kcListCustomPluginsOp        = "kafkaconnect:ListCustomPlugins"
	kcListWorkerConfigurationsOp = "kafkaconnect:ListWorkerConfigurations"
	kcTagsOp                     = "kafkaconnect:ListTagsForResource"
	// kcPageCap is the maxResults range maximum (1-100) the model gives every
	// listing.
	kcPageCap int32 = 100
)

// scanKafkaConnect discovers MSK Connect connectors, custom plugins and
// worker configurations, each with its tags (kafkaConnectTags). Every
// listing runs as its own phase, one at a time and in that order: a phase's
// error does not stop the later ones; a failed write and ctx's end do, and so
// does a first phase that stores nothing and fails only with NXDOMAIN, since
// the region does not serve the service (runHostPhases). Each listing is
// written a page at a time, and the tag reads' denials are one warning for
// the whole scan.
func scanKafkaConnect(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := kafkaconnect.NewFromConfig(acct.cfg, func(o *kafkaconnect.Options) { o.Region = region })

	tags := kafkaConnectTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "kafkaconnect", 0, []hostPhase{
		{op: kcListConnectorsOp, run: func(p *phaseTotals) bool {
			return scanKCConnectors(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: kcListCustomPluginsOp, run: func(p *phaseTotals) bool {
			return scanKCCustomPlugins(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: kcListWorkerConfigurationsOp, run: func(p *phaseTotals) bool {
			return scanKCWorkerConfigurations(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// kafkaConnectTags reads a row's tags with ListTagsForResource, sent the
// row's NativeID: the ARN its listed element carries. The Service
// Reference lists connector, custom plugin and worker configuration on
// ListTagsForResource and TagResource, each with aws:ResourceTag as a
// condition key, and the three Create ops take Tags; no listed summary
// carries them. The op returns every tag at once. NotFoundException, its
// modeled not-found, drops a resource deleted after it was listed;
// ForbiddenException is a denial (isAccessDenied).
func kafkaConnectTags(client kafkaConnectAPI) rowTagReader {
	return rowTagReader{
		op:       kcTagsOp,
		notFound: []string{"NotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &kafkaconnect.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// kcRow is the row of a listed element of typ, stored under its ARN, named
// name, with the element's state as its Status (none when the element has
// no state). An element without an ARN is not stored, and its listing's
// warning calls the type's elements what and names it by name, or by
// noIdentifier without one (arnRow).
func kcRow(acct *account, region, scanID, typ, what string, arn, name *string, state string, element any) (*store.Resource, string, string) {
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: name, Region: &region, Status: nonEmptyPtr(state),
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanKCConnectors stores the connectors, each page written as soon as its
// rows' tags are read. It reports whether a write failed, after which the
// scan must stop.
func scanKCConnectors(ctx context.Context, client kafkaConnectAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, kcListConnectorsOp,
		func(ctx context.Context, token *string) ([]kafkaconnecttypes.ConnectorSummary, *string, error) {
			out, err := client.ListConnectors(ctx, &kafkaconnect.ListConnectorsInput{MaxResults: sdkaws.Int32(kcPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Connectors, out.NextToken, nil
		},
		func(c kafkaconnecttypes.ConnectorSummary) (*store.Resource, string, string) {
			return kcRow(acct, region, scanID, TypeKafkaConnectConnector, "connectors", c.ConnectorArn, c.ConnectorName, string(c.ConnectorState), c)
		}, tags, p)
}

// scanKCCustomPlugins stores the custom plugins, each page written as soon
// as its rows' tags are read. It reports whether a write failed, after which
// the scan must stop.
func scanKCCustomPlugins(ctx context.Context, client kafkaConnectAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, kcListCustomPluginsOp,
		func(ctx context.Context, token *string) ([]kafkaconnecttypes.CustomPluginSummary, *string, error) {
			out, err := client.ListCustomPlugins(ctx, &kafkaconnect.ListCustomPluginsInput{MaxResults: sdkaws.Int32(kcPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.CustomPlugins, out.NextToken, nil
		},
		func(c kafkaconnecttypes.CustomPluginSummary) (*store.Resource, string, string) {
			return kcRow(acct, region, scanID, TypeKafkaConnectCustomPlugin, "custom plugins", c.CustomPluginArn, c.Name, string(c.CustomPluginState), c)
		}, tags, p)
}

// scanKCWorkerConfigurations stores the worker configurations, each page
// written as soon as its rows' tags are read. It reports whether a write
// failed, after which the scan must stop.
func scanKCWorkerConfigurations(ctx context.Context, client kafkaConnectAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, kcListWorkerConfigurationsOp,
		func(ctx context.Context, token *string) ([]kafkaconnecttypes.WorkerConfigurationSummary, *string, error) {
			out, err := client.ListWorkerConfigurations(ctx, &kafkaconnect.ListWorkerConfigurationsInput{MaxResults: sdkaws.Int32(kcPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WorkerConfigurations, out.NextToken, nil
		},
		func(w kafkaconnecttypes.WorkerConfigurationSummary) (*store.Resource, string, string) {
			return kcRow(acct, region, scanID, TypeKafkaConnectWorkerConfiguration, "worker configurations", w.WorkerConfigurationArn, w.Name, string(w.WorkerConfigurationState), w)
		}, tags, p)
}
