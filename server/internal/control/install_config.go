package control

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

type installConfigResponse struct {
	ReleaseVersion          string `json:"releaseVersion"`
	PublicControlURL        string `json:"publicControlUrl"`
	OperatorChartReference  string `json:"operatorChartReference"`
	OperatorImageRepository string `json:"operatorImageRepository"`
	OperatorImageTag        string `json:"operatorImageTag"`
	ConnectorImage          string `json:"connectorImage"`
}

func (h *Handler) GetInstallConfig(w http.ResponseWriter, _ *http.Request) {
	config, err := currentInstallConfig()
	if err != nil {
		// Filesystem paths and read errors stay in the server log only.
		log.Printf("install config unavailable: %v", err)
		jsonError(w, http.StatusServiceUnavailable, "INSTALL_CONFIG_UNAVAILABLE", "当前发布安装配置不可用")
		return
	}
	jsonResp(w, http.StatusOK, config)
}

func currentInstallConfig() (installConfigResponse, error) {
	defaults := releaseDefaults{
		releaseVersion: os.Getenv("WARDEN_RELEASE_VERSION"), operatorRepository: os.Getenv("WARDEN_OPERATOR_IMAGE_REPOSITORY"),
		operatorTag: os.Getenv("WARDEN_OPERATOR_IMAGE_TAG"), connectorImage: os.Getenv("WARDEN_CONNECTOR_IMAGE"),
	}
	if defaults.releaseVersion == "" || defaults.operatorRepository == "" || defaults.operatorTag == "" || defaults.connectorImage == "" {
		chartDefaults, err := chartReleaseDefaults()
		if err != nil {
			return installConfigResponse{}, err
		}
		defaults = chartDefaults
	}
	return installConfigResponse{
		ReleaseVersion:          configValue("WARDEN_RELEASE_VERSION", defaults.releaseVersion),
		PublicControlURL:        strings.TrimRight(os.Getenv("WARDEN_PUBLIC_CONTROL_URL"), "/"),
		OperatorChartReference:  configValue("WARDEN_OPERATOR_CHART_REF", "/downloads/warden-operator-chart.tgz"),
		OperatorImageRepository: configValue("WARDEN_OPERATOR_IMAGE_REPOSITORY", defaults.operatorRepository),
		OperatorImageTag:        configValue("WARDEN_OPERATOR_IMAGE_TAG", defaults.operatorTag),
		ConnectorImage:          configValue("WARDEN_CONNECTOR_IMAGE", defaults.connectorImage),
	}, nil
}

type releaseDefaults struct {
	releaseVersion     string
	operatorRepository string
	operatorTag        string
	connectorImage     string
}

func chartReleaseDefaults() (releaseDefaults, error) {
	chartRoot, err := operatorChartRoot()
	if err != nil {
		return releaseDefaults{}, err
	}
	chartData, err := os.ReadFile(filepath.Join(chartRoot, "Chart.yaml"))
	if err != nil {
		return releaseDefaults{}, fmt.Errorf("读取当前发布 Chart 失败: %w", err)
	}
	valuesData, err := os.ReadFile(filepath.Join(chartRoot, "values.yaml"))
	if err != nil {
		return releaseDefaults{}, fmt.Errorf("读取当前发布 values 失败: %w", err)
	}
	var chart struct {
		AppVersion string `yaml:"appVersion"`
	}
	var values struct {
		ConnectorImage string `yaml:"connectorImage"`
		Image          struct {
			Repository string `yaml:"repository"`
			Tag        string `yaml:"tag"`
		} `yaml:"image"`
	}
	if err := yaml.Unmarshal(chartData, &chart); err != nil {
		return releaseDefaults{}, fmt.Errorf("解析当前发布 Chart 失败: %w", err)
	}
	if err := yaml.Unmarshal(valuesData, &values); err != nil {
		return releaseDefaults{}, fmt.Errorf("解析当前发布 values 失败: %w", err)
	}
	if chart.AppVersion == "" || values.ConnectorImage == "" || values.Image.Repository == "" || values.Image.Tag == "" {
		return releaseDefaults{}, fmt.Errorf("当前发布 Chart 缺少 appVersion、Operator 镜像或 Connector 镜像")
	}
	return releaseDefaults{releaseVersion: chart.AppVersion, operatorRepository: values.Image.Repository,
		operatorTag: values.Image.Tag, connectorImage: values.ConnectorImage}, nil
}

func operatorChartRoot() (string, error) {
	root, err := filepath.Abs(artifactRoot())
	if err != nil {
		return "", err
	}
	for range 8 {
		candidate := filepath.Join(root, "deploy", "helm", "warden-operator")
		if info, err := os.Stat(filepath.Join(candidate, "Chart.yaml")); err == nil && !info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
	}
	return "", fmt.Errorf("当前发布未包含 Operator Chart")
}

func configValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
