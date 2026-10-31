### 结构

```json
{
  "host": "v.qq.com",
  "path": "@gw.alicdn.com",
  "with_at": "gw.alicdn.com",
  "xhttp_download_enabled": true,
  "xhttp_download_detour": ""
}
```

### 字段

#### host

统一覆写订阅节点的 Host，适用于 VMess、VLESS、Trojan 的 WebSocket、HTTP（包括 H2）、HTTPUpgrade 和 XHTTP 传输，以及 CNS 和 HTTP 出站。保留其他请求头，忽略不支持 Host 的节点；即使节点原本未设置 Host，也会写入新值。XHTTP 独立下行 `download_settings` 的 Host 不受影响。

此字段适用于本地和远程订阅，不支持内联 provider；CNS 节点须使用 sing-box 格式订阅。

此字段不会更改 TLS SNI；如需同时覆写，可设置 `"override_tls": {"server_name": "v.qq.com"}`。该覆写仅作用于已有 TLS 配置的节点。

#### path

覆写本地或远程订阅中所有 HTTP 出站节点的 `path`，作为 HTTP/1 CONNECT 请求目标的后缀。例如 `"@gw.alicdn.com"` 会产生 `CONNECT 目标地址:端口@gw.alicdn.com HTTP/1.1`。此字段不影响其他协议的传输路径、Host 请求头或 `del_host`。

不设置时保留订阅原值；设置为空字符串可清除原 `path`。清除后，如果节点配置了 `With-At` 或 `Baidu-Direct` 特殊请求头，其原有逻辑仍会生效。非空后缀要求 HTTP/1，显式配置 HTTP/2 或 HTTP/3 的节点不兼容；后缀不能包含空格或控制字符。

#### with_at

覆写本地或远程订阅中 HTTP 出站的 `With-At` 特殊请求头。值无需包含 `@`，例如 `"gw.alicdn.com"` 会在 HTTP/1 CONNECT 目标后追加 `@gw.alicdn.com`；`With-At` 本身不会作为请求头发送。其他请求头和 `del_host` 不变。保存订阅节点 JSON 时，`With-At` 会排在 `headers` 中的其他字段之前；JSON 字段顺序不会影响实际请求。

不设置时保留原值；空字符串清除 `With-At`。节点的 `path` 非空时优先使用 `path`；`path` 为空且 `Baidu-Direct` 为 `true` 时优先使用 `Baidu-Direct`。如需让 `with_at` 生效，请确保不存在这些冲突。非空值要求 HTTP/1，显式配置 HTTP/2 或 HTTP/3 的节点不兼容；不能包含空格或控制字符。

以下 XHTTP 下行字段作用于已配置 `download_settings` 的节点。

#### xhttp_download_enabled

启用或关闭 `download_settings` 描述的独立下行连接。关闭后上下行都走上行连接。

#### xhttp_download_detour

拨号下行连接所用出站的标签。
