# Sailfin — 通用轻量级 Web 埋点与 Agent 管理平台

**产品需求文档（PRD）+ 技术设计文档（TDD）+ Codex 开发任务书**

版本：V1.0  
项目类型：独立部署的通用 Web 行为分析与 Agent 管理平台  
目标：开发一个可实际安装、运行、管理、采集数据的完整产品

---

# 一、项目目标

## 1.1 产品定位

Sailfin 是一套轻量级、通用、可独立部署的 Web 埋点及行为分析平台。

通过统一管理中心与分布式 Agent，实现网站访问统计、设备分析、用户行为采集、匿名会话轨迹、Agent 生命周期管理与安全监控。

产品不依赖特定网站、Web 框架、服务器管理面板或云服务商。

必须支持常见 Linux 服务器、Docker 环境和主流 Web 服务。

核心产品理念：

**一次安装管理中心，按需部署 Agent，集中配置、集中采集、集中分析。**

## 1.2 核心目标

1. 提供 Linux 单文件安装器，实现一键部署。
2. 安装后可以通过浏览器访问完整管理后台。
3. 支持多个项目、多个主机、多个 Agent。
4. 支持自动采集网站 HTTP 访问日志。
5. 支持 UA 解析、设备分类与请求量分析。
6. 支持增强型浏览器埋点，记录点击、停留、导航及异常。
7. 支持匿名会话行为轨迹。
8. 支持 Agent 在线状态、心跳、版本和健康检查。
9. 支持后台新增 Agent、复制安装命令。
10. 支持经过授权的 Docker Agent 自动部署与管理。
11. 支持安全限流、配额、权限控制和数据生命周期管理。
12. 在低配置 Linux 服务器上稳定运行。

## 1.3 非目标

V1 暂不实现：

- AI 智能分析
- 完整 DOM 录屏或鼠标轨迹回放
- 大规模分布式计算
- 商业化计费与租户结算
- Kubernetes Operator
- 依赖浏览器指纹进行跨站用户身份追踪
- 大规模实时消息队列

不允许为了完成次要功能引入明显超出目标硬件承载能力的基础设施。

---

# 二、运行环境与性能目标

## 2.1 最低目标环境

| 项目 | 要求 |
|---|---|
| 操作系统 | 主流 Linux 发行版 |
| CPU 架构 | 首期 x86_64，预留 ARM64 |
| 内存 | 2GB |
| 磁盘 | 建议至少 1GB 可用空间 |
| Docker | 可选 |
| 数据库 | SQLite |
| 服务端 | Go |
| 前端 | React + TypeScript |
| 网络 | 单机及多主机部署均可 |

优先支持 systemd 管理的 Linux 发行版。

## 2.2 资源目标

在低流量场景下：

- Core 常驻内存目标低于 150MB。
- Host Agent 常驻内存目标低于 50MB。
- 管理前端构建为静态文件，不单独运行 Node 服务。
- 不要求 Redis、MySQL、PostgreSQL 或 Kafka。
- 默认无需 Docker 即可运行 Core。
- 禁止无限制内存缓存及无上限事件队列。

以上为设计和性能验收目标，不是未经测试的性能承诺。应通过实际基准测试验证并记录结果。

## 2.3 部署形式

至少支持：

**方式 A：原生单文件部署**

由可执行安装器完成服务安装，使用 systemd 托管 Core。

**方式 B：Docker 部署**

使用官方提供的 Compose 配置运行 Core。

**方式 C：分布式 Agent**

Core 部署在中心服务器，Agent 部署在其他授权主机，通过 HTTPS 通信。

三种模式采用统一的数据协议和管理界面。

---

# 三、系统架构

## 3.1 服务划分

系统由四个主要部分组成：

### Sailfin Core

负责：

- HTTP API
- 管理员认证
- RBAC 授权
- 项目管理
- Agent 注册和管理
- 配置管理
- 数据接收
- 事件校验
- 数据存储
- 统计聚合
- 数据清理
- 安全审计
- 管理前端静态文件服务

### Sailfin Host Agent

负责：

