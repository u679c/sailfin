# 帆鳍（Sailfin）
<!-- impeccable:product-schema 1 -->
## Platform
web
## Stack
Go Core、宿主机探针和 Browser SDK，React + TypeScript 管理端，SQLite WAL。
## Users
维护网站与采集主机的管理员。当前交付优先本机持续运行和迭代。
## Product Purpose
通过本机探针采集 Nginx 访问日志或注入的浏览器事件。界面按站点展示页面、点击聚合、设备、事件和匿名会话。站点启用后出现在首页，收到数据前显示「等待首批数据」。符合条件的首页 title 可更新显示名称。业务无需编写点击埋点。SDK 按站点的同意配置启动。
## Capabilities and Constraints
管理功能连接真实 API；不允许演示数据冒充采集结果。配置地址和数据路径可变。当前 serve 仅使用内置本机探针；项目管理和远程 Agent 注册属于保留的兼容代码，不是当前界面功能。
当前支持生成单文件自解压安装包。用户自行安装 Docker，支持宿主机 Nginx 和符合条件的 1Panel OpenResty 自动接入。容器化 Web 服务还可以手动配置宿主机日志路径。安装目录中的 `aqua` 启停本次安装的 Core 容器和宿主机探针。systemd 和自动发布流水线单独推进。
## Product Principles
数据来源清晰；最小化敏感数据；有界采集；凭据隔离；状态和错误可见。
## Current Workflow
顶部导航为站点、探针管理、系统设置和备份管理，所有管理页统一左右分栏。站点接入位于探针管理。在「添加站点 → 一键扫描」中主动扫描、勾选并确认接入后，探针修改对应的 Nginx 配置。备份管理提供本机 SQLite 快照和自动备份。布局与交互规范见 DESIGN.md。
## Open Decisions
UI 细节由用户授权自主决定；性能目标需在目标环境测量，不预先承诺。
