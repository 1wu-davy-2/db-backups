package pg

import (
	"strings"
	"testing"
)

// 报错必须把"探测了哪里、怎么修"说全，否则用户只能对着一句提示猜。
func TestNoToolHint(t *testing.T) {
	tc := &Toolchain{searched: []string{"/opt/pg16/bin/pg_dump", "PATH: /usr/bin/pg_dump"}}
	hint := tc.noToolHint()

	for _, want := range []string{
		"未找到可用的 pg_dump",
		"/opt/pg16/bin/pg_dump",
		"BACKUP_PG_BIN_DIRS",
		"docker compose up -d --build",
		"主版本必须 >= 服务端",
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("错误提示缺少 %q：\n%s", want, hint)
		}
	}
}

func TestCompressArgFallback(t *testing.T) {
	// PG15 客户端不支持 zstd，必须回退 gzip 并告知调用方
	got, actual, err := CompressArg("zstd:12", 15)
	if err != nil {
		t.Fatal(err)
	}
	if got != "9" || actual != "gzip:9" {
		t.Fatalf("got %q/%q want 9/gzip:9", got, actual)
	}
	got, actual, err = CompressArg("zstd:5", 16)
	if err != nil || got != "zstd:5" || actual != "zstd:5" {
		t.Fatalf("got %q/%q err=%v", got, actual, err)
	}
}
