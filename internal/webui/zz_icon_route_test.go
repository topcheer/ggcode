package webui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// #5568 后续:web/index.html 以 /icon.png 引用品牌标识(favicon+移动端
// 同款),但 dist 根级文件不在 /assets/ 子树内,无显式路由时被 "/" catch-all
// 的 serveSPA 以 text/html 兜底--隧道域名上 /icon.png 返回空 HTML,浏览器
// Strict MIME 拒绝。本钉测锁定:显式路由直答嵌入 FS 内的 icon.png。
func TestIconRouteServesPNG(t *testing.T) {
	s, _ := newTestServerWithSave(t)

	req := httptest.NewRequest(http.MethodGet, "/icon.png", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/icon.png status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc == "" {
		t.Fatal("Cache-Control header missing")
	}
	body := rec.Body.Bytes()
	if len(body) < 10_000 {
		t.Fatalf("icon body too small (%d bytes) — stale/missing embed?", len(body))
	}
	if !bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("body lacks PNG magic bytes — not serving the icon asset")
	}
}
