package control

import "testing"

func TestCurrentInstallConfigUsesReleaseEnvironment(t *testing.T) {
	t.Setenv("PAM_RELEASE_VERSION", "1.2.3")
	t.Setenv("PAM_PUBLIC_CONTROL_URL", "https://pam.example.test/")
	t.Setenv("PAM_OPERATOR_CHART_REF", "oci://registry.example.test/pam/operator")
	t.Setenv("PAM_OPERATOR_IMAGE_REPOSITORY", "registry.example.test/pam/operator")
	t.Setenv("PAM_OPERATOR_IMAGE_TAG", "v1.2.3")
	t.Setenv("PAM_CONNECTOR_IMAGE", "registry.example.test/pam/connector:v1.2.3")

	config, err := currentInstallConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.ReleaseVersion != "1.2.3" || config.PublicControlURL != "https://pam.example.test" {
		t.Fatalf("unexpected release config: %#v", config)
	}
	if config.OperatorChartReference != "oci://registry.example.test/pam/operator" ||
		config.OperatorImageRepository != "registry.example.test/pam/operator" ||
		config.OperatorImageTag != "v1.2.3" || config.ConnectorImage != "registry.example.test/pam/connector:v1.2.3" {
		t.Fatalf("unexpected image config: %#v", config)
	}
}

func TestCurrentInstallConfigDefaultsToPublishedChart(t *testing.T) {
	for _, name := range []string{"PAM_RELEASE_VERSION", "PAM_OPERATOR_IMAGE_REPOSITORY", "PAM_OPERATOR_IMAGE_TAG", "PAM_CONNECTOR_IMAGE", "PAM_OPERATOR_CHART_REF"} {
		t.Setenv(name, "")
	}
	config, err := currentInstallConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.ReleaseVersion == "" || config.OperatorImageRepository == "" || config.OperatorImageTag == "" || config.ConnectorImage == "" {
		t.Fatalf("Chart defaults are incomplete: %#v", config)
	}
	if config.OperatorChartReference != "/downloads/pam-operator-chart.tgz" {
		t.Fatalf("chart reference = %q", config.OperatorChartReference)
	}
}