- 主机注册
- 心跳上报
- 运行状态上报
- 容器与服务发现
- 授权日志读取
- 文件偏移管理
- 日志解析
- 本地限量缓冲
- 数据批量上报
- 采集配置同步

### Sailfin Browser Agent

负责：

- 自动页面访问采集
- SPA 路由变化监听
- 点击事件采集
- 页面可见性及停留时间
- JavaScript 异常采集
- 轻量性能指标采集
- 匿名会话管理
- 批量事件发送

Browser Agent 是浏览器 JavaScript SDK，不作为独立服务进程。

### Sailfin Management UI

负责：

- 数据看板
- 项目管理
- Agent 管理
- 网站管理
- 设备分析
- 会话分析
- 事件管理
- 安全中心
- 系统配置

## 3.2 通信架构

```text
                  管理员浏览器
                       |
                       | HTTPS
                       v
              +------------------+
              | Sailfin Core   |
              |                  |
              | Management UI    |
              | REST API         |
              | Auth / RBAC      |
              | Agent Registry   |
              | Event Processor  |
              | Statistics       |
              +--------+---------+
                       |
                       v
                  SQLite / WAL


              Agent 数据通信层
                       |
             HTTPS + Token / TLS
                       |
          +------------+-------------+
          |                          |
          v                          v
    Linux Host Agent          Remote Host Agent
          |                          |
     +----+-----+                Web Server
     |          |
     v          v
  Docker     Web Logs
  Discovery


    用户浏览器
         |
         v
    Browser Agent
         |
         v
    网站同源采集入口
         |
         v
    Core 事件采集处理器
```

## 3.3 核心约束

- Core 不依赖某一种 Web 服务器。
- Agent 不直接访问 Core 数据库。
- 管理后台不能匿名访问管理 API。
- 不要求在每个业务容器内部常驻一个采集进程。
- 支持只部署日志采集 Agent。
- 浏览器行为采集是可选增强能力。
- Agent 不应直接执行任意来自 Core 的 Shell 命令。
- 不允许将 Docker Socket 无保护地暴露给管理后台。

---

# 四、Agent 类型与采集模式

## 4.1 Host Agent

Host Agent 运行于 Linux 宿主机。

主要能力：

- 上报主机名称、系统版本、架构
- 上报 Agent 版本和在线状态
- 发现 Docker 容器
- 发现支持的日志来源
- 读取授权日志文件
- 上传解析结果
- 接收配置更新
- 支持安全停止、重新注册和升级

Host Agent 不默认具备对所有业务容器的修改权限。

Docker 发现能力应与日志采集、部署能力分离授权。

## 4.2 Container Agent

支持两种实现模式：

**Sidecar 模式**

通过独立容器读取共享日志目录或授权的容器日志。

**Host Collector 模式**

Host Agent 通过宿主机 Docker 日志接口或挂载目录读取目标容器日志。

默认优先 Host Collector，减少额外常驻进程。

不应通过修改现有业务容器镜像来安装 Agent。

## 4.3 Browser Agent

提供一个独立的轻量 SDK。

推荐产物：

```text
sailfin.browser.min.js
```

支持两种接入：

- 手动引入 SDK。
- 通过兼容的反向代理注入 SDK。

必须明确：日志采集无法获得真实浏览器点击和停留时间。未经注入 SDK 或其他浏览器侧代码，不允许将这些数据描述为真实采集结果。

自动注入只在确认代理兼容且管理员授权后执行。必须进行配置备份、语法验证、功能检测及失败回滚。

不允许采用影响业务稳定性的强制注入方式。

---

# 五、产品功能设计

## 5.1 数据总览 Dashboard

页面包含：

### 统计卡片

- 今日 PV
- 今日 UV
- 今日 HTTP 请求数
- 今日埋点事件数
- 活跃会话数
- 平均可观测停留时间
- JS 错误数
- Agent 在线率

### 趋势图

支持：

- 24 小时
- 7 天
- 30 天
- 自定义时间范围

图表包括：

- PV / UV 趋势
- HTTP 请求量趋势
- 事件数量趋势
- HTTP 错误率趋势
- 设备类型趋势

