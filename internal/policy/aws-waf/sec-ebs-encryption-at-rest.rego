package disco

import rego.v1

# AWS Well-Architected Framework — Security pillar, SEC 8: Protect data at rest.
# Unencrypted EBS volumes violate the baseline data-at-rest control: any host
# that gains raw block access (forensic copy, snapshot share, account compromise)
# reads cleartext.

deny contains f if {
	input.type == "aws:ec2:volume"
	input.attributes.Encrypted == false
	f := {
		"id": "waf-sec-ebs-encryption-at-rest",
		"severity": "high",
		"category": "aws-waf",
		"message": sprintf("EBS volume %q (%s) is not encrypted at rest", [input.name, input.native_id]),
		"remediation": "Snapshot the volume, restore as encrypted with a CMK, swap the attachment.",
		"ref_url": "https://docs.aws.amazon.com/wellarchitected/latest/security-pillar/sec_protect_data_rest_encrypt.html",
		"tags": {
			"waf_pillar": "security",
			"waf_qid": "SEC 8",
		},
	}
}
