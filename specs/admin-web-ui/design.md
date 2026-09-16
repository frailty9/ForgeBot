# Admin Web UI 设计

## 1. 设计目标

Admin Web UI 是 ForgeBot 单实例的运行控制台。它需要让管理员在几秒内判断实例是否可用、哪些连接正在工作、哪些配置可以修改，以及一次修改会带来什么影响。

界面采用自然极简风格：留白、浅边界、低饱和颜色和稳定排版优先，减少装饰性元素。信息层级由文字、间距、边界和状态标记建立，不依赖密集图表或卡片墙。

## 2. 视觉规格

### 2.1 色彩

所有颜色通过 CSS Custom Properties 提供两套主题：

浅色：

- 页面背景 `#F7F7F4`；
- 内容面 `#FFFFFF`；
- 柔和面 `#F0F2ED`；
- 边界 `#E1E5DF`；
- 主文字 `#1F2723`；
- 次级文字 `#68736B`；
- 安静文字 `#8D9790`。

深色：

- 页面背景 `#171A18`；
- 内容面 `#202522`；
- 柔和面 `#252C27`；
- 边界 `#303933`；
- 主文字 `#F0F2ED`；
- 次级文字 `#B8C0B8`；
- 安静文字 `#89958B`。

共用状态色：

- 强调/待处理 `#B7781C`；
- 健康/成功 `#2E7769`；
- 错误/破坏性操作 `#B94A45`。

主题只有浅色和深色两种实际渲染结果。主题来源提供“阳光、月亮、Auto”三个图标按钮：阳光固定浅色，月亮固定深色，Auto 使用 `prefers-color-scheme`。用户选择保存到 `sessionStorage`，主题变化不触发 API 请求。

状态同时通过文字、状态点和布局反馈表达，不只依赖颜色。

### 2.2 字体

- 中文正文使用系统中文无衬线字体栈，兼容 Windows、macOS、Linux 和 Android；
- 拉丁标题使用 `IBM Plex Sans`，不可用时回退到项目定义的无衬线字体；
- 版本、ID、路径、状态码和配置键使用 `IBM Plex Mono`；
- 生产构建不依赖外部字体 CDN，可随资源提供必要的 Latin 字形；
- 不使用 Inter、Roboto、Arial 或系统字体作为唯一视觉方案。

### 2.3 布局结构

桌面端结构固定为：

```text
#app
├── nav.scroll-y
└── div.workspace-shell
    ├── header.topbar
    └── main.scroll-y
```

- 宽屏导航宽度约 224px，显示 ForgeBot 标识、一级菜单和可展开的二级菜单；
- 右侧 `workspace-shell` 占据剩余空间，内部使用两行网格；
- `topbar` 固定在右侧工作区顶部，左侧显示 runtime 状态，右侧显示阳光、月亮、Auto 主题按钮；
- `main` 是唯一的主内容滚动容器，导航和主内容互不带动；
- 低于 1100px 时导航收缩为 72px 图标轨；低于 720px 时变为顶部横向导航或抽屉；
- 所有页面保持相同的页面内边距和内容最大宽度，避免切换页面时产生跳动。

## 3. 导航与路由

一级页面：

- 概览 `/admin/`；
- 配置 `/admin/config`；
- 运行维护 `/admin/operations`；
- 版本历史 `/admin/history`。

配置二级页面使用同一路由加 section 参数：

```text
/admin/config?section=bot
/admin/config?section=llm
/admin/config?section=gate
/admin/config?section=search
/admin/config?section=platforms
/admin/config?section=reflection
/admin/config?section=extensions
/admin/config?section=administration
```

二级导航只在配置页面激活时展开。概览卡片点击后跳转到稳定 section，并由配置页面滚动到目标区块、设置焦点或展开对应编辑区域。

## 4. 页面设计

### 4.1 概览页

页面标题区只包含 `INSTANCE / OVERVIEW` 和主标题“运行状态，保持清楚。”，不再放置重复的站点名称、配置版本或长副标题。

概览内容按以下顺序排列：

1. **连接器**：展示已启用 connector 的类型、状态和绑定数量；点击进入 `platforms`。
2. **已连接群组**：展示逻辑 conversation、外部目标和连接状态；点击进入 `platforms`。
3. **LLM 配置**：展示当前供应商、协议、模型和密钥状态；未来允许展示多个模型；点击进入 `llm`。
4. **LLM 花费统计**：后端统计能力完成前只展示预留状态，不显示虚构数字；后续可按意图判断、问答、反思和 Embedding 分类。
5. **工具/MCP**：展示启用的工具来源、transport 和白名单状态；点击进入 `extensions`。
6. **最近操作**：展示真实可获得的配置应用、索引重载、反思扫描和 runtime 操作结果。

