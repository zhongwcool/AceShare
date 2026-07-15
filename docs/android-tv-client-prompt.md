# Android TV 客户端开发提示词（AceShare Companion）

> 将下文整段复制给 AI / 开发者即可开工。目标：独立 Android TV App，对接局域网 AceShare 服务。

---

## 角色与目标

你是资深 Android TV 工程师。请从零搭建 **AceShare Android TV 客户端**（应用名建议：`AceShare TV`），实现：

1. 局域网内 **自动发现** AceShare 服务器（mDNS / NSD）
2. **展示** 服务器上的 files / lines / texts 内容
3. 全程适配 **遥控器（D-pad）** 交互，而非触控优先
4. 技术栈优先：**Kotlin + Jetpack Compose for TV**（`androidx.tv` / Compose TV Material）

不要做成手机 App 套壳；焦点、选中态、OK/返回键必须按 TV 规范设计。

---

## 后端协议（必须严格遵循）

AceShare 是局域网 HTTP 服务，已通过 mDNS 宣告。

### 服务发现

| 项 | 值 |
| --- | --- |
| Android `NsdManager` serviceType | `_aceshare._tcp` |
| 实例名示例 | `AceShare (DESKTOP-ABC)` |
| 解析后 | host（IPv4）+ port |
| TXT（可选使用） | `txtvers=1`、`version=…`、`path=/`、`ports=80,8000` |

实现要求：

- 使用 `NsdManager` 发现 + resolve；resolve 成功后再加入列表（拿到真实 IP/端口）
- 发现结果去重（同一 host+port 只保留一条）
- 服务消失（`SERVICE_LOST`）时从列表移除
- App 前台持续发现；进入后台可暂停，回到前台恢复
- Manifest：`INTERNET`、`ACCESS_NETWORK_STATE`、`CHANGE_WIFI_MULTICAST_STATE`（若需要）
- 目标 SDK 按现行 TV 建议；注意本地网络相关权限/行为

连接基址：`http://{host}:{port}`（无 TLS）

### HTTP API

- `GET /api/list` → JSON：

```json
{
  "files": ["a.zip", "b.dmg"],
  "lines": [{ "title": "wifi密码", "text": "abc12345" }],
  "texts": [{ "title": "说明", "text": "多行\n文本内容" }]
}
```

- `GET /files/{name}` → 文件下载（TV 端可先只展示文件名列表；下载可选：系统 DownloadManager 或提示「请在手机/电脑打开同地址下载」）
- `GET /api/version` → 版本信息（可选展示）
- `GET /api/events` → SSE：`event: change` 时重新拉 `/api/list`（可用 OkHttp SSE 或轮询兜底，轮询间隔建议 5–10s）

Kotlin 数据类建议：

```kotlin
data class TextItem(val title: String, val text: String)
data class ListResponse(
    val files: List<String> = emptyList(),
    val lines: List<TextItem> = emptyList(),
    val texts: List<TextItem> = emptyList(),
)
data class DiscoveredServer(
    val instanceName: String,
    val host: String,
    val port: Int,
    val version: String? = null,
) {
    val baseUrl: String get() = "http://$host:$port"
}
```

---

## 信息架构与页面

### 1. 启动 / 发现页（Server Discovery）

状态机：

| 状态 | UI |
| --- | --- |
| 搜索中且列表为空 | **全屏 Lottie 占位** + 文案「正在查找局域网中的 AceShare…」+ 次要提示「请确认电脑已运行 AceShare，且与电视同一 Wi‑Fi」 |
| 找到 1 台 | 可自动进入内容页，或短暂展示后进入（推荐：展示卡片，焦点在「进入」，用户按 OK 确认；也可提供「自动进入」设置，默认开启） |
| 找到多台 | **服务器选择列表**（Compose TV `LazyColumn` / 可聚焦卡片），每项显示：实例名、IP:端口、可选 version |
| 曾有服务后全部消失 | 回到 Lottie 空态，不要崩溃 |

交互：

- 上下键在服务器卡片间移动焦点
- OK：选中并进入该服务器内容页
- 颜色按钮或菜单键（可选）：手动刷新发现
- Lottie：使用网络搜索动画或自定义「雷达/搜索」风格；循环播放；深色 TV 背景友好

### 2. 内容页（Browse）

顶部或侧边展示 **当前源**：实例名 + `host:port`。

分区（可用 Tab 或纵向分区，均需 D-pad 可达）：

