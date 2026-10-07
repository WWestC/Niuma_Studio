# 架构演进史（docs/history.md）

> 本文是 ARCHITECTURE.md 的**叙事层存档**：只记"从什么长成什么、为什么
> 这样裁"，不约束当前行为——当前规格一律以 ARCHITECTURE.md 为准，两处
> 不一致时以彼为准。提交主题里的 t_/r_/p_ 是应用自身台账的编号
> （台账本体随应用运行在 ~/.niuma/，不入仓）。

## 铁律的由来（ARCHITECTURE.md §二考古）

- **chat 的分层破口**：历史上 `chat/wire.go` 直接引五个领域包，是分层图
  上唯一的破口；拆除后 chat/wire.go 降为兼容再导出 shim（只缩不涨，新
  代码直接 import wire）。护栏同轮从黑名单改白名单——黑名单认不出新
  出现的领域包，白名单下任何新依赖都是显式的分层决策。
- **persist 的前身**：tmp+rename 的舞蹈曾逐包复制十几份且多数无 fsync；
  收敛为 persist/ 唯一实现（同目录临时文件 0600 → fsync → rename →
  目录 fsync）后，原子写不变量才有了单点。
- **FleetAPI 的前身**：`server.Options` 曾有二十余个 `func` tap 字段，
  每个都是"环境通知、错误绝不拒绝、Nil=tests"的同款契约——契约散落
  二十余处。折叠为单接口字段后，契约成文一份、nil 语义一处。

## 信任与身份

「连接即身份」已废除：管理动词曾按连接自报的身份鉴权，多用户形态下这
不可持——现为**会话身份＋角色**逐帧裁决，hello 无有效身份即拒于门外。
项目可见性从 `?project=` 自报改为服务端裁决（account_projects 表），
未授权答诚实空架/404。单用户默认零摩擦是逐字节保持的历史行为：无身份
服务时一切门槛读作操作者。回环拨号在多用户下自动以操作者（admin）
入座——本机 CLI/webview/mirror 不多敲一个字，机器信任边界自此显式化；
服务令牌 ~/.niuma_service_token（0600）是其 Bearer 形态。

## 协议版本与镜像钉死

- **hello 协议协商**：~~hello 带 protocol 版本协商~~已落地——hello.proto
  客户端报最高版本、welcome.proto 回执服务端版本（wire.ProtoVersion=1），
  高于服务端拒之；hello.auth 携带会话/服务令牌（additive-only，wiregen
  已重生）。
- **帧契约**：wiregen 生成 frame-contract.json 首跑即抓出 5 个真实缺口
  （Whisper origin ＋ 4 帧）已补。
- **入站信封进契约**：memberEnvelope 提取 35 槽进 envelope 段，三面钉
  （server 反射新鲜度、词表、wire.js Frame 文档全量镜像）首跑即抓出
  13 个缺学字段。

## 调度舰队

~~单进程内 ＋ 单 app-server 子进程（SPOF）~~ → 桥池落地
（dispatch/bridgepool.go）：spawn/健康/换代/10s 节流自愈全部收进池接口
（包 main 只留 spawner 与首亮钩子）。每项目调度器黏住一名成员（sticky，
大厅恒 0 号，小助手/启动招聘同乘）——一名子进程死了只闪断它名下的房间；
会话由 resume 归位（CLI 会话库全机共享，任何子进程可 resume 任何 id，
无需迁移注册表）。`NIUMA_BRIDGE_POOL`（1–8，默认 1＝逐字节旧单子进程
行为）。

## 存储与事务

~~内存真源 ＋ 整文件 JSON 重写，跨 store 无事务~~ → 首批三仓
（requirements/plan/merge）＋第二批 tasks/等级表的单库实现
（modernc.org/sqlite）。引擎决策：bbolt 备选因无查询被否（有查询力是
硬需求——多用户数据面按用户/项目过滤是读形状）。

plan_accept 的跨仓事务用故障注入验证：「提案不丢＋任务账不半」。
JSON→单库是一次性单向桥：表空才导、文件留取证；tasks 载入失败走专属
corrupt-reset，不回落 JSON 架防陈旧复活。

~~ns 列预留恒 ''~~ → 激活为逐调用主体：四个 Store 接口每方法首参
subject（""＝单用户历史形状），多用户下全部共享数据落 admin 账户 ns；
LocalStore 对非空主体响亮拒绝（ErrSingleSubject——文件架没有命名空间的
诚实对应物）。

