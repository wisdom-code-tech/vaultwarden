# Vaultwarden for fnOS

这是 Vaultwarden 的飞牛 fnOS 原生 FPK 打包项目。应用进程直接运行在 fnOS 主机上，运行期不使用 Docker 或 Docker Compose。

## 设计

- Vaultwarden 默认版本由 `VAULTWARDEN_VERSION` 指定，当前为 `1.37.5`，从官方 `vaultwarden/server:1.37.5-alpine` 的 `linux/amd64` 镜像提取。
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

- **自动正式版**：上游稳定版高于已发布正式包的上游版本时自动构建，发布 `v上游版本-fnos修订号` 并设为 GitHub **Latest**。
- **手动测试版**：手动触发默认发布 `v上游版本-rc修订号`，标记为 **Pre-release**，不进入 Latest；FPK 内部同样使用三段数字版本。
- **手动正式修订**：递增 `PACKAGING_REVISION` 后，勾选“正式版”可发布同一上游的新打包修订。旧版本或较低修订不会覆盖新正式包的 Latest。
- **重复构建**：相同标签已存在时跳过；仅手动勾选 `force` 才覆盖该标签资产。修订号有效范围为 `1–499`，超过范围构建会明确失败。
- **定时检查**：只比较独立的上游版本，忽略 RC 和打包修订变化，避免将 `1.37.5515` 误认为上游版本。

### 版本规则与旧版迁移

上游版本与 FPK 版本分开记录。`VAULTWARDEN_VERSION` 表示上游源码版本，`PACKAGING_REVISION` 表示本项目打包修订；构建时自动生成 `server/PACKAGE.json`，管理页面分别显示 Vaultwarden 和 FPK 版本。

对上游 `主.次.补丁`，FPK 第三段按以下规则生成：

- 测试版：`补丁 × 1000 + 修订号`。
- 正式版：`补丁 × 1000 + 500 + 修订号`。

| 上游版本 | 渠道与修订 | FPK 版本 | Release 标签 |
| --- | --- | --- | --- |
| 1.37.5 | RC 14 | 1.37.5014 | v1.37.5-rc14 |
| 1.37.5 | 正式修订 15 | 1.37.5515 | v1.37.5-fnos15 |
| 1.37.6 | RC 16 | 1.37.6016 | v1.37.6-rc16 |

表中的 RC 14 为新规则示例；历史已发布的 RC 14 保持原样。每个上游版本的全部 RC 都低于其正式包，下一上游补丁的包版本高于上一补丁的全部修订。发布文件名使用实际 FPK 版本，例如 `vaultwarden-1.37.5515-fnos.fpk`。

**已安装旧 `1.37.5-rc14` 或 `1.37.5` 的用户：**从新 Release 下载 FPK，在飞牛「应用中心 → 手动安装」直接选择新包覆盖升级，无需卸载。旧更新器不识别新标签，首次迁移需要手动安装；之后的更新器按实际 FPK 数字版本比较，不再把同一上游的旧裸版本当作可安装升级。历史 Release 和资产保留，不覆盖。

构建使用官方 `linux/amd64` 静态产物和 `fnpack 1.2.1`，发布 FPK 与 `SHA256SUMS`。

目前不发送邮件通知。

## 管理页面

fnOS 桌面只显示「Vaultwarden」iframe 入口，仅管理员可见，默认打开管理页，通过页面内的「打开密码库」进入 Vaultwarden。管理页路径为 `/app/vaultwarden/manage/`，统一网关注册前缀保持为 `/app/vaultwarden`，同时承载管理页和密码库，复用现有 Unix Socket，不额外开放服务端口。

- 显示 Vaultwarden `/alive` 健康检查结果、实际监听端口、上游程序版本和实际 FPK 包版本；可见时每 15 秒刷新状态。
- 点击「检查更新」查询本仓库 GitHub Releases，按版本号选择最新正式版。过滤草稿、预发布和 RC 标签，并要求 FPK 与 `SHA256SUMS` 均已上传；不会把上游尚未打包的版本显示为可更新。
- 提供正式版 FPK、SHA256 校验文件和更新说明的 GitHub 下载链接；FPK 下载到当前浏览器所在设备，需要在飞牛应用中心手动安装。
- 管理页面及接口均校验统一网关注入的 `X-Trim-Isadmin: true`，普通用户不能通过直接访问 URL 绕过权限限制。

### 在 NAS 上下载并安装

从 `1.37.5-rc14` 起，安装和升级 FPK 时自动部署系统升级助手，无需 SSH 安装。生命周期以 root 执行部署工作；网关、密码库和配置回调在读取 `vaultwarden.env` 之前切换至 `vaultwarden` 专用用户，网关也会拒绝以 root 启动。

升级助手位于 root 管理的 `/usr/local/libexec/vaultwarden-updater`，由独立 systemd 服务运行。应用代码目录由 root 持有，专用用户保留数据目录及网关 Socket 所需的写入权限。管理页面仅通过受文件权限保护的 Unix Socket 请求升级，不能传入安装包 URL、路径、版本或命令。

点击「下载并安装正式版」后，助手重新选择最新正式 FPK，完成 SHA256、文件大小、应用身份、版本、架构和升级向导检查，再调用系统应用中心的原位升级接口，沿用现有存储卷。只包含说明文字的升级向导可自动处理；需要填写表单的版本要求手动安装。未启用无人值守定时安装。

升级过程独立于应用进程，网页断开不会重发安装任务。更新助手本身时，生命周期原子替换程序并请求平滑重载；正在执行的升级完成后才重启助手，避免中断任务或与升级回调相互等待。卸载 FPK 时自动停止并移除助手，密码库数据按卸载向导选择保留或删除。

系统接口协议参考 [fnos-store 的实测实现](https://github.com/conversun/fnos-store/blob/main/internal/platform/rpc.go)，不属于飞牛公开 Open API。当前已覆盖模拟系统升级流程和 Linux 生命周期降权测试，仍需在目标 NAS 验证接口、权限、数据保留、停止重启和最终版本。接口不可用时保留手动下载，不调用卸载重装命令。

如果提交系统任务时丢失响应或助手异常重启，页面会显示结果待核实并阻止重复安装。管理员需先在系统应用中心确认任务结果。诊断日志：

```bash
sudo systemctl status vaultwarden-updater.service
sudo journalctl -u vaultwarden-updater.service -n 100 --no-pager
```

## 安装后

安装向导提供端口和注册策略设置。启动日志位于：

```text
/var/apps/vaultwarden/var/vaultwarden.log
```

Vaultwarden Web Vault 的 WebCrypto 要求安全上下文。生产使用时必须由隧道、反向代理或 fnOS 统一网关提供 HTTPS；不要把 Vaultwarden 后端端口直接暴露到互联网。

## 更新上游版本

更新 `VAULTWARDEN_VERSION` 和 `scripts/fetch-upstream.sh` 的固定镜像摘要，再运行 `./scripts/prepare-release.sh <上游版本> <修订号> stable` 同步包版本及元数据。不要把映射后的 FPK 版本用于镜像标签。

Vaultwarden 为非官方 Bitwarden 兼容服务端。本项目不隶属于 Bitwarden Inc.。上游许可证见 `packaging/vaultwarden/LICENSE`。