1. **短文本 Lines**（核心场景，优先做好）
2. **长文本 Texts**
3. **文件 Files**（列表展示；下载为次要）

#### Lines（遥控器核心交互）

- 纵向列表，每项一行：左侧 `title`，右侧或下方预览 `text`（单行省略）
- **上下键**移动选中项；选中态必须高对比（边框/缩放/背景，符合 TV focus）
- **OK / Center**：将该项 `text` 写入系统剪贴板，并 Toast / Snackbar：「已复制：{title}」
- 长按 OK（可选）：打开详情确认后再复制
- 列表为空时显示「暂无短文本」占位，焦点仍可回到「切换源」

#### Texts

- 列表展示 title；OK 进入详情全屏/半屏阅读
- 详情页：展示全文；OK = 复制全文；返回 = 回到列表
- 长文可用遥控器上下滚动（`verticalScroll` + focusable 容器）

#### Files

- 展示文件名；OK 触发下载或「打开说明」对话框
- 不强制实现复杂播放器

### 3. 切换源（必须方便）

进入内容页后仍可切换服务器，任选其一或组合：

- 顶栏「当前源」可聚焦，OK 打开 **服务器切换** 面板/对话框
- 遥控器 **返回键**：第一次打开切换源面板或回到发现页（二选一，需在 UI 写清；推荐：内容页返回 → 发现/选择页，选择页再返回 → 退出确认）
- 切换源时：取消旧 SSE/轮询，切换 `baseUrl`，重新拉 list，保留发现列表缓存

记住上次选中的服务器（DataStore）：下次启动若仍在线可优先选中。

---

## UI / UX 规范（Compose for TV）

- 依赖：`androidx.tv:tv-foundation`、`androidx.tv:tv-material`（或当前推荐的 Compose TV BOM）
- 使用 TV 焦点 API：`Modifier.focusable()`、`onFocusChanged`、`FocusRequester`、`LazyColumn` 的 TV 变体
- 10-foot UI：字号偏大、留白充足、对比度高；默认 **深色主题**
- 焦点态：明显边框或 scale，避免仅靠颜色
- 不要依赖悬停、双指手势、小点击热区
- 动画克制：焦点移动、页面转场即可；Lottie 仅用于空态搜索

Lottie：

- 使用 `com.airbnb.android:lottie-compose`
- 资源放 `res/raw/`；空态全屏居中，下方文案与「重试搜索」按钮（可聚焦）

---

## 架构建议

```
app/
  discovery/     NsdServerDiscovery（Flow<List<DiscoveredServer>>）
  data/          AceShareApi（Retrofit/Ktor/OkHttp）、SseClient、Repository
  ui/
    discovery/   DiscoveryScreen（Lottie + 服务器列表）
    browse/      BrowseScreen（Lines/Texts/Files + 切换源）
    components/  FocusableCard、CopyFeedback…
  MainActivity   单 Activity + Navigation Compose
```

- 发现与网络 IO 在 `viewModelScope` / 协程；UI 只收集 StateFlow
- 错误：超时、连接失败 → 非阻断文案 +「重试」可聚焦按钮
- 不要在主线程做 resolve / HTTP

---

## 验收标准（DoD）

- [ ] 同一 Wi‑Fi 下，AceShare 启动后 TV 端能在数秒内发现（Lottie → 列表/进入）
- [ ] 多台 AceShare 时可选择其一；内容页可再切换源
- [ ] 无服务器时持续 Lottie 占位 + 说明文案，不白屏、不闪退
- [ ] Lines：上下选中，OK 复制到剪贴板，有明确反馈
- [ ] Texts：可浏览、可复制全文
- [ ] Files：至少列出文件名
- [ ] `/api/events` 或轮询使目录变更后列表自动刷新
- [ ] 全程可用遥控器完成；无触控死角
- [ ] 服务关闭后列表更新；切换到不可达源有错误提示

---

## 非目标（本版不做）

- 账号登录、公网穿透、HTTPS 证书配置
- 把 TV 当上传端（`/api/paste` 可后续再做）
- iOS / Apple TV（另案；协议可复用）
- 复杂媒体播放器

---

## 交付物

1. 可编译的 Android TV 工程（`leanback` / `television` feature，可在模拟器或真机安装）
2. 简短 README：如何与 AceShare 联调、所需权限、模块说明
3. 关键 Lottie 资源与来源说明（或使用可商用免费资源）

请直接开始实现工程；先搭好 discovery + Lines 复制主路径，再补 Texts/Files 与 SSE。
