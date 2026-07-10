# 局域网文件与文本分享工具

一个用 Go 编写的**单可执行文件**。双击运行后，它会自动扫描程序所在目录下的 `files/`、`lines/`、`texts/` 三个子目录，并启动一个 HTTP 服务，让局域网内的其他设备通过浏览器浏览、下载文件、查看并一键复制文本。

- 单文件运行，前端页面通过 Go 的 `embed` 打包进可执行文件，无需任何外部文件。
- **自动检测文件变化**：往目录里放入/删除文件后，已打开的网页会自动刷新，无需手动刷新（基于文件监听 + SSE 实时推送，并有轮询兜底）。
- 编译进单个 exe 后仍是零运行时依赖，双击即用；仅有一个纯 Go 的文件监听依赖 `fsnotify`（编译期，静态链接，支持交叉编译）。
- 支持交叉编译出 Windows(amd64 + arm64) / Linux / macOS(amd64 + arm64) 版本。

## 目录结构

把内容放进与可执行文件**同一目录**下的三个子目录（首次运行会自动创建）：

```
可执行文件所在目录/
├─ aceshare(.exe)      ← 可执行文件
├─ files/              ← 任意可下载文件（列出文件名，点击下载）
├─ lines/              ← 存放 .txt，每个文件是一段短文本（单行展示 + 复制）
└─ texts/              ← 存放 .txt，每个文件是一段长文本（多行展示 + 复制）
```

- `lines/` 与 `texts/` 中每个 `.txt` 文件：**标题** = 文件名去掉 `.txt`，**内容** = 文件全部文本（UTF-8）。
- 三个目录缺失或为空都不会报错，对应区域会显示“为空”。
- 列表按文件名自然排序（`1, 2, 10` 而非 `1, 10, 2`）。

## 使用方法

1. 把要分享的文件放进 `files/`，短文本放进 `lines/`，长文本放进 `texts/`。
2. 双击运行可执行文件（或在终端里运行）。
3. 控制台会打印访问地址，例如：

```
 本机访问地址：   http://localhost
 局域网访问地址： http://192.168.1.23
 可访问端口：     80、8000
```

4. 同一局域网内的手机 / 电脑用浏览器打开「局域网访问地址」即可访问。

### 命令行参数

| 参数     | 说明                                             | 默认值             |
| -------- | ------------------------------------------------ | ------------------ |
| `-port`    | 监听端口；`0`=自动**同时**监听 **80** 与 **8000**；也可显式指定单个端口 | `0`（自动）        |
| `-dir`     | 根目录（内含 files/lines/texts）                 | 可执行文件所在目录 |
| `-open`    | 启动后自动用默认浏览器打开本机页面               | `true`             |
| `-version` | 打印版本信息后退出（简写 `-v`）                  | -                  |

示例：

```bash
# 强制使用 8000 端口（80 被占用或不想用 80 时）
./aceshare -port 8000

# 调试时指定其他根目录
./aceshare -dir /path/to/share
```

> 提示：默认会**同时**监听 **80** 和 **8000** 两个端口；控制台地址按优先端口（通常为 80）展示，可访问端口单独列出。若某个端口被占用或无权限绑定，会跳过该端口并继续启动；两个都失败时才回退到 8001 及后续端口。在 Windows 上绑定 80 可能需要管理员权限。

> 提示：服务监听 `0.0.0.0`，因此局域网可访问。若无法从其他设备访问，请检查系统防火墙是否放行了对应端口。

## HTTP 接口

- `GET /` —— 内嵌的首页 `index.html`。
- `GET /api/list` —— 返回 JSON：

```json
{
  "files": ["a.zip", "b.dmg"],
  "lines": [{ "title": "wifi密码", "text": "abc12345" }],
  "texts": [{ "title": "说明", "text": "多行\n文本内容" }]
}
```

- `GET /files/<name>` —— 下载 `files/` 下对应文件（附带下载响应头，并防止目录穿越）。
- `GET /api/version` —— 返回版本信息 JSON。字段包括 `version` / `commit` / `buildTime` / `goVersion`；若检测到 [GitHub Releases](https://github.com/zhongwcool/AceShare/releases) 有更新，还会附带 `updateAvailable`、`latestVersion`、`releaseURL`。
- `GET /api/events` —— SSE（Server-Sent Events）事件流。目录内容变化时推送 `change` 事件，网页据此自动刷新。
- `GET /favicon.ico` —— 内嵌的网站图标。

## 自行编译

需要安装 [Go](https://go.dev/dl/)（1.24 及以上）。

### 本地运行

```bash
go run .
```

### 一键交叉编译

Windows（PowerShell）：

```bash
./build.ps1
```

Linux / macOS：

```bash
chmod +x build.sh
```
```bash
./build.sh
```

产物会输出到 `dist/` 目录，使用 `-ldflags "-s -w"` 与 `-trimpath` 减小体积并禁用 CGO：

```
dist/
├─ aceshare-windows-amd64.exe
├─ aceshare-windows-arm64.exe
├─ aceshare-linux-amd64
├─ aceshare-macos-amd64
└─ aceshare-macos-arm64
```

### 只编译单个平台（推荐用脚本）

不用手敲环境变量，直接给脚本传平台即可。版本号会自动从 git 取，无需手填。

Windows（PowerShell，`-p` 是 `-Platform` 的简写）：

编译 Windows exe（amd64 + arm64）
```bash
./build.ps1 -p windows
```

Linux / macOS（Bash）：
```bash
./build.sh windows       # 只编译 Windows exe（amd64 + arm64）
```


## 程序图标

- **网页图标（favicon）**：`logo.ico` 通过 `//go:embed` 嵌入可执行文件，浏览器标签页会显示它，无需额外文件。
- **Windows exe 图标**：由 `rsrc_windows_amd64.syso` / `rsrc_windows_arm64.syso` 提供（已包含在仓库中）。Go 编译对应 Windows 架构时会自动链接，使 `.exe` 在资源管理器里显示 `logo.ico` 图标。

若更换了 `logo.ico`，重新生成图标资源即可（一次性工具，不会加入项目依赖）：

```bash
go run github.com/akavel/rsrc@latest -arch amd64 -ico logo.ico -o rsrc_windows_amd64.syso
go run github.com/akavel/rsrc@latest -arch arm64 -ico logo.ico -o rsrc_windows_arm64.syso
```

`build.ps1` / `build.sh` 会在对应 `.syso` 不存在时自动生成。非 Windows 平台会忽略这些文件，不受影响。
