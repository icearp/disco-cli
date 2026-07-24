package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

// ── Load Balancer → VPC ───────────────────────────────────────────────────────

func TestResolveELBv2LBRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	lbARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-lb/abc"
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, lbARN, region,
		elbv2LBAttrs(elbv2types.LoadBalancer{VpcId: sdkaws.String("vpc-lb-777")}))
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-lb-777"), region, "{}")

	if err := resolveELBv2LBRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2LBRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(lbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vpcID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected lb -[attached-to]-> vpc, got %+v", rels[0])
	}
}

func TestResolveELBv2LBRelationships_NoVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	lbARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/bare-lb/xyz"
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, lbARN, "", "{}")

	if err := resolveELBv2LBRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2LBRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(lbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// ── Listener → Load Balancer ──────────────────────────────────────────────────

func TestResolveELBv2ListenerRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	lbARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-lb/abc"
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, lbARN, region, "{}")

	listenerARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/my-lb/abc/def"
	listenerID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2Listener, listenerARN, region,
		`{"LoadBalancerArn": "`+lbARN+`"}`)

	if err := resolveELBv2ListenerRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2ListenerRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(listenerID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != lbID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected listener -[attached-to]-> lb, got %+v", rels[0])
	}
}

func TestResolveELBv2ListenerRelationships_NoLB(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	listenerARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/my-lb/abc/def"
	listenerID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2Listener, listenerARN, "", "{}")

	if err := resolveELBv2ListenerRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2ListenerRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(listenerID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// ── Listener Rule → Listener ──────────────────────────────────────────────────

func TestResolveELBv2RuleRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	listenerARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/my-lb/abc/def"
	listenerID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2Listener, listenerARN, region, "{}")

	ruleARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener-rule/app/my-lb/abc/def/ghi"
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2ListenerRule, ruleARN, region,
		`{"listenerArn": "`+listenerARN+`"}`)

	if err := resolveELBv2RuleRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2RuleRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ruleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != listenerID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected rule -[attached-to]-> listener, got %+v", rels[0])
	}
}

func TestResolveELBv2RuleRelationships_NoListener(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	ruleARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener-rule/app/my-lb/abc/def/ghi"
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2ListenerRule, ruleARN, "", "{}")

	if err := resolveELBv2RuleRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2RuleRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(ruleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// ── Listener Certificate → Listener ──────────────────────────────────────────

func TestResolveELBv2CertRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	listenerARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/my-lb/abc/def"
	listenerID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2Listener, listenerARN, region, "{}")

	certNativeID := listenerARN + ":cert/arn:aws:acm:us-east-1:123456789012:certificate/abc"
	certID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2ListenerCertificate, certNativeID, region,
		`{"listenerArn": "`+listenerARN+`"}`)

	if err := resolveELBv2CertRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2CertRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(certID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != listenerID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected cert -[attached-to]-> listener, got %+v", rels[0])
	}
}

func TestResolveELBv2CertRelationships_NoListener(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	certNativeID := "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/my-lb/abc/def:cert/x"
	certID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2ListenerCertificate, certNativeID, "", "{}")

	if err := resolveELBv2CertRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2CertRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(certID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// ── Target Group → VPC ────────────────────────────────────────────────────────

func TestResolveELBv2TGRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	tgARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/my-tg/abc"
	tgID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2TargetGroup, tgARN, region,
		elbv2TargetGroupAttrs(elbv2types.TargetGroup{VpcId: sdkaws.String("vpc-tg-999")}))
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-tg-999"), region, "{}")

	if err := resolveELBv2TGRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2TGRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(tgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vpcID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected tg -[attached-to]-> vpc, got %+v", rels[0])
	}
}

func TestResolveELBv2TGRelationships_NoVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	tgARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/bare-tg/xyz"
	tgID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2TargetGroup, tgARN, "", "{}")

	if err := resolveELBv2TGRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2TGRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(tgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveELBv2TGRelationships_LambdaTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	tgARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/lambda-tg/abc"
	fnARN := "arn:aws:lambda:us-east-1:123456789012:function:my-fn"
	attrs := elbv2TargetGroupAttrs(
		elbv2types.TargetGroup{TargetType: elbv2types.TargetTypeEnumLambda},
		elbv2types.TargetDescription{Id: sdkaws.String(fnARN)},
	)
	tgID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2TargetGroup, tgARN, region, attrs)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, region, "{}")

	if err := resolveELBv2TGRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2TGRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(tgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, tgID, fnID, store.RelUses)
}

func TestResolveELBv2TGRelationships_InstanceTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	tgARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/inst-tg/abc"
	instID := "i-0123456789abcdef0"
	attrs := elbv2TargetGroupAttrs(
		elbv2types.TargetGroup{TargetType: elbv2types.TargetTypeEnumInstance},
		elbv2types.TargetDescription{Id: sdkaws.String(instID)},
	)
	tgResID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2TargetGroup, tgARN, region, attrs)
	instARN := ec2ARN(region, acct.ID, "instance", instID)
	instResID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instARN, region, "{}")

	if err := resolveELBv2TGRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2TGRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(tgResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, tgResID, instResID, store.RelAttachedTo)
}

// ── Trust Store Revocation → Trust Store ─────────────────────────────────────

func TestResolveELBv2RevocationRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	tsARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:truststore/my-ts/abc"
	tsID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2TrustStore, tsARN, region, "{}")

	revNativeID := tsARN + ":rev/42"
	revID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2TrustStoreRevocation, revNativeID, region,
		`{"TrustStoreArn": "`+tsARN+`"}`)

	if err := resolveELBv2RevocationRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2RevocationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(revID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != tsID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected revocation -[attached-to]-> trust-store, got %+v", rels[0])
	}
}

func TestResolveELBv2RevocationRelationships_NoTrustStore(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	revNativeID := "arn:aws:elasticloadbalancing:us-east-1:123456789012:truststore/my-ts/abc:rev/0"
	revID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2TrustStoreRevocation, revNativeID, "", "{}")

	if err := resolveELBv2RevocationRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2RevocationRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(revID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveELBv2ListenerRelationships_ACM verifies Listener → ACM cert edge.
func TestResolveELBv2ListenerRelationships_ACM(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	lbARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-lb/abc"
	certARN := "arn:aws:acm:us-east-1:123456789012:certificate/abc"
	iamCertARN := "arn:aws:iam::123456789012:server-certificate/legacy"
	listenerARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/my-lb/abc/def"
	attrs := `{"LoadBalancerArn":"` + lbARN + `","Certificates":[{"CertificateArn":"` + certARN + `"},{"CertificateArn":"` + iamCertARN + `"}]}`

	upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, lbARN, region, "{}")
	listenerID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2Listener, listenerARN, region, attrs)
	certID := upsertTestResource(t, st, "aws", acct.ID, TypeACMCertificate, certARN, region, "{}")

	if err := resolveELBv2ListenerRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBv2ListenerRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(listenerID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, listenerID, certID, store.RelUses)
	// IAM server cert should NOT produce an edge (not ACM-prefixed).
	for _, r := range rels {
		if r.Kind == store.RelUses && r.ToID != certID {
			t.Errorf("unexpected uses edge to non-ACM target: %+v", r)
		}
	}
}
