package aws

import (
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/managed"
	"github.com/icearp/disco-cli/internal/untaggable"
)

// TestNoDoubleDeclaredTypes ensures a disco type is declared EITHER the legacy
// way (serviceEntry.emits / registerExtraEmits) OR via registerType, never
// both — a type in both sets would double-forward redact/volatile/managed
// rules while CollectEmits silently dedups the coverage decl. Post-migration
// the legacy sets are empty; the test guards against a future half-migration.
func TestNoDoubleDeclaredTypes(t *testing.T) {
	legacy := make(map[string]bool)
	for _, s := range registeredServices {
		for _, d := range s.emits {
			legacy[d.DiscoType] = true
		}
	}
	for _, d := range registeredDescriptors {
		if legacy[d.Type] {
			t.Errorf("type %q declared via BOTH registerType and a legacy emit — remove the legacy site", d.Type)
		}
	}
}

// TestServiceQuotasQuotaLimitsDeclareNoResourceType guards the classification a service
// quota now has: it is a limit value stored in `quotas`, not a resource.
//
// Re-adding a registerType for it would put ~90% of a real account's rows back
// into `resources` — silently, since every scanner test would still pass — and
// would restore the badge and index pressure the split removed. The scanner is
// still registered, so this is not "we stopped scanning quotas": the service
// entry has to survive alongside the absent type.
//
// A quota request template entry is not a limit value: it is a user-created
// org-level configuration (PutServiceQuotaIncreaseRequestIntoTemplate /
// DeleteServiceQuotaIncreaseRequestFromTemplate),
// so it is the one servicequotas type allowed in `resources`.
func TestServiceQuotasQuotaLimitsDeclareNoResourceType(t *testing.T) {
	notQuotas := map[string]bool{TypeServiceQuotasServiceQuotaIncreaseRequestInTemplate: true}
	for _, d := range registeredDescriptors {
		if d.Service == "servicequotas" && !notQuotas[d.Type] {
			t.Errorf("quota type %q is registered as a resource — quotas belong in the quotas table", d.Type)
		}
	}
	for _, s := range registeredServices {
		for _, d := range s.emits {
			if strings.HasPrefix(d.DiscoType, "aws:servicequotas:") {
				t.Errorf("quota type %q is declared as a legacy emit — quotas belong in the quotas table", d.DiscoType)
			}
		}
	}
	if !serviceRegistered("aws:servicequotas") {
		t.Error("aws:servicequotas is no longer registered as a service — quotas would stop being scanned entirely")
	}
}

// descriptorEmitted reports whether ty was declared via registerType — the
// post-migration source of coverage emits. Test helper for the per-service
// registration-covered tests that used to iterate serviceEntry.emits.
func descriptorEmitted(ty string) bool {
	for _, d := range registeredDescriptors {
		if d.Type == ty {
			return true
		}
	}
	return false
}

// serviceRegistered reports whether a registerService call named `name` exists.
func serviceRegistered(name string) bool {
	for _, s := range registeredServices {
		if s.name == name {
			return true
		}
	}
	return false
}

// TestUnconditionalManagedStampedByType proves each unconditionally-managed
// type's Descriptor.Managed:true reached the shared managed engine (which the
// store consults to stamp ManagedByProvider), replacing the deleted scanner
// literals. Dual-natured types (IAMPolicy, LambdaLayerVersion) are
// deliberately absent — their managed flag stays per-row scanner-set.
func TestUnconditionalManagedStampedByType(t *testing.T) {
	wantManaged := []string{
		TypeBedrockFoundationModel, TypeSecurityHubStandard, TypeTrustedAdvisorChecks,
	}
	for _, ty := range wantManaged {
		if !managed.Is(ty) {
			t.Errorf("%s not registered as managed — Descriptor.Managed:true not forwarded", ty)
		}
	}
	// Dual-natured types must NOT be type-stamped (would wrongly hide customer rows).
	for _, ty := range []string{TypeIAMPolicy, TypeLambdaLayerVersion} {
		if managed.Is(ty) {
			t.Errorf("%s type-stamped managed — dual-natured type must stay scanner-set", ty)
		}
	}
}

