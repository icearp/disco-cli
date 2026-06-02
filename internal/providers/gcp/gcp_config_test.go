package gcp

import "testing"

// TestSelectCredentialMode covers the GCP credential precedence: a
// credential-config file (flag or config) wins, then the ECS/Fargate WIF env
// bridge, then a legacy service-account key file, then ADC.
func TestSelectCredentialMode(t *testing.T) {
	const aud = "//iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/p/providers/aws"
	const sa = "disco@proj.iam.gserviceaccount.com"

	cases := []struct {
		name   string
		cfg    providerCfg
		wifAud string
		wifSA  string
		want   credentialMode
	}{
		{
			name:   "credential_config_file wins over everything",
			cfg:    providerCfg{CredentialConfigFile: "/cred.json", ServiceAccountFile: "/sa.json"},
			wifAud: aud, wifSA: sa,
			want: credModeFile,
		},
		{
			name:   "wif env bridge when no cred-config file",
			cfg:    providerCfg{ServiceAccountFile: "/sa.json"},
			wifAud: aud, wifSA: sa,
			want: credModeWIFEnv,
		},
		{
			name: "service_account_file when no cred-config and no complete wif env",
			cfg:  providerCfg{ServiceAccountFile: "/sa.json"},
			// only half the WIF env contract present — must not select wif
			wifAud: aud,
			want:   credModeSAFile,
		},
		{
			name: "adc when nothing configured",
			cfg:  providerCfg{},
			want: credModeDefault,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := selectCredentialMode(tc.cfg, tc.wifAud, tc.wifSA)
			if got != tc.want {
				t.Errorf("selectCredentialMode = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSetCredentialConfigOverride verifies the scanner stores the flag value so
// loadProjects can override the config file with it.
func TestSetCredentialConfigOverride(t *testing.T) {
	var s Scanner
	s.SetCredentialConfigOverride("/tmp/wif.json")
	if s.credentialConfigFile != "/tmp/wif.json" {
		t.Errorf("credentialConfigFile = %q, want /tmp/wif.json", s.credentialConfigFile)
	}
}
