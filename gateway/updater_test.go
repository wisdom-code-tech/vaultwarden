package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testUpdateFPK(t *testing.T, appname, version, wizard string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for name, body := range map[string]string{
		"manifest":       "appname=" + appname + "\nversion=" + version + "\nplatform=x86\n",
		"wizard/upgrade": wizard,
	} {
		if err := archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(archive, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testReleaseClient(fpk []byte, digest string, size int64) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body []byte
		switch {
		case r.URL.Path == "/releases":
			release := publishedPackage("1.37.2")
			release.Assets[0].Size = size
			body, _ = json.Marshal([]githubRelease{release, publishedPackage("1.37.5-rc13")})
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			body = []byte(digest + "  vaultwarden-1.37.2-fnos.fpk\n")
		case strings.HasSuffix(r.URL.Path, "/vaultwarden-1.37.2-fnos.fpk"):
			body = fpk
		default:
			return nil, fmt.Errorf("unexpected download: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
}

func TestDownloadedUpdateMustMatchChecksumAndIdentity(t *testing.T) {
	for _, test := range []struct {
		name, appname, version, wizard string
		badHash, badSize               bool
		wantError                      bool
	}{
		{"valid", "vaultwarden", "1.37.2", `[{"items":[{"type":"tips"}]}]`, false, false, false},
		{"wrong-app", "other-app", "1.37.2", `[]`, false, false, true},
		{"wrong-version", "vaultwarden", "1.37.3", `[]`, false, false, true},
		{"interactive-wizard", "vaultwarden", "1.37.2", `[{"items":[{"type":"password"}]}]`, false, false, true},
		{"bad-sha256", "vaultwarden", "1.37.2", `[]`, true, false, true},
		{"truncated", "vaultwarden", "1.37.2", `[]`, false, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fpk := testUpdateFPK(t, test.appname, test.version, test.wizard)
			sum := sha256.Sum256(fpk)
			digest := hex.EncodeToString(sum[:])
			if test.badHash {
				digest = strings.Repeat("0", 64)
			}
			size := int64(len(fpk))
			if test.badSize {
				size++
			}
			client := testReleaseClient(fpk, digest, size)
			path, err := downloadUpdate(context.Background(), client, packageRelease{Version: "1.37.2", Size: size, DownloadURL: "https://github.com/test/vaultwarden-1.37.2-fnos.fpk", ChecksumsURL: "https://github.com/test/SHA256SUMS"}, t.TempDir())
			if (err != nil) != test.wantError {
				t.Fatalf("path=%s error=%v", path, err)
			}
		})
	}
}

func TestUpdaterUsesOriginalVolumeAndOnlyUpgradeRoutes(t *testing.T) {
	for _, loseTaskResponse := range []bool{false, true} {
		t.Run(fmt.Sprint(loseTaskResponse), func(t *testing.T) {
			directory := t.TempDir()
			manifestPath := filepath.Join(directory, "manifest")
			if err := os.WriteFile(manifestPath, []byte("appname=vaultwarden\nversion=1.37.1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var routes []string
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routes = append(routes, r.URL.Path)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				data := any(nil)
				switch r.URL.Path {
				case "/rpc/v1/update/info":
					if len(body) == 0 {
						fmt.Fprint(w, `{"code":10030,"msg":"missing parameters"}`)
						return
					}
					data = map[string]any{"wizardInfo": map[string]any{"installedVolumeID": 2, "hasWizard": true}}
				case "/rpc/v1/download/task":
					if body["packageSourceType"] != "file" {
						t.Error("not a locally verified FPK")
					}
					data = map[string]string{"downloadTaskId": "stage-test"}
				case "/rpc/v1/download/status":
					data = map[string]any{"status": 2, "appName": "vaultwarden", "version": "1.37.2", "packageType": "fpk", "installed": true}
				case "/rpc/v1/update/task":
					params := body["systemParameters"].(map[string]any)
					if params["installVolumeID"] != float64(2) || params["dataVolumeId"] != float64(2) || params["immediateStart"] != true {
						t.Errorf("incorrect upgrade parameters: %+v", params)
					}
					if body["appName"] != "vaultwarden" || body["updateVersion"] != "1.37.2" {
						t.Errorf("incorrect package identity: %+v", body)
					}
					if loseTaskResponse {
						fmt.Fprint(w, "broken-response")
						return
					}
					data = map[string]string{"taskId": "upgrade-test"}
				case "/rpc/v1/common/status":
					if err := os.WriteFile(manifestPath, []byte("appname=vaultwarden\nversion=1.37.2\n"), 0600); err != nil {
						t.Error(err)
					}
					data = map[string]any{"status": 2}
				default:
					t.Errorf("unexpected or destructive route: %s", r.URL.Path)
				}
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
			}))
			defer api.Close()
			fpk := testUpdateFPK(t, "vaultwarden", "1.37.2", `[{"items":[{"type":"tips"}]}]`)
			sum := sha256.Sum256(fpk)
			client := api.Client()
			transport := client.Transport
			client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				copy := r.Clone(r.Context())
				copy.URL.Scheme = "http"
				copy.URL.Host = strings.TrimPrefix(api.URL, "http://")
				return transport.RoundTrip(copy)
			})
			u := updater{state: installState{Phase: "checking"}, directory: directory, manifestPath: manifestPath, center: appCenter{client: client}, client: testReleaseClient(fpk, hex.EncodeToString(sum[:]), int64(len(fpk))), releasesURL: "https://fixture/releases"}
			u.run()
			state := u.snapshot()
			want := "complete"
			if loseTaskResponse {
				want = "unknown"
			}
			if state.Phase != want {
				t.Fatalf("unexpected terminal state: %+v", state)
			}
			if slices.Contains(routes, "/rpc/v1/install/task") || slices.Contains(routes, "/rpc/v1/uninstall/task") {
				t.Fatal("must not install or uninstall")
			}
			count := 0
			for _, route := range routes {
				if route == "/rpc/v1/update/task" {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("upgrade submitted %d times", count)
			}
			if loseTaskResponse {
				if err := u.start(); err == nil {
					t.Fatal("unknown outcome must block duplicate mutation")
				}
			}
		})
	}
}

func TestInstallerRejectsCrossOriginAndCallerSelectedPackages(t *testing.T) {
	forwarded := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		forwarded++
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(`{"phase":"checking"}`)), Header: make(http.Header)}, nil
	})}
	for _, test := range []struct {
		origin, body string
		status       int
	}{
		{"", `{}`, 403}, {"https://evil.test", `{}`, 403},
		{"https://nas.test", `{"url":"https://evil.test/a.fpk"}`, 400},
		{"https://nas.test", `{} {"version":"1.37.2"}`, 400},
		{"https://nas.test", `{}`, 202},
	} {
		r := httptest.NewRequest(http.MethodPost, "https://nas.test/api/install", strings.NewReader(test.body))
		r.Header.Set("Origin", test.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		installerProxy(client, "install").ServeHTTP(w, r)
		if w.Code != test.status {
			t.Errorf("%s %s returned %d", test.origin, test.body, w.Code)
		}
	}
	if forwarded != 1 {
		t.Fatalf("unexpected forwarded mutations: %d", forwarded)
	}
}

func TestAppCenterMissingCodeIsNotSuccess(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{}}`))}, nil
	})}
	if err := (appCenter{client: client}).call(context.Background(), "update/task", struct{}{}, nil); err == nil {
		t.Fatal("missing response code was accepted")
	}
}
