# Changelog

## 0.1.2

- 账号卡片补齐「测试模型」（对齐 workbuddy 面板）：
  卡片新增「测试」按钮 → 弹窗选择模型 → 发起一次真实推理。
  测试走流式首帧即停（收到首个有效输出立即返回，不等全文），
  并拦截 HTTP 200 下的 `STREAM_ERROR` 帧（额度耗尽/限流不会被误判为活跃）；
  上游不回包由 20 秒上下文兜住。模型列表只含对话类别名——生图/生视频
  会真正触发上游生成，既耗额度又要等数十秒，且失败原因与账号不可用难区分。
- 修复宿主 OAuth 登录入口报 `failed to generate authorization url`：
  插件声明了 `auth_provider`，却只实现了 `auth.identifier`，宿主的
  `/v0/management/<provider>-auth-url` 调用 `auth.login.start` 拿到
  `unknown_method` 后统一回该错误。现补齐 `auth.login.start` /
  `auth.login.poll` / `auth.parse` / `auth.refresh`，并把宿主弹窗接到
  「浏览器 Cookie 导入」桥接页：豆包的登录态是 HttpOnly Cookie，只能由用户
  从浏览器导出，插件不实现一键授权（与既定认证路线一致）。
  登录成功的判据是「账号集合出现新成员」或「已有账号 UpdatedAt 晚于会话开始」，
  不使用宿主文件时间戳——插件刷新存量账号也会重置它，会造成未授权即成功。
- 新增回归：
  - Go 单测 20 个（首帧即停、错误帧拦截、空回复、超时兜底、登录起点/轮询/
    历史账号不误判、账号文件认领、刷新、方法分发、帧判定语义）；
  - 面板 `test/doubao/panel_test_model_repro.mjs`（19 断言）；
  - 桥接页 `test/doubao/login_bridge_repro.mjs`（18 断言）。

## 0.1.1

- 面板补齐 management key 鉴权（修复生产「导入失败：missing management key」）：
  宿主对 `/v0/management/*` 统一鉴权，不带 `Authorization` 时返回
  401 `{"error":"missing management key"}`。面板此前直接裸 fetch，
  导致导入、刷新账号、启停、删除全部被宿主拦断。
  现按既有 6 个插件的同构实现补齐三段式取 key：① CPA 主面板 iframe 内
  从 `localStorage["cli-proxy-auth"]` 解密（XOR+base64）；② URL `?key=`
  读取后从地址栏剔除并落 `sessionStorage`；③ 面板内手动填入。另补
  401/403 时清 key、重新弹框并给出可读提示，不再回显「未知错误」。
- 新增 `test/doubao/panel_auth_repro.mjs`（16 项 DOM 断言，覆盖三条兜底路径与 401 处理）

## 0.1.0

- 插件骨架与宿主注册：`doubao-provider` 能力声明、管理路由、内嵌面板页
- 登录态导入与有效性校验：面板粘贴浏览器 Cookie → 判据端点
  （`accounts.doubao.com/passport/account/info/v2/`）确认登录态 → 原子落盘
  账号文件；垃圾凭据明确拒绝，不落盘
- 账号管理：面板列出/启用/禁用/删除账号，登录态状态上报（`auth_state`）
- Cookie Jar 写回：捕获上游滚动下发的 `Set-Cookie`（如 `gd_random`）并持久化，
  写入前比对文件身份防止覆盖外部新导入的凭据
- 对话链路（`/chat/completion`）：按 `content_block` + `mode_id:"1"` 契约构造请求体，
  解析 SSE 多类增量事件（`STREAM_MSG_NOTIFY` / `STREAM_CHUNK` / `CHUNK_DELTA`），
  识别流内 `STREAM_ERROR` 并转成可操作中文提示；**关键修正**：`client_meta.local_conversation_id`
  为必填项，缺失或空串会被上游以 `710020202 common invalid param` 拒绝
- 模型目录：声明 4 个稳定语义别名（`doubao/default`、`doubao/pro`、`doubao/image`、
  `doubao/video`），`image`/`video` 携带 `skill` 3/5 激活生成路径；未知名原样透传；
  出口自行响应宿主 `oauth-excluded-models`
- executor 接入：非流式返回完整 OpenAI 响应，流式逐帧转 `chat.completion.chunk`
  （首帧带 `role`，末帧 `finish_reason=stop`），深度思考走 `reasoning_content`
- 生图生视频：解析 `CREATION` 块（`block_type=2074`），按 `id` 合并同一资源的
  多帧上报（首帧只有占位尺寸、后帧才带 `key` 与直链）；图片优先取 `image_ori`，
  再调 `/creativity/resource/get_without_watermark` 换无水印全尺寸直链（失败时
  回退流内缩略图，不失败整个请求）；回复转成 OpenAI 多模态 `image_url` + `media` 扩展
  - **实测修正**：`skill`（3/5）不是生成路径的必要条件——上游按提示词意图决定是否
    绘制，不带 `skill` 的「画一只猫」同样产出图片，带 `skill=3` 的闲聊仍只回文本
    （上游回显 `input_skill.skill_id="0"`）。注入保留为语义声明
- 多账号池与故障转移：账号启停/删除直写物理文件；429 / 402 / 401 / 403 / 5xx /
  传输错误按阶梯冷却（30s × 连续失败次数，封顶 10 分钟），业务 400 不计入（换号
  无意义且会误伤健康账号）；同请求内遇 429/额度耗尽/登录态失效立即换号并排除刚失败
  的账号，一次成功即清零；`scheduler.pick` 只在宿主候选集内取号，无可用候选时返回
  `Handled:false` 让位宿主跨 provider 轮换
- Cookie Jar 防覆盖判定改用 SHA-256 内容摘要：Windows 下文件索引可能复用、等长
  JSON 可能落在同一时间戳刻度，`os.SameFile + ModTime + Size` 三项同时失效会让旧
  快照覆盖用户刚重新导入的凭据
