package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

//go:embed index.html
var embeddedFiles embed.FS

//go:embed logo.ico
var faviconBytes []byte

// 版本信息。可在编译时通过 -ldflags 注入覆盖，例如：
//
//	go build -ldflags "-X main.version=v1.0.0 -X main.commit=abc123 -X main.buildTime=2026-07-06"
var (
	version   = "dev"     // 版本号
	commit    = "unknown" // Git 提交哈希
	buildTime = "unknown" // 构建时间
)

// versionString 返回用于展示的版本描述。
func versionString() string {
	return fmt.Sprintf("局域网文件与文本分享工具 %s (commit %s, built %s, %s)",
		version, commit, buildTime, runtime.Version())
}

// TextItem 表示 lines/ 或 texts/ 目录下的一个文本条目。
type TextItem struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// ListResponse 是 /api/list 返回的 JSON 结构。
type ListResponse struct {
	Files []string   `json:"files"`
	Lines []TextItem `json:"lines"`
	Texts []TextItem `json:"texts"`
}

// Broadcaster 管理所有 SSE 客户端连接，并在目录变化时向它们广播通知。
type Broadcaster struct {
	mu      sync.Mutex
	clients map[chan struct{}]struct{}
}

// NewBroadcaster 创建广播器。
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{clients: make(map[chan struct{}]struct{})}
}

// Subscribe 注册一个新客户端，返回其通知通道与取消订阅函数。
func (b *Broadcaster) Subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.clients, ch)
		b.mu.Unlock()
	}
}

// Notify 向所有客户端发送一次变化通知（非阻塞：通道满则跳过）。
func (b *Broadcaster) Notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func main() {
	defaultDir := executableDir()

	port := flag.Int("port", 0, "监听端口；0=自动同时监听 80 与 8000。也可显式指定单个端口如 8000")
	dir := flag.String("dir", defaultDir, "根目录（内含 files/ lines/ texts/ 三个子目录）")
	open := flag.Bool("open", true, "启动后自动用默认浏览器打开本机页面（-open=false 可禁用）")
	showVersion := flag.Bool("version", false, "打印版本信息后退出")
	flag.BoolVar(showVersion, "v", false, "打印版本信息后退出（-version 简写）")
	flag.Parse()

	if *showVersion {
		fmt.Println(versionString())
		return
	}

	rootDir := *dir
	filesDir := filepath.Join(rootDir, "files")
	linesDir := filepath.Join(rootDir, "lines")
	textsDir := filepath.Join(rootDir, "texts")

	// 尽量确保三个子目录存在（不存在则创建，创建失败也不影响运行）。
	for _, d := range []string{filesDir, linesDir, textsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Printf("警告：无法创建目录 %s：%v", d, err)
		}
	}

	// 首次运行：目录为空时各写入一个演示文件，方便直接看到效果（不覆盖已有内容）。
	seedDemoContent(filesDir, linesDir, textsDir)

	mux := http.NewServeMux()

	// 首页：内嵌的 index.html。
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := embeddedFiles.ReadFile("index.html")
		if err != nil {
			http.Error(w, "无法读取内嵌页面", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})

	// 网站图标（浏览器标签页）：内嵌的 logo.ico。
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(faviconBytes)
	})

	// 版本信息接口，供页面页脚展示。
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version":   version,
			"commit":    commit,
			"buildTime": buildTime,
			"goVersion": runtime.Version(),
		})
	})

	// 列表接口。
	mux.HandleFunc("/api/list", func(w http.ResponseWriter, r *http.Request) {
		resp := ListResponse{
			Files: scanFiles(filesDir),
			Lines: scanTexts(linesDir),
			Texts: scanTexts(textsDir),
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(resp); err != nil {
			log.Printf("编码 /api/list 失败：%v", err)
		}
	})

	// 文件下载：使用 http.FileServer（自带路径清理，可防目录穿越），并附加下载响应头。
	fileServer := http.StripPrefix("/files/", downloadHeaders(http.FileServer(noDirListing{http.Dir(filesDir)})))
	mux.Handle("/files/", fileServer)

	// 变化通知（SSE）：目录内容发生增删改时，向所有连接的网页推送事件，网页据此自动刷新。
	broadcaster := NewBroadcaster()
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "当前服务器不支持 SSE", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch, unsubscribe := broadcaster.Subscribe()
		defer unsubscribe()

		// 建立连接时先发一次，确保刚打开页面即为最新。
		fmt.Fprintf(w, "event: change\ndata: init\n\n")
		flusher.Flush()

		// 定期心跳，避免代理/浏览器判定连接超时断开。
		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ch:
				fmt.Fprintf(w, "event: change\ndata: update\n\n")
				flusher.Flush()
			case <-heartbeat.C:
				fmt.Fprintf(w, ": keep-alive\n\n")
				flusher.Flush()
			}
		}
	})

	// 启动目录变化监听：优先用 fsnotify 实时监听，失败则回退为定时扫描。
	startWatching(broadcaster, filesDir, linesDir, textsDir)

	// 绑定端口：默认同时监听 80 与 8000；显式 -port 时只监听指定端口。
	var listeners []net.Listener
	var boundPorts []int
	var err error
	if *port == 0 {
		listeners, boundPorts = listenOnPorts(80, 8000)
		if len(listeners) == 0 {
			var ln net.Listener
			var fallbackPort int
			ln, fallbackPort, err = listenWithFallback(8001)
			if err != nil {
				log.Fatalf("无法绑定任何端口（已尝试 80、8000 及后续端口）：%v", err)
			}
			listeners = []net.Listener{ln}
			boundPorts = []int{fallbackPort}
		}
	} else {
		var ln net.Listener
		var singlePort int
		ln, singlePort, err = listenWithFallback(*port)
		if err != nil {
			log.Fatalf("无法绑定任何端口（从 %d 开始尝试）：%v", *port, err)
		}
		listeners = []net.Listener{ln}
		boundPorts = []int{singlePort}
	}

	printBanner(rootDir, boundPorts)

	// 启动后自动打开本机页面。优先使用 192.168.* 局域网地址，不使用 localhost。
	if *open {
		if host := preferredLANHost(); host == "" {
			log.Printf("提示：未检测到局域网 IPv4 地址，跳过自动打开浏览器")
		} else {
			openURL := formatURL(host, preferredOpenPort(boundPorts))
			go func() {
				// 稍等片刻，确保服务已开始接受连接。
				time.Sleep(300 * time.Millisecond)
				if err := openBrowser(openURL); err != nil {
					log.Printf("提示：未能自动打开浏览器，请手动访问 %s（%v）", openURL, err)
				}
			}()
		}
	}

	server := &http.Server{Handler: mux}
	errCh := make(chan error, len(listeners))
	for _, ln := range listeners {
		go func(l net.Listener) {
			if err := server.Serve(l); err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		}(ln)
	}
	if err := <-errCh; err != nil {
		log.Fatalf("HTTP 服务异常退出：%v", err)
	}
}

