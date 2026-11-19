package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageChartContainsPublishedFiles(t *testing.T) {
	root, err := operatorChartRoot()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := packageChart(root)
	if err != nil {
		t.Fatal(err)
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	seen := map[string]bool{}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seen[header.Name] = true
	}
	for _, name := range []string{"warden-operator/Chart.yaml", "warden-operator/values.yaml", "warden-operator/templates/operator.yaml"} {
		if !seen[name] {
			t.Fatalf("packaged Chart is missing %s", name)
		}
	}
}

func TestDownloadChecksumsIncludesAllLinuxArtifacts(t *testing.T) {
	root := t.TempDir()
	files := []string{
		"deploy/linux/install.sh", "deploy/linux/uninstall.sh", "deploy/network/install.sh",
		"server/bin/linux-amd64/warden-linux-connector", "server/bin/linux-amd64/warden-linux-helper",
		"server/bin/linux-arm64/warden-linux-connector", "server/bin/linux-arm64/warden-linux-helper",
		"server/bin/linux-amd64/warden-network-connector", "server/bin/linux-arm64/warden-network-connector",
	}
	for _, name := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("WARDEN_ARTIFACT_ROOT", root)
	response := httptest.NewRecorder()
	(&Handler{}).DownloadChecksums(response, httptest.NewRequest("GET", "/downloads/SHA256SUMS", nil))
	if response.Code != 200 {
		t.Fatalf("checksum endpoint returned %d: %s", response.Code, response.Body.String())
	}
	for _, name := range []string{"install-linux.sh", "install-network.sh", "uninstall-linux.sh", "warden-linux-connector-linux-amd64", "warden-linux-helper-linux-arm64", "warden-network-connector-linux-amd64"} {
		if !strings.Contains(response.Body.String(), "  "+name+"\n") {
			t.Fatalf("checksum manifest is missing %s: %s", name, response.Body.String())
		}
	}
}
