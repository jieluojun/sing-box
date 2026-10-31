### Structure

```json
{
  "host": "v.qq.com",
  "xhttp_download_enabled": true,
  "xhttp_download_detour": ""
}
```

### Fields

#### host

Override the Host of subscription nodes using VMess, VLESS, or Trojan with WebSocket, HTTP (including H2), HTTPUpgrade, or XHTTP transport, and CNS and HTTP outbounds. Other headers are preserved; nodes without a supported Host field are unchanged. A Host is set even if the node did not previously specify one. The Host of a separate XHTTP `download_settings` connection is not changed.

This applies to local and remote providers; inline providers do not support overrides. CNS nodes must be in sing-box subscription format.

This does not change TLS SNI. To override it separately, set `"override_tls": {"server_name": "v.qq.com"}`; this only applies to nodes with existing TLS options.

The following XHTTP download fields apply to nodes using XHTTP with `download_settings`.

#### xhttp_download_enabled

Enable or disable the separate download connection described by `download_settings`. When disabled, both directions use the upload connection.

#### xhttp_download_detour

Tag of the outbound used to dial the download connection.
