`cns` inbound accepts CNS TCP and UDP-over-TCP connections.

!!! warning

    CNS uses a weak XOR obfuscation, not authenticated encryption. In particular, the CNS UDP handshake does not authenticate the password. Do not expose this inbound to untrusted networks; use a secure tunnel or network access controls.

### Structure

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

### Fields

#### key

Name of the destination header. Defaults to `Meng`.

#### password

XOR password. Empty disables XOR. Must match the client.

#### flag

UDP handshake marker. Defaults to `httpUDP`. Must match the client.

### Listen Fields

See [Listen Fields](/configuration/shared/listen/). `udp_timeout` controls the idle timeout for each UDP destination.
