# Mermaid 渲染测试

本文档包含各种 Mermaid 图表，用于测试桌面版 Markdown 渲染能力。

---

## 1. Flowchart（流程图）

### 1.1 基本流程图

```mermaid
flowchart TD
    A[开始] --> B{是否有缓存?}
    B -->|是| C[读取缓存]
    B -->|否| D[发起请求]
    D --> E[处理响应]
    E --> F[写入缓存]
    C --> G[返回数据]
    F --> G
    G --> H[结束]
```

### 1.2 复杂流程图（多分支 + 子图）

```mermaid
flowchart LR
    subgraph 用户侧
        A[用户输入] --> B[前端校验]
        B --> C{校验通过?}
        C -->|否| D[显示错误]
        C -->|是| E[提交请求]
    end

    subgraph 服务端
        E --> F[API 网关]
        F --> G[鉴权中间件]
        G --> H{权限检查}
        H -->|无权限| I[返回 403]
        H -->|有权限| J[业务处理]
        J --> K[数据库操作]
        K --> L[返回结果]
    end

    subgraph 基础设施
        L --> M[日志记录]
        L --> N[指标上报]
        L --> O[缓存更新]
    end
```

### 1.3 横向流程图

```mermaid
flowchart LR
    A[📦 构建] --> B[🧪 测试]
    B --> C[🔍 代码审查]
    C --> D[🚀 部署 Staging]
    D --> E[✅ 验收测试]
    E --> F[🚢 部署生产]
```

---

## 2. Sequence Diagram（时序图）

### 2.1 用户登录时序图

```mermaid
sequenceDiagram
    participant U as 用户
    participant FE as 前端
    participant API as API 网关
    participant Auth as 认证服务
    participant DB as 数据库
    participant Cache as Redis 缓存

    U->>FE: 输入用户名和密码
    FE->>API: POST /api/login
    API->>Auth: 验证凭据
    Auth->>DB: 查询用户信息
    DB-->>Auth: 返回用户数据
    Auth->>Auth: 校验密码哈希
    
    alt 认证成功
        Auth->>Auth: 生成 JWT Token
        Auth->>Cache: 存储 Session
        Auth-->>API: 返回 Token
        API-->>FE: 200 OK + Token
        FE-->>U: 登录成功，跳转首页
    else 认证失败
        Auth-->>API: 返回 401
        API-->>FE: 401 Unauthorized
        FE-->>U: 显示错误提示
    end
```

### 2.2 微服务通信时序图

```mermaid
sequenceDiagram
    actor Client
    participant GW as API Gateway
    participant US as User Service
    participant OS as Order Service
    participant PS as Payment Service
    participant NS as Notification Service

    Client->>GW: 创建订单
    GW->>US: 验证用户
    US-->>GW: 用户有效
    
    GW->>OS: 创建订单
    OS->>OS: 生成订单 ID
    OS-->>GW: 订单已创建
    
    GW->>PS: 发起支付
    PS-->>GW: 支付成功
    
    GW->>OS: 更新订单状态
    OS-->>GW: 状态已更新
    
    GW->>NS: 发送通知
    NS-->>GW: 通知已发送
    
    GW-->>Client: 订单创建成功
```

---

## 3. Class Diagram（类图）

```mermaid
classDiagram
    class Agent {
        +string ID
        +string Name
        +Config Config
        +Provider Provider
        +Run(ctx) error
        +RunStream(ctx) error
        +ExecuteTool(name, params) Result
        +Compact() error
    }

    class Provider {
        <<interface>>
        +Name() string
        +Chat(ctx, messages) Response
        +ChatStream(ctx, messages) Response
        +CountTokens(text) int
    }

    class OpenAIProvider {
        +string APIKey
        +string BaseURL
        +string Model
        +Chat(ctx, messages) Response
        +ChatStream(ctx, messages) Response
    }

    class AnthropicProvider {
        +string APIKey
        +string Model
        +Chat(ctx, messages) Response
        +ChatStream(ctx, messages) Response
    }

    class GeminiProvider {
        +string APIKey
        +Chat(ctx, messages) Response
        +ChatStream(ctx, messages) Response
    }

    class Tool {
        <<interface>>
        +Name() string
        +Description() string
        +Parameters() Schema
        +Execute(params) Result
    }

    class FileReadTool {
        +Execute(params) Result
    }

    class RunCommandTool {
        +Execute(params) Result
    }

    Provider <|.. OpenAIProvider : implements
    Provider <|.. AnthropicProvider : implements
    Provider <|.. GeminiProvider : implements
    Agent --> Provider : uses
    Agent --> Tool : manages
    Tool <|.. FileReadTool : implements
    Tool <|.. RunCommandTool : implements
```

