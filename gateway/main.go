package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type config struct {
	socketPath      string
	upstream        *url.URL
	prefix          string
	vaultwardenPath string
	domainCachePath string
}

func parseConfig() (config, error) {
	var socketPath string
	var upstreamRaw string
	var prefix string
	var vaultwardenPath string
	var domainCachePath string
	flag.StringVar(&socketPath, "socket", "", "Unix socket path exposed to the fnOS gateway")
	flag.StringVar(&upstreamRaw, "upstream", "http://127.0.0.1:8222", "Vaultwarden upstream URL")
	flag.StringVar(&prefix, "prefix", "/app/vaultwarden", "gateway path prefix")
	flag.StringVar(&vaultwardenPath, "vaultwarden", "", "Vaultwarden executable managed by this gateway")
	flag.StringVar(&domainCachePath, "domain-cache", "", "path used to persist the detected public domain")
	flag.Parse()

	if socketPath == "" || !filepath.IsAbs(socketPath) {
		return config{}, errors.New("socket must be an absolute path")
	}
	if vaultwardenPath == "" || !filepath.IsAbs(vaultwardenPath) {
		return config{}, errors.New("vaultwarden must be an absolute path")
	}
	if domainCachePath == "" || !filepath.IsAbs(domainCachePath) {
		return config{}, errors.New("domain-cache must be an absolute path")
	}
	upstream, err := url.Parse(upstreamRaw)
	if err != nil || upstream.Scheme != "http" || upstream.Host == "" {
		return config{}, fmt.Errorf("invalid HTTP upstream %q", upstreamRaw)
	}
	if !strings.HasPrefix(prefix, "/") || prefix == "/" || strings.HasSuffix(prefix, "/") {
		return config{}, fmt.Errorf("prefix must look like /app/name without a trailing slash")
	}
	return config{
		socketPath:      socketPath,
		upstream:        upstream,
		prefix:          prefix,
		vaultwardenPath: vaultwardenPath,
		domainCachePath: domainCachePath,
	}, nil
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

func publicDomain(request *http.Request, prefix string) (string, error) {
	proto := firstForwardedValue(request.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		proto = "https"
	}
	if proto != "http" && proto != "https" {
		return "", fmt.Errorf("invalid forwarded protocol %q", proto)
	}
	host := firstForwardedValue(request.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = request.Host
	}
	if host == "" || strings.ContainsAny(host, "/\\\r\n\t ") {
		return "", fmt.Errorf("invalid forwarded host %q", host)
	}
	domain := (&url.URL{Scheme: proto, Host: host, Path: prefix}).String()
	return strings.TrimSuffix(domain, "/"), nil
}

func firstForwardedValue(value string) string {
	value, _, _ = strings.Cut(value, ",")
	return strings.TrimSpace(strings.ToLower(value))
}

type supervisor struct {
	mu              sync.Mutex
	vaultwardenPath string
	upstream        *url.URL
	prefix          string
	domainCachePath string
	command         *exec.Cmd
	done            chan error
	domain          string
}

func (s *supervisor) ensure(request *http.Request) error {
	domain, err := publicDomain(request, s.prefix)
	if err != nil {
		return err
	}
	return s.ensureDomain(domain)
}

func (s *supervisor) ensureDomain(domain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.command != nil {
		select {
		case <-s.done:
			s.command = nil
			s.done = nil
			s.domain = ""
		default:
			if s.domain == domain {
				return nil
			}
		}
	}
	if err := s.stopLocked(); err != nil {
		log.Printf("stop previous Vaultwarden process: %v", err)
	}

	command := exec.Command(s.vaultwardenPath)
	if err := syncConfigDomain(os.Getenv("DATA_FOLDER"), domain); err != nil {
		return fmt.Errorf("synchronize Vaultwarden config domain: %w", err)
	}
	command.Env = environmentWithDomain(os.Environ(), domain)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = vaultwardenSysProcAttr()
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Vaultwarden: %w", err)
	}
	s.command = command
	s.done = make(chan error, 1)
	s.domain = domain
	go func(done chan<- error) { done <- command.Wait() }(s.done)

	if err := waitForUpstream(s.upstream.Host, 20*time.Second); err != nil {
		s.stopLocked()
		return err
	}
	if err := writeDomainCache(s.domainCachePath, domain); err != nil {
		log.Printf("write gateway domain cache: %v", err)
	}
	log.Printf("Vaultwarden started with automatic DOMAIN=%s", domain)
	return nil
}

func (s *supervisor) stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked()
}

func (s *supervisor) stopLocked() error {
	if s.command == nil || s.command.Process == nil {
		s.command = nil
		s.done = nil
		s.domain = ""
		return nil
	}
	pid := s.command.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case err := <-s.done:
		if err != nil {
			log.Printf("Vaultwarden exited: %v", err)
		}
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
		}
	}
	s.command = nil
	s.done = nil
	s.domain = ""
	return nil
}

func environmentWithDomain(environment []string, domain string) []string {
	filtered := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, "DOMAIN=") {
			filtered = append(filtered, item)
		}
	}
	return append(filtered, "DOMAIN="+domain)
}

func waitForUpstream(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err == nil {
			connection.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("Vaultwarden did not listen on %s within %s", address, timeout)
}

func writeDomainCache(path, domain string) error {
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, []byte(domain+"\n"), 0600); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func syncConfigDomain(dataFolder, domain string) error {
	if dataFolder == "" {
		return nil
	}
	path := filepath.Join(dataFolder, "config.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var configValues map[string]any
	if err := json.Unmarshal(data, &configValues); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if existing, ok := configValues["domain"].(string); ok && strings.TrimSuffix(existing, "/") == domain {
		return nil
	}
	configValues["domain"] = domain
	updated, err := json.MarshalIndent(configValues, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, updated, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func readDomainCache(path, prefix string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	domain := strings.TrimSpace(string(data))
	parsed, err := url.Parse(domain)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || strings.TrimSuffix(parsed.Path, "/") != prefix {
		return ""
	}
	return strings.TrimSuffix(domain, "/")
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

func run(ctx context.Context, cfg config) error {
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

	supervisor := &supervisor{
		vaultwardenPath: cfg.vaultwardenPath,
		upstream:        cfg.upstream,
		prefix:          cfg.prefix,
		domainCachePath: cfg.domainCachePath,
	}
	if cachedDomain := readDomainCache(cfg.domainCachePath, cfg.prefix); cachedDomain != "" {
		if err := supervisor.ensureDomain(cachedDomain); err != nil {
			log.Printf("start with cached gateway domain: %v", err)
		}
	}

	proxy := newReverseProxy(cfg.upstream, cfg.prefix)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := supervisor.ensure(request); err != nil {
			log.Printf("derive gateway domain: %v", err)
			http.Error(writer, "Unable to initialize Vaultwarden", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(writer, request)
	})
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	log.Printf("fnOS gateway listening on unix://%s; DOMAIN will be derived automatically", cfg.socketPath)
	return serveUntilDone(ctx, server, listener, supervisor.stop)
}

func serveUntilDone(ctx context.Context, server *http.Server, listener net.Listener, stopVaultwarden func() error) error {
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()

	select {
	case err := <-serveDone:
		stopErr := stopVaultwarden()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		return errors.Join(err, stopErr)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := server.Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			log.Printf("gateway graceful shutdown: %v", shutdownErr)
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		stopErr := stopVaultwarden()
		serveErr := <-serveDone
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(shutdownErr, stopErr, serveErr)
	}
}

func main() {
	cfg, err := parseConfig()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}
