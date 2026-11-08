### Structure

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
  "domain_resolver": "", // or {}
  "network_strategy": "default",
  "network_type": [],
  "fallback_network_type": [],
  "fallback_delay": "300ms",
  "tcp_keep_alive": "5m",
  "tcp_keep_alive_interval": "75s",
  "tcp_keep_alive_count": 0,
  "disable_tcp_keep_alive": false,

  // Deprecated

  "domain_strategy": "prefer_ipv6"
}
```

### Fields

**Migration:** `host`, `path`, and `with_at` have moved from `override_transport` to `override_dialer`. Move these fields in existing configurations; XHTTP download overrides remain in `override_transport`.

`detour` `bind_interface` `inet4_bind_address` `inet6_bind_address` `routing_mark` `reuse_addr` `connect_timeout` `tcp_fast_open` `tcp_multi_path` `udp_gso` `udp_fragment` `domain_resolver` `network_strategy` `network_type` `fallback_network_type` `fallback_delay` `tcp_keep_alive` `tcp_keep_alive_interval` `tcp_keep_alive_count` `disable_tcp_keep_alive` `domain_strategy` see [Dial Fields](/configuration/shared/dial).

For local subscriptions in standard sing-box JSON, effective overrides are written back to the source file. Other nodes and fields are preserved, but JSON formatting may change. Original values are replaced and are not restored when overrides are removed; back up the source first. Other formats (including commented JSON, JSON with duplicate keys, Clash, and share links) are overridden in memory only, without rewriting the source, and a warning is logged.

#### host

Override the Host of subscription nodes using VMess, VLESS, or Trojan with WebSocket, HTTP (including H2), HTTPUpgrade, or XHTTP transport, and CNS and HTTP outbounds. Other headers are preserved; nodes without a supported Host field are unchanged. A Host is set even if the node did not previously specify one. The Host of a separate XHTTP `download_settings` connection is not changed.

This applies to local and remote providers; inline providers do not support overrides. CNS nodes must be in sing-box subscription format.

This does not change TLS SNI. To override it separately, set `"override_tls": {"server_name": "v.qq.com"}`; this only applies to nodes with existing TLS options.

#### path

Override `path` on HTTP outbounds in local or remote subscriptions. This is a suffix appended to the HTTP/1 CONNECT request target: for example, `"@gw.alicdn.com"` produces `CONNECT destination:port@gw.alicdn.com HTTP/1.1`. It does not change other protocols' transport paths, the Host header, or `del_host`.

When omitted, the subscription value is preserved; an empty string clears it. After clearing it, any `With-At` or `Baidu-Direct` special headers on the node still take effect. A nonempty suffix requires HTTP/1 and is incompatible with an explicitly configured HTTP/2 or HTTP/3 version. The suffix must not contain spaces or control characters.

#### with_at

Override the `With-At` special header on HTTP outbounds in local or remote subscriptions. Supply the value without `@`: for example, `"gw.alicdn.com"` appends `@gw.alicdn.com` to the HTTP/1 CONNECT target. `With-At` itself is not sent as a header. Other headers and `del_host` are unchanged. In saved subscription JSON, `With-At` is placed before other fields in `headers`; JSON field order does not affect the actual request.

When omitted, the original value is preserved; an empty string clears it. A nonempty node `path` takes precedence; when `path` is empty, `Baidu-Direct: true` also takes precedence. Ensure these do not conflict if `with_at` should take effect. A nonempty value requires HTTP/1 and is incompatible with explicitly configured HTTP/2 or HTTP/3. It must not contain spaces or control characters.
