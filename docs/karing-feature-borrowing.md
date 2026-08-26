# karing 功能借鉴方案文档

> 调研时间：2026-08-26
> 调研对象：`/data/karing`（Flutter 代理客户端，自研 sing-box fork 核心）
> 目标项目：v2rayA（Web 代理管理器，v2raya-core = xray fork）
> 调研方式：4 路并行代码调研（订阅/节点管理、网络系统级、备份同步/UX、核心协议），部分被外移到 vpn-service 包的定义通过 git 历史 `3c00ed64~1` 恢复比对

---

## 1. 背景

用户提供两个 karing 能解析而 v2rayA 不能的订阅后，本项目先后补齐了：

- Clash YAML 订阅解析 + clash UA（commit `516b2129`）
- mieru 协议（核心侧 `core/hint/proxy/mieru`）
- 加密订阅（`subscription-encryption` + AES-128-CBC/MD5 密码解密，commit `fbfc0471`）
- vless ML-KEM `encryption` 参数透传

本方案文档基于对 karing 代码的全面调研，梳理其余值得借鉴的功能与特性，并按价值/成本分层，作为后续迭代的路线图。

---

## 2. 双方能力现状对比总览

| 维度 | v2rayA（本项目）现状 | karing 现状 |
| --- | --- | --- |
| 订阅格式 | v2rayN base64、SIP008、Clash YAML（本会话新增） | clash YAML / sing-box JSON / ss 文本 / wireguard conf / v2ray 文本 / 多行链接，自动探测 |
| 加密订阅 | 已支持（本会话新增，密码存库） | 支持（decryptPassword 订阅级字段） |
| 订阅 UA | 硬编码 `clash-verge` | 每订阅多选 UA + 追加真实 UA + X-HWID 头 |
| 订阅下载策略 | 仅系统 HTTP 客户端（可走代理） | 每订阅 4 态策略（先代理/先直连/仅代理/仅直连） |
| 订阅更新 | 全局 ticker 批量更新（`pre_update.go`） | 每订阅独立间隔 + 多触发点（启动/回前台/连接后） |
| 节点过滤 | 无 | include/exclude 正则 |
| 节点级操作 | 无 | 禁用/收藏/删除跨更新记忆/搜索 |
| 延迟测试 | 一次性并发 ping（`latency.go`） | 并发/超时/重试 3 次/历史保留/自动移除死节点/更新后保留旧延迟 |
| 流量信息 | 纯文本 `Info` 字段 | 结构化解析 `subscription-userinfo` + 到期 14 天预警 |
| 分流路由 | RoutingA 文本 + 端口白名单（两态） | 规则→任意节点/urltest 组/直连/阻断 + 规则级 DNS 覆盖 |
| 规则匹配维度 | 端口/IP 为主 | geosite/geoip/acl/域名 4 类/ip_cidr/port/protocol/进程/网络类型/WiFi SSID + AND/OR |
| 规则命中排查 | 无 | 输入域名显示命中规则与链路 |
| 连接监控 | 流量统计（无进程/规则） | 实时连接列表（进程/规则/链路/速率）+ 一键转分流规则 |
| DNS | 全局 DNS 配置 + GFWList | 四类 DNS 分离（resolver/direct/proxy/outbound）+ 一键自动择优 + FakeIP + staticIP 映射 |
| 自动选优 | 订阅级 AutoSelect 一次性测速 | 容忍值/优先收藏/网络变化重测/当前节点健康检查 |
| 备份 | 无 | 本地按日轮转 5 份 + WebDAV/iCloud/LAN 扫码互传 |
| 运营 | 无 | 远程配置下发 + 公告系统 + 错误弹窗（复制/FAQ/版本号） |
| 协议 | vmess/vless/trojan/ss/ssr/hysteria2/juicity/tuic/anytls/mieru/wireguard | 17 种出站（含 shadowtls/ssh/tor/naive/socks/http） |
| 传输 | tcp/ws/h2/grpc/quic/xhttp(xmux) + reality + ML-KEM | smux/yamux/h2mux multiplex + brutal + tls_fragment/tls_tricks + utls PQ 指纹 + httpupgrade |

**本项目已领先 karing 的（保持即可）**：juicity 协议、xhttp/xmux 传输、vless ML-KEM encryption（sing-box 系不支持）、自定义 inbound。

