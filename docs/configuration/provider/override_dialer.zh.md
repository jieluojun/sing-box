### 结构

```json
{
  "host": "v.qq.com",
  "path": "@gw.alicdn.com",
  "with_at": "gw.alicdn.com",
  "detour": "upstream-out",
  "bind_interface": "en0",
  "inet4_bind_address": "0.0.0.0",
  "inet6_bind_address": "::",
  "routing_mark": 1234,
  "reuse_addr": false,
  "connect_timeout": "5s",
  "tcp_fast_open": false,
  "tcp_multi_path": false,
  "udp_gso": true,
  "udp_fragment": false,
  "domain_resolver": "", // 或 {}
  "network_strategy": "default",
  "network_type": [],
  "fallback_network_type": [],
  "fallback_delay": "300ms",
  "tcp_keep_alive": "5m",
  "tcp_keep_alive_interval": "75s",
  "tcp_keep_alive_count": 0,
  "disable_tcp_keep_alive": false,

  // 废弃的

  "domain_strategy": "prefer_ipv6"
}
```

### 字段

**迁移提示：**`host`、`path` 和 `with_at` 已从 `override_transport` 移到 `override_dialer`，请将已有配置中的这三个字段移至新位置；XHTTP 下行覆写字段仍在 `override_transport`。

`detour` `bind_interface` `inet4_bind_address` `inet6_bind_address` `routing_mark` `reuse_addr` `connect_timeout` `tcp_fast_open` `tcp_multi_path` `udp_gso` `udp_fragment` `domain_resolver` `network_strategy` `network_type` `fallback_network_type` `fallback_delay` `tcp_keep_alive` `tcp_keep_alive_interval` `tcp_keep_alive_count` `disable_tcp_keep_alive` `domain_strategy` 详情参阅 [拨号字段](/zh/configuration/shared/dial)。

本地订阅使用标准 sing-box JSON 时，有实际变化的覆写会写回原文件；Clash YAML 的 `host`、`path`、`with_at` 也会写回。原有节点及字段的相对顺序保留，新增字段会追加；修改 HTTP 出站的请求头时，`With-At` 优先排在其他请求头之前。文件缩进和排版可能改变。原始值会被替换，移除覆写不会自动恢复，请事先备份。带锚点、别名、合并键或多个文档的 YAML、带注释或重复键的 JSON、Clash JSON 和分享链接仅在内存中覆写，不改写源文件，并会输出警告。其他覆写字段在 Clash YAML 中也只作用于内存。

#### host

统一覆写订阅节点的 Host，适用于 VMess、VLESS、Trojan 的 WebSocket、HTTP（包括 H2）、HTTPUpgrade 和 XHTTP 传输，以及 CNS 和 HTTP 出站。保留其他请求头，忽略不支持 Host 的节点；即使节点原本未设置 Host，也会写入新值。XHTTP 独立下行 `download_settings` 的 Host 不受影响。

此字段适用于本地和远程订阅，不支持内联 provider；CNS 节点须使用 sing-box 格式订阅。

此字段不会更改 TLS SNI；如需同时覆写，可设置 `"override_tls": {"server_name": "v.qq.com"}`。该覆写仅作用于已有 TLS 配置的节点。

#### path

覆写本地或远程订阅中所有 HTTP 出站节点的 `path`，作为 HTTP/1 CONNECT 请求目标的后缀。例如 `"@gw.alicdn.com"` 会产生 `CONNECT 目标地址:端口@gw.alicdn.com HTTP/1.1`。此字段不影响其他协议的传输路径、Host 请求头或 `del_host`。 对 Clash YAML 中 `type: http` 节点，写回的 `path` 是本核心的 HTTP CONNECT 扩展字段，其他 Clash 兼容内核不一定支持。

不设置时保留订阅原值；设置为空字符串可清除原 `path`。清除后，如果节点配置了 `With-At` 或 `Baidu-Direct` 特殊请求头，其原有逻辑仍会生效。非空后缀要求 HTTP/1，显式配置 HTTP/2 或 HTTP/3 的节点不兼容；后缀不能包含空格或控制字符。

#### with_at

覆写本地或远程订阅中 HTTP 出站的 `With-At` 特殊请求头。值无需包含 `@`，例如 `"gw.alicdn.com"` 会在 HTTP/1 CONNECT 目标后追加 `@gw.alicdn.com`；`With-At` 本身不会作为请求头发送。其他请求头和 `del_host` 不变。保存订阅节点 JSON 时，`With-At` 会排在 `headers` 中的其他字段之前；JSON 字段顺序不会影响实际请求。

不设置时保留原值；空字符串清除 `With-At`。节点的 `path` 非空时优先使用 `path`；`path` 为空且 `Baidu-Direct` 为 `true` 时优先使用 `Baidu-Direct`。如需让 `with_at` 生效，请确保不存在这些冲突。非空值要求 HTTP/1，显式配置 HTTP/2 或 HTTP/3 的节点不兼容；不能包含空格或控制字符。
