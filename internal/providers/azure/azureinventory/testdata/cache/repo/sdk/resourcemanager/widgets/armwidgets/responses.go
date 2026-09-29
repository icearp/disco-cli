package armwidgets

type WidgetsClientListResponse struct {
	WidgetListResult
}

type WidgetsClientListByResourceGroupResponse struct {
	WidgetListResult
}

type WidgetsClientGetResponse struct {
	Widget
}

type WidgetsClientInstanceViewResponse struct {
	WidgetInstanceView
}

type WidgetsClientValidateResponse struct {
	WidgetValidation
}

type WidgetPartsClientListResponse struct {
	WidgetPartArray []*WidgetPart
}

type WidgetPartsClientListAllResponse struct {
	PartListAllResult
}

type TenantThingsClientListResponse struct {
	TenantThingListResult
}

type TenantThingsClientCreateResponse struct {
	TenantThing
}

type SKUsClientListResponse struct {
	SKUListResult
}

type SKUsClientGetResponse struct {
	SKU
}

type OperationsClientListResponse struct {
	OperationListResult
}

type RoleAssignmentsClientListForScopeResponse struct {
	RoleAssignmentListResult
}

type RoleAssignmentsClientCreateResponse struct {
	RoleAssignment
}

type GizmosClientListResponse struct {
	GizmoList
}

type GizmosClientGetResponse struct {
	GizmoClassification
}

type PublishedThingsClientListResponse struct {
	PublishedThingList
}

type PublishedThingsClientGetResponse struct {
	PublishedThing
}

type ConfigsClientListConfigsResponse struct {
	ConfigList
}

type ConfigsClientUpdateWebResponse struct {
	Config
}

type ConfigsClientListSnapshotsResponse struct {
	SnapshotList
}

type ConfigsClientGetSnapshotResponse struct {
	Snapshot
}

type SettingsClientListResponse struct {
	SettingList
}

type SettingsClientGetDefaultResponse struct {
	Setting
}

type SettingsClientListRulesResponse struct {
	RuleList
}

type SettingsClientCreateRuleResponse struct {
	Rule
}

type AccountsClientListResponse struct {
	AccountList
}

type AccountsClientGetResponse struct {
	Account
}

type AccountsClientListInvoicesResponse struct {
	InvoiceList
}

type AccountsClientGetInvoiceResponse struct {
	Invoice
}

type AccountsClientListDeletedResponse struct {
	AccountList
}

type DraftsClientGetDraftResponse struct {
	Draft
}

type DraftsClientCreateTestJobResponse struct {
	TestJob
}

type DraftsClientListStreamsResponse struct {
	StreamList
}

type DraftsClientGetStreamResponse struct {
	Stream
}

type DeploymentThingsClientListResponse struct {
	DeploymentThingList
}

type ProviderTypesClientListResponse struct {
	ProviderTypeList
}

type ClientGetByIDResponse struct {
	GenericResource
}

type FeaturesClientListResponse struct {
	FeatureList
}

type AttachmentsClientListResponse struct {
	AttachmentList
}

type BudgetsClientListResponse struct {
	BudgetList
}

type PartnersClientGetResponse struct {
	Partner
}

type PartnersClientCreateResponse struct {
	Partner
}

type QuotasClientListResponse struct {
	QuotaList
}

type QuotasClientListBySubscriptionResponse struct {
	QuotaList
}

type QuotasClientGetResponse struct {
	Quota
}

type VersionsClientListResponse struct {
	VersionList
}

type VersionsClientGetResponse struct {
	Version
}

type UsagesClientListResponse struct {
	UsageList
}
