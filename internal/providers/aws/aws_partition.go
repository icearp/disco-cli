package aws

import (
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/arn"

	"github.com/icearp/disco-cli/internal/providers/aws/awsregions"
)

// partitionSource names where an account's partition came from. Errors carry
// it so a caller can tell which setting to change.
type partitionSource string

const (
	sourceExplicit     partitionSource = "partition"
	sourceRoleARN      partitionSource = "role_arn"
	sourceRoleChain    partitionSource = "role_chain"
	sourceRoleOverride partitionSource = "--role-arn"
	sourceCaller       partitionSource = "sts:GetCallerIdentity"
	sourceRegions      partitionSource = "regions"
	// sourceProfileRegion is the region the account's profile set in its base
	// config (not the us-east-1 fallback). The two env sources below are the
	// same rung when the SDK took the region from the environment instead,
	// which overrides every profile's region.
	sourceProfileRegion    partitionSource = "profile region"
	sourceEnvRegion        partitionSource = "AWS_REGION"
	sourceEnvDefaultRegion partitionSource = "AWS_DEFAULT_REGION"
)

// baseRegionSource names where the SDK got a base config's region. It mirrors
// the SDK's environment lookup (AWS_REGION, then AWS_DEFAULT_REGION); a
// region matching neither came from the profile.
func baseRegionSource(region string) partitionSource {
	for _, env := range []partitionSource{sourceEnvRegion, sourceEnvDefaultRegion} {
		if v := os.Getenv(string(env)); v != "" {
			if v == region {
				return env
			}
			// The SDK stops at the first variable that is set.
			break
		}
	}
	return sourceProfileRegion
}

// partitionEvidence is one partition value and the setting it was read from.
type partitionEvidence struct {
	Partition string
	Source    partitionSource
	Value     string // the ARN, region or partition string the value was read from
}

func (e partitionEvidence) String() string {
	return fmt.Sprintf("%q (from %s %s)", e.Partition, e.Source, e.Value)
}

// partitionInputs is everything an account's partition can be read from.
// Empty fields contribute nothing.
type partitionInputs struct {
	explicit   string
	roleARN    string // role_arn, the last role_chain hop, or --role-arn
	roleSource partitionSource
	callerARN  string // the STS caller ARN, where loadAccounts already calls STS
	// regions are the configured regions only. The implicit us-east-1 default
	// is not evidence: it is what disco picks when nothing names a region.
	regions []string
	// profileRegion is the base config's region before the us-east-1
	// fallback; "" contributes nothing.
	profileRegion string
}

// unknownPartitionError reports a partition, or an ARN, that names no
// partition the pinned SDK knows.
type unknownPartitionError struct {
	Account string
	Source  partitionSource
	Value   string
}

func (e *unknownPartitionError) Error() string {
	ids := make([]string, len(awsregions.Partitions))
	for i, p := range awsregions.Partitions {
		ids[i] = p.ID
	}
	return fmt.Sprintf("aws account %s: %s %q names no known AWS partition; use an ARN or partition from: %s",
		e.Account, e.Source, e.Value, strings.Join(ids, ", "))
}

// invalidARNError reports a role or caller ARN that is not an ARN at all, so
// it names no partition and could not be assumed either.
type invalidARNError struct {
	Account string
	Source  partitionSource
	Value   string
}

func (e *invalidARNError) Error() string {
	return fmt.Sprintf("aws account %s: %s %q is not an ARN; give the full role ARN (arn:<partition>:iam::<account>:role/<name>)",
		e.Account, e.Source, e.Value)
}

// emulatorPartitionError reports emulator-mode evidence for a partition other
// than aws with no explicit partition. Emulator mode keeps aws unless told
// otherwise, so the evidence alone would be silently overridden.
type emulatorPartitionError struct {
	Account  string
	Evidence partitionEvidence
}

func (e *emulatorPartitionError) Error() string {
	return fmt.Sprintf("aws account %s: AWS_ENDPOINT_URL is set and partition %s is not aws; "+
		"set `partition: %s` on the account to scan the emulator as that partition",
		e.Account, e.Evidence, e.Evidence.Partition)
}

// partitionConflictError reports two settings of one account that name
// different partitions.
type partitionConflictError struct {
	Account string
	First   partitionEvidence
	Second  partitionEvidence
}

func (e *partitionConflictError) Error() string {
	return fmt.Sprintf("aws account %s: partition %s conflicts with %s; make them name the same partition",
		e.Account, e.First, e.Second)
}

