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
	packageVersion  string
}

func parseConfig() (config, error) {
	var socketPath string
	var upstreamRaw string
	var prefix string
	var vaultwardenPath string
	var packageVersion string
	flag.StringVar(&socketPath, "socket", "", "Unix socket path exposed to the fnOS gateway")
	flag.StringVar(&upstreamRaw, "upstream", "http://127.0.0.1:8222", "Vaultwarden upstream URL")
	flag.StringVar(&prefix, "prefix", "/app/vaultwarden", "gateway path prefix")
	flag.StringVar(&vaultwardenPath, "vaultwarden", "", "Vaultwarden executable managed by this gateway")
	flag.StringVar(&packageVersion, "package-version", "", "installed fnOS package version")
	flag.Parse()

	if socketPath == "" || !filepath.IsAbs(socketPath) {
		return config{}, errors.New("socket must be an absolute path")
	}
	if vaultwardenPath == "" || !filepath.IsAbs(vaultwardenPath) {
		return config{}, errors.New("vaultwarden must be an absolute path")
	}
	if _, err := parsePackageVersion(packageVersion); err != nil {
		return config{}, err
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
		packageVersion:  packageVersion,
	}, nil
}

func newReverseProxy(upstream *url.URL, prefix string) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalHost := request.Host
		request.URL.Path = stripPrefix(request.URL.Path, prefix)
		if request.URL.RawPath != "" {
			request.URL.RawPath = stripPrefix(request.URL.RawPath, prefix)
		}
		director(request)
		request.Host = originalHost
		request.Header.Set("X-Forwarded-Host", originalHost)
		request.Header.Set("X-Forwarded-Prefix", prefix)
		if request.Header.Get("X-Forwarded-Proto") == "" {
			request.Header.Set("X-Forwarded-Proto", "https")
		}
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		location := response.Header.Get("Location")
		if strings.HasPrefix(location, "/") && !strings.HasPrefix(location, "//") {
			response.Header.Set("Location", prefixGatewayPath(location, prefix))
		}
		cookies := response.Cookies()
		if len(cookies) > 0 {
			response.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				if cookie.Path == "" || cookie.Path == "/" {
					cookie.Path = prefix
				}
				response.Header.Add("Set-Cookie", cookie.String())
			}
		}
		return nil
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, err error) {
		log.Printf("proxy error for %s %s: %v", request.Method, request.URL.Path, err)
		http.Error(writer, "Vaultwarden upstream unavailable", http.StatusBadGateway)
	}
	return proxy
}

func stripPrefix(path, prefix string) string {
	if path == prefix {
		return "/"
	}
	if strings.HasPrefix(path, prefix+"/") {
		return strings.TrimPrefix(path, prefix)
	}
	return path
}

func prefixGatewayPath(path, prefix string) string {
	if path == prefix || strings.HasPrefix(path, prefix+"/") {
		return path
	}
	if path == "" || path == "/" {
		return prefix + "/"
	}
	return prefix + "/" + strings.TrimPrefix(path, "/")
}

type supervisor struct {
	mu              sync.Mutex
	vaultwardenPath string
	upstream        *url.URL
	command         *exec.Cmd
	done            chan error
}

func (s *supervisor) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.command != nil {
		select {
		case <-s.done:
			s.command = nil
			s.done = nil
		default:
			return nil
		}
	}
	if err := s.stopLocked(); err != nil {
		log.Printf("stop previous Vaultwarden process: %v", err)
	}

	command := exec.Command(s.vaultwardenPath)
	if err := removeConfigOverrides(os.Getenv("DATA_FOLDER")); err != nil {
		return fmt.Errorf("remove disabled Vaultwarden configuration: %w", err)
	}
	command.Env = environmentWithoutOverrides(os.Environ())
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = vaultwardenSysProcAttr()
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Vaultwarden: %w", err)
	}
	s.command = command
	s.done = make(chan error, 1)
	go func(done chan<- error) { done <- command.Wait() }(s.done)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-s.done:
			s.command = nil
			s.done = nil
			if err == nil {
				return fmt.Errorf("Vaultwarden exited before listening on %s", s.upstream.Host)
			}
			return fmt.Errorf("Vaultwarden exited before listening on %s: %w", s.upstream.Host, err)
		default:
		}
		connection, err := net.DialTimeout("tcp", s.upstream.Host, 500*time.Millisecond)
		if err == nil {
			connection.Close()
			log.Printf("Vaultwarden started without an explicit DOMAIN on %s", s.upstream.Host)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.stopLocked()
	return fmt.Errorf("Vaultwarden did not listen on %s within 20s", s.upstream.Host)
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
	return nil
}

func environmentWithoutOverrides(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, item := range environment {
		if strings.HasPrefix(item, "DOMAIN=") ||
			strings.HasPrefix(item, "ADMIN_TOKEN=") ||
			strings.HasPrefix(item, "DISABLE_ADMIN_TOKEN=") {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func removeConfigOverrides(dataFolder string) error {
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
	_, hasDomain := configValues["domain"]
	_, hasAdminToken := configValues["admin_token"]
	_, hasDisabledAdminToken := configValues["disable_admin_token"]
	if !hasDomain && !hasAdminToken && !hasDisabledAdminToken {
		return nil
	}
	delete(configValues, "domain")
	delete(configValues, "admin_token")
	delete(configValues, "disable_admin_token")
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
	}
	if err := supervisor.start(); err != nil {
		return err
	}

	proxy := newReverseProxy(cfg.upstream, cfg.prefix)
	server := &http.Server{
		Handler:           newManagementHandler(cfg, proxy),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	log.Printf("fnOS gateway listening on unix://%s and forwarding %s to the Vaultwarden root", cfg.socketPath, cfg.prefix)
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
