package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// maxUploadBytes 限制单次上传请求体大小（含 multipart 开销）。
	maxUploadBytes int64 = 8 << 30
	// maxUploadFiles 限制单次请求里的文件个数。
	maxUploadFiles = 100
	// maxUploadNameRunes 限制保存后的文件名长度，避免超出常见路径上限。
	maxUploadNameRunes = 80
)

// UploadedFile 是成功保存的一个文件。
type UploadedFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// UploadResponse 是 /api/upload 成功后的返回。
type UploadResponse struct {
	Files []UploadedFile `json:"files"`
}

// handleUpload 接收 multipart 字段 file（可重复），流式写入 files/。
// 不开放跨域：网页与本服务同源即可上传；原生客户端不受 CORS 限制。
// 任意网站若被允许跨域调用，就能在用户访问时往局域网里塞文件。
func handleUpload(filesDir string, maxBytes int64) http.HandlerFunc {
	if maxBytes <= 0 {
		maxBytes = maxUploadBytes
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "仅支持 POST", http.StatusMethodNotAllowed)
			return
		}
		if err := os.MkdirAll(filesDir, 0o755); err != nil {
			log.Printf("创建上传目录失败 %s：%v", filesDir, err)
			http.Error(w, "无法创建文件目录", http.StatusInternalServerError)
			return
		}
		if r.ContentLength > maxBytes {
			http.Error(w, "文件过大", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		mr, err := r.MultipartReader()
		if err != nil {
			if isTooLarge(err) {
				http.Error(w, "文件过大", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "请使用 multipart/form-data 上传，字段名为 file", http.StatusBadRequest)
			return
		}

		saved := make([]UploadedFile, 0)
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				if isTooLarge(err) {
					http.Error(w, "文件过大", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "读取上传内容失败", http.StatusBadRequest)
				return
			}

			item, status, msg := saveUploadPart(filesDir, part, len(saved))
			part.Close()
			if status != 0 {
				http.Error(w, msg, status)
				return
			}
			if item != nil {
				saved = append(saved, *item)
				log.Printf("已上传文件 %s（%d 字节）", item.Name, item.Size)
			}
		}
		if len(saved) == 0 {
			http.Error(w, "没有可保存的文件", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(UploadResponse{Files: saved}); err != nil {
			log.Printf("编码 /api/upload 响应失败：%v", err)
		}
	}
}

// saveUploadPart 保存一个 multipart 部件。
// 非 file 字段会被跳过。status != 0 表示应中止并返回该 HTTP 状态。
func saveUploadPart(filesDir string, part *multipart.Part, already int) (item *UploadedFile, status int, msg string) {
	if part.FormName() != "file" || strings.TrimSpace(part.FileName()) == "" {
		_, _ = io.Copy(io.Discard, part)
		return nil, 0, ""
	}
	if already >= maxUploadFiles {
		return nil, http.StatusBadRequest, fmt.Sprintf("一次最多上传 %d 个文件", maxUploadFiles)
	}
	name := sanitizeUploadName(part.FileName())
	if name == "" {
		return nil, http.StatusBadRequest, "文件名无效"
	}
	savedName, size, err := writeUpload(filesDir, name, part)
	if err != nil {
		if isTooLarge(err) {
			return nil, http.StatusRequestEntityTooLarge, "文件过大"
		}
		log.Printf("保存上传文件失败：%v", err)
		return nil, http.StatusInternalServerError, "保存失败"
	}
	return &UploadedFile{Name: savedName, Size: size}, 0, ""
}

// writeUpload 把 r 写入 filesDir 下的临时文件，完成后以不覆盖的方式换成正式文件名。
func writeUpload(dir, name string, r io.Reader) (string, int64, error) {
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	n, err := io.Copy(tmp, r)
	if err != nil {
		_ = tmp.Close()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	_ = os.Chmod(tmpPath, 0o644)
	finalName, err := commitTempFile(dir, name, tmpPath)
	if err != nil {
		return "", 0, err
	}
	success = true
	return finalName, n, nil
}

// commitTempFile 把临时文件变成 dir 下的 name。同名已存在时追加 _2、_3…
// 优先硬链接：目标已存在会失败且不会覆盖。硬链接不可用时再独占复制。
func commitTempFile(dir, name, tmpPath string) (string, error) {
	var linkErr error
	for n := 1; n < 10000; n++ {
		finalName := fileNameWithIndex(name, n)
		dest := filepath.Join(dir, finalName)
		err := os.Link(tmpPath, dest)
		if err == nil {
			_ = os.Remove(tmpPath)
			return finalName, nil
		}
		if os.IsExist(err) {
			continue
		}
		linkErr = err
		break
	}
	if linkErr == nil {
		return "", fmt.Errorf("无法分配不冲突的文件名")
	}

	for n := 1; n < 10000; n++ {
		finalName := fileNameWithIndex(name, n)
		dest := filepath.Join(dir, finalName)
		err := copyTempExclusive(tmpPath, dest)
		if err == nil {
			_ = os.Remove(tmpPath)
			return finalName, nil
		}
		if os.IsExist(err) {
			continue
		}
		return "", err
	}
	return "", fmt.Errorf("无法分配不冲突的文件名")
}

// copyTempExclusive 仅在目标不存在时复制临时文件内容。
func copyTempExclusive(tmpPath, dest string) error {
	in, err := os.Open(tmpPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return closeErr
	}
	return nil
}

// fileNameWithIndex 在扩展名前插入 _n。n<=1 时返回原名。
func fileNameWithIndex(name string, n int) string {
	if n <= 1 {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	return fmt.Sprintf("%s_%d%s", stem, n, ext)
}

// sanitizeUploadName 把客户端给的文件名收成 files/ 下的单层文件名。
// 去掉目录、替换非法字符、避开 Windows 设备名，并截断过长名称。
func sanitizeUploadName(name string) string {
	// 先消掉盘符冒号，避免 Windows 的 filepath.Base 把 "a:b.txt" 当成盘符路径。
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.ReplaceAll(name, ":", "_")
	name = filepath.Base(name)
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t':
			b.WriteByte('_')
		default:
			if r < 32 {
				b.WriteByte('_')
			} else {
				b.WriteRune(r)
			}
		}
	}
	name = strings.Trim(b.String(), " .")
	if name == "" || name == "." || name == ".." {
		return ""
	}

	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		stem = "_" + stem
	}
	if utf8.RuneCountInString(ext) > 16 {
		ext = truncateRunes(ext, 16)
	}
	stemMax := maxUploadNameRunes - utf8.RuneCountInString(ext)
	if stemMax < 1 {
		return truncateRunes(stem+ext, maxUploadNameRunes)
	}
	if utf8.RuneCountInString(stem) > stemMax {
		stem = strings.TrimRight(truncateRunes(stem, stemMax), " .")
	}
	if stem == "" {
		return ""
	}
	return stem + ext
}

func isTooLarge(err error) bool {
	if err == nil {
		return false
	}
	var mb *http.MaxBytesError
	if errors.As(err, &mb) {
		return true
	}
	return strings.Contains(err.Error(), "request body too large")
}