## 任务引擎

~~进程内单实例全局单锁，每次变更锁内全量重写全部项目架（O(全室任务)＋
逐架 fsync 在临界区内）~~ → 按项目分桶：每架一把 shard 锁、写路径
O(本架)、落盘出锁（快照锁内取深拷贝、原子写锁外落，saveMu＋世代计数
保证慢的旧写永不盖新）；跨架重挂走 moveMu＋双锁按键序拼接，boot 按任务
自身 key 归架（崩溃在中途也自愈）。

## dispatch 巨石收敛

~~member ~40 字段、6-7 条平行车道切片，锁内落盘~~ → 车道单条
`[]laneEntry`（四处 lockstep 手工同步与 loadLanes 五段补齐防御随结构
消失；磁盘格式兼容读旧文件）；member 分组为 lanes/cargo/fab/watch 四个
内聚子结构；~~saveLanes 出锁~~（inbox flusher：热路径 marshal＋入队，
读改写＋fsync 在 flusher 上）；~~ordered 落盘出锁~~（v3：快照锁内取＋
seq 单调戳，flusher 按序号取最新、乱序迟到的旧快照当场丢弃；Stop/卸载/
wipe 前一律 FlushInbox 栅栏）；假桥收敛到 baseBridge 基桩（88 个惰性
样板方法删除，Bridge 长方法只改一处）；Fleet 路由层的无调度器报错收敛
为 noDispatcher 单点；dispatcher 的单房 HTTP 面（四端点与 op 助手与
Fleet 面双份维护、生产未挂载的死代码）整块删除。

## Hub 吞吐

~~单 mutex ＋ 锁内 IO~~ → 历史写与三账本（reads/acks/reacts）落盘均出
临界区（~~账本出锁~~、~~term 出锁~~先后落地）：历史走有序 flusher，账本
走 ledger flusher（满队降级 dirty 待重试——丢的是快照不是真相），
reset/unload 先排水再删文件（在途写复活已删文件的竞态随异步化一并堵上）；
终端转录 term 同款出锁（AppendTerm 锁内 marshal＋入队、term flusher 逐批
落盘）。低频 flush 路径（drainLedgers/RewriteHistory 的锁内 barrier
发送）按「flusher 永不反向取锁」论证保留，热路径全部干净。trySend 背压
（ stalled consumer 丢消息、永不阻塞 Hub）与观察者泵异步化（observers
自有泵 goroutine，拥塞丢弃）均已铺。

## session 动词面

~~大 switch~~ → 动词注册表（memberVerbs：32 动词闭包＋具名信封，
session.go 699→309 行），Bye 留在连接循环（生命周期不是房间动词）；
~~注册表按域拆分~~为七个文件（receipts/tasks/kb/admin/capability/
plan/ops——各文件 init 自注册，registerVerbs 对重复键 init 期 panic，
拆错在测试二进制启动即现形）；/dispatch 管理面收敛到 Fleet.HTTPHandler
单份。

## main() 装配

~~1250 行单函数~~ → 阶段函数化（boot_phases.go 四阶段，签名即依赖
契约，微妙次序的理由在各阶段文档注释；main() 收敛到 201 行有序调用）→ boot 冒烟测试（临时家＋临时
端口按真实次序走前三阶段，HTTP 探针十一面）→ ~~阶段级并行化~~
（openStores 八腿并发，依赖边全留串行尾；bootStoresParallel 留缝供同
二进制 A/B 计时：实测串行 11.68ms vs 并行 10.89ms，暖盘五轮中位）。

## 前端形态

- index.html 收敛为壳（387 行）：组件层 CSS（4504 行）抽出为
  `web/app.css`——与全部资产同一条 ETag 管线，NIUMA_DEV 热重载同样
  看护；层叠序未动，link 落在原 style 位置且仍是唯一样式表。
- chat 流渲染的纯函数前导独立成 `web/chat/rich.js`；stream.js 经
  re-export 维持既有导入面（只缩不涨，同 chat/wire.go shim 纪律）。
- XSS：~~esc() 自觉纪律~~ → sink 哨兵（xss.sink.test.js）＋esc() 敌意
  语料钉——外部数据字段直插 innerHTML/insertAdjacentHTML 当场即红，
  `// xss-ok: 理由` 为显式豁免。
