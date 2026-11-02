# 第一波执行计划与记录

依据：2026-10-30-mygo-execution-contract.md。`omp --model @advisor` 最新复审明确条件GO，准许A/B并行，A完成后C；准许实施不等于发布。用户延期语音、插件客户端、网页面板、自动化；2026-11-02 明确要求停止 Mermaid 补齐，记录阻碍后跳过。原 Mermaid 验收尚未通过。

## A：现有协议客户端

Files: packages/native-desktop/go.mod、go.sum、internal/client/、internal/protocol/。拥有Go模块依赖修改权。先完成现有hello/server_info、请求关联、关闭/断线unknown和二进制帧，再真实daemon验证；不改daemon、wire schema或现有Electron。先按现有wire tag，切勿臆造方法。

## B：Mermaid

Files: packages/native-desktop/internal/mermaid/（唯一归属）。无权改go.mod/go.sum；缺依赖发给主代理。候选raster中文缺字，先证明字体支持再写适配。允许在/tmp实验，不以WebView或源码fallback替代。全语法/中文未通过时必须报告，不声称完成。

## C：原生会话

A行为检查和真实daemon smoke通过后已实施。入口 `packages/native-desktop/cmd/paseo-desktop`；消费既有Connect/Call/Observe，覆盖主机身份、工作区/agent目录、历史与实时timeline、prompt提交、权限决定及原生Mermaid代码块。仅permission回复复用daemon给出的业务request ID，禁止同ID并发覆盖等待者；普通RPC仍由client管理request ID。

## 验证与集成

子代理不跑build/lint/tests/formatter中途检查（遵守共享repo策略），实现后交主代理统一运行；行为验证脚本由主代理执行。产品代码测试遵循仓库行为测试约束，不写源码文本断言。主代理负责Go依赖、跨slice集成、真实smoke、npm typecheck/lint和审核记录。

## 当前验收记录

- A的连接取消、订阅归属、能力协商等review问题已修复。真实独立临时daemon上，Go客户端握手、目录查询、owned timeline订阅、历史读取和释放订阅通过；未触碰6767主daemon或wire schema。
- C已在实际MyGo窗口连接临时daemon，显示中文会话、发送中文prompt并接收流输出。provider使用仓库的确定性测试实现，不能据此声称真实Claude/Codex全流程通过。窗口视觉仍等待用户反馈。
- 运行验收发现并修复：缺messageId导致接受的用户消息不落历史；跨turn合并assistant文本；内容区交叉轴居中；渲染状态变化导致图表控件身份变化。消息持久化、跨turn、permission ID并发和全屏缩放均保留了失败后通过的回归证据。
- 权限批准与拒绝已在临时daemon上实跑，等待匹配的resolution后移除请求。后续重建截图环境受窗口焦点限制，未取得持久权限截图，不能把权限视觉验收标为通过。
- B已接入Markdown闭合代码块：本机后台渲染、原生图片、显式错误/复制源码/重试、缩放/平移/全屏。中文代表图已实际显示；放大到1554×249后，水平滚动产生了已查看的截图像素变化。字体当前来自客户端安装，尚未完成跨平台字体交付。
- **B暂停，未验收。** 已补充部分候选库语法，并通过22组代表图的SVG可见文本检查；这不能证明完整兼容。随后原生PNG smoke发现22组图的左上背景均为透明，未保留SVG的百分比背景矩形，输出见 `artifact://295`。百分比几何是待调查阻碍；完整语法、图形保真、中文与跨平台字体验收仍未完成。按用户要求停止修复与验证，移除本轮临时PNG smoke及未实施的百分比几何回归草稿，保留已完成代码。
- 前次定向Go检查通过：client、protocol、mermaid（指定CJK字体）、timeline、workbench，以及桌面入口编译。实际窗口截图和2.5秒会话录屏保存在 `.ryugi/artifacts/artboard/verify-native-desktop/pages/verify/`；画板validation的warnings/pending均为空，尚无用户通过判定。本轮按用户要求暂停Mermaid与视觉验收。
- 根仓库lint通过。根typecheck失败，包含app/website依赖缺失、Expo基础tsconfig缺失、CodeMirror类型冲突等诊断；build:client也存在owned.ts及daemon-client.ts类型错误。仅安装了所需server依赖闭包，没有修改TS产品代码来掩盖问题，不能称仓库检查全绿。
- MyGo editor上游三项换行失败已记录，未重复确认。macOS/Windows未实跑；终端真实PTY回显及第二波attach/input未实现。语音、插件、网页面板和自动化按用户指示延期。本轮没有发布或完整替代完成声明。
- 本轮最终审查的七项重要问题已修复：未知发送结果保留原messageId；分页改用不受活动时间影响的稳定排序；观察结束后禁止继续发送与权限操作；订阅串行等待可取消且本地释放不再被阻塞；释放失败关闭所属连接；有界事件队列保留最新连接状态；未开始发送的排队请求在断线时返回disconnected。prompt重试、分页、取消选择和四项client回归均保留了失败后通过的证据；history错误回归通过，但其修复前运行因中间态签名不一致未执行，不声称该项RED→GREEN。
- 本轮MyGo验证通过：`go test -race ./internal/client ./internal/protocol ./internal/timeline -count=1`、`go test -race ./internal/workbench -count=1`、桌面入口build及生成二进制的`--help`实际执行。检查只覆盖MyGo相关包，不再运行全仓库检查；未重新运行Mermaid测试或视觉验收。
- 本轮真实daemon smoke未通过启动阶段：临时随机端口/临时数据目录实例复用现有daemon测试夹具，其terminal worker缺少`@getpaseo/plugin/server`模块解析。没有修改daemon产品代码，也没有触碰6767实例；按用户限定的MyGo范围记录环境阻碍，不继续补daemon依赖。前次真实daemon成功记录不代表本轮新修复已完成该验证。
- 较低优先级暂缓：timeline合并文本未检查源序列相邻性（交错tool更新可能使两段文本合成一行）。视觉验收与macOS/Windows实机仍未通过；不得称完整替代完成。
