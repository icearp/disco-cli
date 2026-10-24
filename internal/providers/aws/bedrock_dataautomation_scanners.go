package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	bda "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation"
	bdatypes "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBedrockBlueprint, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockDataAutomationProject, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockDataAutomationLibrary, Service: "bedrock"})
}

// bedrockDataAutomationAPI is the narrow bedrockdataautomation surface used by
// the data-automation scanners.
type bedrockDataAutomationAPI interface {
	ListBlueprints(context.Context, *bda.ListBlueprintsInput, ...func(*bda.Options)) (*bda.ListBlueprintsOutput, error)
	ListDataAutomationProjects(context.Context, *bda.ListDataAutomationProjectsInput, ...func(*bda.Options)) (*bda.ListDataAutomationProjectsOutput, error)
	ListDataAutomationLibraries(context.Context, *bda.ListDataAutomationLibrariesInput, ...func(*bda.Options)) (*bda.ListDataAutomationLibrariesOutput, error)
	ListTagsForResource(context.Context, *bda.ListTagsForResourceInput, ...func(*bda.Options)) (*bda.ListTagsForResourceOutput, error)
}

// isBedrockDAGap reports the first-page refusals of a data-automation
// listing that mean the op is not offered here, so the listing ends
// silently: an SCP's explicit deny; the ValidationException shapes "operation
// is not recognized" and "don't have the permissions to perform the requested
// operation"; an AccessDeniedException reading "not supported in this region"
// (the blueprint library sub-feature, while the rest of Data Automation
// works); and the empty-body AccessDeniedException every op answers with
// where the newer, regionally limited service is not offered. That last one
// is not reported as the service unavailable: the region still serves the
// rest of bedrock, and the sentinel would discard the counts of its rows.
func isBedrockDAGap(err error) bool {
	return isSCPExplicitDeny(err) ||
		isAPIErrorWithMessage(err, "ValidationException", "operation is not recognized") ||
		isAPIErrorWithMessage(err, "ValidationException", "don't have the permissions to perform the requested operation") ||
		isAccessDeniedWithMessage(err, "not supported in this region") ||
		isClosedToNewCustomers(err)
}

// scanBedrockBlueprints lists ACCOUNT-owned blueprints (customer resources)
// and SERVICE-owned ones (the AWS-provided sample catalog, flagged
// ManagedByProvider, so their tags are not read). Each owner is its own
// listing, so one's failure does not stop the other.
func scanBedrockBlueprints(ctx context.Context, client bedrockDataAutomationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	for _, owner := range []bdatypes.ResourceOwner{bdatypes.ResourceOwnerAccount, bdatypes.ResourceOwnerService} {
		// The flag comes from the request because BlueprintSummary carries no
		// owner. ResourceOwner filters the listing by owner, so every row of
		// the SERVICE pass is AWS's; its ARNs, which name the "aws" account in
		// place of an account id, bear that out.
		managed := owner == bdatypes.ResourceOwnerService
		if storeTokenListTags(ctx, st, acct, region, "bedrockdataautomation:ListBlueprints",
			bacPages(isBedrockDAGap, func(ctx context.Context, token *string) ([]bdatypes.BlueprintSummary, *string, error) {
				out, err := client.ListBlueprints(ctx, &bda.ListBlueprintsInput{ResourceOwner: owner, NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
				if err != nil {
					return nil, nil, err
				}
				return out.Blueprints, out.NextToken, nil
			}),
			func(b bdatypes.BlueprintSummary) (*store.Resource, string, string) {
				r, why, key := bacRow(acct, region, scanID, TypeBedrockBlueprint, "blueprints", b.BlueprintArn, sv(b.BlueprintName), sv(b.BlueprintName), b)
				if r != nil {
					r.Status = nonEmptyPtr(string(b.BlueprintStage))
					r.CreatedAt = tp(b.CreationTime)
					r.ManagedByProvider = managed
				}
				return r, why, key
			}, tags, p) {
			return true
		}
	}
	return false
}

// scanBedrockDataAutomationProjects lists only the account's own projects —
// AWS ships no SERVICE-owned project catalog, so the blueprint scanner's
// owner loop is deliberately omitted (ListDataAutomationProjectsInput exposes
// ResourceOwner but defaults to ACCOUNT).
func scanBedrockDataAutomationProjects(ctx context.Context, client bedrockDataAutomationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrockdataautomation:ListDataAutomationProjects",
		bacPages(isBedrockDAGap, func(ctx context.Context, token *string) ([]bdatypes.DataAutomationProjectSummary, *string, error) {
			out, err := client.ListDataAutomationProjects(ctx, &bda.ListDataAutomationProjectsInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Projects, out.NextToken, nil
		}),
		func(pr bdatypes.DataAutomationProjectSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockDataAutomationProject, "data automation projects", pr.ProjectArn, sv(pr.ProjectName), sv(pr.ProjectName), pr)
			if r != nil {
				r.Status = nonEmptyPtr(string(pr.ProjectStage))
				r.CreatedAt = tp(pr.CreationTime)
			}
			return r, why, key
		}, tags, p)
}

func scanBedrockDataAutomationLibraries(ctx context.Context, client bedrockDataAutomationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrockdataautomation:ListDataAutomationLibraries",
		bacPages(isBedrockDAGap, func(ctx context.Context, token *string) ([]bdatypes.DataAutomationLibrarySummary, *string, error) {
			out, err := client.ListDataAutomationLibraries(ctx, &bda.ListDataAutomationLibrariesInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Libraries, out.NextToken, nil
		}),
		func(l bdatypes.DataAutomationLibrarySummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockDataAutomationLibrary, "data automation libraries", l.LibraryArn, sv(l.LibraryName), sv(l.LibraryName), l)
			if r != nil {
				r.CreatedAt = tp(l.CreationTime)
			}
			return r, why, key
		}, tags, p)
}
