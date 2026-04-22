package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// ── Load Balancer → VPC ───────────────────────────────────────────────────────

func TestResolveELBv2LBRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	lbARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-lb/abc"
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, lbARN, region,
		`{"lb": {"VpcId": "vpc-lb-777"}}`)
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
		`{"VpcId": "vpc-tg-999"}`)
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