// startWatching 监听给定目录的变化，变化时通过广播器通知客户端。
// 优先使用 fsnotify 实时监听；若初始化失败，则回退为定时扫描指纹的方式。
func startWatching(b *Broadcaster, dirs ...string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("提示：文件监听不可用，回退为定时扫描（%v）", err)
		go pollWatch(b, dirs...)
		return
	}

	added := 0
	for _, d := range dirs {
		if err := watcher.Add(d); err != nil {
			log.Printf("提示：无法监听目录 %s：%v", d, err)
			continue
		}
		added++
	}
	if added == 0 {
		_ = watcher.Close()
		go pollWatch(b, dirs...)
		return
	}

	go func() {
		defer watcher.Close()
		// 事件去抖：短时间内的多次变化合并为一次通知，避免频繁刷新。
		var debounce *time.Timer
		fire := func() {
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(200*time.Millisecond, b.Notify)
		}
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// 关心增删改重命名（写入完成、创建、删除、改名都会触发刷新）。
				if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
					fire()
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("文件监听错误：%v", err)
			}
		}
	}()
}

// pollWatch 是降级方案：定时扫描目录，计算指纹，变化时通知。
func pollWatch(b *Broadcaster, dirs ...string) {
	last := dirsFingerprint(dirs...)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		cur := dirsFingerprint(dirs...)
		if cur != last {
			last = cur
			b.Notify()
		}
	}
}

// dirsFingerprint 根据目录内文件名、大小、修改时间生成一个简单指纹。
func dirsFingerprint(dirs ...string) string {
	var sb strings.Builder
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			fmt.Fprintf(&sb, "%s|%d|%d;", e.Name(), info.Size(), info.ModTime().UnixNano())
		}
	}
	return sb.String()
}

