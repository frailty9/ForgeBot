# Admin Web UI 设计

## 1. 设计目标

Admin Web UI 是 ForgeBot 单实例的本地管理控制台。它应让管理员快速判断实例状态、安全修改运行时配置，并执行少量明确的运维操作。界面不模拟聊天产品，也不采用通用 SaaS 仪表盘的卡片堆叠；整体表达应自然、克制、易读，像一个长期维护的工程工作台，而不是一块需要学习的设备面板。

## 2. 视觉规格

### 2.1 Purpose Statement

控制台服务于部署和维护 ForgeBot 的技术管理员。最重要的信息不是宣传性指标，而是“当前是否可用、正在使用哪一版配置、修改会造成什么动作、失败后如何恢复”。

### 2.2 Aesthetic Direction

采用 **Natural Minimal（自然极简）**。以留白、柔和边界、低对比层次和稳定的文字排版建立秩序，让状态、配置和风险提示自然成为视觉重点。保留少量工程编号和状态标签用于定位，不使用贯穿页面的装饰性信号轨。

### 2.3 Color Palette

浅色主题：

- `#F7F7F4`：页面背景；
- `#FFFFFF`：内容面与表单面；
- `#E5E7E3`：边界、分隔和禁用态；
- `#1F2723`：主文字；
- `#4F5B54`：次级文字。

深色主题：

- `#171A18`：页面背景；
- `#202522`：内容面与表单面；
- `#38413C`：边界、分隔和禁用态；
- `#F0F2ED`：主文字；
- `#B8C0B8`：次级文字。

共用强调色：

- `#B7781C`：主操作和待处理；
- `#2E7769`：健康和成功；
- `#B94A45`：错误和破坏性操作。

颜色全部定义为 CSS Custom Properties。状态不得只通过颜色表达，同时使用文字、图标和形状。主题只支持浅色与深色两种模式，默认使用 `prefers-color-scheme` 跟随系统；用户主动选择后写入 `sessionStorage`，页面刷新时保持本次会话选择。

主题规则：

- `data-theme="light"` 和 `data-theme="dark"` 是唯一的显式主题值，不引入第三套主题。
- 未设置用户偏好时使用 `@media (prefers-color-scheme: dark)`，跟随操作系统。
- 顶部命令条提供浅色、深色、跟随系统三个选项；“跟随系统”只是主题来源选择，渲染结果仍只有浅色或深色。
- 右侧工作区拥有独立顶部条，顶部条横跨主内容宽度并保持轻微粘性；主题入口使用阳光、月亮、`Auto` 三个图标按钮，不使用下拉选择框。
- 全局顶部条只展示 runtime 状态和主题入口，不展示配置版本，也不提供刷新配置操作。
- 主题切换只更新 CSS Variables，不重建页面状态，也不触发管理 API 请求。
- 两套主题保持相同的间距、字号和组件结构，仅调整背景、边界、文字和状态对比度。
### 2.4 Typography

- 中文与正文：优先使用系统的中文无衬线字体栈；
- 版本号、状态码、时间、路径和配置值：`IBM Plex Mono`；
- 仅将 `IBM Plex Mono` 的 Latin 子集随前端资源打包，控制台运行时不依赖第三方字体 CDN；
- 完整中文 Web Font 会产生大量分片与 CSS，不在首期打包；系统字体栈覆盖 Windows、macOS、Linux 与 Android 的常用中文字体。

### 2.5 Layout Strategy

- 桌面端使用 12 列克制布局：宽屏为 224px 展开式左侧导航、弹性主工作区、280px 上下文检查栏；中等宽度收缩为 72px 图标轨；
- 左侧导航支持二级菜单。配置按关注点拆分为 Bot 与提示词、LLM 与密钥、消息 Gate、结构化检索、平台连接、MCP 与 Skill 等页面；二级菜单只在配置域激活时展开，不改变主工作区的状态布局；
- 页面采用独立滚动容器：`#app > (nav.scroll-y + div.workspace-shell > (header + main.scroll-y))`。导航滚动、主内容滚动和顶部条固定互不影响；
- 概览使用宽窄交替的摘要区和清晰分组，不使用四等分统计卡片矩阵，也不依赖装饰性错位；
- 配置页左侧为领域锚点，中间为宽表单，右侧固定展示草稿状态、校验结果和应用影响；
- 版本历史使用时间轴与版本刻度，不使用普通数据表作为唯一表现；
- 小于 960px 时收起检查栏，小于 1100px 时导航收缩为 72px 图标轨，小于 720px 时导航变为顶部或抽屉；复杂配置仍可查看，但提示使用宽屏完成批量编辑。

### 2.6 Motion