PV 和 HTTP 请求数必须区分统计口径。非 HTML 请求不能直接算作页面访问。

### 热门内容

- 热门页面
- 热门事件
- 热门来源
- 最常访问的路径

所有数据可以按照项目和 Agent 筛选。

## 5.2 项目管理

项目作为数据隔离的基本单位。

字段包括：

- 项目 ID
- 项目名称
- 描述
- 域名
- 创建时间
- 绑定 Agent
- 采集模式
- 数据保留期限
- 每日写入配额
- 项目状态

支持新增、修改、暂停、归档。

删除项目必须经过确认，并支持独立的数据清理策略。

## 5.3 Agent 管理

### Agent 列表

显示：

- Agent 名称
- Agent ID
- 类型
- 所属主机
- IP 或授权连接地址
- 版本
- 最近心跳
- 在线状态
- 采集状态
- 绑定项目
- 今日上报量
- 错误数量

状态定义：

- Online
- Offline
- Warning
- Disabled
- Pending Registration

建议默认每 30 秒发送一次心跳，超过 90 秒没有心跳标记为 Offline。

阈值允许配置。

### Agent 详情

包含以下页签：

1. Overview：基本信息及资源状态
2. Sources：日志来源与监控目标
3. Collection：采集策略
4. Metrics：运行指标
5. Logs：Agent 日志
6. Configuration：配置
7. Security：认证与权限

### 新增 Agent

提供两种方式。

**方式 A：复制命令安装**

后台创建一次性注册令牌。

显示目标系统对应的安装命令。

示例：

```bash
sudo ./sailfin-agent-linux-amd64.bin \
  install \
  --server https://trace.example.com \
  --enroll-token <ONE_TIME_TOKEN>
```

安装器完成：

- 检查操作系统
- 检查 CPU 架构
- 验证服务端 TLS
- 使用一次性令牌注册
- 生成 Agent ID
- 安全保存运行凭据
- 创建 systemd 服务
- 启动 Agent
- 上报首次心跳

一次性令牌必须具有有效期、单次使用限制，并且不能等同于管理员令牌。

**方式 B：Docker 自动部署**

后台选择已授权主机。

进入：

```text
选择主机
  ↓
读取 Docker 容器列表
  ↓
选择目标容器
  ↓
检查日志驱动、权限与挂载
  ↓
选择采集方式
  ↓
生成部署计划
  ↓
管理员确认
  ↓
Host Agent 执行受限操作
  ↓
验证采集状态
```

优先启动独立采集 Sidecar，或复用 Host Agent 的日志读取能力。

不向业务容器内部复制可执行文件。

不允许直接从 Web UI 发送任意 Docker 命令。

部署失败必须报告原因并能够清理新增资源。

### Agent 操作

支持：

- 启用
- 停用
- 修改采集配置
- 触发健康检查
- 重新注册
- 删除管理记录
- 卸载受控组件
- 查看操作审计

跨主机远程部署只能通过已经授权和注册的 Host Agent 进行，不应假定 Core 天然拥有目标服务器权限。

## 5.4 UA 设备分析

支持：

- 操作系统
- 操作系统大版本
- 浏览器
- 浏览器大版本
- 设备类型
- 设备品牌
- 设备型号（可识别时）
- 爬虫分类
- 原始请求 UA（受保留策略约束）

设备类型：

- Desktop
- Mobile
- Tablet
- Bot
- Other
- Unknown

### 统计维度

- 按设备类型统计请求数
- 按浏览器统计请求数
- 按操作系统统计请求数
- 按设备型号统计请求数
- 按设备类型统计 PV
- 按设备类型估算 UV
- 按设备类型查看事件数量
- 按时间观察设备占比变化

UA 解析不得简单依赖系统名称字符串匹配。

允许使用经过评估的成熟 UA 解析库。

无法识别时必须返回 Unknown，不能编造设备型号。

匿名 UV 不应宣称等同真实自然人数量。

## 5.5 会话行为轨迹

使用匿名 visitorId 和 sessionId 组织事件。

会话列表展示：

