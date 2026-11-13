package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func publishedPackage(version string) githubRelease {
	return githubRelease{
		Tag: "v" + version,
		Assets: []releaseAsset{
			{Name: "vaultwarden-" + version + "-fnos.fpk", Size: 123456, State: "uploaded"},
			{Name: "SHA256SUMS", Size: 100, State: "uploaded"},
		},
	}
}

func TestFindFormalPackageRelease(t *testing.T) {
	prerelease := publishedPackage("1.38.0")
	prerelease.Prerelease = true
	draft := publishedPackage("1.39.0")
	draft.Draft = true
	incomplete := publishedPackage("1.40.0")
	incomplete.Assets = incomplete.Assets[:1]
	uploading := publishedPackage("1.41.0")
	uploading.Assets[0].State = "new"
	releases := []githubRelease{
		publishedPackage("1.37.9"), publishedPackage("1.37.10"),
		publishedPackage("9.0.0-rc12"), prerelease, draft, incomplete, uploading,
	}
	for _, test := range []struct {
		current string
		updated bool
	}{
		{"1.37.1", true}, {"1.37.10-rc12", false},
		{"1.37.10", false}, {"1.38.0-rc1", false}, {"2.0.0", false},
	} {
		t.Run(test.current, func(t *testing.T) {
			result, err := findPackageRelease(releases, test.current)
			if err != nil {
				t.Fatal(err)
			}
			if result.Release == nil || result.Release.Version != "1.37.10" || result.Available != test.updated {
				t.Fatalf("unexpected release: %+v", result)
			}
			want := "https://github.com/" + releaseRepository + "/releases/download/v1.37.10/vaultwarden-1.37.10-fnos.fpk"
			if result.Release.DownloadURL != want {
				t.Fatalf("unexpected download URL: %s", result.Release.DownloadURL)
			}
		})
	}
	result, err := findPackageRelease([]githubRelease{publishedPackage("1.37.5-rc12")}, "1.37.1")
	if err != nil || result.Release != nil || result.Available {
		t.Fatalf("RC-only repository must not offer an update: %+v, %v", result, err)
	}
	if _, err := findPackageRelease(releases, ""); err == nil {
		t.Fatal("missing installed version must fail")
	}
}

func TestManagementAuthorizationAndProxy(t *testing.T) {
	binaryPath, err := filepath.Abs("../packaging/vaultwarden/app/server/vaultwarden")
	if err != nil {
		t.Fatal(err)
	}
	proxy := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, "vault-proxy:"+request.URL.Path)
	})
	handler := newManagementHandler(config{
		prefix: "/app/vaultwarden", vaultwardenPath: binaryPath, packageVersion: "1.37.1",
	}, proxy)
	for _, path := range []string{"/app/vaultwarden/manage", "/app/vaultwarden/manage/", "/app/vaultwarden/manage/app.js", "/app/vaultwarden/manage/api/status", "/app/vaultwarden/manage/api/updates", "/app/vaultwarden/manage/api/installer", "/app/vaultwarden/manage/api/install-status", "/app/vaultwarden/manage/api/install"} {
		for _, admin := range []string{"", "false", "TRUE"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("X-Trim-Isadmin", admin)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("unprivileged request %s returned %d", path, response.Code)
			}
		}
	}
	for _, path := range []string{"/app/vaultwarden/", "/app/vaultwarden/api/sync", "/identity/connect/token"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Body.String() != "vault-proxy:"+path {
			t.Fatalf("password vault route changed: %s", response.Body.String())
		}
	}
	for _, file := range []string{"", "style.css", "app.js"} {
		request := httptest.NewRequest(http.MethodGet, "/app/vaultwarden/manage/"+file, nil)
		request.Header.Set("X-Trim-Isadmin", "true")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("management asset %q unavailable: %d", file, response.Code)
		}
		if response.Header().Get("Content-Security-Policy") == "" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("management security headers missing")
		}
	}
}

func TestManagementHealthAndActualConfiguration(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/alive" {
					t.Errorf("unexpected health path: %s", request.URL.Path)
				}
				writer.WriteHeader(code)
			}))
			defer upstream.Close()
			upstreamURL, _ := url.Parse(upstream.URL)
			manager := &management{cfg: config{upstream: upstreamURL, packageVersion: "1.37.2515", metadata: packageMetadata{UpstreamVersion: "1.37.2", Channel: "stable", Revision: 15}}, client: upstream.Client()}
			check := func(want string) {
				t.Helper()
				response := httptest.NewRecorder()
				manager.status(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))
				var result map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result["state"] != want || result["port"] != upstreamURL.Port() || result["version"] != "1.37.2515" || result["upstreamVersion"] != "1.37.2" || result["channel"] != "stable" {
					t.Fatalf("incorrect live state: %+v", result)
				}
			}
			if code == http.StatusOK {
				check("healthy")
			} else {
				check("unhealthy")
			}
			upstream.Close()
			check("unreachable")
		})
	}
}

func TestUpdateCheckPaginationAndFailures(t *testing.T) {
	pages := 0
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		pages++
		if request.Header.Get("User-Agent") != "vaultwarden-fnos/1.37.1" {
			t.Error("missing application user agent")
		}
		if request.URL.Query().Get("page") == "1" {
			batch := make([]githubRelease, 100)
			for index := range batch {
				batch[index] = publishedPackage("1.38.0-rc1")
			}
			json.NewEncoder(writer).Encode(batch)
			return
		}
		json.NewEncoder(writer).Encode([]githubRelease{publishedPackage("1.37.2")})
	}))
	defer api.Close()
	manager := &management{cfg: config{packageVersion: "1.37.1"}, client: api.Client(), releasesURL: api.URL}
	result, err := manager.checkUpdates(context.Background())
	if err != nil || !result.Available || result.Release.Version != "1.37.2" || pages != 2 {
		t.Fatalf("failed to find stable package beyond first page: %+v, %v, pages=%d", result, err, pages)
	}
	for _, code := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusOK} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			failedAPI := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(code)
				fmt.Fprint(writer, "invalid-json")
			}))
			defer failedAPI.Close()
			manager.releasesURL = failedAPI.URL
			response := httptest.NewRecorder()
			manager.updates(response, httptest.NewRequest(http.MethodGet, "/api/updates", nil))
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"error"`) {
				t.Fatalf("failure was hidden: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
