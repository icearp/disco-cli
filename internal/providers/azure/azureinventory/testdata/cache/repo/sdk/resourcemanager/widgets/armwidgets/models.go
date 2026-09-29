package armwidgets

type SubResource struct {
	ID *string
}

type SystemData struct {
	CreatedBy *string
}

type Widget struct {
	Location   *string
	Properties *WidgetProperties
	Tags       map[string]*string
	ID         *string
	Name       *string
	Type       *string
	SystemData *SystemData
}

type WidgetProperties struct {
	Extra             *WidgetExtra
	ProvisioningState *string
	SubnetID          *string
	Vault             *SubResource
	Encryption        *Encryption
	HiddenID          *string
	Deep              *Level1
	Image             *ImageRef
	Links             map[string]*SubResource
}

type WidgetExtra struct {
	PolicyID    *string
	Mode        *string
	EndpointURI *string
}

type Level1 struct {
	Level2 *Level2
}

type Level2 struct {
	ShallowID *string
	Level3    *Level3
}

type Level3 struct {
	TargetID *string
}

type ImageRef struct {
	ID             *string
	Publisher      *string
	GalleryImageID *string
}

type WidgetListResult struct {
	Value    []*Widget
	NextLink *string
}

type WidgetInstanceView struct {
	Statuses []*Status
	Disks    []*Disk
	Nics     []*Nic
}

type Disk struct {
	ID *string
}

type Nic struct {
	ID *string
}

type Status struct {
	Code *string
}

type WidgetValidation struct {
	ID      *string
	Details []*Status
}

type WidgetPart struct {
	ID   *string
	Name *string
	Type *string
}

type PartSummary struct {
	Count *int32
}

type PartListAllResult struct {
	Summaries []*PartSummary
	Value     []*WidgetPart
}

type TenantThing struct {
	ID   *string
	Name *string
}

type TenantThingListResult struct {
	Value []*TenantThing
}

type SKU struct {
	Name *string
	Tier *string
}

type SKUListResult struct {
	Value []*SKU
}

type Operation struct {
	Name *string
}

type OperationListResult struct {
	Value []*Operation
}

type RoleAssignment struct {
	ID          *string
	PrincipalID *string
}

type RoleAssignmentListResult struct {
	Value []*RoleAssignment
}

type Gizmo struct {
	Kind       *string
	ID         *string
	SystemData *SystemData
}

type GizmoList struct {
	Value []GizmoClassification
}

type PublishedThing struct {
	ID         *string
	SystemData *SystemData
}

type PublishedThingList struct {
	SystemData *SystemData
	Value      []*PublishedThing
}

type Config struct {
	ID   *string
	Name *string
}

type ConfigList struct {
	Value []*Config
}

type Snapshot struct {
	ID   *string
	Type *string
}

type SnapshotList struct {
	Value []*Snapshot
}

type Setting struct {
	ID *string
}

type SettingList struct {
	Value []*Setting
}

type Rule struct {
	ID *string
}

type RuleList struct {
	Value []*Rule
}

type Account struct {
	ID *string
}

type AccountList struct {
	ID    *string
	Value []*Account
}

type Feature struct {
	Name *string
}

type FeatureList struct {
	Value []*Feature
}

type Attachment struct {
	ID *string
}

type AttachmentList struct {
	Value []*Attachment
}

type Budget struct {
	ID *string
}

type BudgetList struct {
	Value []*Budget
}

type Invoice struct {
	Amount *int32
}

type InvoiceList struct {
	Value []*Invoice
}

type Draft struct {
	ID *string
}

type TestJob struct {
	Status *string
}

type Stream struct {
	Text *string
}

type StreamList struct {
	Value []*Stream
}

type DeploymentThing struct {
	ID *string
}

type DeploymentThingList struct {
	Value []*DeploymentThing
}

type ProviderType struct {
	Name *string
}

type ProviderTypeList struct {
	Value []*ProviderType
}

type GenericResource struct {
	ID *string
}

type Partner struct {
	ID   *string
	Name *string
}

type Quota struct {
	ID         *string
	SystemData *SystemData
}

type QuotaList struct {
	Value []*Quota
}

type Version struct {
	ID         *string
	SystemData *SystemData
}

type VersionList struct {
	Value []*Version
}

type Usage struct {
	Name *string
}

type UsageList struct {
	Value []*Usage
}

// GizmoClassification provides polymorphic access to related types.
type GizmoClassification interface {
	// GetGizmo returns the Gizmo content of the underlying type.
	GetGizmo() *Gizmo
}

func (g *Gizmo) GetGizmo() *Gizmo { return g }
