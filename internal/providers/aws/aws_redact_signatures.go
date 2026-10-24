package aws

import (
	"regexp"
	"strings"

	"github.com/icearp/disco-cli/internal/redact"
)

func init() {
	redact.RegisterSignatures(redact.Signatures{
		Credential: []func(string) bool{accessKeyRE.MatchString, oracleJDBCUserinfoRE.MatchString},
		Reference:  []func(string) bool{isARNOrSecretRef},
		ResourceID: []func(string) bool{ec2ResourceIDRE.MatchString},
	})
}

// ec2IDPrefixes are the EC2 id prefixes kept as resource ids.
var ec2IDPrefixes = []string{
	"sg", "subnet", "vpc", "i", "eni", "vol", "snap", "ami", "eipalloc", "igw",
	"nat", "rtb", "acl", "vpce", "tgw", "tgw-attach", "pcx", "lt",
}

// redactARNPattern is an unanchored ARN: partition, an empty or real-looking
// region, an empty, 12-digit or "aws" account, and a resource of the
// characters ARNs use, with the Lambda ":$LATEST" qualifier allowed only as
// a suffix.
const redactARNPattern = `arn:aws(?:-cn|-us-gov|-eusc|-iso(?:-[a-z])?)?:[a-z0-9-]+:(?:[a-z]{2,4}(?:-[a-z]+)+-\d)?:(?:\d{12}|aws)?:[A-Za-z0-9_+=,.@/:*-]+(?::\$LATEST)?`

var (
	// No word boundaries: a key glued to other text ("xAKIA…") is still a key.
	accessKeyRE = regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`)
	// An Oracle JDBC URL with a user and password, "jdbc:oracle:thin:user/pass@host"
	// or with the password quoted, `user/"p w"@host` (a Glue connection's
	// JDBC_CONNECTION_URL): it puts its credentials before the @ with no "//",
	// so the core userinfo-URL shape misses it.
	oracleJDBCUserinfoRE = regexp.MustCompile(`(?i)jdbc:oracle:[a-z0-9]+:[^/@:\s]+/(?:"[^"]*"|[^@\s"]+)@`)
	redactARNRE          = regexp.MustCompile(`^` + redactARNPattern + `$`)
	// A CloudFormation dynamic reference to an SSM parameter by name or ARN
	// (optionally versioned) or to a Secrets Manager secret, or the Parameter
	// Store path that references a Secrets Manager secret.
	dynamicRefRE = regexp.MustCompile(`^(?:\{\{resolve:(?:ssm|ssm-secure):(?:` + redactARNPattern + `|[A-Za-z0-9_./-]+)(?::\d+)?\}\}|\{\{resolve:secretsmanager:[A-Za-z0-9/_+=.@:-]+\}\}|/aws/reference/secretsmanager/[A-Za-z0-9/_+=.@:-]+)$`)
	// An EC2 id: a listed prefix and 8 or 17 lowercase hex digits.
	ec2ResourceIDRE = regexp.MustCompile(`^(?:` + strings.Join(ec2IDPrefixes, "|") + `)-(?:[0-9a-f]{8}|[0-9a-f]{17})$`)
)

// maxARNLen is the longest ARN IAM accepts; Go's regexp caps a repeat
// count at 1000, so the length is checked outside redactARNRE.
const maxARNLen = 2048

// isARNOrSecretRef reports whether s is an ARN or a reference to an SSM
// parameter or a Secrets Manager secret: it names where a secret lives
// without holding it.
func isARNOrSecretRef(s string) bool {
	return (len(s) <= maxARNLen && redactARNRE.MatchString(s)) || dynamicRefRE.MatchString(s)
}