---

## 4. State Diagram（状态图）

### 4.1 订单状态流转

```mermaid
stateDiagram-v2
    [*] --> 待支付 : 创建订单
    待支付 --> 已支付 : 支付成功
    待支付 --> 已取消 : 超时/用户取消
    已支付 --> 备货中 : 商家确认
    备货中 --> 已发货 : 物流揽收
    已发货 --> 已签收 : 用户确认收货
    已发货 --> 退换货 : 用户申请
    退换货 --> 已退款 : 退款成功
    退换货 --> 已签收 : 换货完成
    已签收 --> 已完成 : 自动确认
    已签收 --> 退换货 : 售后申请
    已完成 --> [*]
    已取消 --> [*]
    已退款 --> [*]
```

### 4.2 Agent 运行状态

```mermaid
stateDiagram-v2
    [*] --> Idle : 启动
    Idle --> Running : 用户输入
    Running --> ToolExecution : 工具调用
    ToolExecution --> Running : 结果返回
    Running --> WaitingInput : 需要用户确认
    WaitingInput --> Running : 用户响应
    Running --> Compacting : 上下文过长
    Compacting --> Running : 压缩完成
    Running --> Idle : 回复完成
    Running --> Error : 异常
    Error --> Idle : 恢复
    Idle --> [*] : 退出
```

---

## 5. ER Diagram（实体关系图）

```mermaid
erDiagram
    USER ||--o{ ORDER : "下单"
    USER ||--o{ REVIEW : "评价"
    USER {
        int id PK
        string username
        string email
        string password_hash
        datetime created_at
        datetime updated_at
    }

    ORDER ||--|{ ORDER_ITEM : "包含"
    ORDER }|--|| PAYMENT : "支付"
    ORDER {
        int id PK
        int user_id FK
        string status
        float total_amount
        datetime created_at
    }

    ORDER_ITEM }|--|| PRODUCT : "关联"
    ORDER_ITEM {
        int id PK
        int order_id FK
        int product_id FK
        int quantity
        float unit_price
    }

    PRODUCT ||--o{ REVIEW : "被评价"
    PRODUCT }|--|| CATEGORY : "属于"
    PRODUCT {
        int id PK
        string name
        float price
        int stock
        int category_id FK
    }

    CATEGORY {
        int id PK
        string name
        string description
    }

    PAYMENT {
        int id PK
        int order_id FK
        string method
        string status
        float amount
        datetime paid_at
    }

    REVIEW {
        int id PK
        int user_id FK
        int product_id FK
        int rating
        string comment
    }
```

---

## 6. Gantt Chart（甘特图）

```mermaid
gantt
    title 项目开发计划
    dateFormat YYYY-MM-DD
    axisFormat %m/%d

    section 需求阶段
        需求调研           :done, req1, 2025-01-01, 10d
        需求评审           :done, req2, after req1, 3d
        原型设计           :done, req3, after req2, 7d

    section 开发阶段
        架构设计           :active, dev1, after req3, 5d
        后端开发           :dev2, after dev1, 20d
        前端开发           :dev3, after dev1, 18d
        联调集成           :dev4, after dev2, 7d

    section 测试阶段
        单元测试           :test1, after dev2, 10d
        集成测试           :test2, after dev4, 7d
        性能测试           :test3, after test2, 5d
        UAT 测试           :test4, after test3, 7d

    section 上线阶段
        预发布验证         :rel1, after test4, 3d
        正式发布           :milestone, rel2, after rel1, 1d
        监控运维           :rel3, after rel2, 14d
```

---

## 7. Pie Chart（饼图）

```mermaid
pie title 技术栈使用分布
    "Go" : 45
    "TypeScript" : 25
    "Python" : 15
    "Rust" : 10
    "其他" : 5
```

