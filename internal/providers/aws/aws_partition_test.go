package aws

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/spf13/viper"
)

// fakeBases is a baseConfigLoader whose configs answer STS GetCallerIdentity
// with callerARN and AssumeRole with fresh credentials, and fail every other
// call. Each config's Region names the profile it was loaded for, and each
// AssumeRole is recorded as "profile role-arn source-identity", so a test can tell which base
// an account's config and STS client came from.
type fakeBases struct {
	callerARN string
	// regions, when it has the profile, is the region that profile's config
	// carries ("" for none); otherwise the region is baseRegionFor(profile),
	// which no partition matches.
	regions map[string]string
	loaded  []string
	assumed []string
	// calls records the region of every STS call the configs answer.
	calls parentCalls
}

func baseRegionFor(profile string) string { return "base-region-" + profile }

func (f *fakeBases) load(_ context.Context, profile string) (sdkaws.Config, error) {
	f.loaded = append(f.loaded, profile)
	cfg := opStubConfig(map[string]func(any) (any, error){
		"GetCallerIdentity": func(any) (any, error) {
			if f.callerARN == "" {
				return nil, errors.New("test did not expect sts:GetCallerIdentity")
			}
			a, err := arn.Parse(f.callerARN)
			if err != nil {
				return nil, err
			}
			return &sts.GetCallerIdentityOutput{Account: &a.AccountID, Arn: &f.callerARN}, nil
		},
		"AssumeRole": func(in any) (any, error) {
			ar := in.(*sts.AssumeRoleInput)
			f.assumed = append(f.assumed, profile+" "+sv(ar.RoleArn)+" "+sv(ar.SourceIdentity))
			return &sts.AssumeRoleOutput{Credentials: &ststypes.Credentials{
				AccessKeyId:     sdkaws.String("AKID"),
				SecretAccessKey: sdkaws.String("SECRET"),
				SessionToken:    sdkaws.String("TOKEN"),
				Expiration:      sdkaws.Time(time.Now().Add(time.Hour)),
			}}, nil
		},
	}, errors.New("test did not expect this call"))
	cfg.Region = baseRegionFor(profile)
	if r, ok := f.regions[profile]; ok {
		cfg.Region = r
	}
	return recordCallRegions(cfg, &f.calls), nil
}

// setAWSConfig installs the aws: config section loadAccounts reads, outside
// emulator mode and with no region variables set, unless the test sets them
// itself afterwards.
func setAWSConfig(t *testing.T, section map[string]any) {
	t.Helper()
	t.Setenv("AWS_ENDPOINT_URL", "")
	t.Setenv("DISCO_CLOUD_ACCOUNT_ID", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	viper.Set("aws", section)
	t.Cleanup(viper.Reset)
}

func accountsSection(accounts ...map[string]any) map[string]any {
	list := make([]any, len(accounts))
	for i, a := range accounts {
		list[i] = a
	}
	return map[string]any{"accounts": list}
}

const (
	roleAWS   = "arn:aws:iam::111111111111:role/Scan"
	roleCN    = "arn:aws-cn:iam::111111111111:role/Scan"
	roleGov   = "arn:aws-us-gov:iam::111111111111:role/Scan"
	roleEUSC  = "arn:aws-eusc:iam::111111111111:role/Scan"
	roleISO   = "arn:aws-iso:iam::111111111111:role/Scan"
	roleMars  = "arn:aws-mars:iam::111111111111:role/Scan"
	callerGov = "arn:aws-us-gov:sts::222222222222:assumed-role/Scan/s"
)

func loadOne(t *testing.T, f *fakeBases, regionOverride []string, roleOverride string) *account {
	t.Helper()
	accts, err := loadAccounts(context.Background(), f.load, "", regionOverride, roleOverride, "", "")
	if err != nil {
		t.Fatalf("loadAccounts: %v", err)
	}
	if len(accts) != 1 {
		t.Fatalf("loadAccounts returned %d accounts; want 1", len(accts))
	}
	return &accts[0]
}

// TestLoadAccounts_PartitionDerivation walks each precedence rung across the
// partitions disco supports. Every case puts a different partition on a lower
// rung where one exists, so a rung that is skipped or misordered changes the
// answer.
func TestLoadAccounts_PartitionDerivation(t *testing.T) {
	cases := []struct {
		name    string
		account map[string]any
		section map[string]any // extra top-level keys
		want    string
	}{
		{"explicit partition", map[string]any{"id": "1", "partition": "aws-iso"}, nil, "aws-iso"},
		{"explicit agrees with role and regions", map[string]any{"id": "1", "partition": "aws-cn", "role_arn": roleCN, "regions": []string{"cn-north-1"}}, nil, "aws-cn"},
		{"role_arn us-gov", map[string]any{"id": "1", "role_arn": roleGov}, nil, "aws-us-gov"},
		{"role_arn eusc", map[string]any{"id": "1", "role_arn": roleEUSC}, nil, "aws-eusc"},
		{"role_arn iso", map[string]any{"id": "1", "role_arn": roleISO}, nil, "aws-iso"},
		{"role_chain last hop", map[string]any{"id": "1", "role_chain": []string{roleAWS, roleGov}}, nil, "aws-us-gov"},
		{"role_chain beats role_arn", map[string]any{"id": "1", "role_arn": roleAWS, "role_chain": []string{roleEUSC}}, nil, "aws-eusc"},
		{"account regions", map[string]any{"id": "1", "regions": []string{"cn-northwest-1"}}, nil, "aws-cn"},
		{"account regions beat default_regions", map[string]any{"id": "1", "regions": []string{"eusc-de-east-1"}}, map[string]any{"default_regions": []string{"us-east-1"}}, "aws-eusc"},
		{"default_regions", map[string]any{"id": "1"}, map[string]any{"default_regions": []string{"us-gov-east-1"}}, "aws-us-gov"},
		{"all and unknown regions say nothing", map[string]any{"id": "1", "regions": []string{"all", "moon-1", "us-isob-east-1"}}, nil, "aws-iso-b"},
		{"nothing configured", map[string]any{"id": "1"}, nil, "aws"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			section := accountsSection(c.account)
			for k, v := range c.section {
				section[k] = v
			}
			setAWSConfig(t, section)
			got := loadOne(t, &fakeBases{}, nil, "")
			if got.Partition != c.want {
				t.Errorf("Partition = %q; want %q", got.Partition, c.want)
			}
		})
	}
}

