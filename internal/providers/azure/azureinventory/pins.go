package azureinventory

// SDKRef is the azure-sdk-for-go commit whose sdk/resourcemanager/**/*_client.go
// is read. Azure has no monorepo tag, hence a commit SHA. Bumping it changes
// the coverage denominator, so every report and the checked-in baseline print
// it; numbers are comparable only across identical pins.
const SDKRef = "f3847e8925003b3ae044a0ce2eaebd0e9983ed90"
