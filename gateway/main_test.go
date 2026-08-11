package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestReverseProxyPreservesGatewayPrefix(t *testing.T) {
	received := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received <- request.Clone(request.Context())
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(newReverseProxy(upstreamURL, "/app/vaultwarden"))
	defer proxy.Close()

	request, err := http.NewRequest(http.MethodGet, proxy.URL+"/app/vaultwarden/api/sync?excludeDomains=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "nas.example.test"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected status: %s", response.Status)
	}

	forwarded := <-received
	if forwarded.URL.Path != "/app/vaultwarden/api/sync" {
		t.Fatalf("path was rewritten: %s", forwarded.URL.Path)
	}
	if forwarded.URL.RawQuery != "excludeDomains=true" {
		t.Fatalf("query was rewritten: %s", forwarded.URL.RawQuery)
	}
	if forwarded.Host != "nas.example.test" {
		t.Fatalf("host was not preserved: %s", forwarded.Host)
	}
	if forwarded.Header.Get("X-Forwarded-Prefix") != "/app/vaultwarden" {
		t.Fatalf("missing forwarded prefix: %s", forwarded.Header.Get("X-Forwarded-Prefix"))
	}
	if forwarded.Header.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("missing forwarded protocol: %s", forwarded.Header.Get("X-Forwarded-Proto"))
	}
}

func TestReverseProxyRestoresStrippedGatewayPrefix(t *testing.T) {
	receivedPath := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedPath <- request.URL.Path
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(newReverseProxy(upstreamURL, "/app/vaultwarden"))
	defer proxy.Close()

	response, err := http.Get(proxy.URL + "/identity/connect/token")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if path := <-receivedPath; path != "/app/vaultwarden/identity/connect/token" {
		t.Fatalf("gateway prefix was not restored: %s", path)
	}
}

func TestPublicDomainUsesGatewayHeaders(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://unix/app/vaultwarden/", nil)
	request.Host = "internal.socket"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "nas.example.test:5667")

	domain, err := publicDomain(request, "/app/vaultwarden")
	if err != nil {
		t.Fatal(err)
	}
	if domain != "https://nas.example.test:5667/app/vaultwarden" {
		t.Fatalf("unexpected public domain: %s", domain)
	}
}

func TestEnvironmentWithDomainReplacesOldValue(t *testing.T) {
	environment := environmentWithDomain([]string{"PORT=8222", "DOMAIN=https://old.invalid"}, "https://nas/app/vaultwarden")
	if slices.Contains(environment, "DOMAIN=https://old.invalid") {
		t.Fatal("old DOMAIN was retained")
	}
	if !slices.Contains(environment, "DOMAIN=https://nas/app/vaultwarden") {
		t.Fatal("new DOMAIN was not injected")
	}
}

func TestDomainCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-domain")
	domain := "https://nas.example.test/app/vaultwarden"
	if err := writeDomainCache(path, domain); err != nil {
		t.Fatal(err)
	}
	if got := readDomainCache(path, "/app/vaultwarden"); got != domain {
		t.Fatalf("unexpected cached domain: %s", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("unexpected cache permissions: %o", info.Mode().Perm())
	}
}

func TestSyncConfigDomainPreservesOtherSettings(t *testing.T) {
	dataFolder := t.TempDir()
	configPath := filepath.Join(dataFolder, "config.json")
	initial := []byte(`{"domain":"https://old.invalid","signups_allowed":false,"smtp_host":"mail.example.test"}`)
	if err := os.WriteFile(configPath, initial, 0600); err != nil {
		t.Fatal(err)
	}
	newDomain := "https://nas.example.test/app/vaultwarden"
	if err := syncConfigDomain(dataFolder, newDomain); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	if values["domain"] != newDomain {
		t.Fatalf("domain was not updated: %v", values["domain"])
	}
	if values["signups_allowed"] != false || values["smtp_host"] != "mail.example.test" {
		t.Fatalf("unrelated settings changed: %#v", values)
	}
}
