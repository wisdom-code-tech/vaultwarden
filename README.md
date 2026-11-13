# Vaultwarden for fnOS

这是 Vaultwarden 的飞牛 fnOS 原生 FPK 打包项目。应用进程直接运行在 fnOS 主机上，运行期不使用 Docker 或 Docker Compose。

## 设计

- Vaultwarden 固定为 `1.37.1`，从官方 `vaultwarden/server:1.37.1-alpine` 的 `linux/amd64` 镜像提取。
- 使用官方静态 PIE x86_64 产物和 Web Vault，不依赖 fnOS 的 glibc 版本。
- 使用静态 Go 代理监听 `${TRIM_APPDEST}/vaultwarden.sock`，通过 fnOS 统一网关转发到仅监听 `127.0.0.1:8222` 的 Vaultwarden。
- 密码库数据、附件、密钥和 SQLite 数据库保存在 `$TRIM_PKGVAR/data`。
- 应用配置保存在 `$TRIM_PKGETC/vaultwarden.env`，不会因升级而覆盖。
- 默认关闭新用户注册；管理后台和 `ADMIN_TOKEN` 均不启用。
- 可在 fnOS“应用设置 → 运行设置”中修改本地端口、注册开关和 WebSocket 通知；保存后自动重启生效。
- 卸载时选择“永久删除全部数据”会清空应用在当前卷的 `@appdata`、`@appconf`、`@apphome`、`@appmeta` 和 `@apptemp` 数据。
- 安装时若检测到残留的 `vaultwarden.env`，可选择应用本次安装设置或完整保留旧配置。

Vaultwarden 不设置显式 `DOMAIN`，默认在 `127.0.0.1:8222` 的根路径提供服务。端口隧道或反向代理的上游只需填写 `http://127.0.0.1:8222`；修改本地端口后使用对应的新端口。

统一网关访问前缀固定为 `/app/vaultwarden`。静态 Go 适配器会把 fnOS 转发到 Unix Socket 的公开前缀剥离，再转发到 Vaultwarden 根路径；WebSocket Upgrade 请求会原样透传。

## 构建

依赖：`bash`、`curl`、`jq`、`tar`、`file`、`Go >= 1.22`、`fnpack >= 1.2.1`。

```bash
make build
```

产物输出到 `dist/vaultwarden.fpk`。仅需重新校验时运行 `make validate`。

## GitHub 自动发布

工作流 `.github/workflows/build-latest.yml` 每天北京时间 **09:17** 检查上游稳定 Release（GitHub 调度可能延迟）。检查和构建是独立任务，没有新版本时跳过构建。

- **自动正式版**：上游稳定版高于当前正式版本时，自动构建不带 RC 的包。例如 `1.37.1 → 1.37.2`，生成 `vaultwarden-1.37.2-fnos.fpk`，发布 `v1.37.2` 并标为 GitHub **Latest**。
- **手动测试版**：在 Actions 页面手动触发，生成 `上游版本-rc修订号`（修订号来自 `PACKAGING_REVISION`）。标记为 **Pre-release**，明确禁止成为 Latest。同一上游版本有新的测试包时递增修订号。
- **手动正式版**：勾选“正式版”生成无 RC 的包。手动发布旧版本不会覆盖较新正式版的 Latest。
- **重复构建**：已存在相同标签时跳过；仅手动勾选 `force` 才覆盖该版本资产。
- **版本基线**：取源码 manifest 版本与已发布正式版版本号的最大值；忽略 RC，包括历史上被误标为正式版的 RC。不会因递增 `PACKAGING_REVISION` 自动构建 RC。

构建使用官方 `linux/amd64` 静态产物和 `fnpack 1.2.1`，发布 FPK 与 `SHA256SUMS`。

目前不发送邮件通知。

## 管理页面

fnOS 桌面新增「Vaultwarden 管理」iframe 入口，仅管理员可见；原有密码库入口保持不变。管理页路径为 `/app/vaultwarden/manage/`，复用现有 Go 网关和 Unix Socket，不额外开放服务端口。

- 显示 Vaultwarden `/alive` 健康检查结果、实际监听端口及系统传入的当前 FPK 版本；可见时每 15 秒刷新状态。
- 点击「检查更新」查询本仓库 GitHub Releases，按版本号选择最新正式版。过滤草稿、预发布和 RC 标签，并要求 FPK 与 `SHA256SUMS` 均已上传；不会把上游尚未打包的版本显示为可更新。
- 提供正式版 FPK、SHA256 校验文件和更新说明的 GitHub 下载链接；FPK 下载到当前浏览器所在设备，需要在飞牛应用中心手动安装。
- 管理页面及接口均校验统一网关注入的 `X-Trim-Isadmin: true`，普通用户不能通过直接访问 URL 绕过权限限制。

### 在 NAS 上下载并安装（开发中）

`1.37.5-rc13` 仅包含管理页面及手动下载。当前开发分支为后续测试版增加「下载并安装正式版」；尚未经过真实 fnOS 验证，不会自动启用 root 服务，也不启用无人值守更新。

应用和密码库继续以 `vaultwarden` 用户运行。管理员需要在安装包含此功能的 FPK 后，通过 SSH 单独启用升级辅助服务：

```bash
sudo bash /var/apps/vaultwarden/target/server/install-updater.sh
```

脚本把辅助程序安装到 root 管理的 `/usr/local/libexec/vaultwarden-updater`，由独立 systemd 服务运行。管理页面仅通过受文件权限保护的 Unix Socket 请求升级，不能传入安装包 URL、路径、版本或命令。

辅助服务每次重新选择最新正式 FPK，依次完成 SHA256、文件大小、应用身份、版本、架构和升级向导检查，再调用系统应用中心的原位升级接口，沿用现有存储卷。只包含说明文字的升级向导可以自动处理；需要填写表单的版本要求手动安装。升级过程独立于应用进程，网页断开不会重发安装任务。

系统接口的协议参考 [fnos-store 的实测实现](https://github.com/conversun/fnos-store/blob/main/internal/platform/rpc.go)，不属于飞牛公开 Open API。服务启用后仍需在目标 NAS 验证接口、权限、数据保留、停止重启和最终版本。接口不可用时保留手动下载，不调用卸载重装命令。

如果辅助服务在提交系统任务时丢失响应或重启，页面会显示结果待核实并阻止重复安装。管理员需先在系统应用中心确认任务结果；诊断日志：

```bash
sudo systemctl status vaultwarden-updater.service
sudo journalctl -u vaultwarden-updater.service -n 100 --no-pager
```

辅助服务独立安装，卸载 FPK 不会删除它。停止并取消开机启动：

```bash
sudo systemctl disable --now vaultwarden-updater.service
```

## 安装后

安装向导会要求管理员令牌和注册策略。启动日志位于：

```text
/var/apps/vaultwarden/var/vaultwarden.log
```

Vaultwarden Web Vault 的 WebCrypto 要求安全上下文。生产使用时必须由隧道、反向代理或 fnOS 统一网关提供 HTTPS；不要把 Vaultwarden 后端端口直接暴露到互联网。

## 更新上游版本

更新 `scripts/fetch-upstream.sh` 中的版本和固定镜像清单摘要，同时更新 `packaging/vaultwarden/manifest` 的版本。不要使用浮动的 `latest` 标签。

Vaultwarden 为非官方 Bitwarden 兼容服务端。本项目不隶属于 Bitwarden Inc.。上游许可证见 `packaging/vaultwarden/LICENSE`。