---

## 3. 第一梯队：订阅/节点管理体验（服务端 + GUI，收益最大）

### 3.1 订阅级 UA 多选 + X-HWID + 下载代理策略

**现状**：`service/server/service/subscription.go` 硬编码 `clash-verge` UA；下载走 `httpClient.GetHttpClientAutomatically()`（透明代理模式可走代理，其余直连）。

**karing 参考**：`lib/screens/add_profile_by_link_or_content_screen.dart:520-630`、`lib/app/modules/server_manager.dart:2472-2492`、`lib/screens/group_helper.dart:241-306`。

**目标**：
- `SubscriptionRaw` 增加字段：`userAgent`（默认 `clash-verge`，下拉多选 karing 同款列表）、`userAgentAppend`（bool）、`xhwid`、`downloadStrategy`（preferProxy/preferDirect/onlyProxy/onlyDirect）。
- 拉取时：`HttpGetUsingSpecificClientWithUA` 增加自定义 header 支持（X-HWID）；下载策略按序尝试直连/代理端口（代理端口 = 当前连接节点的 socks 端口）。
- GUI：订阅编辑弹窗（`modalSubcription.vue`）增加对应表单。

**涉及文件**：`service/db/configure/raw.go`、`service/db/listOp.go`（列扩展）、`service/server/service/subscription.go`、`service/common/httpClient/`、`gui/src/components/modalSubcription.vue`。

**工作量**：约 1 人日。**验证**：用按 UA 差异化返回内容的机场订阅实测四种策略。

### 3.2 include/exclude 正则过滤节点

**现状**：无过滤，导入/刷新全量入列（`import.go` 仅按 protocol://host:port 去重）。

**karing 参考**：`lib/screens/group_helper.dart:1175-1241`、`ServerConfigGroupItem.filterServers`。

**目标**：订阅编辑弹窗增加 include/exclude 两个正则输入框；`Import` 与 `UpdateSubscription` 在解析后按正则过滤节点名（tag）。

**涉及文件**：`service/server/service/import.go`、`subscription.go`、`raw.go`、GUI 弹窗 + i18n。

**工作量**：0.5 人日。**验证**：单元测试过滤逻辑 + 订阅实测。

### 3.3 节点禁用/收藏/删除跨更新记忆

**现状**：无节点级状态，订阅刷新后全部复活（仅有连接映射保持）。

**karing 参考**：`lib/screens/my_profiles_screen.dart:1147-1245`（删除写 `proxyFilterRemove` 黑名单）、`lib/app/modules/server_manager.dart:2116-2159`（fav 上限 10 / recent 上限 5）。

**目标**：
- `SubscriptionRaw` 增加 `disabled`/`removed` 节点 tag 列表（按 tag 匹配，刷新时过滤 + 不复活）。
- 收藏：节点置顶 + 排序权重。
- GUI：节点行操作菜单（禁用/收藏/删除并记忆）。

**涉及文件**：`raw.go`、`subscription.go`（刷新过滤）、`kernel/touch`（状态字段）、`node.vue`。

**工作量**：1~1.5 人日。**验证**：删除节点→刷新订阅→确认不复活。

### 3.4 订阅刷新保留旧延迟 + 延迟测试体系增强

**现状**：`service/server/service/latency.go` 一次性并发 ping；`UpdateSubscription` 按 endpoint/名称重映射已连接节点，但延迟数据丢失。

**karing 参考**：`server_manager.dart:1290-1620`（并发/超时/重试 3 次/自动移除）、`:1738-1772`（按 tag 保留旧延迟）、`:2565-2600`（从核心恢复延迟历史）。

**目标**：
- `UpdateSubscription` 重映射时按 tag 把旧 `Latency` 并入新节点。
- `Ping` 增加失败重试（3 次）与并发上限配置。
- （可选）测速失败自动标记/剔除节点。

**涉及文件**：`service/server/service/subscription.go`、`latency.go`、`db/configure/raw.go`。

**工作量**：1 人日。**验证**：刷新前后延迟字段保持。

### 3.5 订阅流量/到期结构化展示与预警

**现状**：`SubscriptionRaw.Info` 为拼接文本（`parseSubscriptionUserInfo` 已有解析雏形）。