- 会话 ID
- 设备类型
- 浏览器
- 操作系统
- 开始时间
- 最后活动时间
- 访问页面数量
- 交互次数
- 可观测活跃时长
- 来源项目

### 会话详情

展示时间线：

```text
10:32:01  page_view
10:32:08  click
10:32:15  route_change
10:32:36  click
10:33:02  page_hidden
```

支持点击查看事件元数据。

首期不提供完整 DOM 回放。

会话数据必须基于实际采集事件。

不能通过访问日志虚构按钮点击、滚动或精确停留时间。

对于缺少 Browser Agent 的网站，只展示可被服务端实际观测到的请求轨迹。

## 5.6 自动事件采集

### 基础日志采集

支持通过不同 Parser Adapter 接入：

- Nginx
- Apache
- Caddy
- Docker JSON Logs
- 自定义结构化 JSON 日志

每种日志来源都应具备独立解析适配器。

对于无法自动判断结构的日志，提供手动字段映射。

### 浏览器增强采集

支持：

- page_view
- route_change
- click
- session_start
- page_hidden
- js_error
- unhandled_rejection
- performance_event

自动点击采集默认记录受限制的元素类型及安全标识。

不采集输入内容、密码、Cookie、Authorization Header、完整 URL 查询参数等敏感数据。

默认禁止将完整 DOM 文本作为事件属性。

### 采集策略配置

管理后台能够设置：

- 采集开关
- 事件类型
- 采样率
- 排除页面
- 排除元素
- 数据保留天数
- 上报批次大小
- 上报间隔

配置由 Core 管理，Agent 在启动时和固定周期内拉取更新。

配置需要版本号，并支持回滚。

## 5.7 安全中心

需要包含：

- Agent 异常状态
- 认证失败次数
- 事件拒绝数量
- 限流数量
- 项目写入配额
- 异常请求来源的聚合统计
- 最近管理操作审计
- 磁盘使用情况
- 数据库体积
- 采集丢弃数

禁止无限制记录拒绝请求的完整请求体。

---

# 六、API 设计

统一使用 `/api/v1`。

## 6.1 管理 API

```text
POST   /api/v1/auth/login
POST   /api/v1/auth/logout
GET    /api/v1/auth/me

GET    /api/v1/projects
POST   /api/v1/projects
GET    /api/v1/projects/{id}
PATCH  /api/v1/projects/{id}

GET    /api/v1/agents
POST   /api/v1/agents/enrollment
GET    /api/v1/agents/{id}
PATCH  /api/v1/agents/{id}
GET    /api/v1/agents/{id}/metrics
GET    /api/v1/agents/{id}/logs

GET    /api/v1/events
GET    /api/v1/sessions
GET    /api/v1/sessions/{id}

GET    /api/v1/stats/overview
GET    /api/v1/stats/trends
GET    /api/v1/stats/devices
GET    /api/v1/stats/pages

GET    /api/v1/security/summary
GET    /api/v1/system/status
```

所有管理 API 必须经过认证和授权。

## 6.2 Agent API

```text
POST /api/v1/agent/enroll
POST /api/v1/agent/heartbeat
GET  /api/v1/agent/config
POST /api/v1/agent/events/batch
POST /api/v1/agent/metrics
```

Agent 使用独立凭据。

服务端必须验证 Agent 身份以及它被允许上报的项目。

## 6.3 Browser Collector API

```text
POST /api/v1/collect/browser
```

该接口用于浏览器增强采集。

它可以经由网站同源代理访问，也可以在明确配置跨域策略后使用。

接口必须独立于管理 API 的认证体系。

浏览器不能持有服务端管理员密钥或长期 Agent 密钥。

## 6.4 事件格式

```json
{
  "eventId": "evt_001",
  "projectId": "project_001",
  "sessionId": "session_001",
  "visitorId": "visitor_001",
  "eventType": "click",
  "timestamp": 1795140000000,
  "path": "/home",
  "properties": {
    "element": "button",
    "elementId": "contact-button"
  }
}
```

服务端自行维护：

- receivedAt
- sourceType
- agentId
- validationStatus

不信任客户端直接指定的服务器接收时间。

---

