package aws

import (
	"slices"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// efpEndpointPermissions is the efpPhase of scanVPCEndpointServicePermissions
// (shared tests in ec2_ipam_scanners_test.go).
var efpEndpointPermissions = efpPhase{
	name: "vpc endpoint service permissions", op: "ec2:DescribeVpcEndpointServicePermissions", sdkOp: "DescribeVpcEndpointServicePermissions",
	typ: TypeEC2VPCEndpointServicePermissions, parentType: TypeEC2VPCEndpointService,
	parentARN: func(id string) string { return elxARN("vpc-endpoint-service", id) },
	pageSize:  1000,
	request: func(in any) (string, int32, *string) {
		p := in.(*ec2.DescribeVpcEndpointServicePermissionsInput)
		return sv(p.ServiceId), sv32(p.MaxResults), p.NextToken
	},
	page1: &ec2.DescribeVpcEndpointServicePermissionsOutput{NextToken: ptrStr(elxPageNext), AllowedPrincipals: []ec2types.AllowedPrincipal{{
		Principal: ptrStr("arn:aws:iam::210987654321:root"), PrincipalType: ec2types.PrincipalTypeAccount,
		ServiceId: ptrStr(efpParent1), ServicePermissionId: ptrStr("vpce-svc-perm-1"),
		Tags: []ec2types.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}},
	}}},
	page2: &ec2.DescribeVpcEndpointServicePermissionsOutput{AllowedPrincipals: []ec2types.AllowedPrincipal{
		{Principal: ptrStr("*"), PrincipalType: ec2types.PrincipalTypeAll, ServiceId: ptrStr(efpParent1), ServicePermissionId: ptrStr("vpce-svc-perm-2")},
		{ServiceId: ptrStr(efpParent1), ServicePermissionId: ptrStr("vpce-svc-perm-x")},
	}},
	parent2: &ec2.DescribeVpcEndpointServicePermissionsOutput{AllowedPrincipals: []ec2types.AllowedPrincipal{
		{Principal: ptrStr("arn:aws:iam::210987654321:role/r"), PrincipalType: ec2types.PrincipalTypeRole, ServiceId: ptrStr(efpParent2), ServicePermissionId: ptrStr("vpce-svc-perm-3")},
	}},
	empty: &ec2.DescribeVpcEndpointServicePermissionsOutput{},
	run:   scanVPCEndpointServicePermissions,
	rows: map[string]mexRow{
		elxARN("vpc-endpoint-service-permission", efpParent1+"/arn:aws:iam::210987654321:root"): {
			attrs: `{"Principal":"arn:aws:iam::210987654321:root","PrincipalType":"Account","ServiceId":"p-efp-0001","ServicePermissionId":"vpce-svc-perm-1","Tags":[{"Key":"env","Value":"prod"}]}`,
			tags:  map[string]string{"env": "prod"},
		},
		elxARN("vpc-endpoint-service-permission", efpParent1+"/*"): {
			attrs: `{"Principal":"*","PrincipalType":"All","ServiceId":"p-efp-0001","ServicePermissionId":"vpce-svc-perm-2","Tags":null}`,
		},
		elxARN("vpc-endpoint-service-permission", efpParent2+"/arn:aws:iam::210987654321:role/r"): {
			attrs: `{"Principal":"arn:aws:iam::210987654321:role/r","PrincipalType":"Role","ServiceId":"p-efp-0002","ServicePermissionId":"vpce-svc-perm-3","Tags":null}`,
		},
	},
	noKey:      "vpce-svc-perm-x",
	page1Row:   elxARN("vpc-endpoint-service-permission", efpParent1+"/arn:aws:iam::210987654321:root"),
	parent2Row: elxARN("vpc-endpoint-service-permission", efpParent2+"/arn:aws:iam::210987654321:role/r"),
	topLevel: map[string]func(any) (any, error){
		"DescribeVpcEndpointServices": func(any) (any, error) {
			return &ec2.DescribeVpcEndpointServicesOutput{ServiceDetails: []ec2types.ServiceDetail{{ServiceId: ptrStr(efpParent1), ServiceName: ptrStr("svc-1")}}}, nil
		},
	},
}