// TestLoadAccounts_RegionOverrideIsEvidence pins that --regions, which
// replaces every configured region list, is also what the partition is read
// from.
func TestLoadAccounts_RegionOverrideIsEvidence(t *testing.T) {
	setAWSConfig(t, accountsSection(map[string]any{"id": "1", "regions": []string{"us-east-1"}}))
	got := loadOne(t, &fakeBases{}, []string{"cn-north-1"}, "")
	if got.Partition != "aws-cn" {
		t.Errorf("Partition = %q; want aws-cn from --regions", got.Partition)
	}
	if !slices.Equal(got.Regions, []string{"cn-north-1"}) {
		t.Errorf("Regions = %v; want [cn-north-1]", got.Regions)
	}
}

// TestLoadAccounts_ImplicitDefaultRegionIsPartitionHome pins that an account
// with no configured region scans its partition's home region, us-east-1 for
// aws, and that the default is not partition evidence.
func TestLoadAccounts_ImplicitDefaultRegionIsPartitionHome(t *testing.T) {
	for partition, home := range map[string]string{"aws": "us-east-1", "aws-cn": "cn-northwest-1", "aws-us-gov": "us-gov-west-1"} {
		t.Run(partition, func(t *testing.T) {
			setAWSConfig(t, accountsSection(map[string]any{"id": "1", "partition": partition}))
			got := loadOne(t, &fakeBases{}, nil, "")
			if !slices.Equal(got.Regions, []string{home}) {
				t.Errorf("Regions = %v; want [%s]", got.Regions, home)
			}
			if got.Partition != partition {
				t.Errorf("Partition = %q; want %s (the implicit default region is not evidence)", got.Partition, partition)
			}
		})
	}
}