# 七、数据库设计

使用 SQLite，启用 WAL。

主要表：

| 表名 | 用途 |
|---|---|
| users | 管理用户 |
| projects | 项目信息 |
| agents | Agent 注册记录 |
| agent_credentials | Agent 凭据元数据 |
| agent_heartbeats | Agent 状态与心跳 |
| agent_configs | 配置及版本 |
| sources | 日志来源 |
| events | 原始事件 |
| sessions | 匿名会话 |
| device_info | UA 解析结果 |
| daily_stats | 每日聚合 |
| hourly_stats | 小时聚合 |
| security_counters | 安全计数 |
| audit_logs | 管理审计 |
| system_settings | 系统设置 |

要求：

1. 所有事件包含 projectId。
2. 对项目、时间、事件类型建立必要索引。
3. 分页查询不得无上限返回全部数据。
4. 使用参数化 SQL。
5. 使用数据库迁移版本管理。
6. 所有统计明确区分事件数、请求数、PV、UV。
7. 支持按项目设置数据保留期限。
8. 支持数据库在线备份。
9. 禁止直接复制正在写入的 SQLite 主文件作为唯一备份方式。
10. 对 Agent 心跳采用最新状态表结合必要历史记录，避免无限制膨胀。

---

# 八、安全架构

## 8.1 管理员身份认证

V1 采用本地管理员账户。

要求：

- 使用 Argon2id 等成熟密码哈希算法
- 安全会话 Cookie
- HttpOnly
- SameSite
- HTTPS 环境启用 Secure
- 登录失败限制
- 会话过期
- CSRF 防护
- 审计日志

初始安装不得设置公开的默认管理员密码。

## 8.2 Agent 认证

采用一次性注册令牌完成引导注册。

注册成功后建立独立 Agent 身份。

运行凭据支持撤销、轮换和最小权限。

不得让所有 Agent 共用一个永久管理员 Token。

## 8.3 采集防刷

至少实现：

**入口层**

- 单 IP 速率限制
- 请求大小限制
- 连接超时
- 请求方法限制
- 并发限制

**应用层**

- JSON Schema 校验
- 事件字段白名单
- 字符串长度限制
- 属性数量限制
- 嵌套深度限制
- projectId 验证
- eventId 去重

**存储层**

- 单项目写入限额
- 有界事件缓冲
- 批量写入
- 数据保留策略
- 存储空间告警
- 数据库写入繁忙保护

默认值可以配置：

```yaml
security:
  max_event_bytes: 8192
  max_batch_events: 100
  max_project_events_per_day: 10000
  max_browser_requests_per_ip_minute: 60
  max_agent_batch_bytes: 262144
```

以上为初始参数，必须通过压力测试调优。

重要：不允许只依赖 CORS、Origin、Referer 或公开 projectId 防止恶意写入。

## 8.4 Docker 操作安全

不得将 `/var/run/docker.sock` 直接挂载给公开管理后台。

自动部署由经过身份验证的 Host Agent 执行。

Host Agent 应只提供有限的受控操作：

- 列出可见容器
- 读取必要元数据
- 检查日志来源
- 创建 Sailfin 自有 Sidecar
- 停止或删除 Sailfin 自有 Sidecar

高风险操作必须经确认。

Docker 访问应部署在信任边界内，必要时使用受限代理或分离的特权辅助进程。任何具备 Docker 管理权限的组件都应视作高权限组件。

## 8.5 数据隐私

默认采用数据最小化原则：

- 不采集表单输入值
- 不采集密码及 Token
- 不采集完整 URL 查询参数
- 不默认永久保存完整 IP
- 不进行跨站身份关联
- 不默认开启 Browser Agent 的增强行为采集
- 支持配置数据保留与删除
- 提供浏览器端采集关闭机制

需要对用户提供必要的隐私告知及适用的同意管理能力。

---

# 九、管理后台 UI / UX 设计

## 9.1 设计目标

使用现代 SaaS 管理平台的设计语言。

要求：

- 简洁、专业
- 信息密度适中
- 层次清晰
- 支持明暗主题
- 响应式布局
- 使用统一设计变量
- 完善加载、空数据、错误、确认交互
- 不允许将所有页面做成简单的表格堆砌

