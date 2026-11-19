package control

import "testing"

func TestCurrentInstallConfigUsesReleaseEnvironment(t *testing.T) {
	t.Setenv("WARDEN_RELEASE_VERSION", "1.2.3")
	t.Setenv("WARDEN_PUBLIC_CONTROL_URL", "https://warden.example.test/")
	t.Setenv("WARDEN_OPERATOR_CHART_REF", "oci://registry.example.test/warden/operator")
	t.Setenv("WARDEN_OPERATOR_IMAGE_REPOSITORY", "registry.example.test/warden/operator")
	t.Setenv("WARDEN_OPERATOR_IMAGE_TAG", "v1.2.3")
	t.Setenv("WARDEN_CONNECTOR_IMAGE", "registry.example.test/warden/connector:v1.2.3")

	config, err := currentInstallConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.ReleaseVersion != "1.2.3" || config.PublicControlURL != "https://warden.example.test" {
		t.Fatalf("unexpected release config: %#v", config)
	}
	if config.OperatorChartReference != "oci://registry.example.test/warden/operator" ||
		config.OperatorImageRepository != "registry.example.test/warden/operator" ||
		config.OperatorImageTag != "v1.2.3" || config.ConnectorImage != "registry.example.test/warden/connector:v1.2.3" {
		t.Fatalf("unexpected image config: %#v", config)
	}
}

func TestCurrentInstallConfigDefaultsToPublishedChart(t *testing.T) {
	for _, name := range []string{"WARDEN_RELEASE_VERSION", "WARDEN_OPERATOR_IMAGE_REPOSITORY", "WARDEN_OPERATOR_IMAGE_TAG", "WARDEN_CONNECTOR_IMAGE", "WARDEN_OPERATOR_CHART_REF"} {
		t.Setenv(name, "")
	}
	config, err := currentInstallConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.ReleaseVersion == "" || config.OperatorImageRepository == "" || config.OperatorImageTag == "" || config.ConnectorImage == "" {
		t.Fatalf("Chart defaults are incomplete: %#v", config)
	}
	if config.OperatorChartReference != "/downloads/warden-operator-chart.tgz" {
		t.Fatalf("chart reference = %q", config.OperatorChartReference)
	}
}
