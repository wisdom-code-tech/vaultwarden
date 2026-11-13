"use strict";

const element = (id) => document.getElementById(id);
const statusButton = element("refresh-status");
const checkButton = element("check-updates");
let statusTimer;
let previousState;

async function getJSON(path) {
  const response = await fetch(path, { cache: "no-store", credentials: "same-origin" });
  if (response.status === 403) {
    throw new Error("仅 fnOS 管理员可访问，请使用管理员账号重新打开页面。");
  }
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
  checkButton.disabled = true;
  checkButton.textContent = "正在检查…";
  element("update-content").setAttribute("aria-busy", "true");
  element("update-title").textContent = "正在检查正式版本…";
  element("update-description").textContent = "正在连接 GitHub，请稍候。";
  element("update-error").hidden = true;
  element("release-actions").hidden = true;
  element("release-meta").hidden = true;
  try {
    const result = await getJSON("api/updates");
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
  } finally {
    checkButton.disabled = false;
    checkButton.textContent = "检查更新";
    element("update-content").setAttribute("aria-busy", "false");
  }
}

statusButton.addEventListener("click", refreshStatus);
checkButton.addEventListener("click", checkUpdates);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && !statusButton.disabled) refreshStatus();
});
refreshStatus();