// TestLoadAccounts_STSCallsStayInPartition pins that, with no region in the
// profile or environment, STS is called in the account partition's home: a
// GovCloud credential sent to the commercial us-east-1 would be refused.
func TestLoadAccounts_STSCallsStayInPartition(t *testing.T) {
	cases := []struct {
		name         string
		section      map[string]any
		callerARN    string
		roleOverride string
		want         string
	}{
		{"auto-detect from gov regions", map[string]any{"default_regions": []string{"us-gov-east-1"}}, callerGov, "", "us-gov-west-1"},
		{"gov role override", map[string]any{}, callerGov, roleGov, "us-gov-west-1"},
		{"gov config role", accountsSection(map[string]any{"id": "1", "role_arn": roleGov}), "", "", "us-gov-west-1"},
		{"cn config role", accountsSection(map[string]any{"id": "1", "role_arn": roleCN}), "", "", "cn-northwest-1"},
		{"commercial config role", accountsSection(map[string]any{"id": "1", "role_arn": roleAWS}), "", "", "us-east-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setAWSConfig(t, c.section)
			f := &fakeBases{callerARN: c.callerARN, regions: map[string]string{"": ""}}
			acct := loadOne(t, f, nil, c.roleOverride)
			// Config-file accounts assume their role lazily, on first use.
			if _, err := acct.cfg.Credentials.Retrieve(context.Background()); err != nil {
				t.Fatalf("Retrieve: %v", err)
			}
			if got := f.calls.distinct(); !slices.Equal(got, []string{c.want}) {
				t.Errorf("STS called in %v; want only [%s]", got, c.want)
			}
		})
	}
}

// TestLoadAccounts_AutoDetectReadsCallerARN covers the STS rung: with no
// accounts configured, the caller ARN names the partition.
func TestLoadAccounts_AutoDetectReadsCallerARN(t *testing.T) {
	setAWSConfig(t, map[string]any{})
	got := loadOne(t, &fakeBases{callerARN: callerGov}, nil, "")
	if got.ID != "222222222222" || got.Partition != "aws-us-gov" {
		t.Errorf("account = (%q, %q); want (222222222222, aws-us-gov)", got.ID, got.Partition)
	}
}

// A failed auto-detect sts:GetCallerIdentity is a *callerIdentityError naming
// the region the call went to, so the partition hint can be shown.
func TestLoadAccounts_AutoDetectCallerIdentityFailure(t *testing.T) {
	setAWSConfig(t, map[string]any{})
	_, err := loadAccounts(context.Background(), (&fakeBases{regions: map[string]string{"": ""}}).load, "", nil, "", "", "")
	var cie *callerIdentityError
	if !errors.As(err, &cie) {
		t.Fatalf("loadAccounts err = %v (%T); want *callerIdentityError", err, err)
	}
	if cie.Region != "us-east-1" || cie.Cause == nil {
		t.Errorf("callerIdentityError = {Region: %q, Cause: %v}; want Region us-east-1 and the STS cause", cie.Region, cie.Cause)
	}
}

// Auto-detect checks the profile region against the other evidence before its
// STS call: a GovCloud profile region with a commercial --regions conflicts
// there, and no sts:GetCallerIdentity is sent.
func TestLoadAccounts_AutoDetectConflictFailsBeforeSTS(t *testing.T) {
	setAWSConfig(t, map[string]any{})
	f := &fakeBases{regions: map[string]string{"": "us-gov-east-1"}}
	_, err := loadAccounts(context.Background(), f.load, "", []string{"us-east-1"}, "", "", "")
	var conflict *partitionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("loadAccounts err = %v (%T); want *partitionConflictError", err, err)
	}
	if got := f.calls.distinct(); len(got) != 0 {
		t.Errorf("STS called in %v; want no call", got)
	}
}

// TestLoadAccounts_RoleOverride covers --role-arn: its ARN is the role rung,
// and config-file accounts and their partitions are ignored.
func TestLoadAccounts_RoleOverride(t *testing.T) {
	setAWSConfig(t, accountsSection(map[string]any{"id": "9", "partition": "aws"}))
	got := loadOne(t, &fakeBases{callerARN: callerGov}, nil, roleGov)
	if got.ID != "222222222222" || got.Partition != "aws-us-gov" {
		t.Errorf("account = (%q, %q); want (222222222222, aws-us-gov)", got.ID, got.Partition)
	}
}

// TestLoadAccounts_CallerARNIsRead pins that both STS paths feed the caller
// ARN into the derivation: one naming no known partition fails the load.
func TestLoadAccounts_CallerARNIsRead(t *testing.T) {
	const callerMars = "arn:aws-mars:sts::222222222222:assumed-role/Scan/s"
	for _, roleOverride := range []string{"", roleAWS} {
		t.Run("role override "+roleOverride, func(t *testing.T) {
			setAWSConfig(t, map[string]any{})
			_, err := loadAccounts(context.Background(), (&fakeBases{callerARN: callerMars}).load, "", nil, roleOverride, "", "")
			var unknown *unknownPartitionError
			if !errors.As(err, &unknown) || unknown.Source != sourceCaller {
				t.Fatalf("loadAccounts error = %v; want *unknownPartitionError from %s", err, sourceCaller)
			}
		})
	}
}

