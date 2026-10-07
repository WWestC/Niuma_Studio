# Niuma Studio 架构说明（ARCHITECTURE.md）

> 本文回答两件事：**现在的分层是什么、靠什么纪律维持**；以及**要长成
> 可扩展平台，接缝已经铺在哪里、下一步落在哪里**。只记现状——"从什么
> 长成什么、为什么这样裁"的演进史在 [docs/history.md](docs/history.md)，
> 全仓文档地图在 [docs/README.md](docs/README.md)。
> 协议细节看 `wire/wire.go` 的文件头注释，房间运行时看 `chat/`，装配看
> `main.go` 与 `boot_*.go`。

## 一、总体形态

单二进制、本地优先的 Go 应用（~11 万行 Go + ~4.6 万行原生 JS 前端，
`go.mod` 外部依赖三个：`coder/websocket`、`golang.org/x/sys` 与
`modernc.org/sqlite`——引擎决策：纯 Go 无 cgo、三平台同源
工具链，有查询力（多用户数据面按用户/项目过滤是读形状）；bbolt 落选
缘由见 docs/history.md）。一个进程内含：HTTP/WS 面、房间运行时、调度器舰队、
以及一个 ZCode app-server 子进程（全部 AI 成员会话共乘）。UI 三态同源：
原生 webview 窗口、浏览器工作台、CLI——吃同一条 wire 协议。

## 二、目标分层（自下而上）

```
persist / util / i18n / vcs / media     ← 零业务依赖的叶子（persist 是原子写不变量的唯一实现）
wire                                    ← 协议契约（零内部依赖！全部帧与载荷自持）
agents / zcode                          ← 人事档案 / 会话桥客户端
capability / staffing / projects        ← 能力库 / 编制 / 项目基座
tasks / requirements / plan / merge /
meeting / notice / kb                   ← 领域包（各持 Store 接口 + wireconv 双向转换；
                                          tasks 的缝是 Engine 注入的持久化底层，域逻辑不复制；
                                          只依赖叶子与 wire——跨域读面走消费者接口，
                                          范本：kb 的 Roster/ConfigShelf/TaskLedger）
sqlstore / storetest                    ← 单库 Store 实现族（requirements/plan/merge/tasks+ranks）/
                                          可移植契约套件（四域两实现同跑是引入第二实现的门）
chat                                    ← 房间运行时（Hub/历史/账本；只依赖叶子与 wire）
dispatch                                ← 编排调度（顶层领域编排者，无人反向依赖）
server 家族                              ← HTTP/WS 面：壳（连接循环/路由矩阵/装配）＋
                                          verbs（动词契约层）＋逐域外迁的域面（fsview/achv/…）
main + boot_*.go                        ← 组合根（唯一允许知道一切的地方）
web/                                    ← 前端（wire/ 是协议唯一口，Go 持权威状态）
```

### 四条铁律（各有护栏测试钉死）

1. **wire 零内部依赖**（`wire/arch_test.go`）：协议包 import 任何本模块
   包即测试失败。领域载荷以**镜像类型**（JSON tag 逐字段同构）自持于
   wire；领域包的 `wireconv_gen.go` 持双向转换（wiregen 从两侧 struct
   形状生成）。改领域字段**不会**自动改协议——协议决策点收窄为「显式
   改 wire 镜像＋补 fixture」两处手写，转换与前端 typedef 跑一次
   `go run ./tools/wiregen` 即同步；`wire/compat_test.go` 用满字段
   fixture（反射钉满）钉 JSON 字节级一致。
2. **chat 不引领域包**（`chat/arch_test.go`，白名单制）：房间运行时只认
   wire 镜像与叶子包（i18n/media/persist/projects/util/wire）。
   chat/wire.go 只是兼容再导出 shim（只缩不涨，新代码直接 import
   wire）。白名单制的理由：黑名单认不出新出现的领域包，白名单下任何
   新依赖都是显式的分层决策。（破口的来龙去脉见 docs/history.md。）
