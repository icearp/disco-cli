package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/iotmanagedintegrations"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsAccountAssociation, Service: "iotmanagedintegrations", Upstream: "AWS::iotmanagedintegrations::account-association", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsCredentialLocker, Service: "iotmanagedintegrations", Upstream: "AWS::iotmanagedintegrations::credential-locker", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsManagedThing, Service: "iotmanagedintegrations", Upstream: "AWS::iotmanagedintegrations::managed-thing", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsOtaTask, Service: "iotmanagedintegrations", Upstream: "AWS::iotmanagedintegrations::ota-task", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsProvisioningProfile, Service: "iotmanagedintegrations", Upstream: "AWS::iotmanagedintegrations::provisioning-profile", Leaf: true})
	registerService(serviceEntry{
		name: "aws:iotmanagedintegrations",
		fn:   scanIoTManagedIntegrations,
	})
}

type iotManagedIntegrationsAPI interface {
	ListAccountAssociations(context.Context, *iotmanagedintegrations.ListAccountAssociationsInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListAccountAssociationsOutput, error)
	ListCredentialLockers(context.Context, *iotmanagedintegrations.ListCredentialLockersInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListCredentialLockersOutput, error)
	ListManagedThings(context.Context, *iotmanagedintegrations.ListManagedThingsInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListManagedThingsOutput, error)
	ListOtaTasks(context.Context, *iotmanagedintegrations.ListOtaTasksInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListOtaTasksOutput, error)
	ListProvisioningProfiles(context.Context, *iotmanagedintegrations.ListProvisioningProfilesInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListProvisioningProfilesOutput, error)
}

func scanIoTManagedIntegrations(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iotmanagedintegrations.NewFromConfig(acct.cfg, func(o *iotmanagedintegrations.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIMIAccountAssociations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIMICredentialLockers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIMIManagedThings(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIMIOtaTasks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIMIProvisioningProfiles(ctx, client, acct, region, st, scanID) },
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

func scanIMIAccountAssociations(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, perr := client.ListAccountAssociations(ctx, &iotmanagedintegrations.ListAccountAssociationsInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "iotmanagedintegrations:ListAccountAssociations", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("iotmanagedintegrations:ListAccountAssociations: %w", perr)
		}
		for _, a := range out.Items {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTManagedIntegrationsAccountAssociation, NativeID: arn,
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iotmanagedintegrations account-associations")
}

func scanIMICredentialLockers(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, perr := client.ListCredentialLockers(ctx, &iotmanagedintegrations.ListCredentialLockersInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "iotmanagedintegrations:ListCredentialLockers", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("iotmanagedintegrations:ListCredentialLockers: %w", perr)
		}
		for _, l := range out.Items {
			arn := sv(l.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTManagedIntegrationsCredentialLocker, NativeID: arn,
				Name: l.Name, Region: &region,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iotmanagedintegrations credential-lockers")
}

func scanIMIManagedThings(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, perr := client.ListManagedThings(ctx, &iotmanagedintegrations.ListManagedThingsInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "iotmanagedintegrations:ListManagedThings", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("iotmanagedintegrations:ListManagedThings: %w", perr)
		}
		for _, t := range out.Items {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTManagedIntegrationsManagedThing, NativeID: arn,
				Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iotmanagedintegrations managed-things")
}

func scanIMIOtaTasks(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, perr := client.ListOtaTasks(ctx, &iotmanagedintegrations.ListOtaTasksInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "iotmanagedintegrations:ListOtaTasks", acct.ID, region, perr)
			}
			// OTA tasks need a registered custom endpoint + onboarded managed
			// thing; unconfigured accounts 403 with "Please register the custom
			// endpoint and onboard the managed thing" — silent per-op skip
			// (sibling IMI phases still scan).
			if isAPIErrorWithMessage(perr, "UnknownError", "register the custom endpoint") {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotmanagedintegrations:ListOtaTasks: %w", perr)
		}
		for _, t := range out.Tasks {
			arn := sv(t.TaskArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTManagedIntegrationsOtaTask, NativeID: arn,
				Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iotmanagedintegrations ota-tasks")
}

func scanIMIProvisioningProfiles(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, perr := client.ListProvisioningProfiles(ctx, &iotmanagedintegrations.ListProvisioningProfilesInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "iotmanagedintegrations:ListProvisioningProfiles", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("iotmanagedintegrations:ListProvisioningProfiles: %w", perr)
		}
		for _, p := range out.Items {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTManagedIntegrationsProvisioningProfile, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iotmanagedintegrations provisioning-profiles")
}
