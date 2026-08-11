package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestServeWaitsForVaultwardenShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.NotFoundHandler()}
	ctx, cancel := context.WithCancel(context.Background())
	stopStarted := make(chan struct{})
	releaseStop := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- serveUntilDone(ctx, server, listener, func() error {
			close(stopStarted)
			<-releaseStop
			return nil
		})
	}()
	cancel()

	select {
	case <-stopStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Vaultwarden shutdown was not started")
	}
	select {
	case err := <-done:
		t.Fatalf("gateway returned before Vaultwarden stopped: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseStop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gateway did not return after Vaultwarden stopped")
	}
}

func TestReverseProxyStripsGatewayPrefix(t *testing.T) {
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
	if forwarded.URL.Path != "/api/sync" {
		t.Fatalf("gateway prefix was not stripped: %s", forwarded.URL.Path)
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

func TestReverseProxyKeepsRootPath(t *testing.T) {
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
	if path := <-receivedPath; path != "/identity/connect/token" {
		t.Fatalf("root path was changed: %s", path)
	}
}

func TestReverseProxyPrefixesRedirectsAndCookies(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.SetCookie(writer, &http.Cookie{Name: "session", Value: "value", Path: "/", HttpOnly: true})
		http.Redirect(writer, request, "/admin", http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(newReverseProxy(upstreamURL, "/app/vaultwarden"))
	defer proxy.Close()
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(proxy.URL + "/app/vaultwarden/admin")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if location := response.Header.Get("Location"); location != "/app/vaultwarden/admin" {
		t.Fatalf("unexpected redirect location: %s", location)
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Path != "/app/vaultwarden" {
		t.Fatalf("unexpected cookies: %#v", cookies)
	}
}

func TestEnvironmentWithoutDomainRemovesOldValue(t *testing.T) {
	environment := environmentWithoutDomain([]string{"PORT=8222", "DOMAIN=https://old.invalid"})
	if slices.Contains(environment, "DOMAIN=https://old.invalid") {
		t.Fatal("old DOMAIN was retained")
	}
	if !slices.Contains(environment, "PORT=8222") {
		t.Fatal("unrelated environment was removed")
	}
}

func TestRemoveConfigDomainPreservesOtherSettings(t *testing.T) {
	dataFolder := t.TempDir()
	configPath := filepath.Join(dataFolder, "config.json")
	initial := []byte(`{"domain":"https://old.invalid","signups_allowed":false,"smtp_host":"mail.example.test"}`)
	if err := os.WriteFile(configPath, initial, 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeConfigDomain(dataFolder); err != nil {
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
	if _, exists := values["domain"]; exists {
		t.Fatalf("domain was retained: %v", values["domain"])
	}
	if values["signups_allowed"] != false || values["smtp_host"] != "mail.example.test" {
		t.Fatalf("unrelated settings changed: %#v", values)
	}
}
