package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/icearp/disco-cli/store"
)

// scanBackupFrameworks stores the audit frameworks with their tags.
func scanBackupFrameworks(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "backup:ListFrameworks",
		func(ctx context.Context, token *string) ([]backuptypes.Framework, *string, error) {
			out, err := client.ListFrameworks(ctx, &backup.ListFrameworksInput{NextToken: token, MaxResults: sdkaws.Int32(backupPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Frameworks, out.NextToken, nil
		},
		func(f backuptypes.Framework) (*store.Resource, string, string) {
			arn := sv(f.FrameworkArn)
			if arn == "" {
				return nil, "backup frameworks have no FrameworkArn", cmp.Or(sv(f.FrameworkName), bkNoName)
			}
			return bkRow(acct, region, scanID, TypeBackupFramework, arn, f.FrameworkName, f), "", ""
		}, tags, p)
}

// scanBackupReportPlans stores the report plans with their tags.
func scanBackupReportPlans(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "backup:ListReportPlans",
		func(ctx context.Context, token *string) ([]backuptypes.ReportPlan, *string, error) {
			out, err := client.ListReportPlans(ctx, &backup.ListReportPlansInput{NextToken: token, MaxResults: sdkaws.Int32(backupPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ReportPlans, out.NextToken, nil
		},
		func(rp backuptypes.ReportPlan) (*store.Resource, string, string) {
			arn := sv(rp.ReportPlanArn)
			if arn == "" {
				return nil, "backup report plans have no ReportPlanArn", cmp.Or(sv(rp.ReportPlanName), bkNoName)
			}
			return bkRow(acct, region, scanID, TypeBackupReportPlan, arn, rp.ReportPlanName, rp), "", ""
		}, tags, p)
}

// scanBackupRestoreTestingPlans stores the restore testing plans with their
// tags and returns the stored ones keyed by name, the parents of the restore
// testing selection phase.
func scanBackupRestoreTestingPlans(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "backup:ListRestoreTestingPlans", "restore testing plans", "RestoreTestingPlanName",
		func(ctx context.Context, token *string) ([]backuptypes.RestoreTestingPlanForList, *string, error) {
			out, err := client.ListRestoreTestingPlans(ctx, &backup.ListRestoreTestingPlansInput{NextToken: token, MaxResults: sdkaws.Int32(backupPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.RestoreTestingPlans, out.NextToken, nil
		},
		func(rtp backuptypes.RestoreTestingPlanForList) (*store.Resource, string, string) {
			arn := sv(rtp.RestoreTestingPlanArn)
			if arn == "" {
				return nil, "restore testing plans have no RestoreTestingPlanArn", cmp.Or(sv(rtp.RestoreTestingPlanName), bkNoName)
			}
			return bkRow(acct, region, scanID, TypeBackupRestoreTestingPlan, arn, rtp.RestoreTestingPlanName, rtp), "", ""
		},
		func(rtp backuptypes.RestoreTestingPlanForList) string { return sv(rtp.RestoreTestingPlanName) },
		tags, p)
}

// scanBackupRestoreTestingSelections stores the selections of each restore
// testing plan the plan phase stored. A selection has no ARN, so its
// NativeID is synthetic: the plan's ARN shape, keyed by plan name, plus
// /selection/{name}. A plan ListRestoreTestingSelections no longer finds was
// deleted since it was listed and is skipped.
func scanBackupRestoreTestingSelections(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, plans []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "backup:ListRestoreTestingSelections", backupNotFound, childParentIDs(plans),
		func(ctx context.Context, plan string, token *string) ([]backuptypes.RestoreTestingSelectionForList, *string, error) {
			out, err := client.ListRestoreTestingSelections(ctx, &backup.ListRestoreTestingSelectionsInput{
				RestoreTestingPlanName: sdkaws.String(plan), NextToken: token, MaxResults: sdkaws.Int32(backupPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.RestoreTestingSelections, out.NextToken, nil
		},
		func(plan string, s backuptypes.RestoreTestingSelectionForList) (*store.Resource, string, string) {
			name := sv(s.RestoreTestingSelectionName)
			if name == "" {
				return nil, "restore testing selections have no RestoreTestingSelectionName", "in plan " + plan
			}
			nativeID := arnFor(acct.partition(), "backup", region, acct.ID, "restore-testing-plan:"+plan+"/selection/"+name)
			return bkRow(acct, region, scanID, TypeBackupRestoreTestingSelection, nativeID, &name, s), "", ""
		})
}

// scanBackupTieringConfigurations stores the tiering configurations with
// their tags.
func scanBackupTieringConfigurations(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "backup:ListTieringConfigurations",
		func(ctx context.Context, token *string) ([]backuptypes.TieringConfigurationsListMember, *string, error) {
			out, err := client.ListTieringConfigurations(ctx, &backup.ListTieringConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(backupPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.TieringConfigurations, out.NextToken, nil
		},
		func(c backuptypes.TieringConfigurationsListMember) (*store.Resource, string, string) {
			arn := sv(c.TieringConfigurationArn)
			if arn == "" {
				return nil, "tiering configurations have no TieringConfigurationArn", cmp.Or(sv(c.TieringConfigurationName), bkNoName)
			}
			return bkRow(acct, region, scanID, TypeBackupTieringConfiguration, arn, c.TieringConfigurationName, c), "", ""
		}, tags, p)
}
