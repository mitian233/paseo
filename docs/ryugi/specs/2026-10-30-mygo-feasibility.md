# MyGo 可行性与功能对照（实施准入记录）

平台列：S=源码存在共用桌面路径，V=本机运行过，U=尚未真实操作验收。S 不代表通过；macOS/Windows 本会话未运行，禁止标 ✓。网页面板、自动化、插件客户端已获用户批准放入后续路线图。语音/Mermaid 没有获准延期。

| 功能                              | macOS | Windows | Linux           | 现有依据/入口                                          | 验收操作                                   | 新实现职责                  |
| --------------------------------- | ----- | ------- | --------------- | ------------------------------------------------------ | ------------------------------------------ | --------------------------- |
| 主机TCP/SSH/relay、配对、身份固定 | S/U   | S/U     | S/U             | host-runtime.ts:580；add-host-modal                    | 添加并认证，断线后恢复；身份变化拒绝       | hosts/session/transport     |
| routed 主机                       | S/U   | S/U     | S/U             | docs/connected-hosts.md                                | 经已有连接访问目标，不扩大权限，释放 scope | session                     |
| 项目/工作区创建、归档、恢复、筛选 | S/U   | S/U     | S/U             | workspace-screen；sidebar-workspace-list               | 创建/切换/归档/重启客户端                  | 原生工作台                  |
| agent 创建/模式/模型/控制/子代理  | S/U   | S/U     | S/U             | composer；docs/agent-lifecycle.md                      | 发送、中止、排队、恢复、打开子代理         | session/workbench           |
| 会话历史/流/权限/工具卡片         | S/U   | S/U     | S/U             | agent-stream；timeline-sync.md                         | 长历史分页、断线gap追齐、允许/拒绝         | timeline owner              |
| 输入草稿/附件/命令/提及           | S/U   | S/U     | S/U             | composer；draft-store                                  | 草稿重启保留、附件上传并发送、取消         | workbench/storage           |
| 终端输出/输入/resize/快照/恢复    | S/U   | S/U     | S；Conn局部V    | terminal-pane；binary-frames/terminal.ts               | 真实daemon按键到PTY回显，resize/reconnect  | terminal adapter            |
| 文件树/下载上传/编辑/冲突         | S/U   | S/U     | S/U             | file-explorer-pane；file-pane                          | 读写远程路径，版本冲突不覆盖               | workbench/session           |
| 代码编辑/查找/选择/Vim            | S/U   | S/U     | 组件局部V       | editor；CodeMirror usage                               | 键盘、IME、撤销、搜索与已有Vim行为         | MyGo editor adapter         |
| Git状态/diff/分支/PR审阅          | S/U   | S/U     | diff组件局部V   | diff-pane；git/pull-request-panel                      | 实际PR读评论/审阅，变更对齐                | session/workbench           |
| 流式Markdown/数学/图表/图片       | S/U   | S/U     | Markdown包测试V | components/markdown；renderer docs                     | 表格选择、数学、流式完整呈现               | native renderers            |
| Mermaid                           | S/U   | S/U     | S/U             | markdown/fence/mermaid                                 | 代表图表语法和标签/交互对照                | 待验证原生渲染              |
| 语音/听写                         | S/U   | S/U     | S/U             | audio-engine.web.ts；use-dictation-audio-source.web.ts | 设备权限、PCM、识别、重连、静音            | 平台音频采集+既有speech协议 |
| 多窗口/标签/分栏/恢复             | S/U   | S/U     | S/U             | preload.window；workspace-layout-store                 | 窗口开关、焦点、恢复、跨窗口订阅释放       | MyGo/workbench              |
| 菜单/快捷键/命令面板              | S/U   | S/U     | S/U             | features/menu.ts；composer keyboard                    | 终端/编辑器/输入框快捷键优先级             | MyGo/workbench              |
| 通知声音/点击/深链                | S/U   | S/U     | S/U             | features/notifications；agent-navigation               | 后台通知点击正确主机会话，冷启动深链       | MyGo/platform               |
| 拖放/剪贴板/外部链接与编辑器      | S/U   | S/U     | S/U             | preload；features/editor-targets                       | 本地文件先上传，远程路径不误作本地         | platform                    |
| 设置/外观/翻译/无障碍             | S/U   | S/U     | S/U             | settings；design.md                                    | 字号/主题/语言、IME、键盘与读屏            | native ui/storage           |
| CLI安装                           | S/U   | S/U     | S/U             | integrations/cli-install；bin/paseo                    | 安装PATH后远程命令可用，不启动daemon       | 独立CLI方案                 |
| 更新通道/分批/重启更新            | S/U   | S/U     | S/U             | auto-updater.ts:143                                    | stable/beta、manifest复核、签名、安装恢复  | MyGo updater+policy         |
| 诊断/空闲/电源/权限               | S/U   | S/U     | S/U             | daemon-manager.ts；desktop-permissions                 | 日志、空闲、休眠恢复、拒绝权限             | MyGo/platform               |

