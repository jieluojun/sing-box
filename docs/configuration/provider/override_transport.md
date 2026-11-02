### Structure

```json
{
  "xhttp_download_enabled": true,
  "xhttp_download_detour": ""
}
```

### Fields

The following XHTTP download fields apply to nodes using XHTTP with `download_settings`.

#### xhttp_download_enabled

Enable or disable the separate download connection described by `download_settings`. When disabled, both directions use the upload connection.

#### xhttp_download_detour

Tag of the outbound used to dial the download connection.