// TestLoadAccounts_RoleOverrideRegionConflict pins that the override path
// checks its regions like the config path does.
func TestLoadAccounts_RoleOverrideRegionConflict(t *testing.T) {
	setAWSConfig(t, map[string]any{"default_regions": []string{"cn-north-1", "us-east-1"}})
	_, err := loadAccounts(context.Background(), (&fakeBases{callerARN: callerGov}).load, "", nil, roleGov, "", "")
	var conflict *partitionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("loadAccounts error = %v; want *partitionConflictError", err)
	}
	if conflict.First.Source != sourceRegions || conflict.Second.Source != sourceRegions {
		t.Errorf("conflict sources = (%s, %s); want both %s", conflict.First.Source, conflict.Second.Source, sourceRegions)
	}
}

// TestLoadAccounts_PartitionConflict pins that every input naming a partition
// must agree, and that the conflict reports the higher-precedence input
// first. That order is the only place precedence is still observable, since
// agreeing inputs give the same answer in any order.
func TestLoadAccounts_PartitionConflict(t *testing.T) {
	type want struct {
		partition string
		source    partitionSource
	}
	cases := []struct {
		name          string
		section       map[string]any
		callerARN     string
		roleOverride  string
		first, second want
	}{
		{
			"explicit vs role_arn", accountsSection(map[string]any{"id": "1", "partition": "aws-cn", "role_arn": roleAWS}), "", "",
			want{"aws-cn", sourceExplicit},
			want{"aws", sourceRoleARN},
		},
		{
			"explicit vs role_chain", accountsSection(map[string]any{"id": "1", "partition": "aws", "role_chain": []string{roleCN}}), "", "",
			want{"aws", sourceExplicit},
			want{"aws-cn", sourceRoleChain},
		},
		{
			"explicit vs regions", accountsSection(map[string]any{"id": "1", "partition": "aws-us-gov", "regions": []string{"us-east-1"}}), "", "",
			want{"aws-us-gov", sourceExplicit},
			want{"aws", sourceRegions},
		},
		// The role rung agrees, so only the lower regions rung can raise it.
		{
			"explicit vs regions below an agreeing role", accountsSection(map[string]any{"id": "1", "partition": "aws-cn", "role_arn": roleCN, "regions": []string{"us-east-1"}}), "", "",
			want{"aws-cn", sourceExplicit},
			want{"aws", sourceRegions},
		},
		{
			"role_arn vs regions", accountsSection(map[string]any{"id": "1", "role_arn": roleCN, "regions": []string{"us-east-1"}}), "", "",
			want{"aws-cn", sourceRoleARN},
			want{"aws", sourceRegions},
		},
		{
			"--role-arn vs caller",
			map[string]any{},
			"arn:aws-cn:sts::222222222222:assumed-role/Scan/s", roleGov,
			want{"aws-us-gov", sourceRoleOverride},
			want{"aws-cn", sourceCaller},
		},
		{
			"caller vs regions",
			map[string]any{"default_regions": []string{"us-east-1"}},
			callerGov, "",
			want{"aws-us-gov", sourceCaller},
			want{"aws", sourceRegions},
		},
		{
			"regions span partitions", accountsSection(map[string]any{"id": "1", "regions": []string{"us-east-1", "cn-north-1"}}), "", "",
			want{"aws", sourceRegions},
			want{"aws-cn", sourceRegions},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setAWSConfig(t, c.section)
			_, err := loadAccounts(context.Background(), (&fakeBases{callerARN: c.callerARN}).load, "", nil, c.roleOverride, "", "")
			var conflict *partitionConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("loadAccounts error = %v; want *partitionConflictError", err)
			}
			got := [2]want{{conflict.First.Partition, conflict.First.Source}, {conflict.Second.Partition, conflict.Second.Source}}
			if got != [2]want{c.first, c.second} {
				t.Errorf("conflict = %v vs %v; want %v vs %v", got[0], got[1], c.first, c.second)
			}
		})
	}
}