## 9.2 技术栈

- React
- TypeScript
- Vite
- Tailwind CSS
- shadcn/ui
- TanStack Query
- ECharts
- React Router

管理前端构建产物由 Core 内嵌或直接托管。

正式部署不需要 Node.js 常驻服务。

## 9.3 导航结构

```text
Sailfin
│
├── 工作台
│   └── 数据总览
│
├── 数据分析
│   ├── 访问分析
│   ├── 设备分析
│   ├── 事件分析
│   └── 会话轨迹
│
├── 资源管理
│   ├── 项目管理
│   ├── Agent 管理
│   └── 数据来源
│
├── 安全中心
│   ├── 安全概览
│   ├── 限流与配额
│   └── 操作审计
│
└── 系统设置
    ├── 系统状态
    ├── 用户与权限
    ├── 数据存储
    └── 备份与更新
```

## 9.4 Agent 管理页面

主界面：

```text
┌────────────────────────────────────────────────────┐
│ Agent 管理                             [+ 新增 Agent] │
├────────────────────────────────────────────────────┤
│ 总数: 12      在线: 10      离线: 1      异常: 1       │
├────────────────────────────────────────────────────┤
│ 搜索 Agent...    类型筛选     状态筛选      所属主机    │
├────────────────────────────────────────────────────┤
│ 名称         类型       状态     版本      最后心跳     │
│ Host-01      Linux      在线     1.0.0     8 秒前       │
│ Web-02       Container  在线     1.0.0     12 秒前      │
│ Host-03      Linux      离线     1.0.0     2 小时前     │
└────────────────────────────────────────────────────┘
```

新增 Agent 使用分步式向导：

1. 选择目标环境
2. 选择安装方式
3. 生成部署命令或选择授权容器
4. 执行预检查
5. 注册 Agent
6. 验证连接
7. 配置采集来源

## 9.5 会话轨迹页面

左侧为会话列表。

右侧为选定会话的详细信息。

展示：

- 设备信息
- 浏览器信息
- 会话开始时间
- 最后活动时间
- 访问页面
- 时间线
- 事件属性

允许切换列表/详情视图。

## 9.6 设备分析页面

包含：

- 设备类型分布
- 操作系统分布
- 浏览器分布
- 设备型号排行
- 请求趋势
- PV/UV 对比
- 未知设备统计
- 原始 UA 查看

需要支持筛选项目、日期与设备类型。

不得使用虚构的设备型号填补无法解析的 UA。

---

# 十、单文件安装器

## 10.1 发布产物

建议：

```text
sailfin-linux-amd64.bin
sailfin-agent-linux-amd64.bin
SHA256SUMS
```

发布时可以增加包含哈希短码的文件名。

但真实性与完整性校验必须基于可信分发的完整 SHA-256 校验值或数字签名，而不只是文件名。

## 10.2 Core 安装

期望命令：

```bash
chmod +x sailfin-linux-amd64.bin
sudo ./sailfin-linux-amd64.bin install
```

安装过程：

1. 环境检测
2. 端口冲突检查
3. 磁盘检查
4. 创建安装目录
5. 创建专用系统用户
6. 解包或安装程序
7. 初始化 SQLite
8. 创建 systemd 服务
9. 启动 Core
10. 进行健康检查
11. 输出管理后台访问地址
12. 引导首次管理员初始化

推荐目录：

```text
/opt/sailfin/
  bin/
  web/
  config/

/var/lib/sailfin/
  sailfin.db
  backups/

/var/log/sailfin/
```

首次初始化必须采用仅一次有效的引导流程，不能长期开放匿名创建管理员接口。

## 10.3 生命周期命令

```bash
./sailfin-linux-amd64.bin install
./sailfin-linux-amd64.bin upgrade
./sailfin-linux-amd64.bin uninstall
./sailfin-linux-amd64.bin status
./sailfin-linux-amd64.bin version
```

升级要求：

