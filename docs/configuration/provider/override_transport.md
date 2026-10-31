### Structure

```json
{
  "host": "v.qq.com",
  "path": "@gw.alicdn.com",
  "with_at": "gw.alicdn.com",
  "xhttp_download_enabled": true,
  "xhttp_download_detour": ""
}
```

### Fields

#### host

Override the Host of subscription nodes using VMess, VLESS, or Trojan with WebSocket, HTTP (including H2), HTTPUpgrade, or XHTTP transport, and CNS and HTTP outbounds. Other headers are preserved; nodes without a supported Host field are unchanged. A Host is set even if the node did not previously specify one. The Host of a separate XHTTP `download_settings` connection is not changed.

This applies to local and remote providers; inline providers do not support overrides. CNS nodes must be in sing-box subscription format.

This does not change TLS SNI. To override it separately, set `"override_tls": {"server_name": "v.qq.com"}`; this only applies to nodes with existing TLS options.

#### path

Override `path` on HTTP outbounds in local or remote subscriptions. This is a suffix appended to the HTTP/1 CONNECT request target: for example, `"@gw.alicdn.com"` produces `CONNECT destination:port@gw.alicdn.com HTTP/1.1`. It does not change other protocols' transport paths, the Host header, or `del_host`.

When omitted, the subscription value is preserved; an empty string clears it. After clearing it, any `With-At` or `Baidu-Direct` special headers on the node still take effect. A nonempty suffix requires HTTP/1 and is incompatible with an explicitly configured HTTP/2 or HTTP/3 version. The suffix must not contain spaces or control characters.

#### with_at

Override the `With-At` special header on HTTP outbounds in local or remote subscriptions. Supply the value without `@`: for example, `"gw.alicdn.com"` appends `@gw.alicdn.com` to the HTTP/1 CONNECT target. `With-At` itself is not sent as a header. Other headers and `del_host` are unchanged.

`"With-At"` can also be used as an alias for `"with_at"` in `override_transport`. If both are set, `with_at` takes precedence, including an empty string. When omitted, the original value is preserved; an empty string clears it. A nonempty node `path` takes precedence; when `path` is empty, `Baidu-Direct: true` also takes precedence. Ensure these do not conflict if `with_at` should take effect. A nonempty value requires HTTP/1 and is incompatible with explicitly configured HTTP/2 or HTTP/3. It must not contain spaces or control characters.

The following XHTTP download fields apply to nodes using XHTTP with `download_settings`.

#### xhttp_download_enabled

Enable or disable the separate download connection described by `download_settings`. When disabled, both directions use the upload connection.

#### xhttp_download_detour

Tag of the outbound used to dial the download connection.
