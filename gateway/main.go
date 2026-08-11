package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type config struct {
	socketPath string
	upstream   *url.URL
	prefix     string
}

func parseConfig() (config, error) {
	var socketPath string
	var upstreamRaw string
	var prefix string
	flag.StringVar(&socketPath, "socket", "", "Unix socket path exposed to the fnOS gateway")
	flag.StringVar(&upstreamRaw, "upstream", "http://127.0.0.1:8222", "Vaultwarden upstream URL")
	flag.StringVar(&prefix, "prefix", "/app/vaultwarden", "gateway path prefix")
	flag.Parse()

	if socketPath == "" || !filepath.IsAbs(socketPath) {
		return config{}, errors.New("socket must be an absolute path")
	}
	upstream, err := url.Parse(upstreamRaw)
	if err != nil || upstream.Scheme != "http" || upstream.Host == "" {
		return config{}, fmt.Errorf("invalid HTTP upstream %q", upstreamRaw)
	}
	if !strings.HasPrefix(prefix, "/") || prefix == "/" || strings.HasSuffix(prefix, "/") {
		return config{}, fmt.Errorf("prefix must look like /app/name without a trailing slash")
	}
	return config{socketPath: socketPath, upstream: upstream, prefix: prefix}, nil
}

func newReverseProxy(upstream *url.URL, prefix string) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalHost := request.Host
		request.URL.Path = ensurePrefix(request.URL.Path, prefix)
		if request.URL.RawPath != "" {
			request.URL.RawPath = ensurePrefix(request.URL.RawPath, prefix)
		}
		director(request)
		request.Host = originalHost
		request.Header.Set("X-Forwarded-Host", originalHost)
		request.Header.Set("X-Forwarded-Prefix", prefix)
		if request.Header.Get("X-Forwarded-Proto") == "" {
			request.Header.Set("X-Forwarded-Proto", "https")
		}
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, err error) {
		log.Printf("proxy error for %s %s: %v", request.Method, request.URL.Path, err)
		http.Error(writer, "Vaultwarden upstream unavailable", http.StatusBadGateway)
	}
	return proxy
}

func ensurePrefix(path, prefix string) string {
	if path == prefix || strings.HasPrefix(path, prefix+"/") {
		return path
	}
	if path == "" || path == "/" {
		return prefix + "/"
	}
	return prefix + "/" + strings.TrimPrefix(path, "/")
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path %s", path)
	}
	return os.Remove(path)
}

func run(cfg config) error {
	if err := removeStaleSocket(cfg.socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", cfg.socketPath)
	if err != nil {
		return fmt.Errorf("listen on unix socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(cfg.socketPath)
	if err := os.Chmod(cfg.socketPath, 0660); err != nil {
		return fmt.Errorf("chmod unix socket: %w", err)
	}

	server := &http.Server{
		Handler:           newReverseProxy(cfg.upstream, cfg.prefix),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("gateway shutdown: %v", err)
		}
	}()

	log.Printf("fnOS gateway listening on unix://%s -> %s, preserving prefix %s", cfg.socketPath, cfg.upstream, cfg.prefix)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func main() {
	cfg, err := parseConfig()
	if err != nil {
		log.Fatal(err)
	}
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}