**karing 参考**：`server_manager.dart:2452-2563`（HEAD 读 `subscription-userinfo`）、`my_profiles_screen.dart:713-758`（到期 14 天高亮）。

**目标**：将 upload/download/total/expire 结构化存入 `SubscriptionRaw`（新增 `TrafficInfo` 字段），GUI 订阅表格渲染进度条与到期倒计时。

**涉及文件**：`raw.go`、`subscription.go`、`touch.go`、`node.vue` 订阅表格。

**工作量**：0.5 人日。**验证**：订阅列表显示流量/到期。

### 3.6 每订阅独立更新间隔

**现状**：`pre_update.go` 全局 ticker，所有订阅同间隔批量更新。

**karing 参考**：`server_manager.dart:474-497`、每订阅 `updateDuration`（最小 5 分钟）。

**目标**：`SubscriptionRaw` 增加 `updateIntervalHour`；ticker 循环内按订阅各自到期时间判断。

**涉及文件**：`pre_update.go`、`raw.go`、GUI 编辑弹窗。

**工作量**：0.5 人日。**验证**：不同订阅不同间隔实测。

---

## 4. 第二梯队：路由/DNS 能力（Web 端差异化亮点）

### 4.1 分流规则模型升级：规则→任意节点/组 + 规则级 DNS 覆盖

**现状**：RoutingA 文本规则 + 端口白名单，只能"直连/代理"两态；DNS 全局统一。

**karing 参考**：`lib/screens/diversion_rules_screen.dart:215-474`（规则→出站映射，`dnsServers` 每规则 DNS 覆盖，`:526-688`）、`lib/app/modules/server_manager.dart:281-350`（数据模型）。

**目标（分期）**：
- 一期：路由规则出站目标扩展为"具体节点/订阅组/AutoSelect/直连/阻断/不设置（穿透）"——生成 xray routing rule 时用对应 outboundTag。
- 二期：规则级 DNS 覆盖（xray 核心 DNS 规则 `domain`/`geosite` → 指定 DNS server tag）。
- 三期：GUI 规则管理页（Web 表格化，替代文本 RoutingA）。

**涉及文件**：`service/kernel/v2ray/template_routing.go`（规则生成）、`template_dns.go`（DNS 分流）、`db/configure/`（规则模型）、`gui/src/`（新页面）。

**工作量**：一期 2 人日，二期 1 人日，三期 3 人日。**验证**：核心路由日志验证命中链路。

### 4.2 规则命中检测器（输入域名 → 显示规则与链路）

**现状**：无。用户排查"流量走哪了"只能看日志。

**karing 参考**：`lib/screens/diversion_rule_detect_screen.dart:342-450`（核心 API `outboundQuery` + 规则集下载状态）。

**目标**：Web 页面输入域名 → 后端用核心观测 API（本项目核心已有 MultiObservatory 扩展）返回：编码后域名、命中规则、出站链路、远程规则集状态。

**涉及文件**：`service/server/controller/`（新 API）、`core/hint/app/observatory`（如缺 API 则扩展）、`gui/src/`（新页面）。

**工作量**：1.5 人日。**验证**：域名 `www.google.com` 显示命中代理规则与节点 tag。

### 4.3 连接监控 + 一键从连接创建分流规则

**现状**：仅进出流量统计（无进程/规则/链路维度）。

**karing 参考**：`lib/screens/net_connections_screen.dart:725-1032`（WebSocket 拉流、进程/规则/链路/速率）、`:1335-1515`（点击连接→预填分流规则）。

**目标（分期）**：
- 一期：核心侧采集连接日志（来源 IP:port/目的域名/协议/出站 tag/进程信息——Linux 端经 netlink 获取 pid→进程名）→ API → Web 实时列表。
- 二期：行内"创建规则"按钮，预填域名/IP/端口，落到 4.1 的规则模型。

**涉及文件**：核心连接观测扩展（`core/hint/app/observatory`）、`service/server/controller/`、GUI 新页面。

**工作量**：一期 3 人日，二期 1 人日。**验证**：curl 后能看到连接与进程名。

### 4.4 DNS 一键自动择优（四类 DNS 分离）

**现状**：全局 DNS 手填，无内置列表与择优。

