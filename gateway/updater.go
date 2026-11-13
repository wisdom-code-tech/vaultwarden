package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const updaterSocket = "/run/vaultwarden-updater/control.sock"
const appCenterSocket = "/var/run/com.trim.app.center.sock"
const installedManifest = "/var/apps/vaultwarden/manifest"
const updaterStateDir = "/var/lib/vaultwarden-updater"

type installState struct {
	Phase   string `json:"phase"`
	Version string `json:"version,omitempty"`
	TaskID  string `json:"taskId,omitempty"`
	Message string `json:"message,omitempty"`
}

type appCenterError struct {
	Code int
	Text string
}

func (e *appCenterError) Error() string {
	return fmt.Sprintf("应用中心返回 %d：%s", e.Code, e.Text)
}

func unixHTTPClient(socket string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
}

type appCenter struct{ client *http.Client }

func (a appCenter) call(ctx context.Context, route string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/rpc/v1/"+route, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("应用中心请求未确认，请勿重复提交：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("应用中心返回 HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Code *int            `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("应用中心响应格式异常：%w", err)
	}
	if envelope.Code == nil {
		return errors.New("应用中心响应缺少 code")
	}
	if *envelope.Code != 0 {
		return &appCenterError{Code: *envelope.Code, Text: envelope.Msg}
	}
	if out != nil {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("应用中心任务数据异常：%w", err)
		}
	}
	return nil
}

func (a appCenter) probe(ctx context.Context) error {
	err := a.call(ctx, "update/info", struct{}{}, nil)
	var refusal *appCenterError
	if errors.As(err, &refusal) && refusal.Code == 10030 {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("应用中心升级接口未返回预期的参数校验结果")
}

func readManifest(reader io.Reader) (map[string]string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 65537))
	if err != nil {
		return nil, err
	}
	if len(data) > 65536 {
		return nil, errors.New("manifest 超出大小限制")
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values, nil
}

func currentInstalledVersion(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	manifest, err := readManifest(file)
	if err != nil {
		return "", err
	}
	if manifest["appname"] != "vaultwarden" {
		return "", errors.New("已安装应用的 appname 不匹配")
	}
	version := manifest["version"]
	_, err = parsePackageVersion(version)
	return version, err
}

func validateUpdatePackage(path, version string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	manifestFound, wizardFound := false, false
	for {
		header, err := archive.Next()
		if err != nil {
			return fmt.Errorf("FPK 缺少有效 manifest：%w", err)
		}
		name := strings.TrimPrefix(header.Name, "./")
		if name != "manifest" && name != "wizard/upgrade" {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return fmt.Errorf("FPK %s 不是普通文件", name)
		}
		if name == "wizard/upgrade" {
			var pages []struct {
				Items []struct {
					Type string `json:"type"`
				} `json:"items"`
			}
			if err := json.NewDecoder(io.LimitReader(archive, 65536)).Decode(&pages); err != nil {
				return fmt.Errorf("升级向导格式异常：%w", err)
			}
			for _, page := range pages {
				for _, item := range page.Items {
					if item.Type != "tips" {
						return errors.New("该版本需要填写升级向导，请使用飞牛应用中心手动安装")
					}
				}
			}
			wizardFound = true
		} else {
			manifest, err := readManifest(archive)
			if err != nil {
				return err
			}
			if manifest["appname"] != "vaultwarden" || manifest["version"] != version || manifest["platform"] != "x86" {
				return errors.New("FPK 应用名称、版本或架构不匹配")
			}
			manifestFound = true
		}
		if manifestFound && wizardFound {
			return nil
		}
	}
}

func downloadUpdate(ctx context.Context, client *http.Client, release packageRelease, directory string) (string, error) {
	get := func(url string) (*http.Response, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return nil, fmt.Errorf("下载失败：HTTP %d", response.StatusCode)
		}
		return response, nil
	}
	checksums, err := get(release.ChecksumsURL)
	if err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(checksums.Body, 1<<20))
	checksums.Body.Close()
	if err != nil {
		return "", err
	}
	filename := "vaultwarden-" + release.Version + "-fnos.fpk"
	expected := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == filename {
			if expected != "" {
				return "", errors.New("SHA256SUMS 存在重复的包校验记录")
			}
			expected = strings.ToLower(fields[0])
		}
	}
	if decoded, err := hex.DecodeString(expected); err != nil || len(decoded) != sha256.Size {
		return "", errors.New("SHA256SUMS 缺少有效的目标包校验值")
	}
	response, err := get(release.DownloadURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	path := filepath.Join(directory, filename)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, release.Size+1))
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	if size != release.Size || hex.EncodeToString(hash.Sum(nil)) != expected {
		return "", errors.New("FPK 大小或 SHA256 校验失败，已阻止安装")
	}
	if err := validateUpdatePackage(path, release.Version); err != nil {
		return "", err
	}
	return path, nil
}

