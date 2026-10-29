package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/greengrassv2"
	ggv2types "github.com/aws/aws-sdk-go-v2/service/greengrassv2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGreengrassV2ComponentVersion, Service: "greengrass-v2"})
	registerType(restype.Descriptor{Type: TypeGreengrassV2Deployment, Service: "greengrass-v2"})
	registerType(restype.Descriptor{Type: TypeGreengrassV2Component, Service: "greengrass-v2"})
	registerType(restype.Descriptor{Type: TypeGreengrassV2CoreDevice, Service: "greengrass-v2"})
	registerService(serviceEntry{
		name: "aws:greengrass-v2",
		fn:   scanGreengrassV2,
	})
}

type greengrassV2API interface {
	ListComponents(context.Context, *greengrassv2.ListComponentsInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListComponentsOutput, error)
	ListComponentVersions(context.Context, *greengrassv2.ListComponentVersionsInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListComponentVersionsOutput, error)
	ListCoreDevices(context.Context, *greengrassv2.ListCoreDevicesInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListCoreDevicesOutput, error)
	ListDeployments(context.Context, *greengrassv2.ListDeploymentsInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListDeploymentsOutput, error)
	ListTagsForResource(context.Context, *greengrassv2.ListTagsForResourceInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListTagsForResourceOutput, error)
}

const (
	ggv2TagsOp = "greengrass-v2:ListTagsForResource"
	// ggv2PageCap is the MaxResults maximum the model gives every Greengrass
	// v2 listing read here.
	ggv2PageCap = 100
)

// ggv2NotFound is the not-found code ListComponentVersions and
// ListTagsForResource model, for a resource deleted after it was listed.
var ggv2NotFound = []string{"ResourceNotFoundException"}

