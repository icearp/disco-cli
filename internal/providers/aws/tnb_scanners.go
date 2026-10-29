package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/tnb"
	tnbtypes "github.com/aws/aws-sdk-go-v2/service/tnb/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeTnbFunctionInstance, Service: "tnb"})
	registerType(restype.Descriptor{Type: TypeTnbFunctionPackage, Service: "tnb"})
	registerType(restype.Descriptor{Type: TypeTnbNetworkInstance, Service: "tnb"})
	registerType(restype.Descriptor{Type: TypeTnbNetworkPackage, Service: "tnb"})
	registerType(restype.Descriptor{Type: TypeTnbNetworkOperation, Service: "tnb"})
	registerService(serviceEntry{
		name: "aws:tnb",
		fn:   scanTnb,
	})
}

type tnbAPI interface {
	ListSolFunctionInstances(context.Context, *tnb.ListSolFunctionInstancesInput, ...func(*tnb.Options)) (*tnb.ListSolFunctionInstancesOutput, error)
	ListSolFunctionPackages(context.Context, *tnb.ListSolFunctionPackagesInput, ...func(*tnb.Options)) (*tnb.ListSolFunctionPackagesOutput, error)
	ListSolNetworkInstances(context.Context, *tnb.ListSolNetworkInstancesInput, ...func(*tnb.Options)) (*tnb.ListSolNetworkInstancesOutput, error)
	ListSolNetworkPackages(context.Context, *tnb.ListSolNetworkPackagesInput, ...func(*tnb.Options)) (*tnb.ListSolNetworkPackagesOutput, error)
	ListSolNetworkOperations(context.Context, *tnb.ListSolNetworkOperationsInput, ...func(*tnb.Options)) (*tnb.ListSolNetworkOperationsOutput, error)
	ListTagsForResource(context.Context, *tnb.ListTagsForResourceInput, ...func(*tnb.Options)) (*tnb.ListTagsForResourceOutput, error)
}

const (
	tnbTagsOp = "tnb:ListTagsForResource"
	// tnbPageCap is the MaxResults maximum of every TNB listing read here.
	tnbPageCap = 100
)

