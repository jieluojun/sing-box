`cns` 出站连接 CNS 服务器，支持 TCP 和 UDP over TCP。

!!! warning "安全提示"

    CNS 使用弱 XOR 混淆，并非认证加密。在不可信网络上不要依赖它提供保密性或身份验证。

### 结构

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

### 字段

#### server、server_port

CNS 服务器地址及 TCP 端口。

#### key

目标地址请求头名称，默认为 `Meng`；必须与服务端一致。

#### password

XOR 密码，留空则不使用 XOR；必须与服务端一致。

#### flag

UDP 握手标记，默认为 `httpUDP`；必须与服务端一致。

#### network

启用的网络，`tcp` 和/或 `udp`，默认均启用。CNS UDP 帧仅支持 IP 地址，域名目标在发送前由 sing-box 解析。

#### headers

额外的 HTTP 握手请求头。自定义 `Host` 也会作为 CONNECT 请求目标；未指定端口时默认为 443。未设置时目标为 `/`。

### 拨号字段

参阅 [拨号字段](/zh/configuration/shared/dial/)。