Runtime 的详细 generation、配置版本和错误上下文放在运行维护或配置上下文栏中，概览只保留顶部状态和必要摘要。

### 4.2 配置页

配置页采用“二级导航 + 表单工作区 + 草稿状态栏”结构：

- 左侧列出配置关注点，并显示错误计数或未保存标记；
- 中央只渲染当前 section，减少长页面扫描成本；
- 右侧显示当前版本、影响级别、校验结果和即将执行的动作；
- 底部保存栏提供“刷新配置、校验、应用配置”；刷新属于配置页局部操作；
- 存在未保存修改时，刷新、离开 section 和离开页面都必须提示；
- 结构化字段优先使用输入框、选择框、开关、列表编辑器和可展开详情；高级字段可折叠展示 JSON 预览；
- section 标识和字段路径可复制，便于管理员报告问题。

API Key 使用隔离安全卡片，只显示 configured 状态和更新时间。写入后立即清空输入框，普通配置草稿和差异视图不包含密钥正文。

### 4.3 运行维护页

页面按影响从低到高排列操作：刷新 runtime 状态、重建结构化索引、执行反思扫描、软重启 runtime。每个操作显示影响范围、最近结果、执行中状态和错误详情。

软重启需要填写原因并确认。请求期间只禁用同一操作，允许继续读取状态。页面不显示虚假的百分比进度。

### 4.4 版本历史页

左侧为按时间倒序的版本时间线，右侧为选中版本摘要和差异。当前版本有明确标记。回滚操作说明“从目标版本创建新的回滚版本”，并携带当前版本进行乐观并发保护。

## 5. 组件边界

基础组件：

- `UiButton`、`UiIconButton`、`UiInput`、`UiSelect`、`UiTextarea`、`UiToggle`；
- `UiCard`、`UiBadge`、`UiNotice`、`UiDialog`、`UiTimeline`、`UiListEditor`。

领域组件：

- `RuntimeStatusBar`；
- `OverviewConnectorCard`、`OverviewGroupCard`、`OverviewLlmCard`、`OverviewToolsCard`；
- `ConfigSectionNav`、`ConfigDraftBar`、`ConfigImpactPanel`；
- `LlmConfigSection`、`PlatformConfigSection`、`McpConfigSection`；
- `SecretField`、`VersionConflictNotice`、`OperationCard`。

页面组件：

- `LoginPage`、`OverviewPage`、`ConfigPage`、`OperationsPage`、`HistoryPage`。

基础组件只处理 DOM、可访问性和样式；领域组件只处理领域数据展示和事件；API 请求、草稿、认证和操作状态进入 Pinia store 与强类型 API client。

## 6. SCSS 与 Vapor 约束

- Vue 使用 Vapor Mode，入口使用 Vapor runtime；
- 不引入依赖传统 VDOM 的后台组件库；
- 组件采用 Composition API 和 `<script setup>`；
- Sass/SCSS 只用于嵌套、模块拆分、局部变量和计算，提升 CSS 可读性；
- 禁止使用会展开大量重复 CSS 的 mixin，避免 `%placeholder` 和难以预测的 `@extend`；
- CSS Custom Properties 负责运行时主题和状态令牌；
- SFC 样式默认 `scoped`，全局只保留 reset、字体和主题令牌；
- 选择器最多三层嵌套；动态状态通过 class 和 CSS Variables 表达；
- UnoCSS 只用于少量布局工具，不以长串原子类替代语义样式；
- 图标使用静态 SVG，禁止 emoji 图标。

## 7. 安全、可访问性与反馈

- Bearer token 只保存在内存和 `sessionStorage`，不进入 URL、日志或可见错误；
- 所有输入拥有可关联 label、帮助文本和错误说明；
- 键盘可完成导航、section 切换、表单编辑、确认和回滚；
- 状态同时提供文字和图形标记；
- 弹窗打开时焦点进入弹窗，关闭后返回触发按钮；
- 主题切换、API 请求、保存、版本冲突和操作结果通过页面内状态条反馈；
- 不使用 `v-html` 渲染服务端内容，不把服务端错误字符串当作流程判断依据。

## 8. 测试与发布

- `vue-tsc --noEmit`、ESLint、格式检查和 Vite 构建必须通过；
- Vitest 覆盖 API client、认证会话、配置草稿、section 跳转、版本冲突和密钥动作；
- Playwright 覆盖登录、概览卡片跳转、主题切换、配置刷新/校验/应用、运维操作和回滚；
- 浏览器验证桌面、窄屏、浅色和深色主题，并确认 `nav` 与 `main` 独立滚动；
- 前端产物输出到 Go 静态资源目录，由 `go:embed` 随服务发布；
- 生产运行不依赖 Node.js、外部字体 CDN 或额外 Web 服务。
