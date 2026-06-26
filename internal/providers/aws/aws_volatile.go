package aws

import "codeberg.org/icearp/disco/internal/volatile"

// Provider-declared volatile-field rules. Each path names a field disco drops
// from AttributesJSON before write because the AWS API rotates it on every read
// independent of any real change — leaving it in version-splits an unchanged
// resource on every scan. Centralised here (like aws_redact.go) so a reviewer
// can audit "what does aws drop as volatile?" in one read.
//
// Path syntax: dot-separated literal keys (no wildcards).
func init() {
	volatile.Register(volatile.TypeRules{
		// CloudWatch Logs UploadSequenceToken is deprecated (PutLogEvents no
		// longer needs sequence tokens) and DescribeLogStreams returns a fresh
		// value on every call regardless of log activity — so all log streams
		// would version-split on every scan. No resolver/rule reads it.
		Type:  TypeLogsLogStream,
		Paths: []string{"UploadSequenceToken"},
	})
}
