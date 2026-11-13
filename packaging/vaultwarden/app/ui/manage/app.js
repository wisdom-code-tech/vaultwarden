"use strict";

const element = (id) => document.getElementById(id);
const statusButton = element("refresh-status");
const checkButton = element("check-updates");
let statusTimer;
let previousState;
let updateAvailable = false;
let checkingUpdates = false;
let installerAvailable = false;
let installRunning = false;
let installTimer;

async function getJSON(path, options = {}) {
  const response = await fetch(path, { ...options, cache: "no-store", credentials: "same-origin" });
  const contentType = response.headers.get("content-type") || "";
  if (!contentType.includes("application/json")) {
    throw new Error("无法读取管理接口，请检查飞牛登录状态后重新打开页面。");
  }
  const result = await response.json();
  if (!response.ok) {
    throw new Error(result.error);
  }
  return result;
}

function announce(message) {
  element("announcement").textContent = message;
}

async function refreshStatus() {
  clearTimeout(statusTimer);
  statusButton.disabled = true;
  try {
    const status = await getJSON("api/status");
    const states = {
      healthy: ["运行正常", "服务健康检查通过"],
      unhealthy: ["服务异常", "服务已响应，但健康检查未通过"],
      unreachable: ["服务未响应", "无法连接 Vaultwarden 后端"],
    };
    const [title, detail] = states[status.state];
    element("service-state").dataset.state = status.state;
    element("state-text").textContent = title;
    element("state-detail").textContent = detail;
    element("service-port").textContent = status.port;
    element("service-address").textContent = `${status.address} · 仅本机监听`;
    element("package-version").textContent = status.version;
    element("version-channel").textContent = status.version.includes("-rc") ? "RC 测试版本" : "正式版本";
    element("status-error").hidden = true;
    element("status-time").textContent = `更新于 ${new Date().toLocaleTimeString("zh-CN", { hour12: false })} · 每 15 秒刷新`;
    if (previousState !== status.state) announce(`服务状态：${title}`);
    previousState = status.state;
  } catch (error) {
    element("service-state").dataset.state = "unknown";
    element("state-text").textContent = "状态未知";
    element("state-detail").textContent = "管理接口暂不可用";
    element("status-error").textContent = error.message;
    element("status-error").hidden = false;
    element("status-time").textContent = "未能刷新状态";
    previousState = "unknown";
  } finally {
    statusButton.disabled = false;
    statusTimer = setTimeout(() => {
      if (!document.hidden) refreshStatus();
    }, 15000);
  }
}

async function checkUpdates() {
  checkingUpdates = true;
  updateAvailable = false;
  syncInstallButton();
  checkButton.textContent = "正在检查…";
  element("update-content").setAttribute("aria-busy", "true");
  element("update-title").textContent = "正在检查正式版本…";
  element("update-description").textContent = "正在连接 GitHub，请稍候。";
  element("update-error").hidden = true;
  element("release-actions").hidden = true;
  element("release-meta").hidden = true;
  try {
    const result = await getJSON("api/updates");
    updateAvailable = result.available;
    syncInstallButton();
    const release = result.release;
    if (!release) {
      element("update-title").textContent = "暂无可下载的正式版";
      element("update-description").textContent = "仓库中尚无同时提供 FPK 与校验文件的正式发布。";
      announce("检查完成，暂无可下载的正式版。");
      return;
    }
    let title = "有新的正式版本可用";
    if (!result.available) {
      title = result.currentVersion === release.version ? "当前已是最新正式版" : "当前版本高于最新正式版";
    }
    element("update-title").textContent = title;
    element("update-description").textContent = `最新正式版 ${release.version} · 当前安装 ${result.currentVersion}`;
    const published = new Date(release.publishedAt);
    const date = Number.isNaN(published.getTime()) ? "未提供发布时间" : `发布于 ${published.toLocaleDateString("zh-CN")}`;
    element("release-meta").textContent = `${date} · ${(release.size / 1024 / 1024).toFixed(1)} MB · x86_64`;
    element("release-meta").hidden = false;
    element("download-fpk").href = release.downloadUrl;
    element("download-fpk").textContent = `下载 ${release.version} FPK`;
    element("download-checksums").href = release.checksumsUrl;
    element("release-notes").href = release.releaseUrl;
    element("release-actions").hidden = false;
    announce(`检查完成：${title}，最新正式版 ${release.version}。`);
  } catch (error) {
    element("update-title").textContent = "暂时无法检查更新";
    element("update-description").textContent = "当前安装的服务不受影响，可稍后重试。";
    element("update-error").textContent = error.message;
    element("update-error").hidden = false;
    updateAvailable = false;
    syncInstallButton();
  } finally {
    checkingUpdates = false;
    syncInstallButton();
    checkButton.textContent = "检查更新";
    element("update-content").setAttribute("aria-busy", "false");
  }
}

statusButton.addEventListener("click", refreshStatus);
checkButton.addEventListener("click", checkUpdates);

function syncInstallButton() {
  element("install-update").hidden = !installerAvailable || !updateAvailable;
  element("install-update").disabled = installRunning || checkingUpdates;
  checkButton.disabled = installRunning || checkingUpdates;
}

async function refreshInstaller() {
  element("refresh-installer").disabled = true;
  try {
    const result = await getJSON("api/installer");
    installerAvailable = result.available;
    if (result.available) {
      element("installer-message").textContent = "升级辅助服务已就绪。检查到新正式版后，可下载到 NAS 并完成安装。";
      await pollInstallation();
    } else {
      element("installer-message").textContent = `系统升级接口暂不可用：${result.reason}`;
    }
  } catch (error) {
    installerAvailable = false;
    element("installer-message").textContent = error.message;
  } finally {
    element("refresh-installer").disabled = false;
    syncInstallButton();
  }
}

async function pollInstallation() {
  clearTimeout(installTimer);
  try {
    const state = await getJSON("api/install-status");
    installRunning = !["idle", "complete", "failed"].includes(state.phase);
    if (state.phase !== "idle") element("installer-message").textContent = state.message;
    if (state.phase === "complete") {
      updateAvailable = false;
      refreshStatus();
    }
    syncInstallButton();
    if (installRunning && state.phase !== "unknown") {
      installTimer = setTimeout(pollInstallation, 2000);
    }
  } catch (error) {
    element("installer-message").textContent = installRunning
      ? "升级期间连接暂时中断，正在等待管理页面恢复；请勿重复提交安装。"
      : error.message;
    if (installRunning) installTimer = setTimeout(pollInstallation, 5000);
  }
}

async function installUpdate() {
  if (!window.confirm("确认下载并安装最新正式版，并同意升级包所附的应用协议？服务会短暂重启，现有数据与配置由飞牛应用中心保留。")) return;
  installRunning = true;
  syncInstallButton();
  element("installer-message").textContent = "正在提交安装请求…";
  try {
    await getJSON("api/install", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: "{}",
    });
    await pollInstallation();
  } catch (error) {
    element("installer-message").textContent = `${error.message}。正在核对任务状态，请勿重复点击安装。`;
    // POST 响应丢失不代表任务未创建，先查询状态，不自动重发安装。
    await pollInstallation();
  }
}

element("install-update").addEventListener("click", installUpdate);
element("refresh-installer").addEventListener("click", refreshInstaller);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && !statusButton.disabled) refreshStatus();
});
refreshStatus();
refreshInstaller();