- 页面进入仅使用一次 120–180ms 的分段显现；
- 状态切换使用短促的颜色和位置过渡，不使用持续装饰动画；
- 尊重 `prefers-reduced-motion`；
- 运维操作的等待态必须反映真实请求状态，不以虚假进度条表达。

## 3. 技术选型

### 3.1 前端基础

- Vue `3.6.0-rc.8` Vapor Mode，实施时锁定精确版本；
- TypeScript 严格模式，API 边界使用 `unknown` 后显式收窄，禁止以 `any` 绕过类型检查；
- Vue 3.6 RC 的上游声明暂不兼容 `exactOptionalPropertyTypes`，因此仅跳过第三方声明检查；应用源码继续启用 `strict`、`exactOptionalPropertyTypes` 和 ESLint 的 `no-explicit-any`，稳定版升级时重新验证并移除此例外；
- Vite；
- Pinia，仅保存认证会话、服务端快照、配置草稿和操作状态；
- 原生 History API 的轻量路由层；四个一级页面不值得为未知的 Vapor 兼容性引入传统 `RouterView`；
- Sass/SCSS + CSS Custom Properties；
- UnoCSS 仅生成布局、间距和少量状态工具类；
- 静态 SVG 图标，统一封装为 Vapor 兼容组件；
- Vitest + Vue Test Utils + Playwright。

### 3.2 Vapor 边界

- 所有可视组件必须由 Vapor 编译，入口使用 Vapor runtime；
- 不引入 Ant Design Vue 或其他依赖传统 VDOM/render function 的组件库；
- 第三方依赖不得向可视树注入传统 Vue 组件；
- Pinia 等非渲染依赖必须先通过最小构建和运行冒烟测试；
- 升级 Vue 版本前运行 Vapor 编译、类型检查、单元测试与浏览器回归；
- Vue 3.6 稳定版发布后，在独立提交中升级并移除 RC 风险说明。

### 3.3 样式边界

SCSS 的用途是改善 CSS 组织，而不是构造运行时样式系统：

- 使用嵌套、`@use` 模块、局部变量和函数整理组件样式；
- 使用 CSS Custom Properties 表达运行时主题、颜色、间距和尺寸令牌；
- 禁止使用会在多个选择器内展开大量重复声明的 mixin；
- 避免 `%placeholder`/`@extend` 造成难以预测的组合选择器；
- SFC 默认使用 `<style scoped lang="scss">`，全局只保留 reset、字体和 token；
- UnoCSS 只用于约定范围内的布局工具，不用长串原子类替代可读的语义类名；
- CI 记录 JS/CSS gzip 体积，超过预算时失败。

首期建议预算：入口 JS gzip 不超过 90 KiB，CSS gzip 不超过 30 KiB，字体单独统计并按字形子集优化。

## 4. 页面与交互

### 4.1 登录页

- 左侧展示 ForgeBot 实例标识与连接说明，右侧为管理令牌输入区；
- 提交后以 `GET /v1/admin/config` 验证令牌；
- 成功后将令牌写入 `sessionStorage`，关闭标签页即失效；
- 页面不提供“记住我”，也不将令牌写入查询参数或 Local Storage。

### 4.2 概览页

- 全局顶部条展示 `ready / applying / restarting / degraded / unavailable`，使用文字和状态点共同表达；
- 页面标题区只保留页面标识 `INSTANCE / OVERVIEW` 和主标题，不额外放置长副标题；
- 主区域优先展示连接器和已连接群组，采用 5:7 的反向宽度比例：连接器摘要放在左侧，群组列表放在右侧；
- LLM 配置摘要作为独立区块保留，未来允许展示多个正在使用的模型；
- LLM 花费统计作为预留区块，后端具备统计能力后再接入意图判断、问答、反思和 Embedding 等维度；
- Runtime 详细卡在当前原型中暂时隐藏，待运行状态与分析指标的最终信息边界确定后再决定放回概览或迁移到运行维护页；
- 工具与 MCP 作为下方独立服务区展示，连接器、群组、LLM 和工具之间保持清晰分组；
- 右侧检查栏展示 API Key 是否配置和最近错误；
- Gate 统计以紧凑计数条呈现，不制造无意义图表。

### 4.3 配置页

领域顺序：

1. Bot 与提示词；
2. LLM 与密钥；
3. 消息 Gate；
4. 结构化检索；
5. 平台连接器与群组绑定；
6. 管理员与运维群组；
7. 反思与文档沉淀；
8. MCP 与 Skill；
9. 日志及静态配置只读区。

交互规则：

