package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	bda "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation"
	bdatypes "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation/types"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "bedrock", DiscoType: TypeBedrockBlueprint, Leaf: true},
		coverage.TypeDecl{Service: "bedrock", DiscoType: TypeBedrockDataAutomationProject, Leaf: true},
		coverage.TypeDecl{Service: "bedrock", DiscoType: TypeBedrockDataAutomationLibrary, Leaf: true},
	)
}

// bedrockDataAutomationAPI is the narrow bedrockdataautomation surface used by
// the data-automation scanners. None of the three List ops are paginated by the
// SDK — they use manual NextToken loops.
type bedrockDataAutomationAPI interface {
	ListBlueprints(context.Context, *bda.ListBlueprintsInput, ...func(*bda.Options)) (*bda.ListBlueprintsOutput, error)
	ListDataAutomationProjects(context.Context, *bda.ListDataAutomationProjectsInput, ...func(*bda.Options)) (*bda.ListDataAutomationProjectsOutput, error)
	ListDataAutomationLibraries(context.Context, *bda.ListDataAutomationLibrariesInput, ...func(*bda.Options)) (*bda.ListDataAutomationLibrariesOutput, error)
}

// scanBedrockDataAutomation discovers Bedrock Data Automation blueprints,
// projects and libraries. Called from scanBedrock (which owns the client).
func scanBedrockDataAutomation(ctx context.Context, client bedrockDataAutomationAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanBedrockBlueprints(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanBedrockDataAutomationProjects(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanBedrockDataAutomationLibraries(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// bedrockDAListErr soft-skips the SCP-deny / access-denied / per-region feature-
// gap shapes shared by the data-automation List ops. Returns nil to stop the
// phase with no rows.
func bedrockDAListErr(st *store.Store, op, acctID, region string, perr error) error {
	switch {
	case isSCPExplicitDeny(perr):
		return nil
	case isAPIErrorWithMessage(perr, "ValidationException", "operation is not recognized"),
		isAPIErrorWithMessage(perr, "ValidationException", "don't have the permissions to perform the requested operation"):
		return nil
	case isAccessDenied(perr):
		return skipIfAccessDenied(st, op, acctID, region, perr)
	default:
		return fmt.Errorf("%s: %w", op, perr)
	}
}

// scanBedrockBlueprints lists both ACCOUNT-owned blueprints (customer resources)
// and SERVICE-owned ones (the AWS-provided sample catalog, flagged
// ManagedByProvider).
func scanBedrockBlueprints(ctx context.Context, client bedrockDataAutomationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, owner := range []bdatypes.ResourceOwner{bdatypes.ResourceOwnerAccount, bdatypes.ResourceOwnerService} {
		managed := owner == bdatypes.ResourceOwnerService
		var token *string
		for {
			out, perr := client.ListBlueprints(ctx, &bda.ListBlueprintsInput{ResourceOwner: owner, NextToken: token})
			if perr != nil {
				// Soft-skip stops this owner pass but preserves rows already
				// accumulated from the other owner (the fall-through upsert).
				if e := bedrockDAListErr(st, "bedrockdataautomation:ListBlueprints", acct.ID, region, perr); e != nil {
					return 0, 0, e
				}
				break
			}
			for _, b := range out.Blueprints {
				arn := sv(b.BlueprintArn)
				if arn == "" {
					continue
				}
				name := sv(b.BlueprintName)
				status := string(b.BlueprintStage)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockBlueprint, NativeID: arn,
					Name: &name, Region: &region, Status: &status,
					ManagedByProvider: managed,
					AttributesJSON:    mustJSON(b), CreatedAt: tp(b.CreationTime), DiscoveredBy: scanID,
				})
			}
			if token = out.NextToken; token == nil {
				break
			}
		}
	}
	return upsertBatch(st, batch, "bedrock blueprints")
}

// scanBedrockDataAutomationProjects lists only the account's own projects — AWS
// ships no SERVICE-owned project catalog, so the owner loop the blueprint scanner
// uses is deliberately omitted here (ListDataAutomationProjectsInput exposes
// ResourceOwner but defaults to ACCOUNT).
func scanBedrockDataAutomationProjects(ctx context.Context, client bedrockDataAutomationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.ListDataAutomationProjects(ctx, &bda.ListDataAutomationProjectsInput{NextToken: token})
		if perr != nil {
			if e := bedrockDAListErr(st, "bedrockdataautomation:ListDataAutomationProjects", acct.ID, region, perr); e != nil {
				return 0, 0, e
			}
			break
		}
		for _, p := range out.Projects {
			arn := sv(p.ProjectArn)
			if arn == "" {
				continue
			}
			name := sv(p.ProjectName)
			status := string(p.ProjectStage)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockDataAutomationProject, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), CreatedAt: tp(p.CreationTime), DiscoveredBy: scanID,
			})
		}
		if token = out.NextToken; token == nil {
			break
		}
	}
	return upsertBatch(st, batch, "bedrock data-automation-projects")
}

func scanBedrockDataAutomationLibraries(ctx context.Context, client bedrockDataAutomationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.ListDataAutomationLibraries(ctx, &bda.ListDataAutomationLibrariesInput{NextToken: token})
		if perr != nil {
			if e := bedrockDAListErr(st, "bedrockdataautomation:ListDataAutomationLibraries", acct.ID, region, perr); e != nil {
				return 0, 0, e
			}
			break
		}
		for _, l := range out.Libraries {
			arn := sv(l.LibraryArn)
			if arn == "" {
				continue
			}
			name := sv(l.LibraryName)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockDataAutomationLibrary, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(l), CreatedAt: tp(l.CreationTime), DiscoveredBy: scanID,
			})
		}
		if token = out.NextToken; token == nil {
			break
		}
	}
	return upsertBatch(st, batch, "bedrock data-automation-libraries")
}
