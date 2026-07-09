package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/greengrass"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGreengrassConnectorDefinition, Service: "greengrass", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGreengrassConnectorDefinitionVersion, Service: "greengrass"})
	registerType(restype.Descriptor{Type: TypeGreengrassCoreDefinition, Service: "greengrass", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGreengrassCoreDefinitionVersion, Service: "greengrass"})
	registerType(restype.Descriptor{Type: TypeGreengrassDeviceDefinition, Service: "greengrass", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGreengrassDeviceDefinitionVersion, Service: "greengrass"})
	registerType(restype.Descriptor{Type: TypeGreengrassFunctionDefinition, Service: "greengrass", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGreengrassFunctionDefinitionVersion, Service: "greengrass"})
	registerType(restype.Descriptor{Type: TypeGreengrassLoggerDefinition, Service: "greengrass", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGreengrassLoggerDefinitionVersion, Service: "greengrass"})
	registerType(restype.Descriptor{Type: TypeGreengrassResourceDefinition, Service: "greengrass", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGreengrassResourceDefinitionVersion, Service: "greengrass"})
	registerType(restype.Descriptor{Type: TypeGreengrassSubscriptionDefinition, Service: "greengrass", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGreengrassSubscriptionDefinitionVersion, Service: "greengrass"})
	registerType(restype.Descriptor{Type: TypeGreengrassGroup, Service: "greengrass", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGreengrassGroupVersion, Service: "greengrass"})
	registerService(serviceEntry{
		name: "aws:greengrass",
		fn:   scanGreengrass,
	})
}

// greengrassAPI — narrow surface of Greengrass v1 ops. Per kind a List
// definitions + List versions pair shares the same DefinitionInformation
// and VersionInformation shapes; only input field names differ
// (e.g. ConnectorDefinitionId vs CoreDefinitionId).
type greengrassAPI interface {
	ListConnectorDefinitions(context.Context, *greengrass.ListConnectorDefinitionsInput, ...func(*greengrass.Options)) (*greengrass.ListConnectorDefinitionsOutput, error)
	ListConnectorDefinitionVersions(context.Context, *greengrass.ListConnectorDefinitionVersionsInput, ...func(*greengrass.Options)) (*greengrass.ListConnectorDefinitionVersionsOutput, error)
	ListCoreDefinitions(context.Context, *greengrass.ListCoreDefinitionsInput, ...func(*greengrass.Options)) (*greengrass.ListCoreDefinitionsOutput, error)
	ListCoreDefinitionVersions(context.Context, *greengrass.ListCoreDefinitionVersionsInput, ...func(*greengrass.Options)) (*greengrass.ListCoreDefinitionVersionsOutput, error)
	ListDeviceDefinitions(context.Context, *greengrass.ListDeviceDefinitionsInput, ...func(*greengrass.Options)) (*greengrass.ListDeviceDefinitionsOutput, error)
	ListDeviceDefinitionVersions(context.Context, *greengrass.ListDeviceDefinitionVersionsInput, ...func(*greengrass.Options)) (*greengrass.ListDeviceDefinitionVersionsOutput, error)
	ListFunctionDefinitions(context.Context, *greengrass.ListFunctionDefinitionsInput, ...func(*greengrass.Options)) (*greengrass.ListFunctionDefinitionsOutput, error)
	ListFunctionDefinitionVersions(context.Context, *greengrass.ListFunctionDefinitionVersionsInput, ...func(*greengrass.Options)) (*greengrass.ListFunctionDefinitionVersionsOutput, error)
	ListLoggerDefinitions(context.Context, *greengrass.ListLoggerDefinitionsInput, ...func(*greengrass.Options)) (*greengrass.ListLoggerDefinitionsOutput, error)
	ListLoggerDefinitionVersions(context.Context, *greengrass.ListLoggerDefinitionVersionsInput, ...func(*greengrass.Options)) (*greengrass.ListLoggerDefinitionVersionsOutput, error)
	ListResourceDefinitions(context.Context, *greengrass.ListResourceDefinitionsInput, ...func(*greengrass.Options)) (*greengrass.ListResourceDefinitionsOutput, error)
	ListResourceDefinitionVersions(context.Context, *greengrass.ListResourceDefinitionVersionsInput, ...func(*greengrass.Options)) (*greengrass.ListResourceDefinitionVersionsOutput, error)
	ListSubscriptionDefinitions(context.Context, *greengrass.ListSubscriptionDefinitionsInput, ...func(*greengrass.Options)) (*greengrass.ListSubscriptionDefinitionsOutput, error)
	ListSubscriptionDefinitionVersions(context.Context, *greengrass.ListSubscriptionDefinitionVersionsInput, ...func(*greengrass.Options)) (*greengrass.ListSubscriptionDefinitionVersionsOutput, error)
	ListGroups(context.Context, *greengrass.ListGroupsInput, ...func(*greengrass.Options)) (*greengrass.ListGroupsOutput, error)
	ListGroupVersions(context.Context, *greengrass.ListGroupVersionsInput, ...func(*greengrass.Options)) (*greengrass.ListGroupVersionsOutput, error)
}