// TestLoadAccounts_InvalidRoleARN pins that a role that is not an ARN is
// reported as such, not as an unknown partition.
func TestLoadAccounts_InvalidRoleARN(t *testing.T) {
	for _, account := range []map[string]any{
		{"id": "1", "role_arn": "Scan"},
		{"id": "1", "role_chain": []string{roleAWS, "Scan"}},
	} {
		setAWSConfig(t, accountsSection(account))
		_, err := loadAccounts(context.Background(), (&fakeBases{}).load, "", nil, "", "", "")
		var invalid *invalidARNError
		if !errors.As(err, &invalid) {
			t.Errorf("loadAccounts(%v) error = %v; want *invalidARNError", account, err)
		}
	}
}

func TestLoadAccounts_UnknownPartition(t *testing.T) {
	cases := []struct {
		name       string
		account    map[string]any
		wantSource partitionSource
	}{
		{"explicit", map[string]any{"id": "1", "partition": "aws-mars"}, sourceExplicit},
		{"role_arn partition", map[string]any{"id": "1", "role_arn": roleMars}, sourceRoleARN},
		{"role_chain last hop", map[string]any{"id": "1", "role_chain": []string{roleAWS, roleMars}}, sourceRoleChain},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setAWSConfig(t, accountsSection(c.account))
			_, err := loadAccounts(context.Background(), (&fakeBases{}).load, "", nil, "", "", "")
			var unknown *unknownPartitionError
			if !errors.As(err, &unknown) {
				t.Fatalf("loadAccounts error = %v; want *unknownPartitionError", err)
			}
			if unknown.Source != c.wantSource {
				t.Errorf("unknown partition source = %s; want %s", unknown.Source, c.wantSource)
			}
		})
	}
}

// TestLoadAccounts_SharedCredentialsAcrossPartitions pins that two accounts in
// different partitions cannot resolve to the same base config, and that a
// profile-less account shares the --profile base.
func TestLoadAccounts_SharedCredentialsAcrossPartitions(t *testing.T) {
	cases := []struct {
		name        string
		flagProfile string
		a, b        map[string]any
		wantProfile string
	}{
		{"both default chain", "", map[string]any{"id": "1"}, map[string]any{"id": "2", "partition": "aws-cn"}, ""},
		{"same profile", "", map[string]any{"id": "1", "profile": "p"}, map[string]any{"id": "2", "profile": "p", "role_arn": roleGov}, "p"},
		{"profile-less account uses --profile", "ops", map[string]any{"id": "1"}, map[string]any{"id": "2", "profile": "ops", "regions": []string{"cn-north-1"}}, "ops"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setAWSConfig(t, accountsSection(c.a, c.b))
			_, err := loadAccounts(context.Background(), (&fakeBases{}).load, c.flagProfile, nil, "", "", "")
			var shared *sharedCredentialsError
			if !errors.As(err, &shared) {
				t.Fatalf("loadAccounts error = %v; want *sharedCredentialsError", err)
			}
			if shared.Profile != c.wantProfile || shared.FirstAccount != "1" || shared.SecondAccount != "2" ||
				shared.FirstPartition != "aws" || shared.SecondPartition == "aws" {
				t.Errorf("shared = %+v; want profile %q, account 1 (aws) vs account 2 (non-aws)", shared, c.wantProfile)
			}
		})
	}
}

// TestLoadAccounts_PerAccountProfile pins that accounts in different
// partitions load when each names its own profile, that each account's config
// comes from its own profile, and that each profile is loaded once.
func TestLoadAccounts_PerAccountProfile(t *testing.T) {
	setAWSConfig(t, accountsSection(
		map[string]any{"id": "1", "profile": "commercial"},
		map[string]any{"id": "2", "profile": "china", "regions": []string{"cn-north-1"}},
		map[string]any{"id": "3", "profile": "commercial"},
		map[string]any{"id": "4"},
	))
	f := &fakeBases{}
	accts, err := loadAccounts(context.Background(), f.load, "flag", nil, "", "", "")
	if err != nil {
		t.Fatalf("loadAccounts: %v", err)
	}
	want := []struct{ partition, profile string }{{"aws", "commercial"}, {"aws-cn", "china"}, {"aws", "commercial"}, {"aws", "flag"}}
	if len(accts) != len(want) {
		t.Fatalf("loadAccounts returned %d accounts; want %d", len(accts), len(want))
	}
	for i, w := range want {
		if accts[i].Partition != w.partition || accts[i].cfg.Region != baseRegionFor(w.profile) {
			t.Errorf("account %s = (partition %q, base %q); want (%q, %q)",
				accts[i].ID, accts[i].Partition, accts[i].cfg.Region, w.partition, baseRegionFor(w.profile))
		}
	}
	if !slices.Equal(f.loaded, []string{"commercial", "china", "flag"}) {
		t.Errorf("profiles loaded = %v; want each distinct profile once: [commercial china flag]", f.loaded)
	}
}