type updater struct {
	mu           sync.Mutex
	state        installState
	directory    string
	manifestPath string
	center       appCenter
	client       *http.Client
	releasesURL  string
}

func (u *updater) snapshot() installState {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.state
}

func (u *updater) record(state installState) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	path := filepath.Join(u.directory, "status.json")
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return err
	}
	u.state = state
	return nil
}

func installBusy(phase string) bool {
	return phase != "idle" && phase != "complete" && phase != "failed"
}

func (u *updater) start() error {
	u.mu.Lock()
	if installBusy(u.state.Phase) {
		u.mu.Unlock()
		return errors.New("已有升级任务进行中或结果待确认，请勿重复提交")
	}
	u.state = installState{Phase: "checking", Message: "正在检查正式版本与升级权限"}
	u.mu.Unlock()
	go u.run()
	return nil
}

func (u *updater) run() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if err := u.perform(ctx); err != nil {
		state := u.snapshot()
		if state.Phase == "submitting" || state.Phase == "installing" {
			state.Phase = "unknown"
		} else {
			state.Phase = "failed"
		}
		state.Message = err.Error()
		if saveErr := u.record(state); saveErr != nil {
			// 保留内存中的忙碌状态，禁止在持久化失败后重复提交升级。
			fmt.Fprintf(os.Stderr, "保存升级状态失败: %v; 原错误: %v\n", saveErr, err)
		}
	}
}

func (u *updater) perform(ctx context.Context) error {
	if err := u.center.probe(ctx); err != nil {
		return err
	}
	current, err := currentInstalledVersion(u.manifestPath)
	if err != nil {
		return err
	}
	m := management{cfg: config{packageVersion: current}, client: u.client, releasesURL: u.releasesURL}
	update, err := m.checkUpdates(ctx)
	if err != nil {
		return err
	}
	if !update.Available {
		return errors.New("没有高于当前已安装版本的正式 FPK")
	}
	release := *update.Release
	state := installState{Phase: "downloading", Version: release.Version, Message: "正在下载并校验正式版 FPK"}
	if err := u.record(state); err != nil {
		return err
	}
	directory, err := os.MkdirTemp(u.directory, "package-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	path, err := downloadUpdate(ctx, u.client, release, directory)
	if err != nil {
		return err
	}
	state.Phase, state.Message = "staging", "正在让飞牛应用中心校验安装包"
	if err := u.record(state); err != nil {
		return err
	}
	var stageTask struct {
		ID string `json:"downloadTaskId"`
	}
	if err := u.center.call(ctx, "download/task", map[string]string{"packageSourceType": "file", "path": path}, &stageTask); err != nil {
		return err
	}
	if stageTask.ID == "" {
		return errors.New("应用中心未返回包暂存任务 ID")
	}
	var staged struct {
		Status      int    `json:"status"`
		Message     string `json:"message"`
		AppName     string `json:"appName"`
		Version     string `json:"version"`
		PackageType string `json:"packageType"`
		Installed   bool   `json:"installed"`
	}
	stageCtx, cancelStage := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelStage()
	for {
		if err := u.center.call(stageCtx, "download/status", map[string]string{"downloadTaskId": stageTask.ID}, &staged); err != nil {
			return err
		}
		if staged.Status == 2 {
			break
		}
		if staged.Status != 1 {
			return fmt.Errorf("安装包暂存失败：%s（状态 %d）", staged.Message, staged.Status)
		}
		if err := waitUpdatePoll(stageCtx); err != nil {
			return err
		}
	}
	if !staged.Installed || staged.AppName != "vaultwarden" || staged.Version != release.Version || staged.PackageType == "" {
		return errors.New("应用中心识别的包身份或已安装状态不匹配")
	}
	var info struct {
		Wizard struct {
			Volume int `json:"installedVolumeID"`
		} `json:"wizardInfo"`
	}
	identity := map[string]any{"appName": "vaultwarden", "updateVersion": release.Version, "packageType": staged.PackageType, "language": "zh-CN"}
	if err := u.center.call(ctx, "update/info", identity, &info); err != nil {
		return err
	}
	if info.Wizard.Volume <= 0 {
		return errors.New("无法确认应用原有存储卷，已中止升级")
	}
	installedNow, err := currentInstalledVersion(u.manifestPath)
	if err != nil {
		return err
	}
	if installedNow != current {
		return errors.New("下载期间应用版本已发生变化，请重新检查更新")
	}
	// 在提交前落盘。若响应丢失，绝不自动重发有副作用的升级请求。
	state.Phase, state.Message = "submitting", "正在提交系统原位升级任务"
	if err := u.record(state); err != nil {
		return err
	}
	identity["systemParameters"] = map[string]any{"agreedToProtocol": true, "installVolumeID": info.Wizard.Volume, "dataVolumeId": info.Wizard.Volume, "immediateStart": true}
	identity["customParameters"] = []any{}
	var task struct {
		ID string `json:"taskId"`
	}
	if err := u.center.call(ctx, "update/task", identity, &task); err != nil {
		return err
	}
	if task.ID == "" {
		return errors.New("应用中心未返回升级任务 ID，请在系统应用中心核对结果")
	}
	state.Phase, state.TaskID, state.Message = "installing", task.ID, "系统正在升级，密码库服务将短暂重启"
	if err := u.record(state); err != nil {
		return err
	}
	return u.monitor(ctx, state)
}

func waitUpdatePoll(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
		return nil
	}
}