// TestCustomerPostureTypesNotManaged pins that config and posture types whose
// values the customer sets are not type-stamped managed, even when AWS creates
// the row and no API deletes it: managed rows are left out of default
// `disco check` input, so a policy rule about this posture would see nothing.
func TestCustomerPostureTypesNotManaged(t *testing.T) {
	for _, ty := range []string{
		TypeACMAccount, TypeAPIGatewayAccount, TypeARCZonalShiftObserverStatus,
		TypeCloudWatchOTelEnrichment, TypeConnectAttachedFilesConfiguration,
		TypeEC2AllowedImagesSetting, TypeEC2SnapshotBlockPublicAccess,
		TypeEC2VerifiedAccessInstanceLoggingConfiguration, TypeEC2VPCBlockPublicAccessOptions,
		TypeECRPublicRegistry, TypeECRRegistryScanningConfig, TypeECRReplicationConfiguration, TypeECRRepositoryScanningConfig,
		TypeECSAccountSetting, TypeIoTAccountAuditConfiguration, TypeIoTEncryptionConfiguration,
		TypeLakeFormationDataLakeSettings, TypeNotificationsManagedNotificationAdditionalChannelAssoc,
		TypeOrganizationsRoot, TypeQuickSightAccount, TypeQuickSightCustomization,
		TypeQuickSightKeyRegistration, TypeResourceExplorer2DefaultViewAssociation,
		TypeResourceExplorer2Index, TypeRoute53ResolverFirewallConfig, TypeRoute53ResolverResolverConfig,
		TypeSecurityHubStandardsControlAssociation, TypeUXCAccountCustomization,
		TypeWickrNetworkSetting, TypeXRayIndexingRule, TypeXRayTransactionSearchConfig,
	} {
		if managed.Is(ty) {
			t.Errorf("%s registered Managed; customer posture must stay visible to default queries", ty)
		}
	}
}

// TestUntaggableTypesDeclared pins the customer-posture types 9053be5d
// un-managed. Most are settings the Service Reference lists as no resource at
// all, so TestUntaggableMatchesServiceReference cannot hold them; this list
// does, without needing the SDK cache. The second list holds posture types
// the Service Reference does list as taggable: they must stay in the
// denominator.
func TestUntaggableTypesDeclared(t *testing.T) {
	for _, ty := range []string{
		TypeACMAccount, TypeAPIGatewayAccount, TypeARCZonalShiftObserverStatus,
		TypeCloudWatchOTelEnrichment, TypeConnectAttachedFilesConfiguration,
		TypeEC2AllowedImagesSetting, TypeEC2SnapshotBlockPublicAccess,
		TypeEC2VerifiedAccessInstanceLoggingConfiguration, TypeEC2VPCBlockPublicAccessOptions, TypeFMSNotificationChannel,
		TypeECRPublicRegistry, TypeECRRegistryScanningConfig, TypeECRReplicationConfiguration, TypeECRRepositoryScanningConfig,
		TypeECSAccountSetting, TypeIoTAccountAuditConfiguration, TypeIoTEncryptionConfiguration,
		TypeLakeFormationDataLakeSettings, TypeNotificationsManagedNotificationAdditionalChannelAssoc,
		TypeQuickSightAccount, TypeQuickSightKeyRegistration, TypeResourceExplorer2DefaultViewAssociation,
		TypeRoute53ResolverResolverConfig, TypeSecurityHubStandardsControlAssociation,
		TypeUXCAccountCustomization, TypeWickrNetworkSetting, TypeXRayIndexingRule, TypeXRayTransactionSearchConfig,
	} {
		if !untaggable.Is(ty) {
			t.Errorf("%s not declared Untaggable; its rows would lower tag coverage", ty)
		}
	}
	for _, ty := range []string{
		TypeOrganizationsRoot, TypeQuickSightCustomization, TypeResourceExplorer2Index,
		TypeRoute53ResolverFirewallConfig, TypeEC2Instance,
	} {
		if untaggable.Is(ty) {
			t.Errorf("%s declared Untaggable; AWS can tag it, so it belongs in the tag-coverage denominator", ty)
		}
	}
}
