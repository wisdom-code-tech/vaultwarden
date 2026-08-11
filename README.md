# Vaultwarden for fnOS

这是 Vaultwarden 的飞牛 fnOS 原生 FPK 打包项目。应用进程直接运行在 fnOS 主机上，运行期不使用 Docker 或 Docker Compose。

## 设计

- Vaultwarden 固定为 `1.37.1`，从官方 `vaultwarden/server:1.37.1-alpine` 的 `linux/amd64` 镜像提取。
- 使用官方静态 PIE x86_64 产物和 Web Vault，不依赖 fnOS 的 glibc 版本。
- 使用静态 Go 代理监听 `${TRIM_APPDEST}/vaultwarden.sock`，通过 fnOS 统一网关转发到仅监听 `127.0.0.1:8222` 的 Vaultwarden。
- 密码库数据、附件、密钥和 SQLite 数据库保存在 `$TRIM_PKGVAR/data`。
- 应用配置保存在 `$TRIM_PKGETC/vaultwarden.env`，不会因升级而覆盖。
- 默认关闭新用户注册；管理入口为 `/admin`。

Vaultwarden 不设置显式 `DOMAIN`，启动后立即在 `127.0.0.1:8222` 的根路径提供服务。端口隧道或反向代理的上游只需填写 `http://127.0.0.1:8222`。

统一网关访问前缀固定为 `/app/vaultwarden`。静态 Go 适配器会把 fnOS 转发到 Unix Socket 的公开前缀剥离，再转发到 Vaultwarden 根路径；WebSocket Upgrade 请求会原样透传。

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
3. 创建 GitHub Release，上传 FPK 和 `SHA256SUMS`。
4. 同版本需要发布新的打包实现时，递增根目录的 `PACKAGING_REVISION`。例如上游 `1.37.1`、修订号 `2` 会生成 FPK 版本 `1.37.1-rc2`，确保 fnOS 能区分同一上游版本的不同包。

也可以从 Actions 页面手动指定 Vaultwarden 版本。

## 安装后

安装向导会要求管理员令牌和注册策略。启动日志位于：

```text
/var/apps/vaultwarden/var/vaultwarden.log
```

Vaultwarden Web Vault 的 WebCrypto 要求安全上下文。生产使用时必须由隧道、反向代理或 fnOS 统一网关提供 HTTPS；不要把 Vaultwarden 后端端口直接暴露到互联网。

## 更新上游版本

更新 `scripts/fetch-upstream.sh` 中的版本和固定镜像清单摘要，同时更新 `packaging/vaultwarden/manifest` 的版本。不要使用浮动的 `latest` 标签。

Vaultwarden 为非官方 Bitwarden 兼容服务端。本项目不隶属于 Bitwarden Inc.。上游许可证见 `packaging/vaultwarden/LICENSE`。
