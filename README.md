# Vaultwarden for fnOS

这是 Vaultwarden 的飞牛 fnOS 原生 FPK 打包项目。应用进程直接运行在 fnOS 主机上，运行期不使用 Docker 或 Docker Compose。

## 设计

- Vaultwarden 固定为 `1.37.1`，从官方 `vaultwarden/server:1.37.1-alpine` 的 `linux/amd64` 镜像提取。
- 使用官方静态 PIE x86_64 产物和 Web Vault，不依赖 fnOS 的 glibc 版本。
- 使用静态 Go 代理监听 `${TRIM_APPDEST}/vaultwarden.sock`，通过 fnOS 统一网关转发到仅监听 `127.0.0.1:8222` 的 Vaultwarden。
- 密码库数据、附件、密钥和 SQLite 数据库保存在 `$TRIM_PKGVAR/data`。
- 应用配置保存在 `$TRIM_PKGETC/vaultwarden.env`，不会因升级而覆盖。
- 默认关闭新用户注册；管理入口为 `/admin`。

统一网关访问前缀固定为 `/app/vaultwarden`。安装、配置或从旧版升级时，需要填写完整地址，例如：

```text
https://nas.example.com/app/vaultwarden
```

代理会原样保留此前缀和 WebSocket Upgrade 请求；Vaultwarden 的 `DOMAIN` 必须与该地址一致。

## 构建

依赖：`bash`、`curl`、`jq`、`tar`、`file`、`Go >= 1.22`、`fnpack >= 1.2.1`。

```bash
make build
```

产物输出到 `dist/vaultwarden.fpk`。仅需重新校验时运行 `make validate`。

## GitHub 自动发布

工作流 `.github/workflows/build-latest.yml` 每小时检查一次 Vaultwarden 的最新稳定 Release；没有新版本时不会执行下载和构建：

1. 若当前“上游版本 + `PACKAGING_REVISION`”尚未发布，则提取官方 `linux/amd64` 静态产物。
2. 使用官方 `fnpack 1.2.1` 构建并校验 FPK。
3. 创建 GitHub Release，上传 FPK、`SHA256SUMS` 和供 NAS 读取的 `latest.env`。
4. 同版本需要发布新的打包实现时，递增根目录的 `PACKAGING_REVISION`。例如上游 `1.37.1`、修订号 `2` 会生成 FPK 版本 `1.37.1-rc2`，确保 fnOS 能区分同一上游版本的不同包。

也可以从 Actions 页面手动指定 Vaultwarden 版本。Action 会把当前 `owner/repository` 注入 FPK，因此无需在源码中硬编码你的 GitHub 地址。

## FPK 更新检查

由 GitHub Action 构建的 FPK 默认每天读取一次当前打包仓库最新 Release 的更新清单。检查结果保存在：

```text
/var/apps/vaultwarden/var/update-status.env
```

管理员可以从飞牛桌面的“Vaultwarden 更新状态”入口查看版本和 FPK 下载地址。检查器不会静默安装更新；下载后仍由 fnOS 应用中心验证并执行升级。普通本地构建没有 GitHub 仓库上下文，因此默认不启用远程检查。

## 安装后

安装向导会要求端口、外部访问地址、管理员令牌和注册策略。启动日志位于：

```text
/var/apps/vaultwarden/var/vaultwarden.log
```

Vaultwarden Web Vault 的 WebCrypto 要求安全上下文。生产使用时必须通过 fnOS 反向代理提供 HTTPS，并将向导中的外部访问地址设置为最终的 `https://` 地址；不要把 Vaultwarden 端口直接暴露到互联网。

## 更新上游版本

更新 `scripts/fetch-upstream.sh` 中的版本和固定镜像清单摘要，同时更新 `packaging/vaultwarden/manifest` 的版本。不要使用浮动的 `latest` 标签。

Vaultwarden 为非官方 Bitwarden 兼容服务端。本项目不隶属于 Bitwarden Inc.。上游许可证见 `packaging/vaultwarden/LICENSE`。