// TestLoadAccounts_EmulatorPartition pins emulator mode: the caller ARN is
// ignored, evidence is still checked, and a partition other than aws must be
// explicit.
func TestLoadAccounts_EmulatorPartition(t *testing.T) {
	cases := []struct {
		name    string
		account map[string]any
		want    string
	}{
		{"commercial evidence", map[string]any{"id": "1", "role_arn": roleAWS, "regions": []string{"us-east-1"}}, "aws"},
		{"explicit", map[string]any{"id": "1", "partition": "aws-cn"}, "aws-cn"},
		{"explicit agrees with evidence", map[string]any{"id": "1", "partition": "aws-cn", "role_arn": roleCN, "regions": []string{"cn-north-1"}}, "aws-cn"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setAWSConfig(t, accountsSection(c.account))
			t.Setenv("AWS_ENDPOINT_URL", "http://localhost:4566")
			got := loadOne(t, &fakeBases{}, nil, "")
			if got.Partition != c.want {
				t.Errorf("Partition = %q; want %q", got.Partition, c.want)
			}
		})
	}

	for name, account := range map[string]map[string]any{
		"role_arn":   {"id": "1", "role_arn": roleCN},
		"regions":    {"id": "1", "regions": []string{"us-gov-west-1"}},
		"role_chain": {"id": "1", "role_chain": []string{roleAWS, roleEUSC}},
	} {
		t.Run("non-aws "+name+" needs an explicit partition", func(t *testing.T) {
			setAWSConfig(t, accountsSection(account))
			t.Setenv("AWS_ENDPOINT_URL", "http://localhost:4566")
			_, err := loadAccounts(context.Background(), (&fakeBases{}).load, "", nil, "", "", "")
			var emu *emulatorPartitionError
			if !errors.As(err, &emu) || emu.Evidence.Partition == "aws" {
				t.Fatalf("loadAccounts error = %v; want *emulatorPartitionError naming a non-aws partition", err)
			}
		})
	}

	t.Run("explicit still checked against evidence", func(t *testing.T) {
		setAWSConfig(t, accountsSection(map[string]any{"id": "1", "partition": "aws-us-gov", "regions": []string{"us-east-1"}}))
		t.Setenv("AWS_ENDPOINT_URL", "http://localhost:4566")
		_, err := loadAccounts(context.Background(), (&fakeBases{}).load, "", nil, "", "", "")
		var conflict *partitionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("loadAccounts error = %v; want *partitionConflictError", err)
		}
	})

	t.Run("caller ARN is not evidence", func(t *testing.T) {
		setAWSConfig(t, map[string]any{})
		t.Setenv("AWS_ENDPOINT_URL", "http://localhost:4566")
		got := loadOne(t, &fakeBases{callerARN: callerGov}, nil, "")
		if got.ID != "222222222222" || got.Partition != "aws" {
			t.Errorf("account = (%q, %q); want (222222222222, aws)", got.ID, got.Partition)
		}
	})

	t.Run("unknown explicit still fails", func(t *testing.T) {
		setAWSConfig(t, accountsSection(map[string]any{"id": "1", "partition": "aws-mars"}))
		t.Setenv("AWS_ENDPOINT_URL", "http://localhost:4566")
		_, err := loadAccounts(context.Background(), (&fakeBases{}).load, "", nil, "", "", "")
		var unknown *unknownPartitionError
		if !errors.As(err, &unknown) {
			t.Fatalf("loadAccounts error = %v; want *unknownPartitionError", err)
		}
	})

	t.Run("auto-detect skips STS", func(t *testing.T) {
		setAWSConfig(t, map[string]any{"default_regions": []string{"us-east-1"}})
		t.Setenv("AWS_ENDPOINT_URL", "http://localhost:4566")
		t.Setenv("DISCO_CLOUD_ACCOUNT_ID", "333333333333")
		got := loadOne(t, &fakeBases{}, nil, "")
		if got.ID != "333333333333" || got.Partition != "aws" {
			t.Errorf("account = (%q, %q); want (333333333333, aws)", got.ID, got.Partition)
		}
	})
}