// scanGreengrassV2 discovers Greengrass v2 components (catalog artifacts),
// their component versions, core devices, and deployments, each with its
// tags (ggv2Tags). Every listing runs as its own phase, one at a time and in
// that order: a phase's error does not stop the later ones; a failed write
// and ctx's end do, and so does a first phase that stores nothing and fails
// only with NXDOMAIN, since the region does not serve the service
// (runHostPhases). Each listing is written a page at a time, the component
// versions are read for the components stored this scan, and the tag reads'
// denials are one warning for the whole scan.
//
// IoT Greengrass V2 control-plane operations are available only in the regions
// its SDK endpoint table lists. SDK still resolves <svc>.<region>.amazonaws.com
// for the others; AWS rejects with TooManyRequestsException 429 → SDK retries
// 10x → ~2m wall time before surfacing. Checking the table first
// short-circuits the cost on call #1.
func scanGreengrassV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if !sdkTableAllows("aws:greengrass-v2", region) {
		return 0, 0, nil
	}
	client := greengrassv2.NewFromConfig(acct.cfg, func(o *greengrassv2.Options) { o.Region = region })
	tags := ggv2Tags(client).sharingDenials()
	var components []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "greengrass-v2", 0, []hostPhase{
		{op: "greengrass-v2:ListComponents", run: func(p *phaseTotals) (failed bool) {
			components, failed = scanGGV2Components(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "greengrass-v2:ListComponentVersions", run: func(p *phaseTotals) bool {
			return scanGGV2ComponentVersions(ctx, client, components, acct, region, st, scanID, tags, p)
		}},
		{op: "greengrass-v2:ListCoreDevices", run: func(p *phaseTotals) bool {
			return scanGGV2CoreDevices(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "greengrass-v2:ListDeployments", run: func(p *phaseTotals) bool {
			return scanGGV2Deployments(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// ggv2Tags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the listed Arn for a component and a component version, and for
// a core device and a deployment the ARN built in the Service Reference's
// format for its type (coreDevices:{thingName}, deployments:{deploymentId}).
// The Service Reference lists all four types on ListTagsForResource, and the
// op's model accepts components, deployments and coreDevices ARNs. The op
// returns every tag at once. ResourceNotFoundException, its modeled
// not-found, drops a resource deleted after it was listed.
func ggv2Tags(client greengrassV2API) rowTagReader {
	return rowTagReader{
		op:       ggv2TagsOp,
		notFound: ggv2NotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &greengrassv2.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// ggv2Row is the row of a listed element of typ, stored under nativeID,
// named name. An element without its id (id empty) is not stored: why names
// such elements in the listing's warning, and key the element (noIdentifier
// when empty). Stored, every such element would share one NativeID (empty,
// or the ARN built from an empty id) and merge into one resource.
func ggv2Row(acct *account, region, scanID, typ, id, nativeID, why, key string, name, status *string, element any) (*store.Resource, string, string) {
	if id == "" {
		return nil, why, cmp.Or(key, noIdentifier)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID,
		Name: name, Region: &region, Status: status,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}, "", ""
}

// scanGGV2Components stores the components, each page written as soon as its
// rows' tags are read, and returns the stored ones keyed by their ARN
// (ListComponentVersions takes it): the parents of the component version
// phase. The listing asks for Scope PRIVATE, the op's default: the account's
// own components, so no AWS-provided public component (aws.greengrass.*) is
// listed or has its tags read. It reports whether a write failed, after
// which the scan must stop.
func scanGGV2Components(ctx context.Context, client greengrassV2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "greengrass-v2:ListComponents", "components", "Arn",
		func(ctx context.Context, token *string) ([]ggv2types.Component, *string, error) {
			out, err := client.ListComponents(ctx, &greengrassv2.ListComponentsInput{
				Scope: ggv2types.ComponentVisibilityScopePrivate, MaxResults: sdkaws.Int32(ggv2PageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Components, out.NextToken, nil
		},
		func(c ggv2types.Component) (*store.Resource, string, string) {
			arn := sv(c.Arn)
			return ggv2Row(acct, region, scanID, TypeGreengrassV2Component, arn, arn, "components have no Arn", sv(c.ComponentName), c.ComponentName, nil, c)
		},
		func(c ggv2types.Component) string { return sv(c.Arn) },
		tags, p)
}

// scanGGV2ComponentVersions stores each component's versions, the components
// read concurrently and each page written as soon as its rows' tags are read
// (storeEachParentPagesTags), so one component's failure costs the others
// none of theirs. A version is named {componentName}:{componentVersion}. It
// reports whether a write failed, after which the scan must stop.
func scanGGV2ComponentVersions(ctx context.Context, client greengrassV2API, components []childParent, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "greengrass-v2:ListComponentVersions", ggv2NotFound, components,
		func(ctx context.Context, component string, token *string) ([]ggv2types.ComponentVersionListItem, *string, error) {
			out, err := client.ListComponentVersions(ctx, &greengrassv2.ListComponentVersionsInput{
				Arn: &component, MaxResults: sdkaws.Int32(ggv2PageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ComponentVersions, out.NextToken, nil
		},
		func(_ string, v ggv2types.ComponentVersionListItem) (*store.Resource, string, string) {
			arn := sv(v.Arn)
			label := fmt.Sprintf("%s:%s", sv(v.ComponentName), sv(v.ComponentVersion))
			// A version carrying neither part is keyed noIdentifier, not ":".
			key := ""
			if sv(v.ComponentName) != "" || sv(v.ComponentVersion) != "" {
				key = label
			}
			return ggv2Row(acct, region, scanID, TypeGreengrassV2ComponentVersion, arn, arn, "component versions have no Arn", key, &label, nil, v)
		},
		tags, p)
}

// scanGGV2CoreDevices stores the core devices, each page written as soon as
// its rows' tags are read. ListCoreDevices summaries carry the IoT thing
// name and status but no ARN, so the row is stored under the canonical
// coreDevices ARN built from the thing name. It reports whether a write
// failed, after which the scan must stop.
func scanGGV2CoreDevices(ctx context.Context, client greengrassV2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "greengrass-v2:ListCoreDevices",
		func(ctx context.Context, token *string) ([]ggv2types.CoreDevice, *string, error) {
			out, err := client.ListCoreDevices(ctx, &greengrassv2.ListCoreDevicesInput{MaxResults: sdkaws.Int32(ggv2PageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.CoreDevices, out.NextToken, nil
		},
		func(d ggv2types.CoreDevice) (*store.Resource, string, string) {
			thing := sv(d.CoreDeviceThingName)
			status := string(d.Status)
			return ggv2Row(acct, region, scanID, TypeGreengrassV2CoreDevice, thing,
				arnFor(acct.partition(), "greengrass", region, acct.ID, "coreDevices:"+thing),
				"core devices have no CoreDeviceThingName", "", d.CoreDeviceThingName, &status, d)
		},
		tags, p)
}

// scanGGV2Deployments stores the deployments (the op's default history
// filter: each target's latest revision), each page written as soon as its
// rows' tags are read. The summaries carry no ARN, so the row is stored under
// the deployments ARN built from the deployment id. It reports whether a
// write failed, after which the scan must stop.
func scanGGV2Deployments(ctx context.Context, client greengrassV2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "greengrass-v2:ListDeployments",
		func(ctx context.Context, token *string) ([]ggv2types.Deployment, *string, error) {
			out, err := client.ListDeployments(ctx, &greengrassv2.ListDeploymentsInput{MaxResults: sdkaws.Int32(ggv2PageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Deployments, out.NextToken, nil
		},
		func(d ggv2types.Deployment) (*store.Resource, string, string) {
			id := sv(d.DeploymentId)
			status := string(d.DeploymentStatus)
			return ggv2Row(acct, region, scanID, TypeGreengrassV2Deployment, id,
				arnFor(acct.partition(), "greengrass", region, acct.ID, "deployments:"+id),
				"deployments have no DeploymentId", sv(d.DeploymentName), d.DeploymentName, &status, d)
		},
		tags, p)
}
