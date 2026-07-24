package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/backup"
)

// scanBackupExtended discovers five additional Backup resource types:
// frameworks (audit), report plans, restore testing plans, restore testing
// selections (per testing plan), and tiering configurations.
func scanBackupExtended(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t, i, ferr := scanBackupFrameworks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanBackupReportPlans(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	planNames, t, i, ferr := scanBackupRestoreTestingPlans(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, pn := range planNames {
		t, i, ferr = scanBackupRestoreTestingSelections(ctx, client, acct, region, st, scanID, pn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	t, i, ferr = scanBackupTieringConfigurations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanBackupFrameworks(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListFrameworks(ctx, &backup.ListFrameworksInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "backup:ListFrameworks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("backup:ListFrameworks: %w", err)
		}
		for _, f := range out.Frameworks {
			arn := sv(f.FrameworkArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupFramework, NativeID: arn,
				Name: f.FrameworkName, Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "backup frameworks")
}

func scanBackupReportPlans(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListReportPlans(ctx, &backup.ListReportPlansInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "backup:ListReportPlans", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("backup:ListReportPlans: %w", err)
		}
		for _, p := range out.ReportPlans {
			arn := sv(p.ReportPlanArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupReportPlan, NativeID: arn,
				Name: p.ReportPlanName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "backup report-plans")
}

func scanBackupRestoreTestingPlans(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var names []string
	var nextToken *string
	for {
		out, err := client.ListRestoreTestingPlans(ctx, &backup.ListRestoreTestingPlansInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "backup:ListRestoreTestingPlans", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("backup:ListRestoreTestingPlans: %w", err)
		}
		for _, p := range out.RestoreTestingPlans {
			arn := sv(p.RestoreTestingPlanArn)
			if arn == "" {
				continue
			}
			if n := sv(p.RestoreTestingPlanName); n != "" {
				names = append(names, n)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupRestoreTestingPlan, NativeID: arn,
				Name: p.RestoreTestingPlanName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "backup restore-testing-plans")
	return names, t, i, err
}

// scanBackupRestoreTestingSelections synthesizes ARN: parent restore testing
// plan ARN-shape + /selection/{name}. Selection has no native ARN.
func scanBackupRestoreTestingSelections(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, planName string) (int, int, error) {
	pn := planName
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListRestoreTestingSelections(ctx, &backup.ListRestoreTestingSelectionsInput{
			RestoreTestingPlanName: &pn,
			NextToken:              nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "backup:ListRestoreTestingSelections", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("backup:ListRestoreTestingSelections: %w", err)
		}
		for _, s := range out.RestoreTestingSelections {
			selName := sv(s.RestoreTestingSelectionName)
			if selName == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:backup:%s:%s:restore-testing-plan:%s/selection/%s", region, acct.ID, pn, selName)
			label := selName
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupRestoreTestingSelection, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "backup restore-testing-selections")
}

func scanBackupTieringConfigurations(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListTieringConfigurations(ctx, &backup.ListTieringConfigurationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "backup:ListTieringConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("backup:ListTieringConfigurations: %w", err)
		}
		for _, c := range out.TieringConfigurations {
			arn := sv(c.TieringConfigurationArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupTieringConfiguration, NativeID: arn,
				Name: c.TieringConfigurationName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "backup tiering-configurations")
}