- 服务端快照和编辑草稿分离；任何表单修改只改变草稿；
- “刷新配置”属于配置页面的局部操作，用于重新读取服务端配置快照；全局顶部条不提供该操作；
- 刷新配置前若存在未保存草稿，必须提示用户；确认刷新后才以服务端快照替换草稿；
- 页面离开、刷新或重新加载服务端配置前提示未保存变更；
- “校验”不改变 runtime；“应用”必须先通过最新一次服务端校验；
- 右侧检查栏显示已修改领域、当前版本和预计应用动作；最终动作仍以服务端返回为准；
- 列表项使用稳定 ID，删除连接器时同步提示受影响的群组、管理员和运维绑定；
- 密钥输入区与普通配置提交区隔离，不进入配置差异摘要。

### 4.4 运行维护页

- 三个独立操作单元：软重启、重载搜索索引、扫描反思队列；
- 每个单元显示适用条件、最近状态、触发按钮和结果详情；
- 请求期间禁用同一操作，允许不冲突的状态读取；
- 软重启要求确认并记录原因；索引重载展示 documents、sections、warnings 和 took；反思扫描展示 processed、retried 和 skipped。

### 4.5 版本历史页

- 时间轴按版本倒序展示版本号、来源和更新时间；
- 当前版本有明确定位；
- 回滚操作显示“从当前版本生成一个新的回滚版本”，避免误解为删除历史；
- 回滚使用 `expected_version`，冲突后保留页面状态并要求重新加载。

## 5. 前端架构

### 5.1 目录

```text
web/
  package.json
  vite.config.ts
  src/
    app/                 # 入口、Shell、路由和全局 provider
    api/                 # HTTP client、DTO、边界校验和错误类型
    assets/              # 字体、SVG 与全局 SCSS
    components/base/     # Button、Field、Dialog、Notice 等无业务组件
    features/auth/
    features/overview/
    features/config/
    features/operations/
    features/history/
    stores/              # Pinia setup stores
    utils/               # 无状态格式化与克隆/差异工具
  tests/
internal/server/adminui/
  embed.go
  handler.go
  dist/                  # Vite 生成并由 Go embed 嵌入
```

### 5.2 组件地图

| 组件 | 单一职责 | 主要输入/输出 |
| --- | --- | --- |
| `AdminApp` | 组装认证边界、路由和应用壳 | 读取 auth/router store |
| `AppRail` | 一级导航和当前实例状态 | `route`, `runtimeState`; emit `navigate` |
| `ContextInspector` | 展示当前页面的状态、变更和操作反馈 | typed inspector model |
| `LoginPanel` | 收集令牌并触发验证 | emit `authenticate(token)` |
| `OverviewView` | 编排概览数据区 | stores only, no API details |
| `RuntimeSignalRail` | 表达 runtime 生命周期 | `RuntimeStatus` |
| `ConfigView` | 编排配置领域、草稿和提交栏 | config store actions |
| `ConfigSectionNav` | 配置领域导航及错误计数 | sections; emit `select` |
| `*ConfigSection` | 编辑单一配置领域 | typed `v-model`, validation issues |
| `SecretField` | 只写敏感字段的替换/删除交互 | status; emit replace/delete |
| `OperationsView` | 编排三个运维操作 | operations store actions |
| `OperationUnit` | 单一操作的说明、确认和结果 | operation model; emit run |
| `HistoryView` | 获取并编排版本历史 | history store actions |
| `RevisionTimeline` | 展示版本并发出回滚意图 | revisions; emit rollback |
| `ConfirmDialog` | 可访问的危险操作确认 | title/body; emit confirm/cancel |

路由级 View 只负责组合；HTTP、副作用、草稿状态和领域校验分别进入 API client、Pinia store 与 composable。

### 5.3 状态模型

- `authStore`：内存令牌、会话恢复、认证状态；
- `runtimeStore`：当前服务端快照、runtime 状态、Gate 统计；
- `configStore`：不可变基准快照、可编辑草稿、dirty paths、校验结果和版本冲突；
- `operationStore`：每种操作独立的 pending/result/error；
- 页面派生状态使用 `computed`，网络和 sessionStorage 写入使用显式 action，不在 computed 中产生副作用。

## 6. HTTP 与后端契约

### 6.1 使用现有接口

- `GET /healthz`、`GET /readyz`；
- `GET /v1/admin/config`、`POST /v1/admin/config/validate`、`PUT /v1/admin/config`；
- `GET /v1/admin/config/history`、`POST /v1/admin/config/rollback`；
- `GET /v1/admin/runtime`、`POST /v1/admin/runtime/restart`；
- `POST /v1/admin/search/reload`；
- `POST /v1/admin/reflection/scan`；
- `GET|PUT|DELETE /v1/admin/llm/api-key`。

### 6.2 实现前必须补齐的契约

当前配置读取结果是脱敏视图，而更新接口接收完整 `RuntimeConfig`。这不具备安全往返编辑能力：直接提交脱敏结果可能丢失 `llm.api_key_ref`、OneBot token/secret、MCP key reference 或环境字段。

实现阶段先增加明确的管理 DTO：

