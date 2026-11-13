package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const releaseRepository = "wisdom-code-tech/vaultwarden"

var packageVersionPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:-rc[0-9]+)?$`)

func parsePackageVersion(value string) ([3]uint64, error) {
	var version [3]uint64
	parts := packageVersionPattern.FindStringSubmatch(value)
	if parts == nil {
		return version, fmt.Errorf("invalid package version %q", value)
	}
	for index := range version {
		number, err := strconv.ParseUint(parts[index+1], 10, 64)
		if err != nil {
			return version, fmt.Errorf("invalid package version %q: %w", value, err)
		}
		version[index] = number
	}
	return version, nil
}

type releaseAsset struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}

type githubRelease struct {
	Tag         string         `json:"tag_name"`
	Draft       bool           `json:"draft"`
	Prerelease  bool           `json:"prerelease"`
	PublishedAt string         `json:"published_at"`
	Assets      []releaseAsset `json:"assets"`
}

type packageRelease struct {
	Version      string `json:"version"`
	PublishedAt  string `json:"publishedAt"`
	Size         int64  `json:"size"`
	DownloadURL  string `json:"downloadUrl"`
	ChecksumsURL string `json:"checksumsUrl"`
	ReleaseURL   string `json:"releaseUrl"`
}

type updateResult struct {
	CurrentVersion string          `json:"currentVersion"`
	Available      bool            `json:"available"`
	Release        *packageRelease `json:"release"`
}

func findPackageRelease(releases []githubRelease, current string) (updateResult, error) {
	installed, err := parsePackageVersion(current)
	if err != nil {
		return updateResult{}, err
	}
	result := updateResult{CurrentVersion: current}
	var newest [3]uint64
	for _, release := range releases {
		version := strings.TrimPrefix(release.Tag, "v")
		// 历史 RC 可能被误标为正式发布，不能只依赖 prerelease 字段。
		if release.Draft || release.Prerelease || strings.Contains(version, "-") {
			continue
		}
		parsed, err := parsePackageVersion(version)
		if err != nil {
			continue
		}
		if result.Release != nil && slices.Compare(parsed[:], newest[:]) <= 0 {
			continue
		}
		filename := "vaultwarden-" + version + "-fnos.fpk"
		var size int64
		var hasChecksums bool
		for _, asset := range release.Assets {
			if asset.State != "uploaded" {
				continue
			}
			if asset.Name == filename {
				size = asset.Size
			}
			if asset.Name == "SHA256SUMS" && asset.Size > 0 {
				hasChecksums = true
			}
		}
		if size <= 0 || !hasChecksums {
			continue
		}
		newest = parsed
		baseURL := "https://github.com/" + releaseRepository + "/releases/"
		assetURL := baseURL + "download/" + release.Tag + "/"
		result.Release = &packageRelease{
			Version: version, PublishedAt: release.PublishedAt, Size: size,
			DownloadURL: assetURL + filename, ChecksumsURL: assetURL + "SHA256SUMS",
			ReleaseURL: baseURL + "tag/" + release.Tag,
		}
	}
	if result.Release != nil {
		comparison := slices.Compare(newest[:], installed[:])
		result.Available = comparison > 0 || (comparison == 0 && strings.Contains(current, "-rc"))
	}
	return result, nil
}

type management struct {
	cfg         config
	client      *http.Client
	releasesURL string
}

func (m *management) checkUpdates(ctx context.Context) (updateResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var releases []githubRelease
	for page := 1; ; page++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("%s?per_page=100&page=%d", m.releasesURL, page), nil)
		if err != nil {
			return updateResult{}, err
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("User-Agent", "vaultwarden-fnos/"+m.cfg.packageVersion)
		response, err := m.client.Do(request)
		if err != nil {
			return updateResult{}, fmt.Errorf("连接 GitHub 失败，请检查 NAS 网络后重试")
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
				return updateResult{}, fmt.Errorf("GitHub 暂时限制了请求，请稍后再试")
			}
			return updateResult{}, fmt.Errorf("GitHub 返回 HTTP %d，请稍后再试", response.StatusCode)
		}
		var batch []githubRelease
		err = json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&batch)
		response.Body.Close()
		if err != nil {
			return updateResult{}, fmt.Errorf("GitHub 发布信息格式异常，请稍后再试")
		}
		releases = append(releases, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return findPackageRelease(releases, m.cfg.packageVersion)
}

func writeManagementJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		log.Printf("write management response: %v", err)
	}
}

func (m *management) status(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	healthURL := *m.cfg.upstream
	healthURL.Path = "/alive"
	healthRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL.String(), nil)
	if err != nil {
		http.Error(writer, "invalid health URL", http.StatusInternalServerError)
		return
	}
	state := "unreachable"
	response, err := m.client.Do(healthRequest)
	if err == nil {
		response.Body.Close()
		state = "unhealthy"
		if response.StatusCode == http.StatusOK {
			state = "healthy"
		}
	}
	writeManagementJSON(writer, http.StatusOK, struct {
		State   string `json:"state"`
		Address string `json:"address"`
		Port    string `json:"port"`
		Version string `json:"version"`
	}{state, m.cfg.upstream.Hostname(), m.cfg.upstream.Port(), m.cfg.packageVersion})
}

func (m *management) updates(writer http.ResponseWriter, request *http.Request) {
	result, err := m.checkUpdates(request.Context())
	if err != nil {
		writeManagementJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeManagementJSON(writer, http.StatusOK, result)
}

func newManagementHandler(cfg config, proxy http.Handler) http.Handler {
	m := &management{
		cfg: cfg, client: &http.Client{Timeout: 15 * time.Second},
		releasesURL: "https://api.github.com/repos/" + releaseRepository + "/releases",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", m.status)
	mux.HandleFunc("GET /api/updates", m.updates)
	registerInstallerRoutes(mux)
	uiPath := filepath.Join(filepath.Dir(filepath.Dir(cfg.vaultwardenPath)), "ui", "manage")
	mux.Handle("GET /", http.FileServer(http.Dir(uiPath)))
	managementPrefix := cfg.prefix + "/manage"
	managementHandler := http.StripPrefix(managementPrefix, mux)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != managementPrefix && !strings.HasPrefix(request.URL.Path, managementPrefix+"/") {
			proxy.ServeHTTP(writer, request)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'self'; form-action 'none'")
		// 此服务仅监听受文件权限保护的 Unix Socket，身份由 fnOS 网关注入。
		if request.Header.Get("X-Trim-Isadmin") != "true" {
			writeManagementJSON(writer, http.StatusForbidden, map[string]string{"error": "仅 fnOS 管理员可访问管理页面"})
			return
		}
		if request.URL.Path == managementPrefix {
			http.Redirect(writer, request, managementPrefix+"/", http.StatusTemporaryRedirect)
			return
		}
		managementHandler.ServeHTTP(writer, request)
	})
}
