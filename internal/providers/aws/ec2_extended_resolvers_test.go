package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveEC2VolumeKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abcd-1234", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	volARN := ec2ARN(testRegion, acct.ID, "volume", "vol-aaa")
	attrs := fmt.Sprintf(`{"KmsKeyId":%q}`, keyARN)
	volID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Volume, volARN, testRegion, attrs)

	if err := resolveEC2VolumeKMS(acct, st); err != nil {
		t.Fatalf("resolveEC2VolumeKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(volID)
	assertRelationship(t, rels, volID, keyID, store.RelUses)
}

func TestResolveEC2VolumeKMS_Unscanned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	volARN := ec2ARN(testRegion, acct.ID, "volume", "vol-bbb")
	attrs := fmt.Sprintf(`{"KmsKeyId":"arn:aws:kms:%s:%s:key/missing"}`, testRegion, acct.ID)
	volID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Volume, volARN, testRegion, attrs)
	if err := resolveEC2VolumeKMS(acct, st); err != nil {
		t.Fatalf("resolveEC2VolumeKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(volID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveEC2ImageKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/img-key", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	imgARN := ec2ARN(testRegion, acct.ID, "image", "ami-1")
	attrs := fmt.Sprintf(`{"BlockDeviceMappings":[{"Ebs":{"KmsKeyId":%q}},{"Ebs":{"KmsKeyId":%q}}]}`, keyARN, keyARN)
	imgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Image, imgARN, testRegion, attrs)

	if err := resolveEC2ImageKMS(acct, st); err != nil {
		t.Fatalf("resolveEC2ImageKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(imgID)
	if len(rels) != 1 {
		t.Errorf("expected 1 dedup'd edge, got %d", len(rels))
	}
	assertRelationship(t, rels, imgID, keyID, store.RelUses)
}

func TestResolveEC2VPNGatewayVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-aa")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	vgwARN := ec2ARN(testRegion, acct.ID, "vpn-gateway", "vgw-1")
	attrs := `{"VpcAttachments":[{"VpcId":"vpc-aa"}]}`
	vgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPNGateway, vgwARN, testRegion, attrs)

	if err := resolveEC2VPNGatewayVPC(acct, st); err != nil {
		t.Fatalf("resolveEC2VPNGatewayVPC: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vgwID)
	assertRelationship(t, rels, vgwID, vpcID, store.RelAttachedTo)
}

func TestResolveEC2NetworkInterfacePermissionENI(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	eniARN := ec2ARN(testRegion, acct.ID, "network-interface", "eni-1")
	eniID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterface, eniARN, testRegion, "{}")
	permARN := ec2ARN(testRegion, acct.ID, "network-interface-permission", "eni-perm-1")
	attrs := `{"NetworkInterfaceId":"eni-1"}`
	permID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterfacePermission, permARN, testRegion, attrs)

	if err := resolveEC2NetworkInterfacePermissionENI(acct, st); err != nil {
		t.Fatalf("resolveEC2NetworkInterfacePermissionENI: %v", err)
	}
	rels, _ := st.RelationshipsFrom(permID)
	assertRelationship(t, rels, permID, eniID, store.RelAttachedTo)
}

