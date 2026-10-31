`cns` 入站支持 CNS TCP 和 UDP over TCP 连接。

!!! warning "安全提示"

    CNS 使用弱 XOR 混淆，并非认证加密；其 UDP 握手不验证密码。不要将入站直接暴露给不可信网络，应使用安全隧道或网络访问控制。

### 结构

```json
{
  "type": "cns",
  "tag": "cns-in",
  "listen": "127.0.0.1",
  "listen_port": 23333,
  "key": "Meng",
  "password": "",
  "flag": "httpUDP"
}
```

### 字段

#### key

目标地址请求头名称，默认为 `Meng`。

#### password

XOR 密码，留空则不使用 XOR；必须与客户端一致。

#### flag

UDP 握手标记，默认为 `httpUDP`；必须与客户端一致。

### 监听字段

参阅 [监听字段](/zh/configuration/shared/listen/)。`udp_timeout` 控制每个 UDP 目标的空闲超时。