- 版本兼容检查
- 备份配置和数据库
- 原子替换程序
- 迁移数据库
- 重启服务
- 验证健康状态
- 失败回滚

卸载必须询问是否保留数据。

不得默认删除用户数据库。

---

# 十一、技术实现要求

## 11.1 Go 项目结构

建议采用如下目录：

```text
sailfin/
├── cmd/
│   ├── sailfin/
│   └── sailfin-agent/
│
├── internal/
│   ├── api/
│   ├── auth/
│   ├── config/
│   ├── agent/
│   ├── collector/
│   ├── parser/
│   ├── analytics/
│   ├── storage/
│   ├── security/
│   ├── scheduler/
│   └── installer/
│
├── pkg/
│   └── protocol/
│
├── web/
│   ├── src/
│   ├── public/
│   └── package.json
│
├── browser-agent/
│
├── configs/
├── migrations/
├── deployments/
├── scripts/
├── tests/
├── docs/
│
├── go.mod
└── README.md
```

## 11.2 模块化要求

- Core 与 Agent 使用共享协议定义。
- 业务逻辑不直接依赖具体 HTTP 框架。
- 数据访问通过明确的 Repository 层组织。
- 日志解析器使用可扩展接口。
- Browser Agent 独立构建。
- 所有外部接口有版本。
- 数据库迁移可追踪。
- 配置变更可审计。

V1 不采用微服务拆分。

## 11.3 开发质量

要求：

- 类型明确
- 结构化日志
- 统一错误码
- 上下文超时控制
- 并发安全
- 合理的测试覆盖
- 避免循环依赖
- 对外 API 文档
- 完整 README
- 清晰的部署步骤

不得使用大量无法运行的占位代码。

不得将假数据作为真实接口返回。

---

# 十二、开发阶段与验收标准

Codex 必须按阶段推进，每阶段完成后进行构建、测试和验收。

## Phase 1：工程基础

交付：

- Go Core 基础结构
- React 管理前端
- SQLite 初始化
- 配置系统
- 管理员登录
- 单文件安装器雏形
- 健康检查

验收：

- 可以启动真实服务
- 可以登录后台
- SQLite 正常持久化
- 重启后数据保留
- 前端生产构建正常

## Phase 2：Agent 管理

交付：

- Host Agent
- 一次性注册流程
- Agent 心跳
- Agent 列表
- Agent 详情
- 配置下发
- 手动安装命令生成

验收：

- 能注册真实 Agent
- 后台能显示在线状态
- Agent 退出后能够判定离线
- 禁用 Agent 后拒绝继续上报
- 无效或过期令牌被拒绝

## Phase 3：HTTP 日志采集

交付：

- Nginx Adapter
- Docker JSON 日志 Adapter
- 可扩展 Parser 接口
- 文件偏移管理
- 批量上报
- 断线重试
- 写入配额

验收：

- 真实访问日志能够解析并写入 SQLite
- Agent 重启不会大规模重复采集
- 日志轮转后可继续采集
- 网络中断后能够恢复
- 本地缓冲有严格上限

## Phase 4：分析后台

交付：

- Dashboard
- PV / 请求分析
- UA 解析
- 设备分布
- 统计图
- 事件查询
- 项目筛选

验收：

- 基于真实数据统计
- 统计结果与测试数据一致
- 时间范围切换正确
- 不同项目数据隔离
- 查询有分页和时间范围限制

## Phase 5：Browser Agent

交付：

- 独立 JS SDK
- 自动页面访问
- SPA 路由监听
- 自动点击
- JS 错误
- 会话管理
- 会话时间线
- 批量上报

验收：

- React、Vue、原生 HTML 测试站点可用
- 页面切换无明显性能问题
- 不采集敏感表单内容
- 关闭采集功能后停止上报
- 会话轨迹准确反映实际事件

## Phase 6：Docker 管理

交付：

- Docker 主机发现
- 容器列表
- 日志源关联
- 预检查
- 受控 Sidecar 部署
- 部署状态
- 失败清理

验收：

- 能列出授权主机的容器
- 不改变业务容器镜像
- 不因部署失败破坏现有业务
- 未授权用户不能执行操作
- 支持撤销 Sailfin 创建的资源