func TestResolveEC2TrafficMirrorTargetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	eniARN := ec2ARN(testRegion, acct.ID, "network-interface", "eni-tm")
	eniID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterface, eniARN, testRegion, "{}")
	nlbARN := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/net/my-nlb/abc", testRegion, acct.ID)
	nlbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, nlbARN, testRegion, "{}")

	tmARN := ec2ARN(testRegion, acct.ID, "traffic-mirror-target", "tmt-eni")
	attrs := `{"NetworkInterfaceId":"eni-tm"}`
	tmID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TrafficMirrorTarget, tmARN, testRegion, attrs)

	tmARN2 := ec2ARN(testRegion, acct.ID, "traffic-mirror-target", "tmt-nlb")
	attrs2 := fmt.Sprintf(`{"NetworkLoadBalancerArn":%q}`, nlbARN)
	tmID2 := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TrafficMirrorTarget, tmARN2, testRegion, attrs2)

	if err := resolveEC2TrafficMirrorTargetRefs(acct, st); err != nil {
		t.Fatalf("resolveEC2TrafficMirrorTargetRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tmID)
	assertRelationship(t, rels, tmID, eniID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(tmID2)
	assertRelationship(t, rels, tmID2, nlbID, store.RelAttachedTo)
}

func TestResolveEC2TrafficMirrorFilterRuleParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	filterARN := ec2ARN(testRegion, acct.ID, "traffic-mirror-filter", "tmf-1")
	filterID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TrafficMirrorFilter, filterARN, testRegion, "{}")
	ruleARN := ec2ARN(testRegion, acct.ID, "traffic-mirror-filter-rule", "tmfr-1")
	attrs := `{"TrafficMirrorFilterId":"tmf-1"}`
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TrafficMirrorFilterRule, ruleARN, testRegion, attrs)

	if err := resolveEC2TrafficMirrorFilterRuleParent(acct, st); err != nil {
		t.Fatalf("resolveEC2TrafficMirrorFilterRuleParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ruleID)
	assertRelationship(t, rels, ruleID, filterID, store.RelAttachedTo)
}

func TestResolveEC2VerifiedAccessInstanceTrustProvider(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tpARN := ec2ARN(testRegion, acct.ID, "verified-access-trust-provider", "vatp-1")
	tpID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VerifiedAccessTrustProvider, tpARN, testRegion, "{}")
	vaiARN := ec2ARN(testRegion, acct.ID, "verified-access-instance", "vai-1")
	attrs := `{"VerifiedAccessTrustProviders":[{"VerifiedAccessTrustProviderId":"vatp-1"}]}`
	vaiID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VerifiedAccessInstance, vaiARN, testRegion, attrs)

	if err := resolveEC2VerifiedAccessInstanceTrustProvider(acct, st); err != nil {
		t.Fatalf("resolveEC2VerifiedAccessInstanceTrustProvider: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vaiID)
	assertRelationship(t, rels, vaiID, tpID, store.RelUses)
}

func TestResolveEC2ClientVPNAuthorizationRuleEndpoint(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	epARN := ec2ARN(testRegion, acct.ID, "client-vpn-endpoint", "cvpn-1")
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2ClientVPNEndpoint, epARN, testRegion, "{}")
	ruleARN := ec2ARN(testRegion, acct.ID, "client-vpn-auth-rule", "cvpn-1/0.0.0.0/0/grp")
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2ClientVPNAuthorizationRule, ruleARN, testRegion, "{}")

	if err := resolveEC2ClientVPNAuthorizationRuleEndpoint(acct, st); err != nil {
		t.Fatalf("resolveEC2ClientVPNAuthorizationRuleEndpoint: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ruleID)
	assertRelationship(t, rels, ruleID, epID, store.RelAttachedTo)
}

func TestResolveEC2ClientVPNRouteRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	epARN := ec2ARN(testRegion, acct.ID, "client-vpn-endpoint", "cvpn-2")
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2ClientVPNEndpoint, epARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-99")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	routeARN := ec2ARN(testRegion, acct.ID, "client-vpn-route", "cvpn-2/10.0.0.0/16/subnet-99")
	attrs := `{"TargetSubnet":"subnet-99"}`
	routeID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2ClientVPNRoute, routeARN, testRegion, attrs)

	if err := resolveEC2ClientVPNRouteRefs(acct, st); err != nil {
		t.Fatalf("resolveEC2ClientVPNRouteRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(routeID)
	assertRelationship(t, rels, routeID, epID, store.RelAttachedTo)
	assertRelationship(t, rels, routeID, subID, store.RelAttachedTo)
}

func TestResolveEC2NetworkInsightsPathRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	eniARN := ec2ARN(testRegion, acct.ID, "network-interface", "eni-src")
	eniID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterface, eniARN, testRegion, "{}")
	instARN := ec2ARN(testRegion, acct.ID, "instance", "i-dst")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instARN, testRegion, "{}")

	pathARN := fmt.Sprintf("arn:aws:ec2:%s:%s:network-insights-path/nip-1", testRegion, acct.ID)
	attrs := `{"Source":"eni-src","Destination":"i-dst"}`
	pathID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInsightsPath, pathARN, testRegion, attrs)

	if err := resolveEC2NetworkInsightsPathRefs(acct, st); err != nil {
		t.Fatalf("resolveEC2NetworkInsightsPathRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pathID)
	assertRelationship(t, rels, pathID, eniID, store.RelUses)
	assertRelationship(t, rels, pathID, instID, store.RelUses)
}

func TestResolveEC2CapacityReservationPlacementGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pgARN := ec2ARN(testRegion, acct.ID, "placement-group", "pg-1")
	pgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2PlacementGroup, pgARN, testRegion, "{}")
	crARN := fmt.Sprintf("arn:aws:ec2:%s:%s:capacity-reservation/cr-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"PlacementGroupArn":%q}`, pgARN)
	crID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2CapacityReservation, crARN, testRegion, attrs)

	if err := resolveEC2CapacityReservationPlacementGroup(acct, st); err != nil {
		t.Fatalf("resolveEC2CapacityReservationPlacementGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(crID)
	assertRelationship(t, rels, crID, pgID, store.RelAttachedTo)
}

func TestResolveEC2TGPeeringAttachmentParents(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tgARN := ec2ARN(testRegion, acct.ID, "transit-gateway", "tgw-1")
	tgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGateway, tgARN, testRegion, "{}")
	pARN := ec2ARN(testRegion, acct.ID, "transit-gateway-peering-attachment", "tgw-attach-pa-1")
	attrs := fmt.Sprintf(`{"RequesterTgwInfo":{"TransitGatewayId":"tgw-1","Region":%q}}`, testRegion)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayPeeringAttachment, pARN, testRegion, attrs)

	if err := resolveEC2TGPeeringAttachmentParents(acct, st); err != nil {
		t.Fatalf("resolveEC2TGPeeringAttachmentParents: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, tgID, store.RelAttachedTo)
}

func TestResolveEC2SpotFleetIAM(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/spot-fleet", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	sfARN := ec2ARN(testRegion, acct.ID, "spot-fleet-request", "sfr-1")
	attrs := fmt.Sprintf(`{"SpotFleetRequestConfig":{"IamFleetRole":%q}}`, roleARN)
	sfID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SpotFleet, sfARN, testRegion, attrs)

	if err := resolveEC2SpotFleetIAM(acct, st); err != nil {
		t.Fatalf("resolveEC2SpotFleetIAM: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sfID)
	assertRelationship(t, rels, sfID, roleID, store.RelAssumes)
}

func TestResolveEC2FleetLaunchTemplate(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ltARN := ec2ARN(testRegion, acct.ID, "launch-template", "lt-1")
	ltID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LaunchTemplate, ltARN, testRegion, "{}")
	fARN := ec2ARN(testRegion, acct.ID, "fleet", "fleet-1")
	attrs := `{"LaunchTemplateConfigs":[{"LaunchTemplateSpecification":{"LaunchTemplateId":"lt-1"}}]}`
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Fleet, fARN, testRegion, attrs)

	if err := resolveEC2FleetLaunchTemplate(acct, st); err != nil {
		t.Fatalf("resolveEC2FleetLaunchTemplate: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fID)
	assertRelationship(t, rels, fID, ltID, store.RelUses)
}

func TestClientVPNEndpointFromChildARN(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"arn:aws:ec2:us-east-1:123:client-vpn-route/cvpn-abc/10.0.0.0/16/subnet-x", "cvpn-abc"},
		{"arn:aws:ec2:us-east-1:123:client-vpn-auth-rule/cvpn-x/0.0.0.0/0/grp", "cvpn-x"},
		{"", ""},
		{"arn:aws:ec2:us-east-1:123:vpc/vpc-1", ""},
	}
	for _, c := range cases {
		if got := clientVPNEndpointFromChildARN(c.in); got != c.want {
			t.Errorf("clientVPNEndpointFromChildARN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveEC2TGWMeteringPolicyRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tgwID := "tgw-aaa"
	tgwARN := ec2ARN(testRegion, acct.ID, "transit-gateway", tgwID)
	tgwRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGateway, tgwARN, testRegion, "{}")
	attID := "tgw-attach-bbb"
	attARN := ec2ARN(testRegion, acct.ID, "transit-gateway-attachment", attID)
	attRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayAttachment, attARN, testRegion, "{}")
	mpARN := ec2ARN(testRegion, acct.ID, "transit-gateway-metering-policy", "tgmp-1")
	mpAttrs := fmt.Sprintf(`{"TransitGatewayId":%q,"MiddleboxAttachmentIds":[%q]}`, tgwID, attID)
	mpID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayMeteringPolicy, mpARN, testRegion, mpAttrs)

	if err := resolveEC2TGWMeteringPolicyRefs(acct, st); err != nil {
		t.Fatalf("resolveEC2TGWMeteringPolicyRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mpID)
	assertRelationship(t, rels, mpID, tgwRowID, store.RelAttachedTo)
	assertRelationship(t, rels, mpID, attRowID, store.RelAttachedTo)
}

func TestResolveEC2VPCEndpointConnectionNotificationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpceID := "vpce-aaa"
	vpceARN := ec2ARN(testRegion, acct.ID, "vpc-endpoint", vpceID)
	vpceRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpoint, vpceARN, testRegion, "{}")
	svcID := "vpce-svc-bbb"
	svcARN := ec2ARN(testRegion, acct.ID, "vpc-endpoint-service", svcID)
	svcRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpointService, svcARN, testRegion, "{}")
	topicARN := fmt.Sprintf("arn:aws:sns:%s:%s:my-topic", testRegion, acct.ID)
	topicRowID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")
	notifARN := ec2ARN(testRegion, acct.ID, "vpc-endpoint-connection-notification", "vpce-notif-1")
	notifAttrs := fmt.Sprintf(`{"VpcEndpointId":%q,"ServiceId":%q,"ConnectionNotificationArn":%q}`, vpceID, svcID, topicARN)
	notifID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpointConnectionNotification, notifARN, testRegion, notifAttrs)

	if err := resolveEC2VPCEndpointConnectionNotificationRefs(acct, st); err != nil {
		t.Fatalf("resolveEC2VPCEndpointConnectionNotificationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(notifID)
	assertRelationship(t, rels, notifID, vpceRowID, store.RelAttachedTo)
	assertRelationship(t, rels, notifID, svcRowID, store.RelAttachedTo)
	assertRelationship(t, rels, notifID, topicRowID, store.RelUses)
}

func TestResolveEC2RouteServerSNS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	topicARN := fmt.Sprintf("arn:aws:sns:%s:%s:rs-topic", testRegion, acct.ID)
	topicRowID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")
	rsARN := ec2ARN(testRegion, acct.ID, "route-server", "rs-1")
	rsAttrs := fmt.Sprintf(`{"SnsTopicArn":%q}`, topicARN)
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteServer, rsARN, testRegion, rsAttrs)

	if err := resolveEC2RouteServerSNS(acct, st); err != nil {
		t.Fatalf("resolveEC2RouteServerSNS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rsID)
	assertRelationship(t, rels, rsID, topicRowID, store.RelUses)
}

func TestResolveEC2CapacityReservationFleetMembers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	crID := "cr-aaa"
	crARN := ec2ARN(testRegion, acct.ID, "capacity-reservation", crID)
	crRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2CapacityReservation, crARN, testRegion, "{}")
	fleetARN := fmt.Sprintf("arn:aws:ec2:%s:%s:capacity-reservation-fleet/crf-1", testRegion, acct.ID)
	fleetAttrs := fmt.Sprintf(`{"InstanceTypeSpecifications":[{"CapacityReservationId":%q}]}`, crID)
	fleetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2CapacityReservationFleet, fleetARN, testRegion, fleetAttrs)

	if err := resolveEC2CapacityReservationFleetMembers(acct, st); err != nil {
		t.Fatalf("resolveEC2CapacityReservationFleetMembers: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fleetID)
	assertRelationship(t, rels, fleetID, crRowID, store.RelContains)
}
