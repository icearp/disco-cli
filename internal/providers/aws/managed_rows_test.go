package aws

import (
	"context"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apprunner"
	apprunnertypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	athenatypes "github.com/aws/aws-sdk-go-v2/service/athena/types"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	eventbridgetypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	memorydbtypes "github.com/aws/aws-sdk-go-v2/service/memorydb/types"
	"github.com/aws/aws-sdk-go-v2/service/notifications"
	notificationstypes "github.com/aws/aws-sdk-go-v2/service/notifications/types"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	organizationstypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2"
	smsvoicetypes "github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	"github.com/aws/aws-sdk-go-v2/service/s3control"
	s3controltypes "github.com/aws/aws-sdk-go-v2/service/s3control/types"
	"github.com/aws/aws-sdk-go-v2/service/xray"
	xraytypes "github.com/aws/aws-sdk-go-v2/service/xray/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

// TestDefaultRowsWithCustomerSettingsNotManaged pins that the AWS-created
// default of each type below is stored unmanaged. Each has an API that changes
// its settings, so its posture is the customer's, and a managed row is left
// out of default `disco check` input. Each case feeds the scanner the field
// that marks the default (IsDefault, the name "default", ...).
func TestDefaultRowsWithCustomerSettingsNotManaged(t *testing.T) {
	const acctID = testAccountID
	arn := func(svc, rest string) string { return "arn:aws:" + svc + ":" + testRegion + ":" + acctID + ":" + rest }
	denied := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"}
	type handlers = map[string]func(in any) (any, error)
	ret := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }

	cases := []struct {
		name     string
		typ      string
		handlers handlers
		run      func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error
	}{
		{
			name: "ec2 default VPC", typ: TypeEC2VPC,
			handlers: handlers{"DescribeVpcs": ret(&ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{{VpcId: sdkaws.String("vpc-d"), IsDefault: sdkaws.Bool(true)}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanVPCs(ctx, ec2.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "ec2 default subnet", typ: TypeEC2Subnet,
			handlers: handlers{"DescribeSubnets": ret(&ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{{SubnetId: sdkaws.String("subnet-d"), DefaultForAz: sdkaws.Bool(true)}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanSubnets(ctx, ec2.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "ec2 default network ACL", typ: TypeEC2NetworkACL,
			handlers: handlers{"DescribeNetworkAcls": ret(&ec2.DescribeNetworkAclsOutput{NetworkAcls: []ec2types.NetworkAcl{{NetworkAclId: sdkaws.String("acl-d"), IsDefault: sdkaws.Bool(true)}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanNetworkACLs(ctx, ec2.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "ec2 default security group", typ: TypeEC2SecurityGroup,
			handlers: handlers{"DescribeSecurityGroups": ret(&ec2.DescribeSecurityGroupsOutput{SecurityGroups: []ec2types.SecurityGroup{{GroupId: sdkaws.String("sg-d"), GroupName: sdkaws.String("default")}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanSecurityGroups(ctx, ec2.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "eventbridge default bus", typ: TypeEventsEventBus,
			handlers: handlers{"ListEventBuses": ret(&eventbridge.ListEventBusesOutput{EventBuses: []eventbridgetypes.EventBus{{Arn: sdkaws.String(arn("events", "event-bus/default")), Name: sdkaws.String("default")}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, _, err := scanEventBridgeBuses(ctx, eventbridge.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "backup default vault", typ: TypeBackupVault,
			handlers: handlers{"ListBackupVaults": ret(&backup.ListBackupVaultsOutput{BackupVaultList: []backuptypes.BackupVaultListMember{{
				BackupVaultArn: sdkaws.String(arn("backup", "backup-vault:Default")), BackupVaultName: sdkaws.String("Default"), CreatorRequestId: sdkaws.String("Default"),
			}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanBackupAll(ctx, backup.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "athena primary workgroup", typ: TypeAthenaWorkgroup,
			handlers: handlers{
				"ListWorkGroups": ret(&athena.ListWorkGroupsOutput{WorkGroups: []athenatypes.WorkGroupSummary{{Name: sdkaws.String("primary")}}}),
				"GetWorkGroup":   ret(&athena.GetWorkGroupOutput{WorkGroup: &athenatypes.WorkGroup{Name: sdkaws.String("primary"), State: athenatypes.WorkGroupStateEnabled}}),
			},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, _, _, err := scanAthenaWorkGroups(ctx, athena.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "resource explorer region-named view", typ: TypeResourceExplorer2View,
			handlers: handlers{"ListViews": ret(&resourceexplorer2.ListViewsOutput{Views: []string{arn("resource-explorer-2", "view/"+testRegion+"/0b1c2d3e-0000-0000-0000-000000000000")}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanRE2Views(ctx, resourceexplorer2.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "apprunner default auto scaling configuration", typ: TypeAppRunnerAutoScalingConfiguration,
			handlers: handlers{"ListAutoScalingConfigurations": ret(&apprunner.ListAutoScalingConfigurationsOutput{AutoScalingConfigurationSummaryList: []apprunnertypes.AutoScalingConfigurationSummary{{
				AutoScalingConfigurationArn: sdkaws.String(arn("apprunner", "autoscalingconfiguration/mine/1/abc")), AutoScalingConfigurationName: sdkaws.String("mine"), IsDefault: sdkaws.Bool(true),
			}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanAppRunnerAutoScalingConfigs(ctx, apprunner.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "s3 default storage lens dashboard", typ: TypeS3StorageLens,
			handlers: handlers{
				"ListStorageLensConfigurations": ret(&s3control.ListStorageLensConfigurationsOutput{StorageLensConfigurationList: []s3controltypes.ListStorageLensConfigurationEntry{{
					Id: sdkaws.String("default-account-dashboard"), StorageLensArn: sdkaws.String(arn("s3", "storage-lens/default-account-dashboard")), HomeRegion: sdkaws.String(testRegion),
				}}}),
				"GetStorageLensConfiguration": ret(&s3control.GetStorageLensConfigurationOutput{StorageLensConfiguration: &s3controltypes.StorageLensConfiguration{Id: sdkaws.String("default-account-dashboard")}}),
			},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanStorageLens(ctx, acct, testRegion, s3control.NewFromConfig(cfg), st, testScanID)
				return err
			},
		},
		{
			name: "xray Default group", typ: TypeXRayGroup,
			handlers: handlers{"GetGroups": ret(&xray.GetGroupsOutput{Groups: []xraytypes.GroupSummary{{GroupARN: sdkaws.String(arn("xray", "group/Default")), GroupName: sdkaws.String("Default")}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanXRayGroups(ctx, xray.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "xray Default sampling rule", typ: TypeXRaySamplingRule,
			handlers: handlers{"GetSamplingRules": ret(&xray.GetSamplingRulesOutput{SamplingRuleRecords: []xraytypes.SamplingRuleRecord{{SamplingRule: &xraytypes.SamplingRule{
				RuleARN: sdkaws.String(arn("xray", "sampling-rule/Default")), RuleName: sdkaws.String("Default"),
			}}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanXRaySamplingRules(ctx, xray.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "organizations root", typ: TypeOrganizationsRoot,
			handlers: handlers{"ListRoots": ret(&organizations.ListRootsOutput{Roots: []organizationstypes.Root{{
				Id: sdkaws.String("r-ab12"), Arn: sdkaws.String("arn:aws:organizations::" + acctID + ":root/o-abc/r-ab12"), Name: sdkaws.String("Root"),
			}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				var pairs [][2]string
				_, _, _, _, err := scanOrgRoots(ctx, organizations.NewFromConfig(cfg), acct, st, testScanID, "o-abc", &pairs)
				return err
			},
		},
		{
			name: "elasticache default user", typ: TypeElastiCacheUser,
			handlers: handlers{"DescribeUsers": ret(&elasticache.DescribeUsersOutput{Users: []elasticachetypes.User{{ARN: sdkaws.String(arn("elasticache", "user:default")), UserId: sdkaws.String("default")}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanElastiCacheUsers(ctx, elasticache.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "memorydb default user", typ: TypeMemoryDBUser,
			handlers: handlers{"DescribeUsers": ret(&memorydb.DescribeUsersOutput{Users: []memorydbtypes.User{{ARN: sdkaws.String(arn("memorydb", "user/default")), Name: sdkaws.String("default")}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanMemDBUsers(ctx, memorydb.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "rds default DB security group", typ: TypeRDSDBSecurityGroup,
			handlers: handlers{"DescribeDBSecurityGroups": ret(&rds.DescribeDBSecurityGroupsOutput{DBSecurityGroups: []rdstypes.DBSecurityGroup{{
				DBSecurityGroupArn: sdkaws.String(arn("rds", "secgrp:default")), DBSecurityGroupName: sdkaws.String("default"),
			}}})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				_, _, err := scanDBSecurityGroups(ctx, rds.NewFromConfig(cfg), acct, testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "sms-voice Default opt-out list", typ: TypeSMSVoiceOptOutList,
			handlers: handlers{
				"DescribeOptOutLists": ret(&pinpointsmsvoicev2.DescribeOptOutListsOutput{OptOutLists: []smsvoicetypes.OptOutListInformation{{
					OptOutListArn: sdkaws.String(arn("sms-voice", "opt-out-list/Default")), OptOutListName: sdkaws.String("Default"),
				}}}),
				"ListTagsForResource": ret(&pinpointsmsvoicev2.ListTagsForResourceOutput{}),
			},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				client := pinpointsmsvoicev2.NewFromConfig(cfg)
				var p phaseTotals
				scanSVOptOutLists(ctx, client, acct, testRegion, st, testScanID, svTags(client), &p)
				_, _, err := p.result(st, acct, testRegion)
				return err
			},
		},
		{
			name: "notifications managed-notification channel association", typ: TypeNotificationsManagedNotificationAdditionalChannelAssoc,
			handlers: handlers{"ListManagedNotificationChannelAssociations": ret(&notifications.ListManagedNotificationChannelAssociationsOutput{
				ChannelAssociations: []notificationstypes.ManagedNotificationChannelAssociationSummary{{ChannelIdentifier: sdkaws.String("arn:aws:chatbot::" + acctID + ":chat-configuration/slack-channel/ops")}},
			})},
			run: func(ctx context.Context, cfg sdkaws.Config, acct *account, st *store.Store) error {
				mc := "arn:aws:notifications::" + acctID + ":managed-notification-configuration/category/AWS-Health/sub-category/Issue"
				_, _, err := scanNotifManagedChannelAssocs(ctx, notifications.NewFromConfig(cfg), acct, testRegion, st, testScanID, mc)
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(acctID)
			if err := tc.run(context.Background(), opStubConfig(tc.handlers, denied), acct, st); err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := rowsByNativeID(t, st, tc.typ)
			if len(rows) != 1 {
				t.Fatalf("%s rows = %v, want 1", tc.typ, rows)
			}
			for id, r := range rows {
				if r.ManagedByProvider {
					t.Errorf("%s row %s is ManagedByProvider; its settings are customer posture", tc.typ, id)
				}
			}
		})
	}
}
