package disco

import rego.v1

# AWS Well-Architected Framework — Security pillar, SEC 5: Control traffic at
# all layers. Publicly accessible RDS instances expose the database engine
# directly to the internet — relies on credential strength alone for the
# control boundary.

deny contains f if {
	input.type == "aws:rds:db-instance"
	input.attributes.PubliclyAccessible == true
	f := {
		"id": "waf-sec-rds-publicly-accessible",
		"severity": "high",
		"category": "aws-waf",
		"message": sprintf("RDS instance %q is publicly accessible", [input.name]),
		"remediation": "Set PubliclyAccessible=false; reach the database through a bastion or VPN.",
		"ref_url": "https://docs.aws.amazon.com/wellarchitected/latest/security-pillar/sec_network_protection_layered.html",
	}
}
