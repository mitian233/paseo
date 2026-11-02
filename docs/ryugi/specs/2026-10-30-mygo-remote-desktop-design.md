# MyGo 原生远程桌面客户端（终审稿：分阶段实施已获准）

## 决策与目标

用户决定：MyGo 和 Go 原生 UI 重写桌面；只连接现有 daemon，不打包管理 daemon。网页面板、浏览器自动化、插件客户端、语音/听写由用户明确批准延期。除上述四项及 daemon 管理外，其余 Electron 桌面用户功能必须完整替代，Mermaid 不允许延期。补齐后续项才可称 Electron 全功能替代；各平台验收前与 Electron 并存。

工作台不用 React Native、Electron renderer 或 WebView。用户网页在后续阶段使用 MyGo 系统 WebView；插件客户端兼容策略留给其后续路线图，当前不实施。Mermaid 仍在完整替代范围，不能默默降级。Go 应用内调用不复制 `paseo:invoke` IPC。以 `CGO_ENABLED=0` 为构建目标；终端通过 purego 在运行时加载 `libghostty-vt`，无需 cgo 但需打包动态库。现有 daemon 和 relay 的线路协议不作破坏性更改。

## 范围及对照清单

以 Electron 桌面真实可用的用户操作为清单逐项核对：主机保存、配对、重连与路由；项目、工作区及 agent 生命周期；会话、输入、附件、权限及工具交互；终端、文件、Git/diff/PR；插件客户端贡献、语音与听写、Mermaid 与富媒体；多窗口、标签和分栏、窗口恢复；菜单、快捷键、拖放、剪贴板、外部编辑器、深链、通知点击、发行通道与更新、诊断及 CLI 安装。对每项记录现有入口、三平台实际可用性、旧行为、新实现、真实验收场景；仅在现有桌面可用时要求对应替代。网页面板及其专属控制和自动化置于最后阶段。不能仅凭 IPC 命令名或组件同名断言覆盖。

本地 daemon 启停、替换、本地管理凭据和 daemon 日志是用户明确排除的 daemon 管理职责；不将其损失重新包装为待确认事项。连接支持 `directTcp`、`remoteSsh`、`relay` 和经 daemon 路由的连接。本地 socket/pipe 不是远程访问方式。CLI 安装是独立桌面用户功能，仍须核对是否与打包 daemon 耦合并实现可用的独立安装路径。Electron 的旧连接若需要迁移，必须先核实原数据存放处及可读性；不能默认这些数据可自动导入或必须手动重配。

## 架构与所有权

单个 Go 应用：transport 负责 TCP/SSH/relay 的帧传输；protocol 实现现有 `packages/protocol` 的线路格式（绝不另立协议）；session 管理单 daemon 的请求、订阅与物理重连；hosts 管理多主机和 routed 候选；directory/timeline owner 负责表面需求、缓存可用性、游标与对账；原生工作台和系统集成直接使用 MyGo。这里只规定职责边界，不预先强制每个职责单独成包。连接与状态可跨窗口共享，窗口关闭释放订阅需求。无 renderer IPC/Base64 帧搬运。

按 `server_info.features` 协商；老 daemon 的既有功能继续可用，新能力缺失时提示更新。结果未知的非幂等操作不能自动重放。目录序列与 generation 一致，时间线缺口逐页追至 `hasNewer: false`；缓存可先展示，但只有权威对账完成才标记新鲜。不得按 advisor 建议删掉这层语义：现有产品的离线阅读及恢复依赖它。先用现有 Go 文件持久化需求评估实际数据量和事务边界；无需提前决定独立数据库或强制三份 JSON 文件。协议用现有向量和真实新旧 daemon 验证；不引入生成工具链或另一套服务端协议。

MyGo Secrets 用于用户密码或令牌，daemon 公钥可作为身份 pin 存储；不得将认证凭据当作 daemon 公钥写在普通配置。Linux 缺少系统凭据库时提供可见的重新输入路径，不能静默写明文。保存主机、设置及窗口布局；必要时保存供离线阅读和游标对账的状态，具体存储方式由可验证的原子性需求决定。旧 Electron 设置/连接导入仅在核实用户数据可访问及具体兼容方案后实施；不假定能读取其缓存，也不强制用户放弃已保存连接。

## 组件实现

Sidebar、Tree、editor、DiffView 的 CGO_ENABLED=0 组合探针已运行，ui.Tester 检出侧栏与树、buffer 修改后 diff 重算。真实 terminal.Conn 窗口报告78×20 resize和22486字节截图；用户按键与真实daemon尚未证明。上游Markdown/terminal测试通过，editor三项换行断言失败未定位。语音/插件延期，Mermaid保留本期闸口。go-mermaid/raster 的无cgo PNG流程已实际绘制simple.mmd，检查图片可见文字与箭头；候选库自行绘制SVG text/marker，避免MyGo SVG缺字。仍须完成代表图表语法、中文、缩放和原生显示验证，不能把一张流程图视为完整兼容。