// seedDemoContent 在对应目录为空时写入一个演示文件；目录非空则跳过，不覆盖用户内容。
func seedDemoContent(filesDir, linesDir, textsDir string) {
	if dirIsEmpty(filesDir) {
		demo := "这是一个演示文件。把任意文件放进 files/ 目录，即可在网页上列出并下载。\n"
		writeIfAbsent(filepath.Join(filesDir, "示例文件.txt"), demo)
	}
	if dirIsEmpty(linesDir) {
		demo := "这是一段演示用的单行短文本，点击右侧按钮即可一键复制。"
		writeIfAbsent(filepath.Join(linesDir, "示例单行文本.txt"), demo)
	}
	if dirIsEmpty(textsDir) {
		demo := "这是一段演示用的长文本。\n" +
			"lines/ 里的每个 .txt 会显示为可复制的单行（超出以省略号缩略）；\n" +
			"texts/ 里的每个 .txt 会显示为自动换行的多行块，保留原始换行。\n" +
			"把你的内容分别放进 lines/ 和 texts/ 目录即可。"
		writeIfAbsent(filepath.Join(textsDir, "示例长文本.txt"), demo)
	}
}

// dirIsEmpty 判断目录是否为空（不含任何条目）。目录无法读取时按“非空”处理，避免误写。
func dirIsEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) == 0
}

// writeIfAbsent 仅在目标文件不存在时写入，避免覆盖用户已有文件。
func writeIfAbsent(path, content string) {
	if _, err := os.Stat(path); err == nil {
		return // 已存在，跳过
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		log.Printf("警告：写入演示文件 %s 失败：%v", path, err)
	}
}

// openBrowser 用系统默认程序打开指定 URL，跨平台。
func openBrowser(target string) error {
	switch runtime.GOOS {
	case "windows":
		// rundll32 对含 & 等字符的 URL 更稳妥，无需担心 shell 转义。
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		return exec.Command("open", target).Start()
	default: // linux 及其他类 Unix
		return exec.Command("xdg-open", target).Start()
	}
}

// executableDir 返回可执行文件所在目录；失败时回退到当前工作目录。
func executableDir() string {
	exe, err := os.Executable()
	if err == nil {
		if resolved, err2 := filepath.EvalSymlinks(exe); err2 == nil {
			exe = resolved
		}
		return filepath.Dir(exe)
	}
	if wd, err2 := os.Getwd(); err2 == nil {
		return wd
	}
	return "."
}

// scanFiles 返回目录下所有普通文件的文件名（自然排序）。目录不存在时返回空切片。
func scanFiles(dir string) []string {
	result := []string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// 跳过隐藏文件（以 . 开头）。
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		result = append(result, e.Name())
	}
	sort.Slice(result, func(i, j int) bool {
		return naturalLess(result[i], result[j])
	})
	return result
}

// scanTexts 读取目录下所有 .txt 文件，返回条目列表（自然排序）。
// title = 去掉 .txt 后缀；text = 文件全部内容。目录不存在或文件读失败均安全跳过。
func scanTexts(dir string) []TextItem {
	result := []TextItem{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".txt") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			log.Printf("跳过无法读取的文件 %s：%v", filepath.Join(dir, name), err)
			continue
		}
		title := name[:len(name)-len(".txt")]
		result = append(result, TextItem{
			Title: title,
			Text:  string(content),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return naturalLess(result[i].Title, result[j].Title)
	})
	return result
}

// downloadHeaders 为文件请求附加 Content-Disposition，使浏览器以下载方式处理。
func downloadHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path_Base(r.URL.Path)
		if name != "" && name != "/" {
			w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
		}
		next.ServeHTTP(w, r)
	})
}

// path_Base 返回 URL 路径中的文件名部分（已解码）。
func path_Base(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	if decoded, err := url.PathUnescape(p); err == nil {
		return decoded
	}
	return p
}

// noDirListing 包装文件系统，禁止目录列表（访问目录时返回 404）。
type noDirListing struct {
	fs http.FileSystem
}

func (n noDirListing) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}

// listenOnPorts 尝试同时绑定多个端口，成功的全部保留；失败的仅记录提示。
func listenOnPorts(ports ...int) ([]net.Listener, []int) {
	var listeners []net.Listener
	var bound []int
	for _, p := range ports {
		ln, err := tryListen(p)
		if err == nil {
			listeners = append(listeners, ln)
			bound = append(bound, p)
		} else {
			log.Printf("提示：端口 %d 不可用（%v）", p, err)
		}
	}
	return listeners, bound
}

