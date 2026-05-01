package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// ec2API is the narrow set of EC2 operations called across every ec2_*_scanners.go
// file in this package. EC2 has the broadest API surface in AWS (~80 ops here);
// the iface lifts every op the scanners use so unit tests can stub the whole
// service without standing up the real *ec2.Client. *ec2.Client satisfies this
// interface; tests substitute a hand-rolled implementation.
//
// New EC2 op needed by a scanner? Add the method here AND ensure the SDK has
// it (`go doc github.com/aws/aws-sdk-go-v2/service/ec2.Client.<Op>`). The
// "narrow API" rule (CLAUDE.md) is intentionally bent for EC2: keeping per-file
// ifaces would create 10+ overlapping shapes; one shared iface is the more
// maintainable trade-off.
type ec2API interface {
	// Direct (non-paginated) calls.
	DescribeAddresses(context.Context, *ec2.DescribeAddressesInput, ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error)
	DescribeCustomerGateways(context.Context, *ec2.DescribeCustomerGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeCustomerGatewaysOutput, error)
	DescribeKeyPairs(context.Context, *ec2.DescribeKeyPairsInput, ...func(*ec2.Options)) (*ec2.DescribeKeyPairsOutput, error)
	DescribePlacementGroups(context.Context, *ec2.DescribePlacementGroupsInput, ...func(*ec2.Options)) (*ec2.DescribePlacementGroupsOutput, error)
	DescribeTrafficMirrorFilterRules(context.Context, *ec2.DescribeTrafficMirrorFilterRulesInput, ...func(*ec2.Options)) (*ec2.DescribeTrafficMirrorFilterRulesOutput, error)
	DescribeVpcBlockPublicAccessExclusions(context.Context, *ec2.DescribeVpcBlockPublicAccessExclusionsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcBlockPublicAccessExclusionsOutput, error)
	DescribeVpcBlockPublicAccessOptions(context.Context, *ec2.DescribeVpcBlockPublicAccessOptionsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcBlockPublicAccessOptionsOutput, error)
	DescribeVpcEndpointServices(context.Context, *ec2.DescribeVpcEndpointServicesInput, ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointServicesOutput, error)
	DescribeVpnConnections(context.Context, *ec2.DescribeVpnConnectionsInput, ...func(*ec2.Options)) (*ec2.DescribeVpnConnectionsOutput, error)
	DescribeVpnGateways(context.Context, *ec2.DescribeVpnGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeVpnGatewaysOutput, error)
	GetSnapshotBlockPublicAccessState(context.Context, *ec2.GetSnapshotBlockPublicAccessStateInput, ...func(*ec2.Options)) (*ec2.GetSnapshotBlockPublicAccessStateOutput, error)
	SearchTransitGatewayRoutes(context.Context, *ec2.SearchTransitGatewayRoutesInput, ...func(*ec2.Options)) (*ec2.SearchTransitGatewayRoutesOutput, error)
	// Paginator-backed ops. NewListXxxPaginator(client, ...) constructors only
	// require the matching DescribeXxx / GetXxx / SearchXxx method on `client`,
	// so listing the underlying op is sufficient.
	DescribeCapacityReservationFleets(context.Context, *ec2.DescribeCapacityReservationFleetsInput, ...func(*ec2.Options)) (*ec2.DescribeCapacityReservationFleetsOutput, error)
	DescribeCapacityReservations(context.Context, *ec2.DescribeCapacityReservationsInput, ...func(*ec2.Options)) (*ec2.DescribeCapacityReservationsOutput, error)
	DescribeCarrierGateways(context.Context, *ec2.DescribeCarrierGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeCarrierGatewaysOutput, error)
	DescribeClientVpnAuthorizationRules(context.Context, *ec2.DescribeClientVpnAuthorizationRulesInput, ...func(*ec2.Options)) (*ec2.DescribeClientVpnAuthorizationRulesOutput, error)
	DescribeClientVpnEndpoints(context.Context, *ec2.DescribeClientVpnEndpointsInput, ...func(*ec2.Options)) (*ec2.DescribeClientVpnEndpointsOutput, error)
	DescribeClientVpnRoutes(context.Context, *ec2.DescribeClientVpnRoutesInput, ...func(*ec2.Options)) (*ec2.DescribeClientVpnRoutesOutput, error)
	DescribeClientVpnTargetNetworks(context.Context, *ec2.DescribeClientVpnTargetNetworksInput, ...func(*ec2.Options)) (*ec2.DescribeClientVpnTargetNetworksOutput, error)
	DescribeDhcpOptions(context.Context, *ec2.DescribeDhcpOptionsInput, ...func(*ec2.Options)) (*ec2.DescribeDhcpOptionsOutput, error)
	DescribeEgressOnlyInternetGateways(context.Context, *ec2.DescribeEgressOnlyInternetGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeEgressOnlyInternetGatewaysOutput, error)
	DescribeFleets(context.Context, *ec2.DescribeFleetsInput, ...func(*ec2.Options)) (*ec2.DescribeFleetsOutput, error)
	DescribeFlowLogs(context.Context, *ec2.DescribeFlowLogsInput, ...func(*ec2.Options)) (*ec2.DescribeFlowLogsOutput, error)
	DescribeHosts(context.Context, *ec2.DescribeHostsInput, ...func(*ec2.Options)) (*ec2.DescribeHostsOutput, error)
	DescribeImages(context.Context, *ec2.DescribeImagesInput, ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
	DescribeInstanceConnectEndpoints(context.Context, *ec2.DescribeInstanceConnectEndpointsInput, ...func(*ec2.Options)) (*ec2.DescribeInstanceConnectEndpointsOutput, error)
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeInternetGateways(context.Context, *ec2.DescribeInternetGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error)
	DescribeIpamPools(context.Context, *ec2.DescribeIpamPoolsInput, ...func(*ec2.Options)) (*ec2.DescribeIpamPoolsOutput, error)
	DescribeIpamResourceDiscoveries(context.Context, *ec2.DescribeIpamResourceDiscoveriesInput, ...func(*ec2.Options)) (*ec2.DescribeIpamResourceDiscoveriesOutput, error)
	DescribeIpamResourceDiscoveryAssociations(context.Context, *ec2.DescribeIpamResourceDiscoveryAssociationsInput, ...func(*ec2.Options)) (*ec2.DescribeIpamResourceDiscoveryAssociationsOutput, error)
	DescribeIpamScopes(context.Context, *ec2.DescribeIpamScopesInput, ...func(*ec2.Options)) (*ec2.DescribeIpamScopesOutput, error)
	DescribeIpams(context.Context, *ec2.DescribeIpamsInput, ...func(*ec2.Options)) (*ec2.DescribeIpamsOutput, error)
	DescribeLaunchTemplates(context.Context, *ec2.DescribeLaunchTemplatesInput, ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error)
	DescribeLocalGatewayRouteTables(context.Context, *ec2.DescribeLocalGatewayRouteTablesInput, ...func(*ec2.Options)) (*ec2.DescribeLocalGatewayRouteTablesOutput, error)
	DescribeLocalGatewayRouteTableVirtualInterfaceGroupAssociations(context.Context, *ec2.DescribeLocalGatewayRouteTableVirtualInterfaceGroupAssociationsInput, ...func(*ec2.Options)) (*ec2.DescribeLocalGatewayRouteTableVirtualInterfaceGroupAssociationsOutput, error)
	DescribeLocalGatewayRouteTableVpcAssociations(context.Context, *ec2.DescribeLocalGatewayRouteTableVpcAssociationsInput, ...func(*ec2.Options)) (*ec2.DescribeLocalGatewayRouteTableVpcAssociationsOutput, error)
	DescribeLocalGatewayVirtualInterfaceGroups(context.Context, *ec2.DescribeLocalGatewayVirtualInterfaceGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeLocalGatewayVirtualInterfaceGroupsOutput, error)
	DescribeLocalGatewayVirtualInterfaces(context.Context, *ec2.DescribeLocalGatewayVirtualInterfacesInput, ...func(*ec2.Options)) (*ec2.DescribeLocalGatewayVirtualInterfacesOutput, error)
	DescribeRouteServers(context.Context, *ec2.DescribeRouteServersInput, ...func(*ec2.Options)) (*ec2.DescribeRouteServersOutput, error)
	DescribeRouteServerEndpoints(context.Context, *ec2.DescribeRouteServerEndpointsInput, ...func(*ec2.Options)) (*ec2.DescribeRouteServerEndpointsOutput, error)
	DescribeRouteServerPeers(context.Context, *ec2.DescribeRouteServerPeersInput, ...func(*ec2.Options)) (*ec2.DescribeRouteServerPeersOutput, error)
	DescribeManagedPrefixLists(context.Context, *ec2.DescribeManagedPrefixListsInput, ...func(*ec2.Options)) (*ec2.DescribeManagedPrefixListsOutput, error)
	DescribeNatGateways(context.Context, *ec2.DescribeNatGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error)
	DescribeNetworkAcls(context.Context, *ec2.DescribeNetworkAclsInput, ...func(*ec2.Options)) (*ec2.DescribeNetworkAclsOutput, error)
	DescribeNetworkInsightsAccessScopeAnalyses(context.Context, *ec2.DescribeNetworkInsightsAccessScopeAnalysesInput, ...func(*ec2.Options)) (*ec2.DescribeNetworkInsightsAccessScopeAnalysesOutput, error)
	DescribeNetworkInsightsAccessScopes(context.Context, *ec2.DescribeNetworkInsightsAccessScopesInput, ...func(*ec2.Options)) (*ec2.DescribeNetworkInsightsAccessScopesOutput, error)
	DescribeNetworkInsightsAnalyses(context.Context, *ec2.DescribeNetworkInsightsAnalysesInput, ...func(*ec2.Options)) (*ec2.DescribeNetworkInsightsAnalysesOutput, error)
	DescribeNetworkInsightsPaths(context.Context, *ec2.DescribeNetworkInsightsPathsInput, ...func(*ec2.Options)) (*ec2.DescribeNetworkInsightsPathsOutput, error)
	DescribeNetworkInterfacePermissions(context.Context, *ec2.DescribeNetworkInterfacePermissionsInput, ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacePermissionsOutput, error)
	DescribeNetworkInterfaces(context.Context, *ec2.DescribeNetworkInterfacesInput, ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error)
	DescribeRouteTables(context.Context, *ec2.DescribeRouteTablesInput, ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error)
	DescribeSecurityGroups(context.Context, *ec2.DescribeSecurityGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	DescribeSecurityGroupVpcAssociations(context.Context, *ec2.DescribeSecurityGroupVpcAssociationsInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupVpcAssociationsOutput, error)
	DescribeSpotFleetRequests(context.Context, *ec2.DescribeSpotFleetRequestsInput, ...func(*ec2.Options)) (*ec2.DescribeSpotFleetRequestsOutput, error)
	DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeTrafficMirrorFilters(context.Context, *ec2.DescribeTrafficMirrorFiltersInput, ...func(*ec2.Options)) (*ec2.DescribeTrafficMirrorFiltersOutput, error)
	DescribeTrafficMirrorSessions(context.Context, *ec2.DescribeTrafficMirrorSessionsInput, ...func(*ec2.Options)) (*ec2.DescribeTrafficMirrorSessionsOutput, error)
	DescribeTrafficMirrorTargets(context.Context, *ec2.DescribeTrafficMirrorTargetsInput, ...func(*ec2.Options)) (*ec2.DescribeTrafficMirrorTargetsOutput, error)
	DescribeTransitGatewayAttachments(context.Context, *ec2.DescribeTransitGatewayAttachmentsInput, ...func(*ec2.Options)) (*ec2.DescribeTransitGatewayAttachmentsOutput, error)
	DescribeTransitGatewayConnectPeers(context.Context, *ec2.DescribeTransitGatewayConnectPeersInput, ...func(*ec2.Options)) (*ec2.DescribeTransitGatewayConnectPeersOutput, error)
	DescribeTransitGatewayConnects(context.Context, *ec2.DescribeTransitGatewayConnectsInput, ...func(*ec2.Options)) (*ec2.DescribeTransitGatewayConnectsOutput, error)
	DescribeTransitGatewayMulticastDomains(context.Context, *ec2.DescribeTransitGatewayMulticastDomainsInput, ...func(*ec2.Options)) (*ec2.DescribeTransitGatewayMulticastDomainsOutput, error)
	DescribeTransitGatewayPeeringAttachments(context.Context, *ec2.DescribeTransitGatewayPeeringAttachmentsInput, ...func(*ec2.Options)) (*ec2.DescribeTransitGatewayPeeringAttachmentsOutput, error)
	DescribeTransitGatewayRouteTables(context.Context, *ec2.DescribeTransitGatewayRouteTablesInput, ...func(*ec2.Options)) (*ec2.DescribeTransitGatewayRouteTablesOutput, error)
	DescribeTransitGateways(context.Context, *ec2.DescribeTransitGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeTransitGatewaysOutput, error)
	DescribeTransitGatewayVpcAttachments(context.Context, *ec2.DescribeTransitGatewayVpcAttachmentsInput, ...func(*ec2.Options)) (*ec2.DescribeTransitGatewayVpcAttachmentsOutput, error)
	DescribeVerifiedAccessEndpoints(context.Context, *ec2.DescribeVerifiedAccessEndpointsInput, ...func(*ec2.Options)) (*ec2.DescribeVerifiedAccessEndpointsOutput, error)
	DescribeVerifiedAccessGroups(context.Context, *ec2.DescribeVerifiedAccessGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeVerifiedAccessGroupsOutput, error)
	DescribeVerifiedAccessInstances(context.Context, *ec2.DescribeVerifiedAccessInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeVerifiedAccessInstancesOutput, error)
	DescribeVerifiedAccessTrustProviders(context.Context, *ec2.DescribeVerifiedAccessTrustProvidersInput, ...func(*ec2.Options)) (*ec2.DescribeVerifiedAccessTrustProvidersOutput, error)
	DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	DescribeVpcEndpointConnectionNotifications(context.Context, *ec2.DescribeVpcEndpointConnectionNotificationsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointConnectionNotificationsOutput, error)
	DescribeVpcEndpointServicePermissions(context.Context, *ec2.DescribeVpcEndpointServicePermissionsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointServicePermissionsOutput, error)
	DescribeVpcEndpoints(context.Context, *ec2.DescribeVpcEndpointsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error)
	DescribeVpcPeeringConnections(context.Context, *ec2.DescribeVpcPeeringConnectionsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcPeeringConnectionsOutput, error)
	DescribeVpcs(context.Context, *ec2.DescribeVpcsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	GetIpamPoolAllocations(context.Context, *ec2.GetIpamPoolAllocationsInput, ...func(*ec2.Options)) (*ec2.GetIpamPoolAllocationsOutput, error)
	GetIpamPoolCidrs(context.Context, *ec2.GetIpamPoolCidrsInput, ...func(*ec2.Options)) (*ec2.GetIpamPoolCidrsOutput, error)
	GetTransitGatewayMulticastDomainAssociations(context.Context, *ec2.GetTransitGatewayMulticastDomainAssociationsInput, ...func(*ec2.Options)) (*ec2.GetTransitGatewayMulticastDomainAssociationsOutput, error)
	GetTransitGatewayRouteTableAssociations(context.Context, *ec2.GetTransitGatewayRouteTableAssociationsInput, ...func(*ec2.Options)) (*ec2.GetTransitGatewayRouteTableAssociationsOutput, error)
	GetTransitGatewayRouteTablePropagations(context.Context, *ec2.GetTransitGatewayRouteTablePropagationsInput, ...func(*ec2.Options)) (*ec2.GetTransitGatewayRouteTablePropagationsOutput, error)
	SearchLocalGatewayRoutes(context.Context, *ec2.SearchLocalGatewayRoutesInput, ...func(*ec2.Options)) (*ec2.SearchLocalGatewayRoutesOutput, error)
	SearchTransitGatewayMulticastGroups(context.Context, *ec2.SearchTransitGatewayMulticastGroupsInput, ...func(*ec2.Options)) (*ec2.SearchTransitGatewayMulticastGroupsOutput, error)
}
