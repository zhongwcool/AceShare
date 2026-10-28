package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInDir(t *testing.T) {
	dir := t.TempDir()

	ok, err := resolveInDir(dir, "a.txt")
	if err != nil {
		t.Fatalf("合法文件名失败：%v", err)
	}
	if filepath.Base(ok) != "a.txt" {
		t.Fatalf("路径 basename 不正确：%s", ok)
	}

	rejects := []string{"", ".", "..", filepath.Join("..", "x"), filepath.Join("a", "b")}
	for _, name := range rejects {
		if _, err := resolveInDir(dir, name); err == nil {
			t.Fatalf("应拒绝文件名 %q", name)
		}
	}

	// 子目录中的文件不能通过裸文件名之外的方式命中
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveInDir(dir, "sub"); err != nil {
		t.Fatalf("目录名作为 basename 可以解析（删除时再拒绝非普通文件）：%v", err)
	}
	if _, err := resolveInDir(dir, filepath.Join("sub", "a.txt")); err == nil {
		t.Fatal("应拒绝子路径")
	}
}

func TestSanitizeUploadName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`..\..\outside.txt`, "outside.txt"},
		{"foo/bar.txt", "bar.txt"},
		{"CON.txt", "_CON.txt"},
		{".secret.txt", "secret.txt"},
		{"  照片.png  ", "照片.png"},
		{"..", ""},
		{"", ""},
		{"a:b.txt", "a_b.txt"},
	}
	for _, tc := range cases {
		if got := sanitizeUploadName(tc.in); got != tc.want {
			t.Fatalf("sanitizeUploadName(%q)=%q，期望 %q", tc.in, got, tc.want)
		}
	}
	long := strings.Repeat("名", 200) + ".txt"
	got := sanitizeUploadName(long)
	if len([]rune(got)) > maxUploadNameRunes {
		t.Fatalf("文件名过长：%d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, ".txt") {
		t.Fatalf("截断后应保留扩展名：%q", got)
	}
}

func TestHandleUpload(t *testing.T) {
	dir := t.TempDir()
	h := handleUpload(dir, 1<<20)

	body, ctype := multipartBody(t,
		uploadPart{"file", "note.txt", "hello"},
		uploadPart{"file", `..\outside.txt`, "nope"},
		uploadPart{"title", "", "ignore-me"},
	)
	rr := postUpload(t, h, body, ctype, int64(body.Len()))
	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 %d，响应 %s", rr.Code, rr.Body.String())
	}
	var resp UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 2 {
		t.Fatalf("应保存 2 个文件，实际 %#v", resp.Files)
	}
	if resp.Files[0].Name != "note.txt" || resp.Files[1].Name != "outside.txt" {
		t.Fatalf("保存的文件名不正确：%#v", resp.Files)
	}
	assertFile(t, filepath.Join(dir, "note.txt"), "hello")
	assertFile(t, filepath.Join(dir, "outside.txt"), "nope")
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "outside.txt")); !os.IsNotExist(err) {
		t.Fatal("上传不应写到目录外")
	}

	// 重名不覆盖。
	body, ctype = multipartBody(t, uploadPart{"file", "note.txt", "second"})
	rr = postUpload(t, h, body, ctype, int64(body.Len()))
	if rr.Code != http.StatusOK {
		t.Fatalf("重名上传状态码 %d，响应 %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 1 || resp.Files[0].Name != "note_2.txt" {
		t.Fatalf("重名应保存为 note_2.txt，实际 %#v", resp.Files)
	}
	assertFile(t, filepath.Join(dir, "note.txt"), "hello")
	assertFile(t, filepath.Join(dir, "note_2.txt"), "second")
}

func TestHandleUploadRejects(t *testing.T) {
	dir := t.TempDir()
	h := handleUpload(dir, 1<<20)

	req := httptest.NewRequest(http.MethodGet, "/api/upload", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应返回 405，实际 %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/upload", strings.NewReader("hi"))
	req.Header.Set("Content-Type", "text/plain")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("非 multipart 应返回 400，实际 %d", rr.Code)
	}

	body, ctype := multipartBody(t, uploadPart{"file", "..", "x"})
	rr = postUpload(t, h, body, ctype, int64(body.Len()))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("非法文件名应返回 400，实际 %d", rr.Code)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("非法文件名不应留下文件：%v", entries)
	}
}

func TestHandleUploadTooLarge(t *testing.T) {
	dir := t.TempDir()
	h := handleUpload(dir, 48)
	body, ctype := multipartBody(t, uploadPart{"file", "big.txt", strings.Repeat("x", 200)})
	raw := append([]byte(nil), body.Bytes()...)

	rr := postUpload(t, h, bytes.NewReader(raw), ctype, int64(len(raw)))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限应返回 413，实际 %d，响应 %s", rr.Code, rr.Body.String())
	}

	// Content-Length 未知时，靠读取过程中的上限截断，且不留下临时文件。
	rr = postUpload(t, h, bytes.NewReader(raw), ctype, -1)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("读取中超限应返回 413，实际 %d，响应 %s", rr.Code, rr.Body.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("超限上传不应留下文件：%v", entries)
	}
}

type uploadPart struct {
	field, name, content string
}

func multipartBody(t *testing.T, parts ...uploadPart) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		var fw io.Writer
		var err error
		if p.name == "" && p.field != "file" {
			fw, err = w.CreateFormField(p.field)
		} else {
			fw, err = w.CreateFormFile(p.field, p.name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(p.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func postUpload(t *testing.T, h http.Handler, body io.Reader, contentType string, contentLength int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = contentLength
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s 内容为 %q，期望 %q", path, got, want)
	}
}

func TestHandleRawFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.pdf"), []byte("%PDF-1.4 demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := handleRawFile(dir)
	req := httptest.NewRequest(http.MethodPost, "/api/file", strings.NewReader(`{"name":"a.pdf"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 %d，响应 %s", rr.Code, rr.Body.String())
	}
	if disp := rr.Header().Get("Content-Disposition"); disp != "" {
		t.Fatalf("预览读取不应带下载头：%s", disp)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %s", ct)
	}
	if rr.Body.String() != "%PDF-1.4 demo" {
		t.Fatalf("内容为 %q", rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/file", strings.NewReader(`{"name":"../a.pdf"}`))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("目录穿越应返回 400，实际 %d", rr.Code)
	}
}