**karing 参考**：`lib/screens/dns_auto_setup_screen.dart:153-420`（内置 ~60 个 DNS，直连/代理双延迟，自动分 resolver/direct/proxy/outbound 写入）、`setting_manager.dart:646-1045`。

**目标**：内置 DNS 列表 + "一键测速择优"按钮；测速经当前代理与直连双路径；自动分级（resolver 用 IP 直连、proxy 用 DoH 防泄漏）。

**涉及文件**：`service/server/service/`（DNS 测速）、`template_dns.go`、`gui/src/components/modalDnsSetting.vue`。

**工作量**：1.5 人日。**验证**：一键配置后核心 DNS 查询正常且无泄漏。

### 4.5 网络变化自动重测 + 当前节点健康检查

**现状**：AutoSelect 订阅级开关，只在订阅更新后一次性测速选择。

**karing 参考**：`setting_manager.dart:1461-1544`（interval/tolerance/优先收藏/`reTestIfNetworkUpdate`/`selectedHealthCheckInterval`）。

**目标**：设置项增加：容忍值（延迟优于当前节点才切换）、当前节点定时健康检查（失败重选）、（Linux 可监听网络接口变化事件触发重测）。

**涉及文件**：`db/configure/setting.go`、`server/service/subscription.go`（AutoSelect 逻辑）、`pre_update.go`（健康检查 ticker）。

**工作量**：1 人日。**验证**：断开网络→恢复→自动重测。

---

## 5. 第三梯队：协议/传输能力（核心侧）

### 5.1 ShadowTLS v3（高价值）

**现状**：无。机场已批量分发 `shadowtls://` 链接。

**karing 参考**：`my_profiles_screen.dart:1470`、历史 `singbox_json.dart:2663-2779`（version/password/tls/tls_fragment）。

**方案**：核心侧按 sing-box 协议实现 shadowtls outbound（TLS 握手后接 SS）；服务端新增 `serverObj.ShadowTLS` + 链接解析（shadowtls:// 与 clash YAML `type: shadowtls`）。

**涉及文件**：`core/hint/proxy/shadowtls/`、`service/kernel/serverObj/shadowtls.go`、`clash.go`。

**工作量**：2~3 人日。**验证**：真实 shadowtls 节点 e2e。

### 5.2 Shadowsocks 2022 方法（低成本）—— 已调研，需核心支持

**现状**：`shadowsocks.go` 未限制方法名，但 v2raya-core（xray fork）**未实现** 2022-blake3 系列（无 KDF/握手实现），仅解析通过也无法连接。

**方案（修订）**：需在核心 `core/xray/proxy/shadowsocks` 实现 SS2022（ECDH 密钥交换 + BLAKE3 KDF + 新握手格式），工作量约 2~3 人日，属核心协议开发，单独排期。服务端解析无需改动（方法名透传）。

**涉及文件**：`core/xray/proxy/shadowsocks/`、`service/kernel/serverObj/shadowsocks.go`（验证 PSK 格式）。

**工作量**：核心 2~3 人日。**验证**：真实 2022-blake3 节点 e2e。

**状态**：2026-08-27 调研确认，暂缓（无机场刚需，xdollar 订阅不含 2022 节点）。

### 5.3 通用 multiplex（smux/yamux + brutal）（中高价值）

**现状**：xray 侧仅旧式 mux 与 xhttp xmux；机场链接 `mux=smux`/`brutal=` 参数无法解析。

**方案**：xray 核心对 vmess/vless/trojan/ss 挂载通用 multiplex 传输层工作量较大；一期先做**解析兼容**（解析并忽略/降级提示），二期视需求在核心实现 smux。

**涉及文件**：`service/kernel/serverObj/`（参数解析）、`core/xray/`（二期）。

**工作量**：一期 0.5 人日，二期 5 人日+。**验证**：带 mux 参数的链接可导入（一期）；e2e 多路复用（二期）。

### 5.4 抗封锁差异化能力（中价值）

- tls_fragment / tls_tricks（xray 无，需核心实现 TLS 记录分片与混合大小写 SNI）
- utls 指纹补 chrome_pq 系列
- hysteria2/tuic 端口跳跃（hop_ports）
- httpupgrade 传输（xray 已支持，服务端补订阅解析即可，成本最低）

