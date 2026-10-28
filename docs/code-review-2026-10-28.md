# 代码审查与修复记录（2026-10-28）

审查对象是当前工作区，包含此前尚未提交的 Karing 借鉴功能。按模块检查了 service 的认证、接口、订阅、数据库、配置生成与运行状态处理，core 的 DNS 和代理接入代码，以及 gui 的请求、设置和页面交互；对 service/core 全模块执行测试与静态检查。没有逐行审计第三方 core/xray、utls fork、node_modules，也没有验证所有操作系统、协议服务端或真实透明代理环境。测试通过不等于不存在其他问题。

以下问题已在工作区修复。P1 表示应优先处理的认证或数据损坏问题；P2 表示功能、并发和失败恢复问题。

| 优先级 | 触发与原有影响 | 修复位置与行为 |
| --- | --- | --- |
| P1 | JWT 密钥从订阅地址或节点主机名的 SHA-256 推导；知道完整地址列表即可推导签名密钥 | `service/pkg/server/jwt/index.go` 使用持久化的 32 字节随机密钥；验证旧地址派生令牌被拒绝。用户名读取统一为签发时的 `uname` |
| P1 | 编辑订阅节点时写入独立节点列表，同序号节点可能被覆盖 | `service/server/service/import.go` 按节点类型、订阅索引和节点索引定位；保留收藏/禁用属性，重载失败时恢复原节点 |
| P1 | 恢复接口先返回成功再异步验证；仅检查 SQLite 文件头；替换失败可能移走当前数据库；安全副本丢失 WAL 中已提交的数据 | `service/server/controller/webdav.go`、`service/server/service/webdav.go` 先校验完整性及必要表，再暂存并安排重启；保留当前数据库直到替换成功；保存旧 WAL/SHM，失败时恢复旁文件；正确编码包含特殊字符的路径 |
| P2 | 请求锁表存在并发读写且永久增长；仅按请求 ID 缓存的成功响应可能替代认证失败或其他请求的响应 | `service/pkg/server/reqCache`、`service/common/response.go` 在认证后启用缓存，按凭证/方法/URL 隔离，引用计数回收锁；未认证接口不使用该缓存 |
| P2 | LRU 重复插入同一个键仍添加链表节点，导致积累及错误淘汰 | `service/infra/dataStructure/lru/lru.go` 原位更新现有节点并调整顺序 |
| P2 | 连续删除多个已连接节点时继续遍历旧长度切片，可能索引错位或越界 | `service/server/service/touch.go` 每次使用当前连接切片；删除请求过滤 nil 项 |
| P2 | 首次连接失败未恢复空选择；不存在的节点/组可进入批量替换流程；设置及端口重载失败留下新配置 | `service/server/service/connect.go`、`setting.go`、`ports.go` 提前校验，恢复旧配置和选择，必要时尝试重启旧核心并返回恢复错误；设置接口不再在校验失败时无条件停核心 |
| P2 | 自动更新时间为零、负数或溢出时，Ticker.Reset 可导致 panic；端口遗漏范围及 API 端口重复检查 | `service/db/configure/setting_validation.go` 校验新配置并为旧异常间隔提供启动默认值；端口检查覆盖范围、API 与自定义入站冲突 |
| P2 | 更新订阅的元数据、删除与插入节点分多次提交，中途出错会留下部分更新 | `service/db/listOp.go` 将订阅更新放进同一事务，保留现有位置对应的节点行 ID；故障注入验证整体回滚 |
| P2 | 订阅“仅直连”复用系统客户端，可能仍走代理；“优先直连”没有代理备用路径；导入修改共享客户端的 Timeout | `service/server/service/subscription.go`、`import.go` 显式构建直连客户端，补齐备用顺序，复制客户端后修改超时 |
| P2 | DNS 找不到指定代理时自动直连；健康检查绕过代理/加密路径，且在读锁下更新健康状态 | `core/dns/upstream.go`、`upstream_stub.go` 找不到代理时明确报错；健康检查使用真实传输和分组通道，通过替换实例发布状态，避免读写竞争 |
| P2 | 手写 SOCKS5 与 DNS TCP 读取把一次 Read 当作完整报文，且缺少响应 ID 检查 | `core/dns/upstream_stub.go` 使用 SOCKS5 库和 dns.Conn 完整读取帧，校验 ID、问题类别和数量，域名匹配忽略大小写；补齐 `udp://` 地址解析 |
| P2 | 分块下载只记录错误仍返回成功，可能覆盖目标为不完整文件；小文件产生无效 Range | `service/pkg/util/gopeed/down.go` 返回各分块错误、校验状态/范围/长度，限制分块数量，成功后才替换目标，清理临时文件并设置超时 |
| P2 | 备份列表模板直接访问未定义的组件 localStorage/apiRoot，影响下载链接渲染 | `gui/src/components/modalBackup.vue` 在组件方法内构造下载链接；浏览器验证含本地备份记录时链接正确、无页面异常 |

## 回归与构建

使用本机 Go 1.26.7 和隔离缓存 `/tmp/v2raya-go-cache`，所有新数据库测试使用临时目录。没有修改运行中的服务数据库或重启系统服务。

- `service`: `go test -race ./...`。覆盖随机密钥持久化/伪造令牌拒绝、并发请求隔离与锁回收、LRU 更新、订阅事务失败回滚、节点索引、无效端口与节点选择、备份损坏/替换失败/WAL 保留、下载异常以及现有用例。
- `core`: `go test -race ./...`。包含 DNS 分片响应、错误 ID、代理缺失、并发健康检查、DoH/DoT 及现有代理集成用例。此命令不递归运行嵌套第三方模块的测试。
- 两个 Go 模块执行 `go vet ./...`；服务端使用具名字段修复既有 JWT 提取器 vet 告警。
- 构建定制核心，并以 `V2RAYA_TEST_CORE=/tmp/v2raya-core-review go test ./kernel/v2ray -run TestPreProxy -count=1 -v` 验证真实核心的代理链、共享节点隔离及 DNS 分组路由，流量只经过临时本地服务。
- 服务端构建标签验证：`with_gvisor` 与 `with_gvisor,tinytun`。这只验证编译，不代表已完成真实 TUN/iptables 运行测试。
- 前端 Vite 生产构建；ESLint 关闭既有 Prettier 格式规则后通过。Browserslist 有数据过期提示，未在本次审查中更新依赖。
- Playwright + 模拟 API 验证备份列表下载链接、概览、分组 DNS 诊断、订阅过滤/DNS 提交、前置组保存、桌面及窄屏布局。脚本在 `/tmp/v2raya-review-ui-check.cjs`。
- 原有 common 测试的循环导入，以及 gopeed 测试依赖公网和 Windows 固定路径的问题已移除，改为可重复的本地断言与临时 HTTP 服务。

## 应用注意

JWT 签名方案变更后，第一次运行新版本需要重新登录；之后密钥随数据库持久化。恢复备份也会恢复对应数据库中的认证状态。

本次修复与此前的 Karing 功能一起提交；验证期间未部署或替换运行中的服务。构建产物在 `/tmp`，服务端构建沿用仓库现有嵌入资源，前端另行构建到 `/tmp/v2raya-web`，没有打包发布。后续部署需使用配套的新服务端、定制核心和前端资源。