## Linux 已运行证据

工具链 `/tmp/paseo-mygo-toolchain/go/bin/go` 为官方 Go1.27.3；MyGo checkout `/tmp/paseo-mygo-source` revision 2b66322。全部命令用 CGO_ENABLED=0、GOTOOLCHAIN=local。

- `go run ./examples/paseo-native-probe`：sidebar=true tree=true rendered=1400000；编辑前后 diff hunks=1，buffer 从 first/second 到 first/second/third。ui.Tester 为内存渲染，不证明真实IME/鼠标行为。
- `go run ./examples/paseo-terminal-probe`：Conn输出成功，resize [[78 20]]，原生截图PNG22486字节。程序直接写入13字节，不是用户按键；不连接Paseo daemon。
- `go test ./ui/editor ./ui/markdown ./plugins/terminal`：Markdown、terminal通过；editor的 TestLineBackgrounds、TestTextBackgrounds、TestScrollYAndLineTop 失败（期望3行得到2行），未定位，不重复跑来掩盖。
- `go test ./examples/code-editor ./examples/markdown-chat ./examples/gallery --run Test --count=1`：code-editor无测试，markdown-chat与gallery通过。
- 原生窗口code-editor例子运行到70秒截止，没有声明交互验收通过。

## Electron 数据来源与过渡方案

已核实：host-runtime 使用 AsyncStorage，主机保存键为 `@paseo:daemon-registry`；draft/sidebar/layout也用 AsyncStorage；web版本实质存储须通过现有依赖实现及Electron origin确认，不把它说成普通hosts.json。桌面主进程设置文件为 `desktop-settings.json`，settings含releaseChannel、notifications、daemon；后者明确不导入新客户端。

方案：不解析Chromium私有LevelDB格式。实现现有Electron应用端的显式导出（只导出客户端连接配置、用户设置和布局的可移植字段），Go端导入、预览和逐字段校验；认证凭据经一次性本地文件/用户确认导入后存Secrets，文件权限与删除提示必须明确，不写日志。不覆盖旧数据；旧socket/pipe显示不支持并允许用户添加远程端点。导出工具为转移用户状态的产品功能，须适配实际schema且保持用户主动操作。如现有版本已有可用导出，优先复用，不能平行新增约定。没有实际运行旧Electron导出/导入，尚未通过验收。

## CLI 耦合结论

`packages/desktop/bin/paseo` 通过ELECTRON_RUN_AS_NODE运行app.asar内 @getpaseo/cli，并在macOS依赖Paseo Helper；不能拷贝到MyGo应用直接运行。安装器创建 ~/.local/bin/paseo 符号链接或paseo.cmd并更新shell PATH。

方案：新Go二进制提供远程CLI模式，共用Go session API，覆盖当前已可对远程host运行的命令、退出码及JSON输出，不打包Node/Electron/server。安装行为保留PATH安装/检测，但不得用不完整CLI冒充已有CLI；远程命令清单在实施前由packages/cli核实。daemon生命周期子命令明确不支持，属于用户已排除范围。CLI完整性是额外工作量而非原理阻塞。

## 剩余高风险路径

语音：保留客户端PCM→daemon STT；Linux ALSA/Windows WASAPI社区无cgo候选有源码，macOS需CoreAudio purego；没采集设备实测。Mermaid：Go parser/layout候选不等于Mermaid.js，MyGo SVG text缺失，需Go文字布局转路径/原生绘制及代表语法比较；尚未实测，必须全覆盖当前功能，不能静态源码降级。插件客户端已由用户批准后续实现，不再作为本期GO阻塞。

## 实施GO与发布GO分离

本记录支持盘点/实验已形成可执行工作分解，不支持声称产品完成。请求advisor决定是否允许独立的协议/session与原生主路径实施，同时保留语音、Mermaid、真实daemon终端及三平台为相应功能/发布硬验收条件；若这些路径不可实现需再向用户报告，不自动砍功能。macOS/Windows无法在当前Linux宿主原生运行，后续需要相应runner或设备；不把该外部条件伪装为现在已验证。