// greengrassV1Regions enumerates the AWS Regions where IoT Greengrass V1
// control-plane operations are available. SDK still resolves
// <svc>.<region>.amazonaws.com for unsupported regions; AWS rejects with
// TooManyRequestsException 429 → SDK retries 10x → ~2m wall time before
// surfacing. Allowlist short-circuits the cost on call #1.
// Source: https://docs.aws.amazon.com/general/latest/gr/greengrass.html
var greengrassV1Regions = map[string]bool{
	"us-east-1": true, "us-east-2": true, "us-west-2": true,
	"ap-south-1":     true,
	"ap-northeast-1": true, "ap-northeast-2": true,
	"ap-southeast-1": true, "ap-southeast-2": true,
	"eu-central-1": true,
	"eu-west-1":    true, "eu-west-2": true,
	"cn-north-1":    true,
	"us-gov-east-1": true, "us-gov-west-1": true,
}

func scanGreengrass(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if !greengrassV1Regions[region] {
		return 0, 0, nil
	}
	client := greengrass.NewFromConfig(acct.cfg, func(o *greengrass.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanGGConnector(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGGCore(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGGDevice(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGGFunction(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGGLogger(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGGResource(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGGSubscription(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGGGroups(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// All 7 *Definition list ops use NextToken with manual loops. Each kind
// reuses the same per-page handler shape.

func scanGGConnector(ctx context.Context, client greengrassAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	defs, t1, i1, err := ggListDefs(ctx, acct, region, st, scanID, "ConnectorDefinitions", TypeGreengrassConnectorDefinition,
		func(token *string) (*greengrass.ListConnectorDefinitionsOutput, error) {
			return client.ListConnectorDefinitions(ctx, &greengrass.ListConnectorDefinitionsInput{NextToken: token})
		},
		func(o *greengrass.ListConnectorDefinitionsOutput) (any, *string) {
			return o.Definitions, o.NextToken
		})
	if err != nil {
		return 0, 0, err
	}
	t2, i2, err := scanGGVersions(ctx, acct, region, st, scanID, "ConnectorDefinitionVersions", TypeGreengrassConnectorDefinitionVersion, defs,
		func(defID, token *string) (versOut, error) {
			out, e := client.ListConnectorDefinitionVersions(ctx, &greengrass.ListConnectorDefinitionVersionsInput{ConnectorDefinitionId: defID, NextToken: token})
			if e != nil {
				return versOut{}, e
			}
			return versOut{Versions: out.Versions, NextToken: out.NextToken}, nil
		})
	return t1 + t2, i1 + i2, err
}

func scanGGCore(ctx context.Context, client greengrassAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	defs, t1, i1, err := ggListDefs(ctx, acct, region, st, scanID, "CoreDefinitions", TypeGreengrassCoreDefinition,
		func(token *string) (*greengrass.ListCoreDefinitionsOutput, error) {
			return client.ListCoreDefinitions(ctx, &greengrass.ListCoreDefinitionsInput{NextToken: token})
		},
		func(o *greengrass.ListCoreDefinitionsOutput) (any, *string) {
			return o.Definitions, o.NextToken
		})
	if err != nil {
		return 0, 0, err
	}
	t2, i2, err := scanGGVersions(ctx, acct, region, st, scanID, "CoreDefinitionVersions", TypeGreengrassCoreDefinitionVersion, defs,
		func(defID, token *string) (versOut, error) {
			out, e := client.ListCoreDefinitionVersions(ctx, &greengrass.ListCoreDefinitionVersionsInput{CoreDefinitionId: defID, NextToken: token})
			if e != nil {
				return versOut{}, e
			}
			return versOut{Versions: out.Versions, NextToken: out.NextToken}, nil
		})
	return t1 + t2, i1 + i2, err
}

func scanGGDevice(ctx context.Context, client greengrassAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	defs, t1, i1, err := ggListDefs(ctx, acct, region, st, scanID, "DeviceDefinitions", TypeGreengrassDeviceDefinition,
		func(token *string) (*greengrass.ListDeviceDefinitionsOutput, error) {
			return client.ListDeviceDefinitions(ctx, &greengrass.ListDeviceDefinitionsInput{NextToken: token})
		},
		func(o *greengrass.ListDeviceDefinitionsOutput) (any, *string) {
			return o.Definitions, o.NextToken
		})
	if err != nil {
		return 0, 0, err
	}
	t2, i2, err := scanGGVersions(ctx, acct, region, st, scanID, "DeviceDefinitionVersions", TypeGreengrassDeviceDefinitionVersion, defs,
		func(defID, token *string) (versOut, error) {
			out, e := client.ListDeviceDefinitionVersions(ctx, &greengrass.ListDeviceDefinitionVersionsInput{DeviceDefinitionId: defID, NextToken: token})
			if e != nil {
				return versOut{}, e
			}
			return versOut{Versions: out.Versions, NextToken: out.NextToken}, nil
		})
	return t1 + t2, i1 + i2, err
}

func scanGGFunction(ctx context.Context, client greengrassAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	defs, t1, i1, err := ggListDefs(ctx, acct, region, st, scanID, "FunctionDefinitions", TypeGreengrassFunctionDefinition,
		func(token *string) (*greengrass.ListFunctionDefinitionsOutput, error) {
			return client.ListFunctionDefinitions(ctx, &greengrass.ListFunctionDefinitionsInput{NextToken: token})
		},
		func(o *greengrass.ListFunctionDefinitionsOutput) (any, *string) {
			return o.Definitions, o.NextToken
		})
	if err != nil {
		return 0, 0, err
	}
	t2, i2, err := scanGGVersions(ctx, acct, region, st, scanID, "FunctionDefinitionVersions", TypeGreengrassFunctionDefinitionVersion, defs,
		func(defID, token *string) (versOut, error) {
			out, e := client.ListFunctionDefinitionVersions(ctx, &greengrass.ListFunctionDefinitionVersionsInput{FunctionDefinitionId: defID, NextToken: token})
			if e != nil {
				return versOut{}, e
			}
			return versOut{Versions: out.Versions, NextToken: out.NextToken}, nil
		})
	return t1 + t2, i1 + i2, err
}

func scanGGLogger(ctx context.Context, client greengrassAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	defs, t1, i1, err := ggListDefs(ctx, acct, region, st, scanID, "LoggerDefinitions", TypeGreengrassLoggerDefinition,
		func(token *string) (*greengrass.ListLoggerDefinitionsOutput, error) {
			return client.ListLoggerDefinitions(ctx, &greengrass.ListLoggerDefinitionsInput{NextToken: token})
		},
		func(o *greengrass.ListLoggerDefinitionsOutput) (any, *string) {
			return o.Definitions, o.NextToken
		})
	if err != nil {
		return 0, 0, err
	}
	t2, i2, err := scanGGVersions(ctx, acct, region, st, scanID, "LoggerDefinitionVersions", TypeGreengrassLoggerDefinitionVersion, defs,
		func(defID, token *string) (versOut, error) {
			out, e := client.ListLoggerDefinitionVersions(ctx, &greengrass.ListLoggerDefinitionVersionsInput{LoggerDefinitionId: defID, NextToken: token})
			if e != nil {
				return versOut{}, e
			}
			return versOut{Versions: out.Versions, NextToken: out.NextToken}, nil
		})
	return t1 + t2, i1 + i2, err
}

func scanGGResource(ctx context.Context, client greengrassAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	defs, t1, i1, err := ggListDefs(ctx, acct, region, st, scanID, "ResourceDefinitions", TypeGreengrassResourceDefinition,
		func(token *string) (*greengrass.ListResourceDefinitionsOutput, error) {
			return client.ListResourceDefinitions(ctx, &greengrass.ListResourceDefinitionsInput{NextToken: token})
		},
		func(o *greengrass.ListResourceDefinitionsOutput) (any, *string) {
			return o.Definitions, o.NextToken
		})
	if err != nil {
		return 0, 0, err
	}
	t2, i2, err := scanGGVersions(ctx, acct, region, st, scanID, "ResourceDefinitionVersions", TypeGreengrassResourceDefinitionVersion, defs,
		func(defID, token *string) (versOut, error) {
			out, e := client.ListResourceDefinitionVersions(ctx, &greengrass.ListResourceDefinitionVersionsInput{ResourceDefinitionId: defID, NextToken: token})
			if e != nil {
				return versOut{}, e
			}
			return versOut{Versions: out.Versions, NextToken: out.NextToken}, nil
		})
	return t1 + t2, i1 + i2, err
}

func scanGGSubscription(ctx context.Context, client greengrassAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	defs, t1, i1, err := ggListDefs(ctx, acct, region, st, scanID, "SubscriptionDefinitions", TypeGreengrassSubscriptionDefinition,
		func(token *string) (*greengrass.ListSubscriptionDefinitionsOutput, error) {
			return client.ListSubscriptionDefinitions(ctx, &greengrass.ListSubscriptionDefinitionsInput{NextToken: token})
		},
		func(o *greengrass.ListSubscriptionDefinitionsOutput) (any, *string) {
			return o.Definitions, o.NextToken
		})
	if err != nil {
		return 0, 0, err
	}
	t2, i2, err := scanGGVersions(ctx, acct, region, st, scanID, "SubscriptionDefinitionVersions", TypeGreengrassSubscriptionDefinitionVersion, defs,
		func(defID, token *string) (versOut, error) {
			out, e := client.ListSubscriptionDefinitionVersions(ctx, &greengrass.ListSubscriptionDefinitionVersionsInput{SubscriptionDefinitionId: defID, NextToken: token})
			if e != nil {
				return versOut{}, e
			}
			return versOut{Versions: out.Versions, NextToken: out.NextToken}, nil
		})
	return t1 + t2, i1 + i2, err
}

func scanGGGroups(ctx context.Context, client greengrassAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var ids []string
	var token *string
	for {
		out, err := client.ListGroups(ctx, &greengrass.ListGroupsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "greengrass:ListGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("greengrass:ListGroups: %w", err)
		}
		for _, g := range out.Groups {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			id := sv(g.Id)
			ids = append(ids, id)
			label := sv(g.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGreengrassGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	t1, i1, err := upsertBatch(st, batch, "greengrass groups")
	if err != nil {
		return 0, 0, err
	}

	var vbatch []*store.Resource
	for _, gid := range ids {
		id := gid
		var vtoken *string
		for {
			out, verr := client.ListGroupVersions(ctx, &greengrass.ListGroupVersionsInput{GroupId: &id, NextToken: vtoken})
			if verr != nil {
				if isAccessDenied(verr) {
					break
				}
				return t1, i1, fmt.Errorf("greengrass:ListGroupVersions %s: %w", gid, verr)
			}
			for _, v := range out.Versions {
				arn := sv(v.Arn)
				if arn == "" {
					continue
				}
				label := sv(v.Version)
				if label == "" {
					label = sv(v.Id)
				}
				vbatch = append(vbatch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeGreengrassGroupVersion, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			vtoken = out.NextToken
		}
	}
	t2, i2, err := upsertBatch(st, vbatch, "greengrass group-versions")
	return t1 + t2, i1 + i2, err
}
