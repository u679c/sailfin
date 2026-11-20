# 帆鳍（Sailfin）
<!-- impeccable:product-schema 1 -->
## Platform
web
## Stack
Go Core 内置宿主机探针和 Browser SDK，React + TypeScript 管理端，SQLite WAL。
## Users
维护网站与采集主机的管理员。当前交付优先本机持续运行和迭代。
## Product Purpose
通过随 Core 自动启动的本机探针采集 Nginx 访问日志或注入的浏览器事件。界面按站点展示页面、点击聚合、设备、事件和匿名会话。站点在首批有效数据入库后出现，首页 title 可作为显示名称。业务无需编写点击埋点；对外站点默认仍需提供同意信号。
## Capabilities and Constraints
管理功能连接真实 API；不允许演示数据冒充采集结果。配置地址和数据路径可变。当前 serve 仅使用内置本机探针；项目管理和远程 Agent 注册属于保留的兼容代码，不是当前界面功能。
当前支持生成单文件自解压安装包。用户自行安装 Docker，宿主机 Nginx 用于自动注入。容器化 Web 服务可手动配置宿主机日志路径。安装目录中的 `aqua` 启停本次安装的 Core 容器和宿主机探针。systemd 和自动发布流水线单独推进。
## Product Principles
数据来源清晰；最小化敏感数据；有界采集；凭据隔离；状态和错误可见。
## Current Workflow
顶部导航为站点、探针管理、系统设置，所有管理页统一左右分栏。站点接入仅位于探针管理，进入自动扫描、确认后才修改 Nginx。布局与交互规范见 DESIGN.md。
## Open Decisions
UI 细节由用户授权自主决定；性能目标需在目标环境测量，不预先承诺。
