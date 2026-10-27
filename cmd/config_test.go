package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// The template's AWS account examples are meant to be uncommented in place, so
// doing that must give YAML that parses into the account list.
func TestBoilerplateConfig_AWSExamplesUncommentInPlace(t *testing.T) {
	start := strings.Index(boilerplateConfig, "\naws:\n")
	end := strings.Index(boilerplateConfig, "\ngcp:\n")
	if start < 0 || end < start {
		t.Fatal("template has no aws: section followed by gcp:")
	}
	// An entry line is "  # - key: value" or "  #   key: value".
	entry := regexp.MustCompile(`(?m)^  # (- |  )(\w+:)`)
	uncommented := entry.ReplaceAllString(boilerplateConfig[start:end], "  $1$2")

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(uncommented)); err != nil {
		t.Fatalf("uncommented template does not parse: %v\n%s", err, uncommented)
	}
	var cfg struct {
		Accounts []struct {
			ID        string `mapstructure:"id"`
			Profile   string `mapstructure:"profile"`
			Partition string `mapstructure:"partition"`
		} `mapstructure:"accounts"`
	}
	if err := v.UnmarshalKey("aws", &cfg); err != nil {
		t.Fatalf("UnmarshalKey(aws): %v", err)
	}
	if len(cfg.Accounts) != 2 {
		t.Fatalf("accounts = %+v, want the 2 examples", cfg.Accounts)
	}
	if gov := cfg.Accounts[1]; gov.Profile != "govcloud" || gov.Partition != "aws-us-gov" {
		t.Errorf("GovCloud example = %+v, want profile govcloud, partition aws-us-gov", gov)
	}
}