3. **分层格只向下引**（`layering_arch_test.go`，组合根持有、白名单
   制）：分层图整张是测试——叶子（persist/util/i18n/vcs/media）只许引
   叶行兄弟；档案/桥（agents/zcode）加 wire；基座（capability/
   staffing/projects）加档案行；七个领域包（tasks/requirements/plan/
   merge/meeting/notice/kb）只加 wire 与叶子（i18n/persist/projects/
   util）。任何包 import 白名单之外的本模块包即测试失败。**领域之间
   不互相 import**：跨域读面走**消费者接口**或 wire 镜像，跨包词汇
   （状态/等级/岗位词）以私有镜像常量＋钉等测试替代 import。范本：kb
   的 Roster/ConfigShelf/TaskLedger/ReqTitler——真实 store 结构化满足，
   server 以 kbShelf/kbLedger 适配并保住「typed-nil 读作 nil 接口」的
   宽容；词汇钉等见 `kb/vocab_test.go`，与 tasks.LobbyKey 镜像
   wire.LobbyKey 同一纪律。
4. **持久化原子写唯一实现**（`persist/`）：所有 store 落盘走
   `persist.Save/SaveJSON`（同目录临时文件 0600 → fsync → rename →
   目录 fsync）。凭据文件从出生就是 0600，无变宽窗口。

### server 与 dispatch 的解耦：FleetAPI

server 对舰队的全部依赖是**一个接口字段** `Fleet FleetAPI`
（`server/fleetapi.go`）：22 个方法、一份契约注释（环境通知、错误绝不
拒绝、nil＝tests）、一处 nil 语义。main 以 `boot_fleet.go` 适配（包
`*dispatch.Fleet` + 两个路由到别处的 tap）；测试用 `FleetFuncs` 函数
适配器只挂关心的方法。这也是**平台化的第一个接缝**：远程舰队/多进程
调度器实现同一接口即可替换单机舰队，server 零改动。其余字段按域分组
为四个子结构——`Endpoint`（进程/传输生命周期）、`Stores`（域 store
集）、`Web`（工作台静态面）、`Faces`（挂载的 handler 面）——新面知道
挂哪、多进程拆分时每个进程面各持一份子字面量即成；nil 即特性开关的
语义保持在字段级（各字段的降级注释随字段住在子结构里）。

### 前端形态

零 npm、无构建、ES modules 直出（`go:embed web`）。index.html 只是
壳：启动门/主题早落脚本内联在 head，组件层 CSS 在 `web/app.css`
（`/app/app.css`，与全部资产同一条 ETag 管线，NIUMA_DEV 热重载同样
看护；唯一样式表，层叠序即原序）。chat 流渲染的纯函数前导在
`web/chat/rich.js`（装扮语言 dress、引用/编制建议/快捷入口解析、提案
等待钟面、裁头方案与常量词表——不触 DOM 不持状态）；stream.js 经
re-export 维持既有导入面（只缩不涨，同 chat/wire.go shim 纪律），新代
码直接 import rich.js。`web/wire/` 是协议唯一口；权威状态在 Go，前端
只投影。房间变化扇出走 `web/ui/bus.js` 订阅总线（hint 过滤 + 通配 +
顺序钉测试）——加板加 hint 不再动别人的接线。i18n 以中文原文为键、
缺键设计内回落中文；`i18n_orphan.test.js` 是近失哨兵：调用点与词典
条目只差标点/空白（改标点导致的静默回落）会被测试当场指认。

## 三、已知边界与平台化路线

当前形态是**刻意的单机取舍**。下表只记**现状、已铺接缝、下一步**；
每条边界"从什么长成什么、为什么这样裁"的演进尾迹在
[docs/history.md](docs/history.md)。

