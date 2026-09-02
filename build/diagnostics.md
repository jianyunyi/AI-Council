# Desktop diagnostics

在桌面版中调用 `ExportDiagnostics(destination)` 会生成 ZIP 支持包。包内只包括：

- `summary.json`：版本、服务状态和数据/日志目录摘要；
- `runner.log`、`council.log`：每份最多 64 KiB，并对 Bearer 凭据、`--token` 参数、API Key、Secret 和 Token 键值进行脱敏。

支持包不会包含 Provider Key、临时 Session Token、SQLite 数据库、Workspace 文件、制品或补丁内容。提交支持请求前，仍应由用户检查 ZIP 内容。