func TestAccountPartition_DefaultsToAWS(t *testing.T) {
	if got := (&account{}).partition(); got != "aws" {
		t.Errorf("account{}.partition() = %q; want aws", got)
	}
	if got := (&account{Partition: "aws-cn"}).partition(); got != "aws-cn" {
		t.Errorf("account{Partition: aws-cn}.partition() = %q; want aws-cn", got)
	}
}

// TestLoadAccounts_ProfileCredentials pins that an account's assumed-role
// credentials are minted by its own profile's STS client, for both role_arn
// and role_chain. AssumeRole runs only when credentials are retrieved, so the
// test retrieves them; the stub short-circuits before signing, so a chain
// calls only its last hop. That hop carries no SourceIdentity (only a chain's
// entry hop does), which tells a chain from a single assume of its last role.
func TestLoadAccounts_ProfileCredentials(t *testing.T) {
	setAWSConfig(t, accountsSection(
		map[string]any{"id": "1", "profile": "single", "role_arn": roleAWS},
		map[string]any{"id": "2", "profile": "chain", "role_chain": []string{roleAWS, roleCN}, "partition": "aws-cn"},
	))
	f := &fakeBases{}
	accts, err := loadAccounts(context.Background(), f.load, "flag", nil, "", "", "disco-test")
	if err != nil {
		t.Fatalf("loadAccounts: %v", err)
	}
	for i := range accts {
		if _, err := accts[i].cfg.Credentials.Retrieve(context.Background()); err != nil {
			t.Fatalf("account %s: retrieve credentials: %v", accts[i].ID, err)
		}
	}
	want := []string{"single " + roleAWS + " disco-test", "chain " + roleCN + " "}
	if !slices.Equal(f.assumed, want) {
		t.Errorf("AssumeRole calls = %q; want %q", f.assumed, want)
	}
}

