package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveDirectoryServiceVpcRefs,
		EdgeDecl{TypeDSMicrosoftAD, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeDSMicrosoftAD, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDSMicrosoftAD, TypeEC2SecurityGroup, store.RelAttachedTo},
		EdgeDecl{TypeDSSimpleAD, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeDSSimpleAD, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDSSimpleAD, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveDSEventTopics,
		EdgeDecl{TypeDSEventTopic, TypeSNSTopic, store.RelRoutesTo},
	)
	registerResolver(
		resolveDSLogSubscriptions,
		EdgeDecl{TypeDSLogSubscription, TypeLogsLogGroup, store.RelRoutesTo},
	)
	registerResolver(
		noopDSHierarchy,
		EdgeDecl{TypeDSMicrosoftAD, TypeDSCertificate, store.RelContains},
		EdgeDecl{TypeDSMicrosoftAD, TypeDSEventTopic, store.RelContains},
		EdgeDecl{TypeDSMicrosoftAD, TypeDSIPRoute, store.RelContains},
		EdgeDecl{TypeDSMicrosoftAD, TypeDSLogSubscription, store.RelContains},
		EdgeDecl{TypeDSMicrosoftAD, TypeDSRegion, store.RelContains},
		EdgeDecl{TypeDSMicrosoftAD, TypeDSSharedDirectory, store.RelContains},
		EdgeDecl{TypeDSMicrosoftAD, TypeDSSnapshot, store.RelContains},
		EdgeDecl{TypeDSMicrosoftAD, TypeDSTrust, store.RelContains},
		EdgeDecl{TypeDSSimpleAD, TypeDSEventTopic, store.RelContains},
		EdgeDecl{TypeDSSimpleAD, TypeDSLogSubscription, store.RelContains},
		EdgeDecl{TypeDSSimpleAD, TypeDSSnapshot, store.RelContains},
		EdgeDecl{TypeDSSimpleAD, TypeDSTrust, store.RelContains},
	)
}

// noopDSHierarchy only declares the directory -> child contains edges the
// scanner records (childFanOut and upsertContained in
// directoryservice_scanners.go), so coverage tooling sees them as wired.
// Simple AD reaches only the children the region-wide list ops return.
func noopDSHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveDSEventTopics wires each event-topic registration to the scanned SNS
// topic the directory publishes its status messages to.
func resolveDSEventTopics(acct *account, st *store.Store) error {
	rows, err := listAccountRows(acct, st, TypeDSEventTopic)
	if err != nil || len(rows) == 0 {
		return err
	}
	topics, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range rows {
		var attrs struct{ TopicArn *string }
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			errs = append(errs, fmt.Errorf("ds event topic %s attributes: %w", r.NativeID, err))
			continue
		}
		arn := sv(attrs.TopicArn)
		if arn == "" {
			continue
		}
		topicID := store.ResourceID("aws", acct.ID, arn)
		if !topics[topicID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, topicID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ds event topic→sns topic: %w", err)
		}
	}
	return errors.Join(errs...)
}

// resolveDSLogSubscriptions wires each log subscription to the scanned log
// group it forwards domain-controller security logs to. The element carries
// only the group's name; the group is in the subscription's account and
// region.
func resolveDSLogSubscriptions(acct *account, st *store.Store) error {
	rows, err := listAccountRows(acct, st, TypeDSLogSubscription)
	if err != nil || len(rows) == 0 {
		return err
	}
	groups, err := scannedIDSet(acct, st, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range rows {
		var attrs struct{ LogGroupName *string }
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			errs = append(errs, fmt.Errorf("ds log subscription %s attributes: %w", r.NativeID, err))
			continue
		}
		name := sv(attrs.LogGroupName)
		if name == "" {
			continue
		}
		groupID := store.ResourceID("aws", acct.ID, logGroupARNIn(acct.partition(), acct.ID, sv(r.Region), name))
		if !groups[groupID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, groupID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ds log subscription→log group: %w", err)
		}
	}
	return errors.Join(errs...)
}

// resolveDirectoryServiceVpcRefs wires Microsoft AD and Simple AD directories
// to the VPC, subnets, and SG they live in via VpcSettings.
func resolveDirectoryServiceVpcRefs(acct *account, st *store.Store) error {
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	subSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, t := range []string{TypeDSMicrosoftAD, TypeDSSimpleAD} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				VpcSettings *struct {
					VpcID           *string  `json:"VpcId"`
					SubnetIDs       []string `json:"SubnetIds"`
					SecurityGroupID *string  `json:"SecurityGroupId"`
				} `json:"VpcSettings"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			if attrs.VpcSettings == nil {
				continue
			}
			region := sv(r.Region)
			if v := sv(attrs.VpcSettings.VpcID); v != "" {
				tgtID := store.ResourceID("aws", acct.ID, ec2ARNIn(acct.partition(), region, acct.ID, "vpc", v))
				if vpcSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert ds %s→vpc: %w", t, err)
					}
				}
			}
			for _, sid := range attrs.VpcSettings.SubnetIDs {
				if sid == "" {
					continue
				}
				tgtID := store.ResourceID("aws", acct.ID, ec2ARNIn(acct.partition(), region, acct.ID, "subnet", sid))
				if !subSet[tgtID] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ds %s→subnet: %w", t, err)
				}
			}
			if g := sv(attrs.VpcSettings.SecurityGroupID); g != "" {
				tgtID := store.ResourceID("aws", acct.ID, ec2ARNIn(acct.partition(), region, acct.ID, "security-group", g))
				if sgSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert ds %s→sg: %w", t, err)
					}
				}
			}
		}
	}
	return nil
}