```mermaid
pie showData
    title 每周时间分配
    "编码开发" : 35
    "代码审查" : 15
    "会议沟通" : 12
    "文档编写" : 10
    "学习研究" : 10
    "测试调试" : 18
```

---

## 8. Mindmap（思维导图）

```mermaid
mindmap
  root((ggcode 架构))
    核心模块
      Agent Loop
        工具执行
        上下文管理
        自动压缩
      Provider 适配
        OpenAI
        Anthropic
        Gemini
        Copilot
      TUI 界面
        Bubble Tea
        多面板
        i18n
    基础设施
      会话持久化
        JSONL 格式
        恢复/续聊
      权限系统
        五种模式
        工具策略
      配置管理
        YAML 配置
        环境变量
    扩展能力
      MCP 集成
        JSON-RPC
        工具桥接
      IM 网关
        QQ / Telegram
        Discord / Slack
        钉钉 / 飞书
      子代理
        并发管理
        进度追踪
    部署分发
      CLI 模式
      Daemon 模式
      Desktop GUI
```

---

## 9. Timeline（时间线）

```mermaid
timeline
    title ggcode 版本发布历程
    section v1.0
        初始发布 : 基础 Agent Loop
                 : OpenAI Provider
                 : TUI 界面
    section v1.1
        多 Provider : Anthropic 支持
                    : Gemini 支持
                    : 流式输出优化
    section v1.2
        工具系统 : 内置文件/命令工具
                 : MCP 集成
                 : 权限模式
    section v1.3
        多代理 : Sub-agent 系统
               : Swarm 团队协作
               : A2A 协议
               : IM 网关
```

---

## 10. Gitgraph（Git 图）

```mermaid
gitGraph
    commit id: "init"
    commit id: "feat: basic agent"
    
    branch develop
    checkout develop
    commit id: "feat: provider interface"
    commit id: "feat: openai adapter"
    
    branch feature/mcp
    checkout feature/mcp
    commit id: "feat: mcp client"
    commit id: "feat: tool bridge"
    
    checkout develop
    merge feature/mcp id: "merge: mcp support"
    commit id: "feat: tui panels"
    
    checkout main
    merge develop id: "release: v1.0.0" tag: "v1.0.0"
    
    checkout develop
    branch feature/im
    commit id: "feat: im gateway"
    commit id: "feat: telegram adapter"
    commit id: "feat: qq adapter"
    
    checkout develop
    merge feature/im id: "merge: im support"
    
    checkout main
    merge develop id: "release: v1.1.0" tag: "v1.1.0"
    commit id: "fix: hotfix"
```

---

## 11. Sankey Diagram（桑基图）

```mermaid
---
config:
  sankey:
    showValues: false
---
sankey-beta

用户输入,Agent Loop,30
Agent Loop,LLM 调用,25
Agent Loop,工具执行,15
LLM 调用,OpenAI,10
LLM 调用,Anthropic,8
LLM 调用,Gemini,7
工具执行,文件操作,8
工具执行,命令执行,5
工具执行,Web 工具,4
工具执行,MCP 工具,3
文件操作,读写文件,5
文件操作,搜索文件,3
命令执行,Shell,5
Web 工具,搜索,2
Web 工具,抓取,2
```

---

## 12. XYChart（折线/柱状图）

```mermaid
xychart-beta
    title "月度活跃用户增长趋势"
    x-axis [1月, 2月, 3月, 4月, 5月, 6月, 7月, 8月, 9月, 10月, 11月, 12月]
    y-axis "用户数（千）" 0 --> 100
    line [12, 18, 25, 32, 38, 45, 52, 61, 70, 78, 85, 95]
```

```mermaid
xychart-beta
    title "各模块代码量对比"
    x-axis ["agent", "provider", "tui", "im", "harness", "mcp", "a2a"]
    y-axis "代码行数（千行）" 0 --> 20
    bar [6.2, 4.5, 17.6, 8.3, 6.2, 5.1, 3.8]
```

---

## 13. Block Diagram（方块图）

```mermaid
block-beta
    columns 3
    
    block:input:1
        columns 1
        A["用户输入"]
        B["IM 消息"]
    end
    
    block:core:1
        columns 2
        C["Agent Loop"] D["Provider"]
        E["Tool Engine"] F["Context"]
    end
    
    block:output:1
        columns 1
        G["TUI 输出"]
        H["API 响应"]
    end
    
    input --> core
    core --> output

    style input fill:#e1f5fe
    style core fill:#fff3e0
    style output fill:#e8f5e9
```