// TestLoadAccounts_ProfileRegion covers the lowest rung: the region the
// account's profile resolves to, never the us-east-1 fallback.
func TestLoadAccounts_ProfileRegion(t *testing.T) {
	t.Run("profile region alone names the partition", func(t *testing.T) {
		setAWSConfig(t, accountsSection(map[string]any{"id": "1", "profile": "cn"}))
		got := loadOne(t, &fakeBases{regions: map[string]string{"cn": "cn-north-1"}}, nil, "")
		if got.Partition != "aws-cn" {
			t.Errorf("Partition = %q; want aws-cn", got.Partition)
		}
	})

	t.Run("conflicts with configured regions", func(t *testing.T) {
		setAWSConfig(t, accountsSection(map[string]any{"id": "1", "profile": "cn", "regions": []string{"us-east-1"}}))
		_, err := loadAccounts(context.Background(), (&fakeBases{regions: map[string]string{"cn": "cn-north-1"}}).load, "", nil, "", "", "")
		var conflict *partitionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("loadAccounts error = %v; want *partitionConflictError", err)
		}
		if conflict.First.Source != sourceRegions || conflict.Second.Source != sourceProfileRegion || conflict.Second.Partition != "aws-cn" {
			t.Errorf("conflict = %v vs %v; want regions first, then aws-cn from %s", conflict.First, conflict.Second, sourceProfileRegion)
		}
	})

	t.Run("fallback region is the partition home", func(t *testing.T) {
		// A profile naming no region gets the partition's home region,
		// which is not evidence either.
		setAWSConfig(t, accountsSection(map[string]any{"id": "1", "profile": "none", "partition": "aws-us-gov"}))
		got := loadOne(t, &fakeBases{regions: map[string]string{"none": ""}}, nil, "")
		if got.Partition != "aws-us-gov" || got.cfg.Region != "us-gov-west-1" {
			t.Errorf("account = (partition %q, cfg region %q); want (aws-us-gov, us-gov-west-1 fallback)", got.Partition, got.cfg.Region)
		}
	})

	t.Run("commercial fallback stays us-east-1", func(t *testing.T) {
		setAWSConfig(t, accountsSection(map[string]any{"id": "1", "profile": "none"}))
		got := loadOne(t, &fakeBases{regions: map[string]string{"none": ""}}, nil, "")
		if got.Partition != "aws" || got.cfg.Region != "us-east-1" {
			t.Errorf("account = (partition %q, cfg region %q); want (aws, us-east-1 fallback)", got.Partition, got.cfg.Region)
		}
	})

	t.Run("no profile region falls to aws", func(t *testing.T) {
		setAWSConfig(t, accountsSection(map[string]any{"id": "1"}))
		got := loadOne(t, &fakeBases{regions: map[string]string{"": ""}}, nil, "")
		if got.Partition != "aws" {
			t.Errorf("Partition = %q; want aws", got.Partition)
		}
	})

	t.Run("each account reads its own profile", func(t *testing.T) {
		setAWSConfig(t, accountsSection(
			map[string]any{"id": "1", "profile": "cn"},
			map[string]any{"id": "2", "profile": "gov"},
		))
		f := &fakeBases{regions: map[string]string{"cn": "cn-northwest-1", "gov": "us-gov-west-1"}}
		accts, err := loadAccounts(context.Background(), f.load, "", nil, "", "", "")
		if err != nil {
			t.Fatalf("loadAccounts: %v", err)
		}
		if len(accts) != 2 || accts[0].Partition != "aws-cn" || accts[1].Partition != "aws-us-gov" {
			t.Errorf("partitions = %v; want [aws-cn aws-us-gov]", []string{accts[0].Partition, accts[1].Partition})
		}
	})

	t.Run("override path reads the profile region", func(t *testing.T) {
		setAWSConfig(t, map[string]any{})
		_, err := loadAccounts(context.Background(), (&fakeBases{callerARN: callerGov, regions: map[string]string{"": "cn-north-1"}}).load, "", nil, roleGov, "", "")
		var conflict *partitionConflictError
		if !errors.As(err, &conflict) || conflict.Second.Source != sourceProfileRegion {
			t.Fatalf("loadAccounts error = %v; want *partitionConflictError against %s", err, sourceProfileRegion)
		}
	})

	t.Run("emulator ignores it", func(t *testing.T) {
		setAWSConfig(t, accountsSection(map[string]any{"id": "1", "profile": "cn"}))
		t.Setenv("AWS_ENDPOINT_URL", "http://localhost:4566")
		got := loadOne(t, &fakeBases{regions: map[string]string{"cn": "cn-north-1"}}, nil, "")
		if got.Partition != "aws" {
			t.Errorf("Partition = %q; want aws", got.Partition)
		}
	})

	t.Run("emulator explicit partition ignores it", func(t *testing.T) {
		setAWSConfig(t, accountsSection(map[string]any{"id": "1", "profile": "p", "partition": "aws-cn"}))
		t.Setenv("AWS_ENDPOINT_URL", "http://localhost:4566")
		got := loadOne(t, &fakeBases{regions: map[string]string{"p": "us-east-1"}}, nil, "")
		if got.Partition != "aws-cn" {
			t.Errorf("Partition = %q; want aws-cn", got.Partition)
		}
	})

	// The SDK takes AWS_REGION, then AWS_DEFAULT_REGION, over any profile's
	// region, so a conflict must blame the variable the user can unset.
	for _, c := range []struct {
		name string
		env  map[string]string
		want partitionSource
	}{
		{"from AWS_REGION", map[string]string{"AWS_REGION": "cn-north-1"}, sourceEnvRegion},
		{"from AWS_DEFAULT_REGION", map[string]string{"AWS_DEFAULT_REGION": "cn-north-1"}, sourceEnvDefaultRegion},
		{"AWS_REGION wins over AWS_DEFAULT_REGION", map[string]string{"AWS_REGION": "us-west-2", "AWS_DEFAULT_REGION": "cn-north-1"}, sourceProfileRegion},
		{"profile, env names another region", map[string]string{"AWS_REGION": "us-west-2"}, sourceProfileRegion},
	} {
		t.Run("source label "+c.name, func(t *testing.T) {
			setAWSConfig(t, accountsSection(map[string]any{"id": "1", "regions": []string{"us-east-1"}}))
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			_, err := loadAccounts(context.Background(), (&fakeBases{regions: map[string]string{"": "cn-north-1"}}).load, "", nil, "", "", "")
			var conflict *partitionConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("loadAccounts error = %v; want *partitionConflictError", err)
			}
			if conflict.Second.Source != c.want {
				t.Errorf("conflict source = %s; want %s", conflict.Second.Source, c.want)
			}
		})
	}
}
