# 帆鳍（Sailfin）
<!-- impeccable:product-schema 1 -->
## Platform
web
## Stack
Go Core 内置宿主机探针和 Browser SDK，React + TypeScript 管理端，SQLite WAL。
## Users
维护网站与采集主机的管理员。当前交付优先本机持续运行和迭代。
## Product Purpose
通过随 Core 自动启动的本机探针采集 Nginx 访问日志或注入的浏览器事件。后台按站点展示设备、请求和匿名会话，站点在收到数据后自动出现。
## Capabilities and Constraints
管理功能连接真实 API；不允许演示数据冒充采集结果。配置地址和数据路径可变。
当前不开发安装器、systemd、发布流水线。不得修改、停止、删除已有 Docker 容器。
## Product Principles
数据来源清晰；最小化敏感数据；有界采集；凭据隔离；状态和错误可见。
## Open Decisions
UI 细节由用户授权自主决定；性能目标需在目标环境测量，不预先承诺。
