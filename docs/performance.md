# 可复现性能测量

当前未在 Linux 2GB 目标环境测量，不声称达到 Core 150MB / Agent 50MB 目标。

已做的结构限制：SQLite 单写连接/WAL/busy timeout、按项目时间和会话建立索引、分页/时间范围限制、批量事务、原子配额、最多 1000 Host 待发事件、每轮单源最多读取 1 MiB、Browser 最多 200 待发事件、HTTP 并发与大小限制。没有引入 Redis 等服务。

已提供真实 SQLite 100 事件批处理基准，操作者自行运行：

```sh
# Linux；只测基准，不执行普通测试
 go test ./internal/core -run '^$' -bench BenchmarkIngest100 -benchmem -benchtime=5s
# macOS + 当前 Go 1.22 工具链
 go test -ldflags=-linkmode=external ./internal/core -run '^$' -bench BenchmarkIngest100 -benchmem -benchtime=5s
```

基准包含事件校验、UA 解析、SQL 去重、事务和配额更新，100 events/op；每轮清理数据不计时，因此不代表数据库持续增长后的吞吐，也不包含 TCP/TLS 和 SDK。

完整目标机测试方法：记录 OS、CPU、RAM、磁盘类型、Go/Node 版本、配置及 commit；使用隔离数据目录运行 Core/Agent。分别测空闲、持续真实日志、Browser、混合采集、断网恢复。以 `/usr/bin/time -v`、`ps`、`pidstat`（可用时）记录 RSS/CPU，以受限压测客户端记录写入吞吐与延迟分位数；记录数据库/WAL 增长、拒绝/丢弃数及磁盘可用空间。

用相同数据量、事件分布、查询范围与并发测量前后变化；配额和入口限流应在报告中注明，不能把主动拒绝当成最大吞吐。2GB 目标机长时间运行、慢客户端、SQLite Busy、低磁盘与异常重启测试应单独报告。