| 边界 | 现状 | 已铺接缝 | 平台化的下一步 |
|---|---|---|---|
| 信任模型 | 身份层（identity/＋server/auth.go）：工作室级账户表（admin/member/guest 三档，PBKDF2-HMAC-SHA256 凭据、crypto/rand 失败即断）＋登录会话（POST /auth/login → token/cookie，TTL 可配）。管理动词按**会话身份＋角色**鉴权（verbRole 注册表逐动词定级，逐帧裁决）；多用户形态下 hello 无有效身份即拒于门外。HTTP 面是 routeMatrix 方法感知角色表（~120 行路由，缺行 panic）；/p/{key}/* 另过项目授权裁决（服务端裁决，未授权答诚实空架/404）。**单用户默认零摩擦**：无身份服务时一切门槛读作操作者（历史行为逐字节）；多用户显式开启（NIUMA_MULTIUSER=1，强制 sqlite）。回环拨号自动以操作者（admin）入座——机器信任边界的显式化；服务令牌 ~/.niuma_service_token（0600）仅回环受纳（外泄不构成远程凭证）；NIUMA_STRICT_LOOPBACK=1 可整体收掉回环自动操作者（本机 CLI 凭服务令牌零摩擦不变）；远端机器用成员账户会话令牌接入（CLI 侧 NIUMA_AUTH_TOKEN）。令牌门（gate.go）仍是传输圈；hostgate 内圈纪律不变 | 动词角色注册表＋路由矩阵成文（auth_matrix_test.go 四行可达性逐条钉）；identity.Store 缝（sqlstore 唯一实现——账户表在文件架没有诚实对应物）；回环操作者解析单一 choke point（authOf/dialAuth） | 公网暴露自备 TLS 反代；远程 AI 成员会话（非本机）的成员级凭证细化为后续项；账户管理前端面板（当前 curl /auth/users 面）；按成员铸造/吊销的真成员凭证体系（当前为账户会话令牌 stopgap）；登录节流的反代侧真实 IP 配合 |
| 协议版本 | additive-only 演进。帧词汇三方机器钉：wiregen 生成 frame-contract.json（Go 侧新鲜度测试＋JS 侧双向对齐测试）；入站信封（memberEnvelope，35 槽）也进契约，三面钉——server 侧反射新鲜度、词表（信封 tag ⊆ wire.Message，rank 为成文的入站独占白名单）、wire.js Frame 文档全量镜像。hello.proto 客户端报最高版本、welcome.proto 回执服务端版本（wire.ProtoVersion=1），高于服务端拒之；hello.auth 携带会话/服务令牌 | wire 独立成包（零内部依赖）；frame-contract.json 单源，两侧新鲜度/对齐测试把关 | —（TS/JSDoc emitter 已落地：gen/types.js 随 wiregen 一起生成，契约 JSON 已带 Go 类型标注）；下一步按需出 .d.ts 单独产物 |
| 调度舰队 | 桥池（dispatch/bridgepool.go）：spawn/健康/换代/10s 节流自愈全部收进池接口（包 main 只留 spawner 与首亮钩子）。每项目调度器黏住一名成员（sticky，大厅恒 0 号，小助手/启动招聘同乘）——一名子进程死了只闪断它名下的房间；会话由 resume 归位（CLI 会话库全机共享，任何子进程可 resume 任何 id，无需迁移注册表）。`NIUMA_BRIDGE_POOL`（1–8，默认 1＝逐字节旧单子进程行为） | `FleetAPI` 接口 + `Bridge` 接口（假桥测试）+ per-member sharedBridge 扇出 | 远程 FleetAPI 实现；按容量的成员再平衡（当前 sticky 永不迁移） |
| 存储 | **sqlite 转正（一步到位根治，历程见 docs/history.md）**：studio.db 恒为台账唯一持久层，JSON 轨退役（单向桥是布局的最后一个读者）。**每域一个 Engine＋哑缝**（tasks 模式推广到 requirements/plan/merge/meeting/notice）：域语义只在域包引擎里一份，sqlstore 退成整文档哑存取（架即一份文档）；内存形态（nil 缝）服务测试与无 home 降级，不再是第二实现。meeting/notice 进库（notice 的「库是真源、写失败拒绝发布」反向语义原样保留；v1 需求行表折叠成架文档，schema v4）。**回落矩阵**：损坏→隔离重开空库＋一次性导入戳（~/.niuma/.ledger_imported）绝不回导陈旧 JSON；迁移失败/版本超前→拒起（无回退架）；环境类→内存台账响亮降级；行级损坏→家族取证导出＋按域清表重开。**NIUMA_STORE 退役**（残值响亮拒起）。**acceptPlanCore 动词粒度收口（转正硬条件）**：弹槽＋任务架整写＋需求标拆解三腿暂存→LedgerTx 一个事务→引擎内存收编——提案不丢、任务账不半、需求不半 | Store 接口（导出面）＋各域 ShelfStore/SlotStore/RowStore 哑缝（Engine 构造注入）；server.LedgerSeam/Stores.LedgerTx（sqlstore.TxStores 实现）；各域 Engine 的 Accept 暂存面；storetest 契约套件引擎两侧（内存缝/单库缝）同跑 | chat（含 dispatch inbox，B 族 flusher 终态，翻译预案成文于对照表 §2.3，独立立项）与 kb（C 族自管 WAL 终态）豁免成文；agents/staffing/capability/plugins/projects/media（A 族文件终态，单实现无税）；三平台 CI 跑轮 |
| 任务引擎 | 按项目分桶：每架一把 shard 锁（两房写任务不再互相排队），写路径 O(本架) 扫描、只落本架文档。落盘出锁——快照锁内取（深拷贝）、原子写锁外落，saveMu＋世代计数保证慢的旧写永不盖新（持久化失败留世代欠账，下次成功保存补齐——A-族纪律不变）。跨架重挂（patch 改挂靠）走 moveMu＋双锁按键序拼接，在途任务两架皆不可见、永不复制；boot 按任务自身 key 归架（崩溃在中途也自愈）。MaxTasks 全室 CAS 预留；rank 独立小锁（写路径 shard.mu→rankMu 单向嵌套） | Store 接口本就按架读写（分架是前置）；boot 归架即重挂自愈 | 架内再细分——至今无实测竞争数据，不见顶不动 |
| dispatch 巨石 | 车道是单条 `[]laneEntry`（磁盘格式兼容读旧文件）；member 分组为 lanes/cargo/fab/watch 四个内聚子结构。saveLanes 出锁（inbox flusher：热路径 marshal＋入队，读改写＋fsync 在 flusher 上）；「消费即落盘」的 ordered 落盘也出锁（快照锁内取＋seq 单调戳，阻塞入队在锁外；flusher 按序号取最新、跨批记忆已落盘 seq——乱序迟到的旧快照当场丢弃，已捎带的线不会随旧档复活；Stop/卸载/wipe 前一律 FlushInbox 栅栏）。假桥收敛到 baseBridge 基桩（Bridge 长方法只改一处）；Fleet 路由层的无调度器报错收敛为 noDispatcher 单点；/dispatch 管理面收敛到 Fleet.HTTPHandler 单份 | 假桥/假时钟测试面完整；动词面已注册表化 | 架内细分——有实测竞争数据后再考虑 |
| Hub 吞吐 | 历史写与三账本（reads/acks/reacts）落盘均在临界区外：历史走有序 flusher（信道 FIFO 保盘上 seq 序、满队丢弃保房间）；账本走 ledger flusher（锁内 marshal＋入队、满队降级 dirty 待重试——丢的是快照不是真相），读面与退出路径先排 FlushLedgers 栅栏，reset/unload 先排水再删文件；终端转录（term）同款（AppendTerm 锁内 marshal＋入队、term flusher 逐批落盘，FlushTerm 栅栏随 FIFO 尾；TermSnapshot 值拷贝镜像后锁外读文件；PruneTerm 内存腿锁内、重写腿锁外按 termWriteMu 与 flusher 串行且先过栅栏）。低频 flush 路径（drainLedgers/RewriteHistory 的锁内 barrier 发送）按「flusher 永不反向取锁」论证保留，热路径已全部干净 | trySend 背压（stalled consumer 丢消息、永不阻塞 Hub）；观察者泵异步化（observers 自有泵 goroutine，拥塞丢弃） | hub 分片——待实测竞争数据 |
| session 动词面 | 动词注册表住在契约层 server/verbs（Table/Register：动词闭包＋具名信封 Envelope，重复键 init 期 panic），Bye 留在壳的连接循环（生命周期不是房间动词）。七族动词文件随契约层外迁（say/receipts/tasks/kb/plan/ops/capability→verbs_*.go；admin 因 pre-wire 常量留壳注册）；动词闭包第一参是 verbs.Seat（可触面：Stores/Fleet/Local/Registry/Hub＋Taps 棘轮表——壳 seat.go 单点接线，每域外迁删自己的行） | 壳 seat.go 的 Taps 表即「壳还欠动词面什么」的成文清单（boot_fleet 模式内用）；envelope 契约随 wiregen 家族递归提取（35 槽字节不动） | 剩余域逐外迁（配方两遍验证：fsview/achv）；admin 的三个 pre-wire 常量迁 wire 后 admin 族随之入 verbs |
| main() 装配 | 阶段函数化（boot_phases.go：openStores/bootDispatch/startServerFace/runShell 四阶段，签名即依赖契约，微妙次序的理由在各阶段文档注释）。boot 冒烟测试（boot_smoke_test.go：临时家＋临时端口按 main 的真实次序走前三阶段，HTTP 探针十一面——/app、/app/app.css、/members、/zcode、/ 信息面、/owner/token、/kb/people、/projects、/skills、/plugins.json、/dispatch 断 404——阶段次序或装配面的回归测试先红；无共享状态，`-race` 下同样成立）。openStores 八腿并发（agents/staffing/capability/plugins/docs/会议架/台账族/notices），依赖边全留串行尾（房主入座等 staffing、引擎等 owner 名与 projStore、跨仓迁移链等全部仓） | 四阶段签名即依赖契约；bootStoresParallel 留缝供同二进制 A/B 计时 | boot_fleet/embed/helpers 三文件出列 |
| 协议镜像同步 | 全链生成：wiregen（tools/wiregen/gen 库化）一次跑出三件——frame-contract.json（含 Go 类型标注）、五个域包的 wireconv_gen.go（镜像转换，wire 有域无＝生成期报错、域有 wire 无＝生成物尾清单成文）、web/wire/gen/types.js（十个载荷型的 JSDoc 全量镜像；wire.js 只留消费子集与带散文的别名）。改一字段＝域＋镜像＋fixture 三处手写＋跑一次 wiregen，两侧新鲜度测试（Go）与双向对齐测试（JS）把忘跑当场拦下 | `go run ./tools/wiregen` 一命令；wire 测试直接执行生成库（孪生提取器已死）；compat_test 加满字段反射检查（fixture 钉全这件事本身被钉） | 按需出 .d.ts／把 wireconv 族扩到新域包＝Pairs 表加行 |
| 前端 XSS | sink 哨兵（xss.sink.test.js：外部数据字段未过守卫直插 innerHTML/insertAdjacentHTML 即红，`// xss-ok: 理由` 为显式豁免）＋ esc() 敌意语料钉 | esc() 守卫面即纪律本体，敌意语料测试钉行为 | 流渲染层敌意帧端到端测试（需 dom stub 升级）；零依赖选择不变则此为上限 |
| 多用户 | `NIUMA_MULTIUSER=1`＋`NIUMA_STORE=sqlite` 显式开启（JSON 回退转 fatal——命名空间在文件架没有诚实对应物）。账户体系（identity/）＋会话层＋权限矩阵＋存储命名空间（ns 列激活：全部共享数据落 admin 账户的 ns——一间工作室一个命名空间，成员隔离走角色×项目授权而非拆数据）＋一次性迁移（无账户旧目录→房主升格 admin、JSON 桥直导 admin ns、ns='' 存量收拢；幂等）。回环＝操作者的机器信任显式化；远端必须登录 | 角色三档起步（admin/member/guest）；项目授权（account_projects 表）；服务令牌（本机 CLI 的 Bearer）；verbRole/routeMatrix 两张成文矩阵 | member 级独立命名空间（接口缝已留：Store 方法首参 subject）；账户管理前端面板；远程 AI 成员的成员级凭证 |
| server 巨石拆族 | 契约层断环＋八域已迁：server/verbs（信封/注册表/StoreSet/FleetAPI/Seat）；httputil 共享写面；域面 fsview/achv/skills/gitops/roomops/plans/autopilot/auth——动词随域注册（RegisterVerbs 壳注入缝），壳只留路由矩阵/角色地板/hello 协商/镜像线/驾驶舱读面（组装与门是壳的本分）。壳 20086→8153 行（-59%） | verbs.Seat＋Taps 棘轮（26→9 行，余者全属 tasks/kb 域）；有态 Face 缓存化＋ensureSeat 自愈；规模护栏逐包预算行；每域一绿门一提交 | reads 余部（驾驶舱读面族）＋tasks/kb 两域拆出（Taps 9→0） |
| 包内规模 | 规模护栏成测试（size_arch_test.go，组合根持有，与 layering 同款白名单文风）：非测试、非生成 .go 文件 ≤1200 行；历史超标件（tasks/tasks.go、pixart/room.go、server/autopilot.go、dispatch/lanes.go、server/gitops.go、pixart/wardrobe.go、i18n/en.go）记 oversizedAllowlist 钉现值——只许缩不许涨，缩回线下即删行；每包非测试行数走 packageBudgets 显式预算表（立表实测+5% 余量），超预算即红。生成物（Code generated 头）豁免——长度镜像手写真源，不为自己负责 | 文件上限＋超标棘轮＋包预算三层全显式表驱动；新包出生必须同提交加预算行（测试拦住忘记的那次）；拆分/搬迁后删陈旧行保表诚实 | 超标件逐个消化（autopilot/gitops/lanes 随 server 拆族与 dispatch 拆分处理）；server 拆族后预算按家族子包重设立行 |

## 四、迁移纪律（写代码前必读）

- **wire 改动**＝协议事件：改镜像＋补 fixture 两处手写，转换与前端
  typedef 由 `go run ./tools/wiregen` 生成（忘跑会被两侧新鲜度测试拦
  下）；新增字段必须 omitempty、 additive-only；wire 有域无＝生成期
  报错（镜像谎言），域有 wire 无＝生成物尾清单成文（协议决策面）。
- **加 store**：落盘走 persist；`Open("")` 内存态供测试；envelope 带
  `next` 高水位防重启重发号。
- **动存储层**：域包持 `Store` 接口（方法集＝导出面，
  **每方法首参是主体 subject**——"" 单用户历史形状，非空＝多用户
  工作室主体；LocalStore 对非空主体拒绝），LocalStore 是默认实现；
  换后端先过 `storetest` 契约套件——**两实现同跑全绿才准引入第二实
  现**（sqlite 走 MultiSubject 主体隔离用例，文件架走拒绝用例），
  拒绝文案走域包导出的错误构造器（parity 测试钉逐字节相等）。
  进单库的仓每表带 ns 列（多用户下逐调用过滤落 admin ns）；
  语义变更必须显式成文（如"事务失败如实拒绝"取代"log 不拒绝、内存超前"）。
- **加动词**：帧常量进 wire；server 的 handler 只装配（"Handlers
  assemble; room decisions stay in the hub"）；对舰队的通知加进
  FleetAPI 并保持"错误是环境注记，绝不拒绝"。
- **加前端板**：订阅挂 `bus.on(...)`；取数只走 `web/wire/`；渲染过
  `esc()`——外部数据字段直插 HTML sink 会被 xss.sink.test.js 当场指认，
  确证无害注 `// xss-ok: 理由`。
- **改 wire 常量/载荷**：跑 `go run ./tools/wiregen` 重生三件生成物
  （frame-contract.json、五个 wireconv_gen.go、gen/types.js）并提交；
  web/wire/wire.js 只在新增消费面时手写（全量镜像已别名到 gen/types.js）。
- **加文件/加包**：非测试、非生成源文件 ≤1200 行（size_arch_test.go），
  历史超标件只许缩不许涨；新包出生同提交在 packageBudgets 加预算行，
  包超预算＝拆子系统或在此显式上调（走评审）——规模是显式决策，
  不是无声漂移。
- **读聊天历史文件**：先 `FlushHistory()`/`Registry.FlushHistory(key)` 排水
  （ring 是真相、文件是读模型；say 的落盘是异步的）。读/删三账本文件
  同理先 `FlushLedgers()`；dispatch 侧读/删 inbox 文件先
  `FlushInbox()`（wipe 前的排干已接进 Fleet.FlushProjectInbox）。
- **起长寿命 goroutine**：一进程一间工作室，循环里一次 panic 会带走
  全部房间与舰队——节拍钟/看护/观察者的一拍包 `util.Guard`（丢一拍、
  下一拍照常）；一次性 worker（Fleet 通知腿、watchSeat、议程、索引
  注册等）同样包 `go util.Guard(...)`。**带栅栏（done 通道）的
  flusher 不能直接套 Guard**：恢复路径必须先结清欠下的栅栏再弃批次，
  否则排水方会永远等待——护栏换来的不能是死锁。范本：chat 的
  flushHistoryBatch/flushLedgerOne、dispatch 的 inboxFlushBatch。
  **护栏之内的手工临界区必须 defer 解锁或闭包化**：panic 展开时放锁，
  否则 Guard 接住的只是一个永远握着锁的僵尸。
- **领域包要引别的域**：先问能不能反过来——消费者接口（结构化满足，
  范本：ReqTitler/Roster 一族）或 wire 镜像读面（`Engine.WireTasksOf`、
  `Store.WireList` 一族）；真要引用跨包词汇，私有镜像 + 钉等测试
  （kb/vocab_test.go）。import 领域包这条路 layering_arch_test.go 会当
  场拦下——白名单加行是显式的分层决策，走评审。
