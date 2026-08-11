#!/bin/bash

STATUS_FILE="/var/apps/vaultwarden/var/update-status.env"

html_escape() {
  printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g'
}

read_value() {
  sed -n "s/^$1=//p" "${STATUS_FILE}" 2>/dev/null | head -n 1
}

current_version="$(read_value CURRENT_VERSION)"
latest_version="$(read_value LATEST_VERSION)"
update_available="$(read_value UPDATE_AVAILABLE)"
fpk_url="$(read_value FPK_URL)"
fpk_sha256="$(read_value SHA256)"
checked_at="$(read_value CHECKED_AT)"

echo "Content-Type: text/html; charset=utf-8"
echo ""
cat <<'HTML'
<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Vaultwarden 更新</title><style>body{font:15px system-ui;margin:40px;max-width:720px;color:#182230}main{border:1px solid #d8dee8;border-radius:12px;padding:24px}dt{color:#667085;margin-top:14px}dd{margin:4px 0 0;font-weight:600}.new{color:#067647}.ok{color:#344054}a{color:#175cd3}</style></head><body><main><h1>Vaultwarden 更新状态</h1><dl>
HTML
printf '<dt>当前版本</dt><dd>%s</dd>\n' "$(html_escape "${current_version:-未知}")"
printf '<dt>最新版本</dt><dd>%s</dd>\n' "$(html_escape "${latest_version:-尚未检查}")"
printf '<dt>检查时间</dt><dd>%s</dd>\n' "$(html_escape "${checked_at:-尚未检查}")"
if [ -n "${fpk_sha256}" ]; then
  printf '<dt>FPK SHA-256</dt><dd style="word-break:break-all">%s</dd>\n' "$(html_escape "${fpk_sha256}")"
fi
echo '</dl>'
if [ "${update_available}" = "true" ] && [ -n "${fpk_url}" ]; then
  printf '<p class="new">发现新版本。<a target="_blank" rel="noopener noreferrer" href="%s">下载 FPK</a></p>\n' "$(html_escape "${fpk_url}")"
else
  echo '<p class="ok">当前没有检测到可用更新。</p>'
fi
echo '<p>更新不会被静默安装。下载后请通过 fnOS 应用中心完成升级。</p></main></body></html>'