// listenWithFallback 从 startPort 起向后尝试绑定，返回监听器与实际端口。
func listenWithFallback(startPort int) (net.Listener, int, error) {
	const maxTries = 50
	var lastErr error
	for p := startPort; p < startPort+maxTries && p <= 65535; p++ {
		ln, err := tryListen(p)
		if err == nil {
			return ln, p, nil
		}
		lastErr = err
	}
	return nil, 0, lastErr
}

func tryListen(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
}

// formatURL 生成访问地址；80 端口省略端口号，其它端口正常显示。
func formatURL(host string, port int) string {
	if port == 80 {
		return "http://" + host
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

// printBanner 打印访问地址等启动信息。
// 地址只按优先端口展示一次（通常为 80），可访问端口单独列出，避免重复打印 :8000 地址。
func printBanner(rootDir string, ports []int) {
	fmt.Println("========================================")
	fmt.Println(" 局域网文件与文本分享工具")
	fmt.Println("========================================")
	fmt.Printf(" 版本：  %s (%s)\n", version, buildTime)
	fmt.Printf(" 根目录：%s\n", rootDir)
	fmt.Printf(" 端口：  %s\n", formatPorts(ports))
	fmt.Println("----------------------------------------")

	displayPort := preferredOpenPort(ports)
	fmt.Printf(" 本机访问地址：   %s\n", formatURL("localhost", displayPort))

	ips := localIPv4s()
	if len(ips) == 0 {
		fmt.Println(" 局域网访问地址： （未检测到可用网卡 IPv4 地址）")
	} else {
		for _, ip := range ips {
			fmt.Printf(" 局域网访问地址： %s\n", formatURL(ip, displayPort))
		}
	}

	fmt.Println("----------------------------------------")
	fmt.Println(" 按 Ctrl+C 停止服务")
	fmt.Println("========================================")
}

// formatPorts 将端口列表格式化为「80、8000」这类说明文字。
func formatPorts(ports []int) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		parts = append(parts, strconv.Itoa(p))
	}
	return strings.Join(parts, "、")
}

// preferredOpenPort 返回自动打开浏览器时优先使用的端口（80 > 8000 > 其它）。
func preferredOpenPort(ports []int) int {
	for _, want := range []int{80, 8000} {
		for _, p := range ports {
			if p == want {
				return p
			}
		}
	}
	if len(ports) > 0 {
		return ports[0]
	}
	return 8000
}

// preferredLANHost 返回用于自动打开浏览器的局域网主机名。
// 优先 192.168.*；若无则取第一个可用局域网 IPv4；不使用 localhost。
func preferredLANHost() string {
	ips := localIPv4s()
	for _, ip := range ips {
		if strings.HasPrefix(ip, "192.168.") {
			return ip
		}
	}
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

// localIPv4s 返回所有非回环网卡的 IPv4 地址。
func localIPv4s() []string {
	var ips []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		// 跳过未启用或回环网卡。
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue // 跳过 IPv6
			}
			if ip4.IsLinkLocalUnicast() {
				continue // 跳过 169.254.x.x
			}
			ips = append(ips, ip4.String())
		}
	}
	sort.Slice(ips, func(i, j int) bool { return naturalLess(ips[i], ips[j]) })
	return ips
}

// naturalLess 实现简单的自然排序比较：数字段按数值大小比较（1 < 2 < 10），
// 其余字符按 ASCII 小写比较。
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ai, bj := a[i], b[j]
		aDigit := ai >= '0' && ai <= '9'
		bDigit := bj >= '0' && bj <= '9'
		if aDigit && bDigit {
			si := i
			for i < len(a) && a[i] >= '0' && a[i] <= '9' {
				i++
			}
			sj := j
			for j < len(b) && b[j] >= '0' && b[j] <= '9' {
				j++
			}
			na := strings.TrimLeft(a[si:i], "0")
			nb := strings.TrimLeft(b[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			// 数值相等时，原始位数少的（前导零少）排前面。
			if (i - si) != (j - sj) {
				return (i - si) < (j - sj)
			}
			continue
		}
		ca := lowerByte(ai)
		cb := lowerByte(bj)
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	return (len(a) - i) < (len(b) - j)
}

func lowerByte(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