---

## 14. Quadrant Chart（象限图）

```mermaid
quadrantChart
    title 任务优先级矩阵
    x-axis "低影响" --> "高影响"
    y-axis "低紧急" --> "高紧急"
    "紧急 Bug 修复" : [0.8, 0.9]
    "安全漏洞修补" : [0.9, 0.95]
    "新功能开发" : [0.6, 0.4]
    "代码重构" : [0.5, 0.3]
    "文档更新" : [0.3, 0.2]
    "性能优化" : [0.7, 0.5]
    "技术调研" : [0.4, 0.2]
    "CI/CD 改进" : [0.6, 0.6]
```

---

## 15. Requirement Diagram（需求图）

```mermaid
requirementDiagram
    requirement user_auth {
        id: REQ-001
        text: 系统应支持用户认证
        risk: high
        verifymethod: test
    }

    requirement oauth_support {
        id: REQ-002
        text: 支持 OAuth2 登录
        risk: medium
        verifymethod: test
    }

    requirement jwt_token {
        id: REQ-003
        text: 使用 JWT 进行会话管理
        risk: medium
        verifymethod: inspection
    }

    functionalRequirement api_auth {
        id: REQ-004
        text: API 端点需要认证
    }

    performanceRequirement token_perf {
        id: REQ-005
        text: Token 验证延迟 < 10ms
    }

    user_auth - derives -> oauth_support
    user_auth - derives -> jwt_token
    jwt_token - refines -> api_auth
    jwt_token - traces -> token_perf
```

---

## 16. Journey Map（用户旅程图）

```mermaid
journey
    title 新用户首次使用体验
    section 安装
        下载安装包: 5: 用户
        运行安装程序: 4: 用户
        首次启动: 4: 用户
    section 配置
        选择 Provider: 3: 用户
        输入 API Key: 3: 用户
        配置 MCP 服务器: 2: 用户
    section 使用
        第一次对话: 5: 用户, 系统
        使用工具: 4: 用户, 系统
        查看代码变更: 4: 用户
    section 满意
        完成首个任务: 5: 用户
        推荐给同事: 5: 用户
```

---

## 17. C4 Diagram（C4 架构图）

```mermaid
C4Context
    title 系统上下文图 - ggcode 平台

    Person(user, "开发者", "使用 ggcode 的软件工程师")
    Person(admin, "管理员", "系统运维人员")

    System(ggcode, "ggcode", "AI 编程助手 Agent")

    System_Ext(llm, "LLM Provider", "OpenAI / Anthropic / Gemini")
    System_Ext(github, "GitHub", "代码托管平台")
    System_Ext(im, "IM 平台", "Telegram / Discord / Slack")

    Rel(user, ggcode, "使用 CLI / TUI / Desktop")
    Rel(admin, ggcode, "配置管理")
    Rel(ggcode, llm, "API 调用")
    Rel(ggcode, github, "代码操作")
    Rel(ggcode, im, "消息推送")
```

---

## 渲染测试说明

本文档包含以下 Mermaid 图表类型：

| 序号 | 图表类型 | 说明 |
|------|---------|------|
| 1 | Flowchart | 流程图（含子图、多分支） |
| 2 | Sequence | 时序图（含 alt 分支） |
| 3 | Class | 类图（继承、实现关系） |
| 4 | State | 状态图（含复合状态） |
| 5 | ER | 实体关系图 |
| 6 | Gantt | 甘特图（项目计划） |
| 7 | Pie | 饼图 |
| 8 | Mindmap | 思维导图 |
| 9 | Timeline | 时间线 |
| 10 | Gitgraph | Git 分支图 |
| 11 | Sankey | 桑基图（Beta） |
| 12 | XYChart | 折线图/柱状图（Beta） |
| 13 | Block | 方块图（Beta） |
| 14 | Quadrant | 象限图 |
| 15 | Requirement | 需求图 |
| 16 | Journey | 用户旅程图 |
| 17 | C4 | C4 架构图 |

> ✅ 如果所有图表都能正确渲染，说明桌面版 Markdown 渲染器支持完整的 Mermaid 语法。
