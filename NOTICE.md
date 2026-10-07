# 第三方声明 NOTICE

Niuma Studio (牛马工作室)
Copyright 2026 WWestC

This product includes software developed as part of the Niuma Studio
project (https://github.com/WWestC/Niuma_Studio), licensed under the
Apache License, Version 2.0 (see [LICENSE](LICENSE)).

## Vendored third-party components

### webview/ — 原生跨平台 webview 窗口库

- Upstream: <https://github.com/webview/webview>
- License: MIT

```
webview/LICENSE
  Copyright (c) 2017 Serge Zaitsev
  Copyright (c) 2020 webview

webview/libs/webview/include/LICENSE
  Copyright (c) 2017 Serge Zaitsev
  Copyright (c) 2022 Steffen André Langnes
```

### webview/libs/mswebview2/include/WebView2.h — 微软 WebView2 API 头文件

MIDL 生成的接口定义头文件，copyright Microsoft Corporation。原样随仓库分发，
供 Windows（mingw-w64 cgo）构建对接 Edge WebView2 窗口后端。

## Go module 依赖（经 go.mod 解析，未 vendor）

直接依赖：

| 模块 | 许可证 | 版权方 |
|---|---|---|
| `github.com/coder/websocket` v1.8.15 | ISC 风格 | Copyright (c) 2025 Coder（nhooyr/websocket 谱系） |
| `golang.org/x/sys` v0.48.0 | BSD-3-Clause | Copyright 2009 The Go Authors |
| `modernc.org/sqlite` v1.60.1 | BSD-3-Clause | Copyright (c) 2017 The Sqlite Authors |

间接依赖（Release 二进制把它们一并链进产物，MIT/BSD 系要求分发时附带
版权与许可声明，故全量列入；许可证与版权方逐项对照 module cache 中
对应版本的 LICENSE 原文核验）：

| 模块 | 许可证 | 版权方 |
|---|---|---|
| `github.com/dustin/go-humanize` v1.0.1 | MIT | Copyright (c) 2005-2008 Dustin Sallings |
| `github.com/google/uuid` v1.6.0 | BSD-3-Clause | Copyright (c) 2009,2014 Google Inc. |
| `github.com/mattn/go-isatty` v0.0.24 | MIT | Copyright (c) Yasuhiro MATSUMOTO |
| `github.com/ncruces/go-strftime` v1.0.0 | MIT | Copyright (c) 2022 Nuno Cruces |
| `github.com/remyoudompheng/bigfft` v0.0.0-20230129 | BSD-3-Clause | Copyright (c) 2012 The Go Authors |
| `modernc.org/libc` v1.77.1 | BSD-3-Clause | Copyright (c) 2017 The Libc Authors |
| `modernc.org/mathutil` v1.7.1 | BSD-3-Clause | Copyright (c) 2014 The mathutil Authors |
| `modernc.org/memory` v1.12.1 | BSD-3-Clause | Copyright (c) 2017 The Memory Authors |

> `modernc.org/sqlite` 把 SQLite 本体转译为 Go 一并内嵌——SQLite 处于
> 公有领域，本体无署名义务；上表覆盖的是该模块 Go 侧的 BSD 声明。

---

> 注：本文件命名为 `NOTICE.md` 而非 Apache 惯例的 `NOTICE`——根目录已有小写
> `notice/`（房间群公告的 Go 包），macOS 大小写不敏感文件系统下两者会冲突。