---

## 6. 第四梯队：备份/运营/UX

### 6.1 本地自动备份 + WebDAV 备份

**karing 参考**：`home_screen.dart:1442-1469`（按日 zip、保留 5 份、增删配置触发）、`backup_and_sync_webdav_screen.dart`（连接模式多端口尝试、列表/恢复/删除）。

**方案**：后端定时/触发式把配置目录（SQLite + 设置）打包 zip 到备份目录轮转；WebDAV 客户端（Go 侧简单实现或引库）支持上传/列表/下载恢复。

**涉及文件**：`service/server/service/backup.go`（新）、`pre_update.go`、GUI 设置页。

**工作量**：1.5 人日。**验证**：备份→清库→恢复。

### 6.2 远程配置 + 公告系统

**karing 参考**：`remote_config_manager.dart`（URL/公告/FAQ 集中下发、失败退避）、`notice_manager.dart`（未读红点、过期过滤）。

**方案**：官方静态 JSON（文档链接/公告/探测 URL）→ 服务端定时拉取 → GUI 首页公告弹窗 + 设置页公告列表。

**涉及文件**：`service/server/service/`、GUI。

**工作量**：1 人日。

### 6.3 网络自检诊断报告 + 错误弹窗可复制

**karing 参考**：`net_check_screen.dart`（8 项检测 + 一键复制含版本号报告）、`dialog_utils.dart:24-127`（copy/FAQ/version 三件套）。

**方案**：诊断 API（连通性/DNS 三类延迟/当前节点/目标域名命中规则/路由表）→ Web 诊断页一键复制；GUI 错误 toast 统一加"复制错误"按钮。

**涉及文件**：`service/server/service/`（诊断 API）、GUI 组件。

**工作量**：1.5 人日。**验证**：诊断报告可完整复制。

### 6.4 LAN 分享/二维码

**karing 参考**：`backup_and_sync_lan_sync_screen.dart:67-258`（临时 HTTP + 二维码 + 多 IP 探测）。

**方案**：Web 端天然适配——导出配置生成带 token 的分享链接 + 二维码展示；对方打开即导入。

**工作量**：1 人日。

---

## 7. 实施路线图建议

| 阶段 | 内容 | 预计工作量 |
| --- | --- | --- |
| 1 | 第一梯队全部（3.1~3.6 订阅/节点管理） | 4~5 人日 |
| 2 | 4.2 规则命中检测 + 4.5 健康检查 + 5.2 SS2022 + 5.3 一期解析兼容 | 3 人日 |
| 3 | 4.1 分流规则模型一期 + 4.4 DNS 一键择优 | 3.5 人日 |
| 4 | 6.1 备份 + 6.3 诊断报告 + 6.2 公告 | 4 人日 |
| 5 | 4.3 连接监控 + 5.1 ShadowTLS | 5~6 人日 |
| 6 | 其余中低价值项按需 | - |

优先级原则：先做"直接提升订阅可用性"（第一梯队），再做"Web 端差异化排查体验"（4.2/4.3），核心协议类按机场分发趋势择机补齐。

---

## 附录：karing 关键参考文件索引

- 订阅模型：`/data/karing/lib/app/modules/server_manager.dart`
- 订阅添加/编辑 UI：`/data/karing/lib/screens/add_profile_by_link_or_content_screen.dart`、`my_profiles_edit_screen.dart`
- 分流规则：`/data/karing/lib/screens/diversion_rules_screen.dart`、`diversion_group_custom_edit_screen.dart`、`diversion_rule_detect_screen.dart`
- 连接监控：`/data/karing/lib/screens/net_connections_screen.dart`
- DNS：`/data/karing/lib/screens/dns_settings_screen.dart`、`dns_auto_setup_screen.dart`
- 备份同步：`/data/karing/lib/screens/backup_and_sync_*_screen.dart`
- 远程配置/公告：`/data/karing/lib/app/modules/remote_config_manager.dart`、`notice_manager.dart`
- 自动更新：`/data/karing/lib/app/modules/auto_update_manager.dart`
- 历史协议定义（当前 HEAD 外移）：git `3c00ed64~1` 的 `lib/app/utils/singbox_json.dart`、`clash_yaml.dart`、`v2ray_txt_utils.dart`
