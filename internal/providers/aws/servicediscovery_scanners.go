package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/servicediscovery"
	sdtypes "github.com/aws/aws-sdk-go-v2/service/servicediscovery/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeServiceDiscoveryHTTPNamespace, Service: "servicediscovery", Leaf: true})
	registerType(restype.Descriptor{Type: TypeServiceDiscoveryPrivateDNSNamespace, Service: "servicediscovery"})
	registerType(restype.Descriptor{Type: TypeServiceDiscoveryPublicDNSNamespace, Service: "servicediscovery"})
	registerType(restype.Descriptor{Type: TypeServiceDiscoveryService, Service: "servicediscovery"})
	registerType(restype.Descriptor{Type: TypeServiceDiscoveryInstance, Service: "servicediscovery"})
	registerService(serviceEntry{
		name: "aws:servicediscovery",
		fn:   scanServiceDiscovery,
	})
}

type serviceDiscoveryAPI interface {
	ListNamespaces(context.Context, *servicediscovery.ListNamespacesInput, ...func(*servicediscovery.Options)) (*servicediscovery.ListNamespacesOutput, error)
	ListServices(context.Context, *servicediscovery.ListServicesInput, ...func(*servicediscovery.Options)) (*servicediscovery.ListServicesOutput, error)
	ListInstances(context.Context, *servicediscovery.ListInstancesInput, ...func(*servicediscovery.Options)) (*servicediscovery.ListInstancesOutput, error)
}

// scanServiceDiscovery discovers Cloud Map namespaces (split into HTTP /
// private-DNS / public-DNS by NamespaceType), services, and per-service
// instances.
func scanServiceDiscovery(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := servicediscovery.NewFromConfig(acct.cfg, func(o *servicediscovery.Options) { o.Region = region })

	t, i, ferr := scanSDNamespaces(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	serviceIDs, t, i, ferr := scanSDServices(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, sid := range serviceIDs {
		t, i, ferr = scanSDInstances(ctx, client, acct, region, st, scanID, sid)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanSDNamespaces(ctx context.Context, client serviceDiscoveryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := servicediscovery.NewListNamespacesPaginator(client, &servicediscovery.ListNamespacesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "servicediscovery:ListNamespaces", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("servicediscovery:ListNamespaces: %w", err)
		}
		for _, n := range out.Namespaces {
			arn := sv(n.Arn)
			if arn == "" {
				continue
			}
			var dt string
			switch n.Type {
			case sdtypes.NamespaceTypeHttp:
				dt = TypeServiceDiscoveryHTTPNamespace
			case sdtypes.NamespaceTypeDnsPrivate:
				dt = TypeServiceDiscoveryPrivateDNSNamespace
			case sdtypes.NamespaceTypeDnsPublic:
				dt = TypeServiceDiscoveryPublicDNSNamespace
			default:
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: dt, NativeID: arn,
				Name: n.Name, Region: &region,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "servicediscovery namespaces")
}

func scanSDServices(ctx context.Context, client serviceDiscoveryAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := servicediscovery.NewListServicesPaginator(client, &servicediscovery.ListServicesInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "servicediscovery:ListServices", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("servicediscovery:ListServices: %w", err)
		}
		for _, s := range out.Services {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			if id := sv(s.Id); id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeServiceDiscoveryService, NativeID: arn,
				Name: s.Name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "servicediscovery services")
	return ids, t, i, err
}

// scanSDInstances synthesizes an ARN per instance: parent service ARN +
// /instance/{instanceId}. Cloud Map does not issue native ARNs for
// instances; the synthetic shape mirrors the IAM action namespace
// (servicediscovery:GetInstance / RegisterInstance).
func scanSDInstances(ctx context.Context, client serviceDiscoveryAPI, acct *account, region string, st *store.Store, scanID string, serviceID string) (int, int, error) {
	sid := serviceID
	pager := servicediscovery.NewListInstancesPaginator(client, &servicediscovery.ListInstancesInput{ServiceId: &sid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "servicediscovery:ListInstances", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("servicediscovery:ListInstances: %w", err)
		}
		for _, in := range out.Instances {
			id := sv(in.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:servicediscovery:%s:%s:service/%s/instance/%s", region, acct.ID, sid, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeServiceDiscoveryInstance, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(in), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "servicediscovery instances")
}
