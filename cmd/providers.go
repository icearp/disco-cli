package cmd

// Provider packages register themselves via init() by calling providers.Register.
// Add a blank import here for each new provider so its Scanner is available
// as a "disco scan <name>" subcommand.
