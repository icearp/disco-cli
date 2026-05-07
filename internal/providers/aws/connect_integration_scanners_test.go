package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
)

type stubConnectIntegration struct {
	origins       []string
	keys          []cttypes.SecurityKey
	storageByType map[cttypes.InstanceStorageResourceType][]cttypes.InstanceStorageConfig
	integrations  []cttypes.IntegrationAssociationSummary
	notifs        []cttypes.Notification
	rulesBySource map[cttypes.EventSourceName][]cttypes.RuleSummary
	ruleOut       map[string]*connect.DescribeRuleOutput
}

func (s *stubConnectIntegration) ListApprovedOrigins(_ context.Context, _ *connect.ListApprovedOriginsInput, _ ...func(*connect.Options)) (*connect.ListApprovedOriginsOutput, error) {
	return &connect.ListApprovedOriginsOutput{Origins: s.origins}, nil
}

func (s *stubConnectIntegration) ListSecurityKeys(_ context.Context, _ *connect.ListSecurityKeysInput, _ ...func(*connect.Options)) (*connect.ListSecurityKeysOutput, error) {
	return &connect.ListSecurityKeysOutput{SecurityKeys: s.keys}, nil
}

func (s *stubConnectIntegration) ListInstanceStorageConfigs(_ context.Context, in *connect.ListInstanceStorageConfigsInput, _ ...func(*connect.Options)) (*connect.ListInstanceStorageConfigsOutput, error) {
	return &connect.ListInstanceStorageConfigsOutput{StorageConfigs: s.storageByType[in.ResourceType]}, nil
}

func (s *stubConnectIntegration) ListIntegrationAssociations(_ context.Context, _ *connect.ListIntegrationAssociationsInput, _ ...func(*connect.Options)) (*connect.ListIntegrationAssociationsOutput, error) {
	return &connect.ListIntegrationAssociationsOutput{IntegrationAssociationSummaryList: s.integrations}, nil
}

func (s *stubConnectIntegration) ListNotifications(_ context.Context, _ *connect.ListNotificationsInput, _ ...func(*connect.Options)) (*connect.ListNotificationsOutput, error) {
	return &connect.ListNotificationsOutput{NotificationSummaryList: s.notifs}, nil
}

func (s *stubConnectIntegration) ListRules(_ context.Context, in *connect.ListRulesInput, _ ...func(*connect.Options)) (*connect.ListRulesOutput, error) {
	return &connect.ListRulesOutput{RuleSummaryList: s.rulesBySource[in.EventSourceName]}, nil
}

func (s *stubConnectIntegration) DescribeRule(_ context.Context, in *connect.DescribeRuleInput, _ ...func(*connect.Options)) (*connect.DescribeRuleOutput, error) {
	return s.ruleOut[*in.RuleId], nil
}

func TestScanConnectIntegration(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	instID := "11111111-1111-1111-1111-111111111111"
	instARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s", testRegion, acct.ID, instID)
	instances := []cttypes.InstanceSummary{{Id: &instID, Arn: &instARN}}

	origin := "https://example.com"
	originARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/approved-origin/%s", testRegion, acct.ID, instID, origin)
	skAssoc := "sk-1"
	skARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/security-key/%s", testRegion, acct.ID, instID, skAssoc)
	scAssoc := "sc-1"
	scARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/storage-config/CALL_RECORDINGS/%s", testRegion, acct.ID, instID, scAssoc)
	iaARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/integration-association/ia-1", testRegion, acct.ID, instID)
	notifID := "notif-1"
	notifARN := fmt.Sprintf("arn:aws:connect:%s:%s:notification/%s", testRegion, acct.ID, notifID)
	ruleID := "r-1"
	ruleARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/rule/%s", testRegion, acct.ID, instID, ruleID)
	ruleName := "r"

	stub := &stubConnectIntegration{
		origins: []string{origin},
		keys:    []cttypes.SecurityKey{{AssociationId: &skAssoc, CreationTime: &now}},
		storageByType: map[cttypes.InstanceStorageResourceType][]cttypes.InstanceStorageConfig{
			cttypes.InstanceStorageResourceTypeCallRecordings: {{AssociationId: &scAssoc, StorageType: cttypes.StorageTypeS3}},
		},
		integrations: []cttypes.IntegrationAssociationSummary{{IntegrationAssociationArn: &iaARN, IntegrationType: cttypes.IntegrationTypeApplication}},
		notifs:       []cttypes.Notification{{Arn: &notifARN, Id: &notifID, CreatedAt: &now, LastModifiedTime: &now}},
		rulesBySource: map[cttypes.EventSourceName][]cttypes.RuleSummary{
			cttypes.EventSourceNameOnPostCallAnalysisAvailable: {{RuleArn: &ruleARN, RuleId: &ruleID, Name: &ruleName, EventSourceName: cttypes.EventSourceNameOnPostCallAnalysisAvailable}},
		},
		ruleOut: map[string]*connect.DescribeRuleOutput{
			ruleID: {Rule: &cttypes.Rule{RuleArn: &ruleARN, RuleId: &ruleID, Name: &ruleName}},
		},
	}

	total, inserted, err := scanConnectIntegration(context.Background(), stub, instances, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 6 || inserted != 6 {
		t.Fatalf("total=%d inserted=%d want 6/6", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeConnectApprovedOrigin, originARN},
		{TypeConnectSecurityKey, skARN},
		{TypeConnectInstanceStorageConfig, scARN},
		{TypeConnectIntegrationAssociation, iaARN},
		{TypeConnectNotification, notifARN},
		{TypeConnectRule, ruleARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.typ, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanConnectIntegrationEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubConnectIntegration{}
	total, inserted, err := scanConnectIntegration(context.Background(), stub, nil, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