func (u *updater) monitor(ctx context.Context, state installState) error {
	for {
		var status struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		}
		if err := u.center.call(ctx, "common/status", map[string]string{"taskId": state.TaskID}, &status); err != nil {
			return err
		}
		if status.Status == 2 {
			version, err := currentInstalledVersion(u.manifestPath)
			if err != nil {
				return err
			}
			if version != state.Version {
				return fmt.Errorf("系统任务已结束，但安装版本为 %s，目标为 %s，请核对系统应用中心", version, state.Version)
			}
			state.Phase, state.Message = "complete", "正式版已安装，系统已请求重新启动服务"
			return u.record(state)
		}
		if status.Status != 1 {
			return fmt.Errorf("系统升级状态 %d：%s，请在系统应用中心核对结果", status.Status, status.Message)
		}
		if err := waitUpdatePoll(ctx); err != nil {
			return err
		}
	}
}

func (u *updater) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { writeManagementJSON(w, http.StatusOK, u.snapshot()) })
	mux.HandleFunc("GET /capability", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		err := u.center.probe(ctx)
		reason := ""
		if err != nil {
			reason = err.Error()
		}
		writeManagementJSON(w, http.StatusOK, struct {
			Available bool   `json:"available"`
			Reason    string `json:"reason"`
		}{err == nil, reason})
	})
	mux.HandleFunc("POST /install", func(w http.ResponseWriter, r *http.Request) {
		if err := u.start(); err != nil {
			writeManagementJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeManagementJSON(w, http.StatusAccepted, u.snapshot())
	})
	return mux
}

func runUpdaterService(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return errors.New("升级辅助服务需要管理员单独安装并以 root 运行")
	}
	if err := os.MkdirAll(updaterStateDir, 0700); err != nil {
		return err
	}
	u := &updater{
		state: installState{Phase: "idle"}, directory: updaterStateDir, manifestPath: installedManifest,
		center: appCenter{client: unixHTTPClient(appCenterSocket, 30*time.Second)},
		client: &http.Client{Timeout: 15 * time.Minute}, releasesURL: "https://api.github.com/repos/" + releaseRepository + "/releases",
	}
	data, err := os.ReadFile(filepath.Join(updaterStateDir, "status.json"))
	if err == nil {
		if err := json.Unmarshal(data, &u.state); err != nil {
			return err
		}
		if u.state.Phase == "submitting" || u.state.Phase == "installing" || u.state.Phase == "unknown" {
			u.state.Phase, u.state.Message = "unknown", "辅助服务重启前的任务尚未核实，请管理员检查系统应用中心，勿重复安装"
			if err := u.record(u.state); err != nil {
				return err
			}
		} else if installBusy(u.state.Phase) {
			u.state.Phase, u.state.Message = "failed", "上一次下载准备被中断，尚未提交安装，可重新尝试"
			if err := u.record(u.state); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := removeStaleSocket(updaterSocket); err != nil {
		return err
	}
	listener, err := net.Listen("unix", updaterSocket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(updaterSocket)
	if err := os.Chmod(updaterSocket, 0660); err != nil {
		return err
	}
	server := &http.Server{Handler: u.handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	return serveUntilDone(ctx, server, listener, func() error { return nil })
}