// scanTnb discovers TNB function packages, function instances, network
// packages, network instances and network operations, each with its tags
// (tnbTags). Every listing runs as its own phase, one at a time and in that
// order: a phase's error does not stop the later ones; a failed write and
// ctx's end do, and so does a first phase that stores nothing and fails only
// with NXDOMAIN, since the region does not serve the service
// (runHostPhases). The tag reads' denials are one warning for the whole scan.
// AWS may refuse every TNB op as deprecated (403, "All API operations are
// blocked"); each phase then fails alike and dispatch reports the service as
// blocked, not as an error (isServiceBlocked).
func scanTnb(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := tnb.NewFromConfig(acct.cfg, func(o *tnb.Options) { o.Region = region })
	tags := tnbTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "tnb", 0, []hostPhase{
		{op: "tnb:ListSolFunctionPackages", run: func(p *phaseTotals) bool {
			return scanTnbFunctionPackages(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "tnb:ListSolFunctionInstances", run: func(p *phaseTotals) bool {
			return scanTnbFunctionInstances(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "tnb:ListSolNetworkPackages", run: func(p *phaseTotals) bool {
			return scanTnbNetworkPackages(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "tnb:ListSolNetworkInstances", run: func(p *phaseTotals) bool {
			return scanTnbNetworkInstances(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "tnb:ListSolNetworkOperations", run: func(p *phaseTotals) bool {
			return scanTnbNetworkOperations(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// tnbTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference names no
// resource on ListTagsForResource, but its TagResource takes every type
// stored here (function-instance, function-package, network-instance,
// network-operation, network-package), the op's model takes the ARN of any
// TNB resource type of the aws partition (TNBResourceArn ^arn:aws:tnb:), the
// only partition TNB is offered in, and each type's Get response carries
// Tags. The op returns every tag at once.
// ResourceNotFoundException, its modeled not-found, drops a resource deleted
// after it was listed.
func tnbTags(client tnbAPI) rowTagReader {
	return rowTagReader{
		op:       tnbTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &tnb.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// tnbRow is the row of a listed element stored under its ARN, named name; an
// element without an ARN is not stored (arnRow), and what names such
// elements in the listing's warning.
func tnbRow(acct *account, region, scanID, typ, what string, arn, name *string, status string, element any) (*store.Resource, string, string) {
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: name, Region: &region, Status: &status,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanTnbFunctionPackages stores the function packages, each page written as
// soon as its rows' tags are read. It reports whether a write failed, after
// which the scan must stop.
func scanTnbFunctionPackages(ctx context.Context, client tnbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "tnb:ListSolFunctionPackages",
		func(ctx context.Context, token *string) ([]tnbtypes.ListSolFunctionPackageInfo, *string, error) {
			out, err := client.ListSolFunctionPackages(ctx, &tnb.ListSolFunctionPackagesInput{MaxResults: sdkaws.Int32(tnbPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FunctionPackages, out.NextToken, nil
		},
		func(f tnbtypes.ListSolFunctionPackageInfo) (*store.Resource, string, string) {
			return tnbRow(acct, region, scanID, TypeTnbFunctionPackage, "function packages", f.Arn, f.Id, string(f.OnboardingState), f)
		},
		tags, p)
}

// scanTnbFunctionInstances stores the function instances, each page written
// as soon as its rows' tags are read. It reports whether a write failed,
// after which the scan must stop.
func scanTnbFunctionInstances(ctx context.Context, client tnbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "tnb:ListSolFunctionInstances",
		func(ctx context.Context, token *string) ([]tnbtypes.ListSolFunctionInstanceInfo, *string, error) {
			out, err := client.ListSolFunctionInstances(ctx, &tnb.ListSolFunctionInstancesInput{MaxResults: sdkaws.Int32(tnbPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FunctionInstances, out.NextToken, nil
		},
		func(f tnbtypes.ListSolFunctionInstanceInfo) (*store.Resource, string, string) {
			return tnbRow(acct, region, scanID, TypeTnbFunctionInstance, "function instances", f.Arn, f.Id, string(f.InstantiationState), f)
		},
		tags, p)
}

// scanTnbNetworkPackages stores the network packages, each page written as
// soon as its rows' tags are read. It reports whether a write failed, after
// which the scan must stop.
func scanTnbNetworkPackages(ctx context.Context, client tnbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "tnb:ListSolNetworkPackages",
		func(ctx context.Context, token *string) ([]tnbtypes.ListSolNetworkPackageInfo, *string, error) {
			out, err := client.ListSolNetworkPackages(ctx, &tnb.ListSolNetworkPackagesInput{MaxResults: sdkaws.Int32(tnbPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.NetworkPackages, out.NextToken, nil
		},
		func(n tnbtypes.ListSolNetworkPackageInfo) (*store.Resource, string, string) {
			return tnbRow(acct, region, scanID, TypeTnbNetworkPackage, "network packages", n.Arn, n.NsdName, string(n.NsdOnboardingState), n)
		},
		tags, p)
}

// scanTnbNetworkInstances stores the network instances, each page written as
// soon as its rows' tags are read. It reports whether a write failed, after
// which the scan must stop.
func scanTnbNetworkInstances(ctx context.Context, client tnbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "tnb:ListSolNetworkInstances",
		func(ctx context.Context, token *string) ([]tnbtypes.ListSolNetworkInstanceInfo, *string, error) {
			out, err := client.ListSolNetworkInstances(ctx, &tnb.ListSolNetworkInstancesInput{MaxResults: sdkaws.Int32(tnbPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.NetworkInstances, out.NextToken, nil
		},
		func(n tnbtypes.ListSolNetworkInstanceInfo) (*store.Resource, string, string) {
			return tnbRow(acct, region, scanID, TypeTnbNetworkInstance, "network instances", n.Arn, n.NsInstanceName, string(n.NsState), n)
		},
		tags, p)
}

// scanTnbNetworkOperations stores the network operations (each instantiate,
// update or terminate run on a network instance; an operation has its own
// ARN and tags), each page written as soon as its rows' tags are read. It
// reports whether a write failed, after which the scan must stop.
func scanTnbNetworkOperations(ctx context.Context, client tnbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "tnb:ListSolNetworkOperations",
		func(ctx context.Context, token *string) ([]tnbtypes.ListSolNetworkOperationsInfo, *string, error) {
			out, err := client.ListSolNetworkOperations(ctx, &tnb.ListSolNetworkOperationsInput{MaxResults: sdkaws.Int32(tnbPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.NetworkOperations, out.NextToken, nil
		},
		func(o tnbtypes.ListSolNetworkOperationsInfo) (*store.Resource, string, string) {
			return tnbRow(acct, region, scanID, TypeTnbNetworkOperation, "network operations", o.Arn, o.Id, string(o.OperationState), o)
		},
		tags, p)
}