// mexBPAExclusions is the mexPhase of scanVPCBlockPublicAccessExclusions
// (shared tests in ec2_misc_extra_scanners_test.go).
var mexBPAExclusions = mexPhase{
	name: "vpc block public access exclusions", op: "ec2:DescribeVpcBlockPublicAccessExclusions", sdkOp: "DescribeVpcBlockPublicAccessExclusions",
	typ: TypeEC2VPCBlockPublicAccessExclusion,
	page1: &ec2.DescribeVpcBlockPublicAccessExclusionsOutput{NextToken: ptrStr(elxPageNext), VpcBlockPublicAccessExclusions: []ec2types.VpcBlockPublicAccessExclusion{{
		ExclusionId: ptrStr("vpcbpa-exclude-1"), ResourceArn: ptrStr(elxARN("vpc", "vpc-1")),
		State: ec2types.VpcBlockPublicAccessExclusionStateCreateComplete,
		Tags:  []ec2types.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}},
	}}},
	page2: &ec2.DescribeVpcBlockPublicAccessExclusionsOutput{VpcBlockPublicAccessExclusions: []ec2types.VpcBlockPublicAccessExclusion{
		{ExclusionId: ptrStr("vpcbpa-exclude-2"), ResourceArn: ptrStr(elxARN("vpc", "vpc-2"))},
		{ResourceArn: ptrStr(elxARN("vpc", "vpc-x"))},
	}},
	empty: &ec2.DescribeVpcBlockPublicAccessExclusionsOutput{},
	request: func(in any) (int32, *string) {
		p := in.(*ec2.DescribeVpcBlockPublicAccessExclusionsInput)
		return sv32(p.MaxResults), p.NextToken
	},
	run: scanVPCBlockPublicAccessExclusions,
	rows: map[string]mexRow{
		elxARN("vpc-block-public-access-exclusion", "vpcbpa-exclude-1"): {status: "create-complete", attrs: `{"CreationTimestamp":null,"DeletionTimestamp":null,"ExclusionId":"vpcbpa-exclude-1","InternetGatewayExclusionMode":"","LastUpdateTimestamp":null,"Reason":null,"ResourceArn":"arn:aws:ec2:us-east-1:123456789012:vpc/vpc-1","State":"create-complete","Tags":[{"Key":"env","Value":"prod"}]}`, tags: map[string]string{"env": "prod"}},
		elxARN("vpc-block-public-access-exclusion", "vpcbpa-exclude-2"): {attrs: `{"CreationTimestamp":null,"DeletionTimestamp":null,"ExclusionId":"vpcbpa-exclude-2","InternetGatewayExclusionMode":"","LastUpdateTimestamp":null,"Reason":null,"ResourceArn":"arn:aws:ec2:us-east-1:123456789012:vpc/vpc-2","State":"","Tags":null}`},
	},
	noKey:    elxARN("vpc", "vpc-x"),
	page1Row: elxARN("vpc-block-public-access-exclusion", "vpcbpa-exclude-1"),
}

// mexEndpointServices is the mexPhase of scanVPCEndpointServices (shared
// tests in ec2_misc_extra_scanners_test.go).
var mexEndpointServices = mexPhase{
	name: "vpc endpoint services", op: "ec2:DescribeVpcEndpointServices", sdkOp: "DescribeVpcEndpointServices",
	typ: TypeEC2VPCEndpointService,
	page1: &ec2.DescribeVpcEndpointServicesOutput{NextToken: ptrStr(elxPageNext), ServiceDetails: []ec2types.ServiceDetail{{
		ServiceId: ptrStr("vpce-svc-1"), ServiceName: ptrStr("com.amazonaws.vpce.us-east-1.vpce-svc-1"),
		Tags: []ec2types.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}},
	}}},
	page2: &ec2.DescribeVpcEndpointServicesOutput{ServiceDetails: []ec2types.ServiceDetail{
		{ServiceId: ptrStr("vpce-svc-2"), ServiceName: ptrStr("com.amazonaws.vpce.us-east-1.vpce-svc-2")},
		{ServiceName: ptrStr("com.amazonaws.vpce.us-east-1.vpce-svc-x")},
	}},
	empty: &ec2.DescribeVpcEndpointServicesOutput{},
	// A call without the owner filter for this account reports no
	// MaxResults, so mexHandler rejects it: unfiltered, the API also lists
	// AWS-managed services.
	request: func(in any) (int32, *string) {
		p := in.(*ec2.DescribeVpcEndpointServicesInput)
		if len(p.Filters) != 1 || sv(p.Filters[0].Name) != "owner" || !slices.Equal(p.Filters[0].Values, []string{testAccountID}) {
			return 0, p.NextToken
		}
		return sv32(p.MaxResults), p.NextToken
	},
	run: scanVPCEndpointServices,
	rows: map[string]mexRow{
		elxARN("vpc-endpoint-service", "vpce-svc-1"): {name: "com.amazonaws.vpce.us-east-1.vpce-svc-1", attrs: `{"AcceptanceRequired":null,"AvailabilityZoneIds":null,"AvailabilityZones":null,"BaseEndpointDnsNames":null,"ManagesVpcEndpoints":null,"Owner":null,"PayerResponsibility":"","PrivateDnsName":null,"PrivateDnsNameVerificationState":"","PrivateDnsNames":null,"ServiceId":"vpce-svc-1","ServiceName":"com.amazonaws.vpce.us-east-1.vpce-svc-1","ServiceRegion":null,"ServiceType":null,"SupportedIpAddressTypes":null,"Tags":[{"Key":"env","Value":"prod"}],"VpcEndpointPolicySupported":null}`, tags: map[string]string{"env": "prod"}},
		elxARN("vpc-endpoint-service", "vpce-svc-2"): {name: "com.amazonaws.vpce.us-east-1.vpce-svc-2", attrs: `{"AcceptanceRequired":null,"AvailabilityZoneIds":null,"AvailabilityZones":null,"BaseEndpointDnsNames":null,"ManagesVpcEndpoints":null,"Owner":null,"PayerResponsibility":"","PrivateDnsName":null,"PrivateDnsNameVerificationState":"","PrivateDnsNames":null,"ServiceId":"vpce-svc-2","ServiceName":"com.amazonaws.vpce.us-east-1.vpce-svc-2","ServiceRegion":null,"ServiceType":null,"SupportedIpAddressTypes":null,"Tags":null,"VpcEndpointPolicySupported":null}`},
	},
	noKey:    "com.amazonaws.vpce.us-east-1.vpce-svc-x",
	page1Row: elxARN("vpc-endpoint-service", "vpce-svc-1"),
}