窗口、菜单、通知、对话框、更新优先 MyGo；设置、附件、日志用 Go 标准库。SSH 通过 `os/exec` 调用系统 OpenSSH，保留配置与 known_hosts。WebSocket 用纯 Go 库；relay 实现现有 Noise NK 协议并用共享向量校验。更新保留现有 stable/beta 用户选择及分批发布规则；CLI 安装、外部编辑器、权限请求、深链和通知按系统分别核对。不要因 Go 端免 IPC 而删掉这些功能。

## 阶段与准入

1. 完成功能对照与三平台证据矩阵；优先验证 Mermaid、终端 Conn、编辑器与更新。语音/插件是用户批准的路线图项，其余不可降级顶替。
2. 实现 Go 协议/单连接会话并用真实 daemon 验证 hello、RPC、订阅、二进制帧、断线与恢复；临时探针只作为验证工具，不维护为产品功能。
3. 接入 TCP、系统 SSH、relay E2EE 和 routed 候选；分别验证认证、身份固定、跨版本能力和断线后的结果未知状态。
4. 原生主路径接入主机、工作区、agent、流式会话、输入附件与权限；从空设置到创建/发送/断网恢复做端到端验收。
5. 完成终端、文件、编辑器、diff/PR 及所有本期重度功能，包括完整 Mermaid；逐项对照 Electron 用户操作验收。
6. 完成多窗口、深链、菜单、通知、剪贴板、拖放、外部编辑器、CLI 安装、设置与用户数据过渡、诊断及签名更新。在 macOS、Windows、Linux 各自真实安装、运行和升级。
7. 后续路线图：语音/听写和插件客户端单独设计实现；网页面板随后，浏览器自动化最后。WebView缺失的CDP输入、日志、对话框、帧订阅须扩展平台能力并验收。

阶段1-6是除网页面板、自动化、插件客户端、语音/听写以外的远程原生工作台完整替代；后续四项全完成才称 Electron 全功能替代。阶段验收用真实daemon及确定性协议/同步测试；三平台记录运行、连接、视觉和更新证据，Linux不能代表其他系统。

## advisor 审核与执行判定

2026-10-30 以 `omp --model @advisor` 调用指定模型角色审核草稿，结论是 **NO GO（条件 GO）**。采纳：缩减预设包层级、移除常驻连接诊断工具、澄清协议是既有协议的 Go 实现、增加终端/语音/Mermaid/插件及三平台验证闸口。拒绝：删掉客户端缓存/时间线对账、仅支持当前 daemon、把认证凭据写入普通 JSON、把 daemon 打包管理重新列为开放问题、将 cgo 与运行时动态库误判为互斥；这些意见与用户范围或已核实的源码/实测矛盾。MyGo 文档可访问，Go 1.27.3 已以官方 SHA-256 校验并在 Linux 运行临时 WebView 探针；该结果不覆盖原生控件和跨平台终端。

最新 `@advisor` 模型复审给出条件GO，明确准许A既有协议client和B原生Mermaid适配并行；A验收后准许C原生会话纵切片。准许实施不等于准许发布。禁止更改daemon/wire、新建探针服务或以源码fallback替代图表。Mermaid中文/全语法、真实daemon终端按键和三平台仍是对应完成/发布硬验收。执行边界见 [执行契约](2026-10-30-mygo-execution-contract.md)，代理划分见 [执行计划](2026-10-30-mygo-execution-plan.md)。

最新复审补充：插件客户端及语音/听写均获用户明确延期；Mermaid明确不允许延期。本次生成并查看 `/tmp/paseo-mermaid-flow.png`，文字和箭头可见，命令为 `CGO_ENABLED=0 GOTOOLCHAIN=local go run ./cmd/mermaid -png -scale 2 -o /tmp/paseo-mermaid-flow.png testdata/golden/simple.mmd`。候选raster包使用纯Go oksvg/rasterx并自行绘制文字与箭头；仍缺代表语法和中文验收。功能矩阵、数据导入和CLI耦合记录见 [可行性记录](2026-10-30-mygo-feasibility.md)，其中历史语音本期要求已被最新决定覆盖。

参考：`packages/desktop/src/preload.ts`、`packages/desktop/src/daemon/daemon-manager.ts`、`packages/app/src/runtime/host-runtime.ts`、`docs/plugins.md`、`docs/architecture.md`、`docs/timeline-sync.md`、`docs/protocol-compatibility.md`、`docs/relay-endpoint-security.md`、`docs/qa.md`；MyGo https://mygo.egoist.dev/docs/ui.md 、https://mygo.egoist.dev/docs/native.md 、https://mygo.egoist.dev/docs/plugins/terminal.md 。
