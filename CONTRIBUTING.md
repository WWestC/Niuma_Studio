# 贡献指南

## 先读

- [README.md](README.md) —— 项目是什么、怎么构建；
- [docs/README.md](docs/README.md) —— 文档地图：按什么顺序读、改 X 先读什么、编号去哪查；
- `kb/manual.md` —— 房间规矩与操作面契约（也可运行 `niuma kb manual --index` 按节取读）。

本仓库的工作语言是**中文**：注释、提交信息、验收文档都用中文写。

## 开发环境

- **Go 1.27+**，cgo 开启（原生 webview 窗口需要各平台 C 工具链，见 README「前置」）；
- **Node.js**（跑前端测试用内置 runner，`node --test`，无 npm 依赖）；
- **ZCode**（可选）——AI 员工的运行时；不装也能开发与跑测试，只是没有活的 AI 成员。

## 开发循环

```bash
./build.sh dev      # NIUMA_DEV=1 直跑源码：web/ 直读磁盘，改前端存盘即热重载
```

Go 侧改动需要重启（Ctrl+C 干净退出——退出编排会收编全部子进程，重启永远是干净状态）。

## 测试

```bash
go test ./...                       # Go 全量
node --test web/room/*.test.js      # 前端按区跑（改哪个区跑哪个区）
node --test web/ui/*.test.js
node --test web/project/*.test.js
```

约定：

- **合入前全量绿**——新改动至少跑过自己触碰的包/区，拿不准就 `go test ./...` 加各区 `node --test`；
- **新功能带测试**——本仓库的测试密度是资产：DOM 冒烟、CLI 对照、验收破坏性抽验（故意废掉某个判定看测试会不会红）都是既有打法；
- **修 bug 先写红测试**——能复现的红测试比修复本身更值钱，防止回归。

## 提交风格

Conventional Commits 前缀 ＋ 中文主题，正文写清动机、实现要点与验收结论：

```
feat(studio): 工作室功能批——token 预算熔断/建卡 v3（r_26·t_188）

<动机与背景>
<实现要点（分条）>
<验收结论：跑了什么、结果如何>
```

- `type` ∈ `feat` `fix` `docs` `test` `refactor` `chore`；
- `scope` 用功能区名（`studio` / `ops` / `onboarding` …）或包名（`kb` / `dispatch` …）；
- 有任务号/需求号的写进主题（`任务: t_x，需求: r_y`），验收结论写进提交正文。

## 验收结论

重大改动的提交正文写清验收结论：怎么验的、发现了什么偏差、怎么裁定的。写「不通过」的结论同样有价值——它记录了返工的依据，比只写通过的结论可信。

## 文档纪律（docs_test.go 钉死）

- **规格与叙事分离**：ARCHITECTURE.md 只记现状——落地的边界项把「旧状→现状」尾迹写进 [docs/history.md](docs/history.md)，不回填划线与「已落地」标记（测试见 `~~` 即红）；
- **地图锚点**：[docs/README.md](docs/README.md) 的锚点不得缺失（测试核对）。

## 不要提交

- 构建产物：`niuma` / `niuma.exe` / `Niuma_Studio.app` / `icongen`（.gitignore 已挡）；
- 调试截图：`shots/` / `gui-test-screenshots/`（README 用的截图放 `docs/img/`）；
- 运行时数据：`~/.niuma/` 下的一切，以及本机密钥/账号配置。
