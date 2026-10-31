`cns` outbound connects to a CNS server, with TCP and UDP-over-TCP support.

!!! warning

    CNS uses a weak XOR obfuscation, not authenticated encryption. Do not rely on it for confidentiality or authentication on untrusted networks.

### Structure

```json
{
  "type": "cns",
  "tag": "cns-out",
  "server": "127.0.0.1",
  "server_port": 23333,
  "key": "Meng",
  "password": "",
  "flag": "httpUDP",
  "network": ["tcp", "udp"],
  "headers": {"Host": "example.com"}
}
```

### Fields

#### server, server_port

CNS server address and TCP port.

#### key

Destination header name. Defaults to `Meng`; must match the server.

#### password

XOR password. Empty disables XOR; must match the server.

#### flag

UDP handshake marker. Defaults to `httpUDP`; must match the server.

#### network

Enabled networks, `tcp` and/or `udp`. Both are enabled by default. CNS UDP frames carry IP addresses only; domain destinations are resolved by sing-box before transmission.

#### headers

Additional HTTP handshake headers. A custom `Host` also becomes the CONNECT request target, with port 443 if omitted. Without it, the target is `/`.

### Dial Fields

See [Dial Fields](/configuration/shared/dial/).
