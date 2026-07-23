# WebDesk

把网站当应用用的桌面壳 — 一个基于 [Wails v3](https://wails.io/) 的桌面启动器，支持内嵌浏览和 Chrome 独立窗口两种打开方式。

## 功能

- **站点管理** — 添加/编辑/删除/搜索常用网站，支持分类
- **内嵌显示** — 在应用内 iframe 中直接浏览网站
- **Chrome 模式** — 通过 `chrome --app` 打开站点为独立窗口，自动领养窗口、单实例复用
- **桌面快捷方式** — 一键创建 `.desktop` 快捷方式到桌面
- **单实例 IPC** — 第二次启动自动转发 URL 到已有实例
- **5 色主题** — 靛蓝 / 青蓝 / 玫瑰 / 琥珀 / 翠绿，设置持久化
- **GNOME Dock 集成** — 自动安装 `.desktop` 文件并添加到收藏栏
- **Chrome 窗口领养** — X11 递归遍历窗口树，自动识别并管理 Chrome `--app` 窗口

## 环境要求

| 依赖 | 最低版本 |
|------|---------|
| Go | 1.26+ |
| Wails CLI | v3.0.0-alpha2.117+ |
| GTK3 | 3.24+ |
| WebKitGTK | 2.40+ |
| Chrome / Chromium | 可选，Chrome 模式需要 |

### Ubuntu 22.04 依赖安装

```bash
sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev
```

## 编译

```bash
# Linux (GTK3, Ubuntu 22.04 必须)
wails3 build -tags gtk3

# 或使用 Makefile
make linux
```

编译产物在 `build/bin/webdesk`。

## 运行

```bash
./build/bin/webdesk
```

### 命令行参数

| 参数 | 说明 |
|------|------|
| `--open=URL` | 启动时打开指定站点 |
| `URL` | 同上（位置参数） |

```bash
# 示例：从桌面快捷方式打开
./webdesk --open=https://mail.company.com
```

## 项目结构

```
├── main.go                  # 入口：参数解析、窗口创建、单实例
├── siteservice.go           # 核心服务：站点 CRUD、Chrome 窗口管理、IPC、设置持久化
├── x11helper_linux.go       # Linux X11 CGO：窗口查找、激活、WM_CLASS 修改
├── x11helper_other.go       # 非 Linux 平台桩函数
├── icon.svg                 # 源矢量图标
├── icon.png                 # 512x512 应用图标
├── wails.json               # Wails 项目配置
├── Makefile                 # 构建脚本
├── frontend/
│   └── dist/                # 前端资源（嵌入二进制）
│       ├── index.html
│       ├── style.css
│       ├── src/main.js
│       ├── bindings/        # Wails 自动生成的 JS 绑定
│       └── node_modules/    # Wails 运行时
└── build/
    └── bin/                 # 编译输出
```

## 数据存储

| 文件 | 说明 |
|------|------|
| `~/.config/webdesk/sites.json` | 站点列表 |
| `~/.config/webdesk/settings.json` | 主题等设置 |
| `~/.cache/webdesk/webdesk.png` | 缓存的应用图标 |
| `~/.cache/webdesk/ipc.sock` | 单实例 IPC 套接字 |

## 技术要点

### Chrome 窗口领养

Chrome `--app` 窗口嵌套在 Chrome 主窗口内部，不是 X11 root 的直接子窗口。`findAllAppWindows()` 使用递归遍历 `XQueryTree` 查找全部后代窗口，`adoptChromeWindow()` 通过 diff 前后窗口集合识别新增窗口，并修改其 `WM_CLASS` 为 `webdesk/Webdesk`。

### 窗口激活

Mutter 窗口管理器要求 `_NET_ACTIVE_WINDOW` ClientMessage 的 `source` 参数为 2（pager），`source=1`（application）常被忽略。激活流程：映射顶层祖先 → 设置 `WM_STATE=NormalState` → 移除 `_NET_WM_STATE_HIDDEN` → 发送 `_NET_ACTIVE_WINDOW(source=2)` → `XRaiseWindow` + `XSetInputFocus`。

### GTK3 构建

Ubuntu 22.04 没有 GTK4 和 WebKitGTK 6.0，必须使用 `-tags gtk3` 编译。

## License

MIT
