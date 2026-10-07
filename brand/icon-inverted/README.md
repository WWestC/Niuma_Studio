# 应用图标 · 明暗翻转版（定稿 v1.0）

t_111 定稿资产 · 2026-10-02 · 美术 小鹿 · 房主拍板：ZCode 原图标黑白颠倒（B 明暗翻转）

## 来源与做法

- 源：`/Applications/ZCode.app/Contents/Resources/icon.icns`（ZCode 原版图标）
- 处理：逐像素亮度反转 `L → 255-L`（Rec.601 加权），彩色统一映射为对应明度灰阶；alpha 通道原样保留，透明区 RGB 归零（防预乘白边）
- 与全反色版（A，RGB 通道反转）的差别：A 会把彩色变反色（红→青），B 只翻明暗——黑白颠倒语义以 B 为准

## 文件清单

- `icon_16x16.png` … `icon_512x512@2x.png`：十档全尺寸 PNG（@2x 为 Retina 双倍）
- `../icon-inverted.icns`：macOS 图标包（iconutil 由上述 iconset 组装），可直接替换 App 图标
- `../icon-inverted.ico`：Windows 多尺寸图标（16/32/64/128/256，256 档内嵌 PNG 原字节），由 `go run ./tools/icongen -ico` 从本目录 PNG 生成——字节级确定性输出，改动 PNG 后重跑即可刷新

## 接入（t_112 标签应用）

- macOS App：替换 `Niuma_Studio.app/Contents/Resources/icon.icns` 为 `icon-inverted.icns`（需重建 LaunchServices 缓存或重启 Finder 才刷新）
- Web manifest：`web/manifest.webmanifest` 的 icons 数组指向本目录 PNG（建议 192/512 两档，配 purpose "any maskable"）
- Windows：`../icon-inverted.ico` 编进 exe 资源（`build.bat` 自动做：icongen 生成 ico → windres 编 `packaging/windows/app.rc` 出 `.syso` → go build 自动链接）；资源里挂 1 和 32512（IDI_APPLICATION）两个槽位，前者管资源管理器/快捷方式，后者管运行中窗口的标题栏与任务栏按钮
- 启动画面（辅场景）：512 版可直用；建议深色页面用本版、浅色页面用原版，天然成对

## 注意

- 原图标若更新（ZCode 升级换肤），本资产需重跑翻转流程（`/tmp` 工作稿已弃，流程记录见上）
- 本目录纳入版本管理，作为品牌资产单一来源