// sharedCredentialsError reports accounts in different partitions that share
// one base config. AWS credentials are issued per partition and AssumeRole
// cannot cross a partition boundary, so one of the two accounts could never
// authenticate.
type sharedCredentialsError struct {
	Profile         string // "" is the default credential chain
	FirstAccount    string
	FirstPartition  string
	SecondAccount   string
	SecondPartition string
}

func (e *sharedCredentialsError) Error() string {
	shared := "the default credential chain"
	if e.Profile != "" {
		shared = fmt.Sprintf("profile %q", e.Profile)
	}
	return fmt.Sprintf("aws accounts %s (partition %s) and %s (partition %s) both use %s, "+
		"and one set of credentials works in only one partition; give each account its own `profile:`",
		e.FirstAccount, e.FirstPartition, e.SecondAccount, e.SecondPartition, shared)
}

// resolvePartition returns an account's partition from, in precedence order:
// the explicit partition, the role ARN, the STS caller ARN, the configured
// regions, the profile region. Every input that names a partition must name
// the same one; the conflict error reports the highest-precedence input first.
//
// In emulator mode the caller ARN and the profile region are ignored, and a
// partition other than aws must be set explicitly.
func resolvePartition(account string, in partitionInputs) (string, error) {
	var evidence []partitionEvidence
	if in.explicit != "" {
		if _, ok := lookupPartition(in.explicit); !ok {
			return "", &unknownPartitionError{Account: account, Source: sourceExplicit, Value: in.explicit}
		}
		evidence = append(evidence, partitionEvidence{Partition: in.explicit, Source: sourceExplicit, Value: in.explicit})
	}
	emulated := emulatorMode()
	if emulated {
		// An emulator such as LocalStack can answer STS with a commercial
		// ARN whatever partition the scan models, and the region it is
		// reached with is arbitrary (often us-east-1), so neither is
		// evidence.
		in.callerARN = ""
		in.profileRegion = ""
	}
	derived, err := derivePartitions(account, in)
	if err != nil {
		return "", err
	}
	evidence = append(evidence, derived...)

	if len(evidence) == 0 {
		// Nothing names a partition: keep aws, the partition every account
		// had before partitions were resolved and the one the implicit
		// us-east-1 default and the "all" region list belong to.
		return "aws", nil
	}
	for _, e := range evidence[1:] {
		if e.Partition != evidence[0].Partition {
			return "", &partitionConflictError{Account: account, First: evidence[0], Second: e}
		}
	}
	if emulated && in.explicit == "" && evidence[0].Partition != "aws" {
		return "", &emulatorPartitionError{Account: account, Evidence: evidence[0]}
	}
	return evidence[0].Partition, nil
}

// derivePartitions returns the partition each non-explicit input names, in
// precedence order. An ARN that names no known partition is an error: the
// SDK could not sign requests for it either, and guessing would scan the
// wrong partition.
func derivePartitions(account string, in partitionInputs) ([]partitionEvidence, error) {
	var out []partitionEvidence
	for _, arnIn := range []struct {
		value  string
		source partitionSource
	}{{in.roleARN, in.roleSource}, {in.callerARN, sourceCaller}} {
		if arnIn.value == "" {
			continue
		}
		if !arn.IsARN(arnIn.value) {
			return nil, &invalidARNError{Account: account, Source: arnIn.source, Value: arnIn.value}
		}
		p, ok := partitionOf(arnIn.value)
		if !ok {
			return nil, &unknownPartitionError{Account: account, Source: arnIn.source, Value: arnIn.value}
		}
		out = append(out, partitionEvidence{Partition: p, Source: arnIn.source, Value: arnIn.value})
	}

	region, err := regionsPartition(account, in.regions)
	if err != nil {
		return nil, err
	}
	if region != nil {
		out = append(out, *region)
	}
	if p, ok := partitionForRegion(in.profileRegion); ok {
		out = append(out, partitionEvidence{Partition: p, Source: baseRegionSource(in.profileRegion), Value: in.profileRegion})
	}
	return out, nil
}

// regionsPartition returns the one partition the configured regions belong
// to, or nil when none is recognised. A region no partition matches — the
// "all" sentinel among them — says nothing about the partition and is passed
// over; regions from two partitions are a conflict, since one account lives
// in one.
func regionsPartition(account string, regions []string) (*partitionEvidence, error) {
	var first *partitionEvidence
	for _, r := range regions {
		p, ok := partitionForRegion(r)
		if !ok {
			continue
		}
		ev := partitionEvidence{Partition: p, Source: sourceRegions, Value: r}
		if first == nil {
			first = &ev
			continue
		}
		if first.Partition != p {
			return nil, &partitionConflictError{Account: account, First: *first, Second: ev}
		}
	}
	return first, nil
}
