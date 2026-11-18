package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

func artifactRoot() string {
	if value := os.Getenv("WARDEN_ARTIFACT_ROOT"); value != "" {
		return value
	}
	return "."
}

func (h *Handler) DownloadLinuxInstaller(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join(artifactRoot(), "deploy", "linux", "install.sh"))
}

func (h *Handler) DownloadHostInstaller(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join(artifactRoot(), "deploy", "host", "install.sh"))
}

func (h *Handler) DownloadLinuxUninstaller(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join(artifactRoot(), "deploy", "linux", "uninstall.sh"))
}

func (h *Handler) DownloadNetworkInstaller(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join(artifactRoot(), "deploy", "network", "install.sh"))
}

func (h *Handler) DownloadLinuxConnector(w http.ResponseWriter, r *http.Request) {
	downloadLinuxBinary(w, r, "warden-linux-connector")
}

func (h *Handler) DownloadLinuxHelper(w http.ResponseWriter, r *http.Request) {
	downloadLinuxBinary(w, r, "warden-linux-helper")
}

func (h *Handler) DownloadHostConnector(w http.ResponseWriter, r *http.Request) {
	platform, arch := r.PathValue("platform"), r.PathValue("arch")
	if platform != "windows" && platform != "macos" || (arch != "amd64" && arch != "arm64") || (platform == "windows" && arch != "amd64") {
		jsonError(w, http.StatusNotFound, "ARTIFACT_NOT_FOUND", "不支持的主机平台或架构")
		return
	}
	name := "warden-host-connector-" + platform + "-" + arch
	if platform == "windows" {
		name += ".exe"
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	http.ServeFile(w, r, filepath.Join(artifactRoot(), "server", "bin", platform+"-"+arch, name))
}

func (h *Handler) DownloadNetworkConnector(w http.ResponseWriter, r *http.Request) {
	downloadLinuxBinary(w, r, "warden-network-connector")
}

func (h *Handler) DownloadOperatorChart(w http.ResponseWriter, _ *http.Request) {
	chartRoot, err := operatorChartRoot()
	if err != nil {
		jsonError(w, http.StatusNotFound, "ARTIFACT_NOT_FOUND", err.Error())
		return
	}
	payload, err := packageChart(chartRoot)
	if err != nil {
		jsonError(w, http.StatusNotFound, "ARTIFACT_NOT_FOUND", "当前发布未包含有效的 Operator Chart")
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=warden-operator-chart.tgz")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func (h *Handler) DownloadChecksums(w http.ResponseWriter, _ *http.Request) {
	root := artifactRoot()
	files := map[string]string{
		"install-linux.sh":                     filepath.Join(root, "deploy", "linux", "install.sh"),
		"install-network.sh":                   filepath.Join(root, "deploy", "network", "install.sh"),
		"uninstall-linux.sh":                   filepath.Join(root, "deploy", "linux", "uninstall.sh"),
		"warden-linux-connector-linux-amd64":   filepath.Join(root, "server", "bin", "linux-amd64", "warden-linux-connector"),
		"warden-linux-helper-linux-amd64":      filepath.Join(root, "server", "bin", "linux-amd64", "warden-linux-helper"),
		"warden-linux-connector-linux-arm64":   filepath.Join(root, "server", "bin", "linux-arm64", "warden-linux-connector"),
		"warden-linux-helper-linux-arm64":      filepath.Join(root, "server", "bin", "linux-arm64", "warden-linux-helper"),
		"warden-network-connector-linux-amd64": filepath.Join(root, "server", "bin", "linux-amd64", "warden-network-connector"),
		"warden-network-connector-linux-arm64": filepath.Join(root, "server", "bin", "linux-arm64", "warden-network-connector"),
	}
	hostFiles := map[string]string{
		"warden-host-connector-windows-amd64.exe": filepath.Join(root, "server", "bin", "windows-amd64", "warden-host-connector-windows-amd64.exe"),
		"warden-host-connector-macos-amd64":       filepath.Join(root, "server", "bin", "macos-amd64", "warden-host-connector-macos-amd64"),
		"warden-host-connector-macos-arm64":       filepath.Join(root, "server", "bin", "macos-arm64", "warden-host-connector-macos-arm64"),
	}
	for name, path := range hostFiles {
		if _, err := os.Stat(path); err == nil {
			files[name] = path
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var output bytes.Buffer
	for _, name := range names {
		file, err := os.Open(files[name])
		if err != nil {
			jsonError(w, http.StatusNotFound, "ARTIFACT_NOT_FOUND", "当前发布缺少 Linux 安装制品")
			return
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			jsonError(w, http.StatusInternalServerError, "ARTIFACT_HASH_FAILED", "计算 Linux 安装制品摘要失败")
			return
		}
		fmt.Fprintf(&output, "%x  %s\n", hash.Sum(nil), name)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=SHA256SUMS")
	_, _ = w.Write(output.Bytes())
}

func packageChart(chartRoot string) ([]byte, error) {
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	err := filepath.WalkDir(chartRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(filepath.Dir(chartRoot), path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tarWriter, io.LimitReader(file, 4<<20))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return nil, err
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func downloadLinuxBinary(w http.ResponseWriter, r *http.Request, name string) {
	arch := r.PathValue("arch")
	if arch != "amd64" && arch != "arm64" {
		jsonError(w, http.StatusNotFound, "ARTIFACT_NOT_FOUND", "不支持的 Linux 架构")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	http.ServeFile(w, r, filepath.Join(artifactRoot(), "server", "bin", "linux-"+arch, name))
}
