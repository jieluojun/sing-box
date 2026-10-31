`http` outbound is a HTTP CONNECT proxy client.

### Structure

```json
{
  "type": "http",
  "tag": "http-out",
  
  "server": "127.0.0.1",
  "server_port": 1080,
  "username": "sekai",
  "password": "admin",
  "path": "",
  "del_host": false,
  "headers": {},
  "network": "tcp",
  "version": 0,
  "disable_version_fallback": false,
  "tls": {},

  ... // HTTP2 Fields / QUIC Fields
  ... // Dial Fields
}
```

### Fields

#### server

==Required==

The server address.

#### server_port

==Required==

The server port.

#### username

Basic authorization username.

#### password

Basic authorization password.

#### path

Suffix appended to the HTTP/1 CONNECT target, for example `@example.com` or `/path`. This takes precedence over `With-At` and `Baidu-Direct`.

#### headers

Extra headers of HTTP request. `With-At` and `Baidu-Direct` are local HTTP/1 CONNECT options and are not sent upstream. `With-At` appends `@` and its value to the target; `Baidu-Direct: true` removes the space before `HTTP/1.1`. If both are set, Baidu-Direct takes precedence.

#### del_host

For HTTP/1, omit the default Host and User-Agent headers. A custom Host disables this behavior; a custom User-Agent is preserved.

#### network

Enabled network

One of `tcp` `udp`.

Both is enabled by default. UDP is relayed with CONNECT-UDP (MASQUE, RFC 9298), which plain HTTP proxy servers usually do not support: set `tcp` for them so that UDP connections are not routed here and groups filtering on `network` treat the node as TCP only.

Nodes parsed from Clash subscriptions are always TCP only, matching Clash.

#### version

!!! question "Since sing-box 1.15.0"

HTTP version.

Available values: `1`, `2`, `3`.

`2` is used by default, or `1` if `path`, `del_host`, `With-At`, `Baidu-Direct: true`, or the `Host` header is set.

These options are only available for `1`. `path` changes the CONNECT request target, rather than replacing it; this differs from the previous sing-box HTTP outbound behavior.

When `3`, [HTTP2 Fields](#http2-fields) are replaced by [QUIC Fields](#quic-fields).

#### disable_version_fallback

!!! question "Since sing-box 1.15.0"

Disable automatic fallback to lower HTTP version.

#### tls

TLS configuration, see [TLS](/configuration/shared/tls/#outbound).

### HTTP2 Fields

!!! question "Since sing-box 1.15.0"

When `version` is `2` (default).

See [HTTP2 Fields](/configuration/shared/http2/) for details.

### QUIC Fields

!!! question "Since sing-box 1.15.0"

When `version` is `3`.

See [QUIC Fields](/configuration/shared/quic/) for details.

### Dial Fields

See [Dial Fields](/configuration/shared/dial/) for details.