## Phase 7：产品化交付

交付：

- 完整安装器
- 升级流程
- 卸载流程
- 配置及数据库备份
- 安全文档
- API 文档
- 用户操作说明
- 实际性能测试结果
- Release 构建脚本

验收：

在干净 Linux 虚拟机中：

1. 运行安装器。
2. 打开管理界面。
3. 初始化管理员。
4. 新增项目。
5. 新增 Agent。
6. 安装 Agent。
7. 产生真实访问数据。
8. 查看 PV、请求数与 UA 分布。
9. 查看 Agent 心跳。
10. 重启 Core 和 Agent。
11. 确认数据与配置保留。
12. 测试备份、升级和卸载。

整个流程不允许依赖开发环境。

---

# 十三、测试与安全验收

至少覆盖以下场景。

### 功能测试

- 新建项目
- 注册 Agent
- 心跳超时
- 数据采集
- 会话统计
- UA 解析
- 配置更新
- 多项目隔离
- Agent 停用与重新启用

### 安全测试

- 无效 Agent Token
- 重放一次性注册令牌
- 未授权读取项目数据
- 伪造 Origin
- 超大 JSON 请求
- 高频重复事件
- SQL 注入尝试
- XSS 输入
- 登录暴力尝试
- Docker 非授权操作

### 稳定性测试

- Core 重启
- Agent 重启
- 日志轮转
- 网络断开
- SQLite Busy
- 磁盘空间不足
- 数据库备份
- 异常终止
- 大量重复上报
- 慢速客户端连接

### 资源测试

在 2GB RAM 的目标 Linux 环境中测量：

- Core RSS
- Agent RSS
- 空闲 CPU
- 日志采集 CPU
- SQLite 大小增长
- 事件吞吐
- 接口延迟

必须输出可复现的测试方法和测试报告，不允许仅凭理论估算声称达标。

---

# 十四、Codex 执行要求

你是一名资深 Go 后端工程师、React 前端工程师与 DevOps 工程师。

请基于以上需求，在当前工作目录中实际创建完整 Sailfin 项目。

## 执行原则

1. 首先检查当前目录和可用开发工具。
2. 创建清晰的项目目录。
3. 编写架构说明和实施计划。
4. 按 Phase 1 至 Phase 7 逐步实现。
5. 每完成一个阶段，执行相关测试。
6. 修复编译错误、类型错误及关键功能问题。
7. 不允许只生成静态 UI 原型。
8. 不允许用模拟接口代替已要求实现的真实功能。
9. 保证低配置服务器运行目标。
10. 对无法安全实现的功能，保留清晰的能力边界，不采用破坏性替代方案。
11. 不得未经确认修改、停止或删除工作环境中的现有业务容器。
12. 不得未经确认执行具有破坏性的系统操作。

## 优先级

P0：

- Core
- 安装器
- 登录
- SQLite
- 项目管理
- Agent 注册
- 心跳
- 日志采集
- UA 统计
- 数据总览

P1：

- Browser Agent
- 会话轨迹
- Agent 配置下发
- Docker 容器发现
- 安全中心

P2：

- Docker 自动部署
- 代理自动注入
- 完整升级回滚
- 高级可视化
- 多管理员细粒度权限

优先保证 P0 能形成可用、可测试、可独立安装的产品，然后逐步推进 P1、P2。

如果存在架构取舍，优先选择稳定、轻量、可维护、可扩展的实现。

## 最终交付

需要提供：

- 完整源代码
- 可构建的 Go Core
- 可构建的 Host Agent
- Browser Agent SDK
- React 管理界面
- SQLite Schema 和迁移
- Linux 安装器
- Docker Compose 部署文件
- 构建及发布脚本
- API 文档
- 使用说明
- 测试用例
- 测试报告
- 已实现功能及未实现功能清单

最终必须说明实际构建和测试结果，区分已验证功能、未验证功能与后续规划。

**最终目标：交付一个真正能够独立部署、安装 Agent、采集真实数据、查看分析结果并进行可视化管理的 Sailfin V1 产品，而不是概念演示。**