package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveELBv2LBRelationships,
		EdgeDecl{TypeELBv2LoadBalancer, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveELBv2ListenerRelationships,
		EdgeDecl{TypeELBv2Listener, TypeELBv2LoadBalancer, store.RelAttachedTo},
		EdgeDecl{TypeELBv2Listener, TypeACMCertificate, store.RelUses},
	)
	registerResolver(
		resolveELBv2RuleRelationships,
		EdgeDecl{TypeELBv2ListenerRule, TypeELBv2Listener, store.RelAttachedTo},
	)
	registerResolver(
		resolveELBv2CertRelationships,
		EdgeDecl{TypeELBv2ListenerCertificate, TypeELBv2Listener, store.RelAttachedTo},
	)
	registerResolver(
		resolveELBv2TGRelationships,
		EdgeDecl{TypeELBv2TargetGroup, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeELBv2TargetGroup, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeELBv2TargetGroup, TypeEC2Instance, store.RelAttachedTo},
	)
	registerResolver(
		resolveELBv2RevocationRelationships,
		EdgeDecl{TypeELBv2TrustStoreRevocation, TypeELBv2TrustStore, store.RelAttachedTo},
	)
}

// resolveELBv2LBRelationships links each load balancer to its VPC.
func resolveELBv2LBRelationships(acct *account, st *store.Store) error {
	lbs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeELBv2LoadBalancer},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range lbs {
		var attrs struct {
			Lb *struct {
				VpcID *string `json:"VpcID"`
			} `json:"lb"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Lb == nil || attrs.Lb.VpcID == nil {
			continue
		}
		vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.Lb.VpcID))
		if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lb→vpc relationship: %w", err)
		}
	}
	return nil
}

// resolveELBv2ListenerRelationships links each listener to its load balancer.
func resolveELBv2ListenerRelationships(acct *account, st *store.Store) error {
	listeners, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeELBv2Listener},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range listeners {
		var attrs struct {
			LoadBalancerArn *string `json:"LoadBalancerArn"`
			Certificates    []struct {
				CertificateArn *string `json:"CertificateArn"`
			} `json:"Certificates"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.LoadBalancerArn != nil {
			lbID := store.ResourceID("aws", acct.ID, TypeELBv2LoadBalancer, *attrs.LoadBalancerArn)
			if err := st.UpsertRelationship(r.ID, lbID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert listener→lb relationship: %w", err)
			}
		}
		// Listener → ACM certificate. IAM server certs (arn:aws:iam:...:server-certificate/)
		// are skipped; only ACM ARNs produce edges.
		for _, c := range attrs.Certificates {
			arn := sv(c.CertificateArn)
			if !strings.HasPrefix(arn, "arn:aws:acm:") {
				continue
			}
			certID := store.ResourceID("aws", acct.ID, TypeACMCertificate, arn)
			if err := st.UpsertRelationship(r.ID, certID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert listener→acm-cert relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveELBv2RuleRelationships links each listener rule to its listener.
func resolveELBv2RuleRelationships(acct *account, st *store.Store) error {
	rules, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeELBv2ListenerRule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rules {
		var attrs struct {
			ListenerArn string `json:"listenerArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ListenerArn == "" {
			continue
		}
		listenerID := store.ResourceID("aws", acct.ID, TypeELBv2Listener, attrs.ListenerArn)
		if err := st.UpsertRelationship(r.ID, listenerID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert rule→listener relationship: %w", err)
		}
	}
	return nil
}

// resolveELBv2CertRelationships links each listener certificate to its listener.
func resolveELBv2CertRelationships(acct *account, st *store.Store) error {
	certs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeELBv2ListenerCertificate},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range certs {
		var attrs struct {
			ListenerArn string `json:"listenerArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ListenerArn == "" {
			continue
		}
		listenerID := store.ResourceID("aws", acct.ID, TypeELBv2Listener, attrs.ListenerArn)
		if err := st.UpsertRelationship(r.ID, listenerID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cert→listener relationship: %w", err)
		}
	}
	return nil
}

// resolveELBv2TGRelationships links each target group to its VPC.
func resolveELBv2TGRelationships(acct *account, st *store.Store) error {
	tgs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeELBv2TargetGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range tgs {
		// Scanner wraps TG details as {"TargetGroup":{...},"Targets":[...]}.
		var attrs struct {
			TargetGroup struct {
				VpcID      *string `json:"VpcID"`
				TargetType *string `json:"TargetType"`
			} `json:"TargetGroup"`
			Targets []struct {
				ID *string `json:"ID"`
			} `json:"Targets"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TargetGroup.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.TargetGroup.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert target-group→vpc relationship: %w", err)
			}
		}
		// Target group → registered targets (lambda or instance).
		tType := sv(attrs.TargetGroup.TargetType)
		for _, tgt := range attrs.Targets {
			id := sv(tgt.ID)
			if id == "" {
				continue
			}
			switch tType {
			case "lambda":
				fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, id)
				if err := st.UpsertRelationship(r.ID, fnID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert target-group→lambda relationship: %w", err)
				}
			case "instance":
				instARN := ec2ARN(region, acct.ID, "instance", id)
				instID := store.ResourceID("aws", acct.ID, TypeEC2Instance, instARN)
				if err := st.UpsertRelationship(r.ID, instID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert target-group→instance relationship: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveELBv2RevocationRelationships links each trust store revocation to its trust store.
func resolveELBv2RevocationRelationships(acct *account, st *store.Store) error {
	revs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeELBv2TrustStoreRevocation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range revs {
		var attrs struct {
			TrustStoreArn *string `json:"TrustStoreArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.TrustStoreArn == nil {
			continue
		}
		tsID := store.ResourceID("aws", acct.ID, TypeELBv2TrustStore, *attrs.TrustStoreArn)
		if err := st.UpsertRelationship(r.ID, tsID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert revocation→trust-store relationship: %w", err)
		}
	}
	return nil
}