- GET 返回完整的非敏感可编辑字段和每个敏感字段的 `configured` 状态；
- PUT 中缺省的敏感字段表示 **keep**，不得解释为空字符串；
- 替换与删除使用明确动作，不能依赖空字符串的隐含语义；
- LLM API Key 继续使用现有独立接口；
- OneBot 凭据和其他敏感扩展值如果首期允许在 UI 修改，也必须使用同样的只写动作模型；
- 后端使用稳定错误码及可选字段路径，前端不得解析英文错误字符串决定流程；
- 更新成功返回规范化后的可编辑快照、实际应用动作与 runtime 状态。

建议错误结构：

```json
{
  "error": {
    "code": "version_conflict",
    "message": "configuration version changed",
    "field": "platforms.connectors[0].onebot.api_url"
  }
}
```

### 6.3 请求层

- 使用一个轻量、强类型的原生 `fetch` client；
- 自动附加 `Authorization: Bearer`，但绝不记录该 header；
- 每个页面请求使用 `AbortController`，路由离开时取消；
- 统一处理 JSON、204、401、409、503 和网络错误；
- 不自动重试写操作；读取操作只允许用户触发重试；
- 所有 API DTO 在边界收窄，内部组件只接收已验证类型。

## 7. 静态资源与路由

- 开发时 Vite 提供 HMR，并将 `/v1`、`/healthz`、`/readyz` 代理到 ForgeBot；
- 生产时 Vite 输出到 `internal/server/adminui/dist`；
- Go 使用 `embed.FS` 嵌入构建产物，并在 `/admin/` 下提供静态文件；
- `/admin/*` 对未知前端路径回退到 `index.html`，`/v1/*` 永不进入 SPA fallback；
- HTML 使用 `Cache-Control: no-cache`，带 hash 的 JS/CSS/font 使用长期 immutable cache；
- 发布构建顺序为前端校验与构建后再执行 Go 测试和编译；运行 ForgeBot 不需要 Node.js；
- 生成资源是否提交由实现任务确定，但源码归档和发布流水线必须始终能产出可运行的单二进制。

## 8. 安全设计

- Bearer token 仅存于内存和 `sessionStorage`；
- 不使用 Cookie，因此不引入基于 Cookie 的隐式认证与 CSRF 状态；
- 密钥输入使用 password 控件，提交后立即清空；
- UI 不渲染未经信任的 HTML，不使用 `v-html` 展示服务端错误或配置内容；
- 服务端增加 CSP、`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer` 和 frame 限制；
- 所有危险操作使用焦点可控、键盘可操作的确认对话框；
- 对静态 YAML 路径仅作必要展示，避免暴露不影响操作的主机绝对路径。

## 9. 可访问性与响应式

- 所有输入有可见 label、说明和错误关联；
- 键盘可完成登录、导航、配置编辑、确认和回滚；
- 焦点环具有高对比度，弹窗关闭后恢复触发点；
- 状态变化通过 `aria-live` 的克制区域播报；
- 桌面端为主，但 360px 宽度下可完成登录、查看状态和紧急软重启；
- 表单密集页面在小屏不隐藏字段，只改变为单列并提示宽屏体验更佳。

## 10. 测试与质量门禁

### 10.1 前端

- `vue-tsc --noEmit` 零错误；
- ESLint 与格式检查通过；
- Vitest 覆盖 API client、auth store、配置草稿、版本冲突和敏感字段动作；
- Playwright 覆盖登录、401 失效、配置校验/保存、冲突、API Key 替换、运维操作与回滚；
- 构建产物扫描不得包含测试使用的令牌或密钥；
- 浏览器检查无未处理异常、无关键可访问性错误。

### 10.2 后端

- 管理 DTO 脱敏、keep/replace/delete 语义与错误码测试；
- SPA fallback 不覆盖 API 404；
- 静态资源 MIME、缓存头与安全头测试；
- `go test ./...` 和 `go vet ./...` 继续通过。

### 10.3 Vapor 验收

- 构建配置明确启用 Vapor compiler；
- 可视依赖清单不包含传统 Vue 组件库；
- 产物依赖检查确认页面组件未引入 `@vue/runtime-dom` 的 VDOM 渲染路径；
- 在 Chromium 上完成四个页面的交互冒烟测试。

## 11. 实施顺序建议

1. Vapor/Pinia/SCSS/UnoCSS 最小兼容性与产物验证；
2. 管理 API 可安全往返 DTO、稳定错误码及后端测试；
3. 前端壳、认证、轻量路由与 API client；
4. 概览与运行维护页面；
5. 配置编辑、密钥操作和版本冲突；
6. 历史与回滚；
7. Go embed、缓存/安全头、端到端测试与文档。

本设计确认后再生成 `tasks.md`，任务确认后才进入代码实现。
