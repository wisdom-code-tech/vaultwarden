package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
