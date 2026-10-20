package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// Values in several option namespaces can hold credentials (environment
	// properties, EKS env-variables, custom options, URLs with userinfo), so
	// each value is classified under its OptionName and kept only when it is
	// judged safe; Namespace, OptionName and ResourceName always stay.
	registerType(restype.Descriptor{
		Type:    TypeBeanstalkConfigurationTemplate,
		Service: "elasticbeanstalk",
		Redact:  []redact.Rule{{Path: "OptionSettings[*].Value", Mode: redact.RedactUnlessSafe, KeyField: "OptionName"}},
	})
}

const beanstalkDescribeConfigurationSettingsOp = "elasticbeanstalk:DescribeConfigurationSettings"

// beanstalkTemplateARN builds the Service Reference ARN format
// configurationtemplate/${ApplicationName}/${TemplateName}; the API returns no
// template ARN. Template names have no id behind them and are unique only
// within their application. Neither name has a pattern trait, so names
// containing "/" can make two templates share an ARN (application a/b with
// template c, application a with template b/c), as in AWS's own format.
func beanstalkTemplateARN(region, accountID, appName, templateName string) string {
	return "arn:aws:elasticbeanstalk:" + region + ":" + accountID + ":configurationtemplate/" + appName + "/" + templateName
}

// beanstalkTemplate is one saved configuration template named by an
// application stored this scan.
type beanstalkTemplate struct{ appName, name string }

// scanBeanstalkConfigurationTemplates stores each saved configuration
// template of the applications scanBeanstalkApplications stored, one row per
// template holding its DescribeConfigurationSettings description, under its
// application. Per template: InvalidParameterValue is skipped with one
// warning for all such templates (the op models no not-found error; a
// template or application deleted since listing is expected to answer with
// that code, unverified), a denial warns once for the op, a transient error
// after the SDK's retries is skipped with one warning for all such templates,
// a response without the requested template is skipped with one warning for
// all such templates, and anything else fails the phase, storing none of its
// rows.
func scanBeanstalkConfigurationTemplates(ctx context.Context, client elasticbeanstalkAPI, acct *account, region string, st *store.Store, scanID string, apps []beanstalkApplication) (int, int, error) {
	var parents []childParent
	templates := map[string]beanstalkTemplate{}
	for _, a := range apps {
		if a.name == "" {
			continue
		}
		for _, name := range a.templates {
			if name == "" {
				continue
			}
			arn := beanstalkTemplateARN(region, acct.ID, a.name, name)
			parents = append(parents, childParent{id: arn, arn: a.arn})
			templates[arn] = beanstalkTemplate{appName: a.name, name: name}
		}
	}
	var gone, unmatched skipTally
	never := func(error) bool { return false }
	total, inserted, err := childFanOutSkipTransient(ctx, st, acct, region, beanstalkDescribeConfigurationSettingsOp, parents, never, byParentID,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			rows, err := describeBeanstalkTemplate(ctx, client, acct, region, scanID, p.id, templates[p.id])
			if gone.dropCode("InvalidParameterValue", p.id, err) {
				return nil, nil
			}
			if err == nil && len(rows) == 0 {
				unmatched.skipped(p.id, nil)
			}
			return rows, err
		})
	gone.warnCode(st, beanstalkDescribeConfigurationSettingsOp, acct, region, "InvalidParameterValue",
		"template(s)", "a template or its application may have been deleted since listing")
	if unmatched.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: beanstalkDescribeConfigurationSettingsOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d template(s) not stored: the response held no settings description with the requested TemplateName, first %s", unmatched.count, unmatched.first),
		})
	}
	return total, inserted, err
}

func describeBeanstalkTemplate(ctx context.Context, client elasticbeanstalkAPI, acct *account, region, scanID, arn string, t beanstalkTemplate) ([]*store.Resource, error) {
	out, err := client.DescribeConfigurationSettings(ctx, &elasticbeanstalk.DescribeConfigurationSettingsInput{
		ApplicationName: &t.appName, TemplateName: &t.name,
	})
	if err != nil {
		return nil, err
	}
	var rows []*store.Resource
	for _, d := range out.ConfigurationSettings {
		if sv(d.TemplateName) != t.name {
			continue
		}
		rows = append(rows, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeBeanstalkConfigurationTemplate, NativeID: arn,
			Name: d.TemplateName, Region: &region, CreatedAt: tp(d.DateCreated),
			AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
		})
	}
	return rows, nil
}
