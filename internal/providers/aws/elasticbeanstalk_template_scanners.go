package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// Namespace, OptionName and ResourceName always stay. A value in a
	// namespace whose values may be secrets under names that do not say so
	// (beanstalkStrictNamespaces), or with no namespace, is kept only when
	// judged safe under its OptionName; any other value is a platform setting
	// (instance type, scaling, health check path) and is kept unless its
	// OptionName is secret-looking (aws:rds:dbinstance DBPassword) or it holds a
	// credential shape. JSON values are a credential shape, which covers the
	// Beanstalk Cluster env-variables and secrets options that share
	// aws:elasticbeanstalk:eks:environment with cpu and memory, and
	// custom-config in aws:elasticbeanstalk:eks:observability.
	registerType(restype.Descriptor{
		Type:    TypeBeanstalkConfigurationTemplate,
		Service: "elasticbeanstalk",
		Redact: []redact.Rule{{
			Path: "OptionSettings[*].Value", Mode: redact.RedactIfSensitive, KeyField: "OptionName",
			Override: &redact.SiblingOverride{Field: "Namespace", Values: beanstalkStrictNamespaces, Mode: redact.RedactUnlessSafe},
		}},
	})
}

// beanstalkStrictNamespaces are the option namespaces whose values can be
// secrets under option names that do not say so.
var beanstalkStrictNamespaces = []string{
	// Environment properties: any name, any value.
	"aws:elasticbeanstalk:application:environment",
	// Environment-variable names mapped to secret ARNs.
	"aws:elasticbeanstalk:application:environmentsecrets",
	// Free JVM command-line text and arbitrary -X options.
	"aws:elasticbeanstalk:container:tomcat:jvmoptions",
	// NodeCommand is free command-line text.
	"aws:elasticbeanstalk:container:nodejs",
	// User-defined options read by Fn::GetOptionSetting.
	"aws:elasticbeanstalk:customoption",
	// Legacy containers' template parameters; that they carry user values is
	// unverified against AWS documentation.
	"aws:cloudformation:template:parameter",
	// Notification Endpoint can be a webhook URL with its secret in the path;
	// email endpoints are redacted with it, an accepted over-redaction.
	"aws:elasticbeanstalk:sns:topics",
}

const beanstalkDescribeConfigurationSettingsOp = "elasticbeanstalk:DescribeConfigurationSettings"

// beanstalkTemplateARN builds the Service Reference ARN format
// configurationtemplate/${ApplicationName}/${TemplateName}; the API returns no
// template ARN. Template names have no id behind them and are unique only
// within their application. Neither name has a pattern trait, so names
// containing "/" can make two templates share an ARN (application a/b with
// template c, application a with template b/c), as in AWS's own format.
func beanstalkTemplateARN(partition, region, accountID, appName, templateName string) string {
	return arnFor(partition, "elasticbeanstalk", region, accountID, "configurationtemplate/"+appName+"/"+templateName)
}

// beanstalkTemplate is one saved configuration template named by an
// application stored this scan.
type beanstalkTemplate struct{ appName, name string }

// scanBeanstalkConfigurationTemplates stores each saved configuration
// template of the applications scanBeanstalkApplications stored, one row per
// template holding its DescribeConfigurationSettings description and the tags
// read through the tags reader, under its application. The templates are
// described concurrently, and one template's failure costs the others none of
// their rows (storeEachParentTagsUnder). Per template: a denial warns once
// for the op; a response without the requested template is skipped with one
// warning for all such templates; a transient error after the SDK's retries
// is returned as one error for all such templates, apart from the hard ones
// so that it is demoted beside them (phaseErrs.join); any other error is
// returned after the other templates are stored: an application lists only
// its templates' names, so no row can stand in for a template not described.
// The op models no not-found error, so a template or application deleted
// since listing, which may answer InvalidParameterValue, fails like any
// other error. An empty template name is not described and is warned about
// once under the listing that returned it.
func scanBeanstalkConfigurationTemplates(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string, apps []beanstalkApplication, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	var (
		parents []childParent
		unnamed skipTally
	)
	templates := map[string]beanstalkTemplate{}
	for _, a := range apps {
		for _, name := range a.templates {
			if name == "" {
				unnamed.skipped(a.arn, nil)
				continue
			}
			arn := beanstalkTemplateARN(acct.partition(), region, acct.ID, a.name, name)
			parents = append(parents, childParent{id: arn, arn: a.arn})
			templates[arn] = beanstalkTemplate{appName: a.name, name: name}
		}
	}
	// The describes run concurrently, so each tally keeps its smallest ARN.
	var unmatched, throttled skipTally
	failed := storeEachParentTagsUnder(ctx, st, acct, region, beanstalkDescribeConfigurationSettingsOp, nil, parents,
		func(ctx context.Context, arn string, _ *string) ([]ebtypes.ConfigurationSettingsDescription, *string, error) {
			got, err := describeBeanstalkTemplate(ctx, client, templates[arn])
			switch {
			case isDemotableTransient(err):
				throttled.skippedLeast(arn, err)
				return nil, nil, nil
			case err == nil && len(got) == 0:
				unmatched.skippedLeast(arn, nil)
			}
			return got, nil, err
		},
		func(arn string, d ebtypes.ConfigurationSettingsDescription) (*store.Resource, string, string) {
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBeanstalkConfigurationTemplate, NativeID: arn,
				Name: d.TemplateName, Region: &region, CreatedAt: tp(d.DateCreated),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}, "", ""
		}, tags, p)
	var transient error
	if throttled.count > 0 {
		transient = fmt.Errorf("%s: %d template(s) not described, so not stored, first %s: %w",
			beanstalkDescribeConfigurationSettingsOp, throttled.count, throttled.first, throttled.err)
	}
	if failed {
		// A failed write sends no warning, so the transient error joins the
		// write's, the last error the failed write added to p, rather than
		// standing beside it to be demoted to one.
		if transient != nil {
			last := &p.errs[len(p.errs)-1]
			last.err = errors.Join(last.err, transient)
		}
		return true
	}
	p.errs.add(beanstalkDescribeConfigurationSettingsOp, transient)
	if unnamed.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: "elasticbeanstalk:DescribeApplications", Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d configuration template name(s) were listed empty by their application, so those templates were not stored; first application %s", unnamed.count, unnamed.first),
		})
	}
	if unmatched.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: beanstalkDescribeConfigurationSettingsOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d template(s) not stored: the response held no settings description with the requested TemplateName, first %s", unmatched.count, unmatched.first),
		})
	}
	return false
}

// describeBeanstalkTemplate returns the settings descriptions
// DescribeConfigurationSettings returns for t that name t.
func describeBeanstalkTemplate(ctx context.Context, client elasticbeanstalkAPI, t beanstalkTemplate) ([]ebtypes.ConfigurationSettingsDescription, error) {
	out, err := client.DescribeConfigurationSettings(ctx, &elasticbeanstalk.DescribeConfigurationSettingsInput{
		ApplicationName: &t.appName, TemplateName: &t.name,
	})
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(out.ConfigurationSettings, func(d ebtypes.ConfigurationSettingsDescription) bool {
		return sv(d.TemplateName) != t.name
	}), nil
}
