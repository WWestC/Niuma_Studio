# 文档地图（docs/README.md）

> 回答两件事：**按什么顺序读**、**改 X 先读什么**。规格与叙事分离的
> 原则见「阅读原则」；防回归纪律见 CONTRIBUTING.md「文档纪律」
> （docs_test.go 钉死）。

## 新人最小路径（约半小时）

1. [README.md](../README.md) —— 项目是什么、怎么构建怎么跑；
2. [ARCHITECTURE.md](../ARCHITECTURE.md) §一/§二 —— 分层图＋四条铁律。
   铁律各有护栏测试钉着（违规当场红），不必背，改分层前必读；
3. [kb/manual.md](../kb/manual.md) 的「接入 / 命令行 / HTTP 端点」三节
   —— 操作面契约（WebSocket 帧、CLI、HTTP 读面）；
4. 回到本页「按任务路由」按需深入。

## 按任务路由（改 X 先读 Y）

| 要做的事 | 先读 | 动手纪律（正文见 ARCHITECTURE §四） |
|---|---|---|
| 改协议/帧/载荷 | ARCHITECTURE §四第 1、6 条；`wire/wire.go` 头注释 | 跑 `go run ./tools/wiregen`，两侧新鲜度测试把关 |
| 动存储/换后端 | ARCHITECTURE §四第 3 条 | storetest 两实现同跑全绿才准入 |
| 加动词 | ARCHITECTURE §四第 4 条 | handler 只装配，房间裁决留 hub |
| 加前端板 | ARCHITECTURE §四第 5 条 | bus 订阅＋web/wire 取数＋esc() |
| 起 goroutine/flusher | ARCHITECTURE §四第 8 条 | util.Guard；带栅栏的 flusher 先结清栅栏再弃批 |
| 跨域读别的领域 | ARCHITECTURE §四第 9 条、铁律 3 | 消费者接口或 wire 镜像，绝不 import |
| 验收结论 | CONTRIBUTING「验收结论」 | 怎么验的、偏差、裁定，写进提交正文 |
| 理解"为什么这样设计" | [history.md](history.md)（演进史） | — |

## 全量清单

| 文件 | 是什么 | 性质 |
|---|---|---|
| [README.md](../README.md) / README.en.md | 项目门面、构建、开发 | 入口 |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | 分层、铁律、边界现状、迁移纪律 | **当前规格**（只记现状） |
| [docs/history.md](history.md) | 边界演进尾迹（旧状→现状、为什么这样裁） | 叙事存档 |
| [kb/manual.md](../kb/manual.md) | 房间说明书（运行时同面：GET /kb/manual；`niuma kb manual --index` 按节取读） | 规格真源（操作面） |
| CONTRIBUTING.md / SECURITY.md / NOTICE.md | 协作约定、安全、署名 | 制度 |

## 阅读原则

- **规格与叙事分离**：ARCHITECTURE.md 与 kb/manual.md 定「现在是什么」；
  history.md 定「怎么来的」。两处冲突时以规格为准，并把
  冲突当 bug 修掉。
- **测试即护栏**：文档里的纪律多半有测试钉着（arch_test / xss.sink /
  storetest / auth_matrix / docs_test）。拿不准行为面，跑测试比读叙事快。
