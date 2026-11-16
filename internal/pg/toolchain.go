package pg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	versionRe = regexp.MustCompile(`(\d+)(?:\.(\d+))?`)
	// Debian 打包的 pg_dump 输出形如：
	//   pg_dump (PostgreSQL) 16.16 (Debian 16.16-1.pgdg12+1)
	// 取第一个 x.y 才是真实版本号。
	toolVerRe = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`)
)

type tool struct {
	Major int
	Path  string
	Ver   string
}

// Toolchain 维护可用的 pg_dump/pg_restore 版本矩阵。
// 关键规则：pg_dump 主版本必须 >= 服务端主版本，否则 dump 无效。
type Toolchain struct {
	mu    sync.RWMutex
	tools []tool
	dirs  []string
}

func NewToolchain(dirs []string) *Toolchain {
	t := &Toolchain{dirs: dirs}
	t.Refresh()
	return t
}

func (t *Toolchain) Refresh() {
	var found []tool
	seen := map[string]bool{}

	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		if _, err := os.Stat(p); err != nil {
			return
		}
		seen[p] = true
		ver, err := toolVersion(p)
		if err != nil {
			return
		}
		m := versionRe.FindStringSubmatch(ver)
		if m == nil {
			return
		}
		major, _ := strconv.Atoi(m[1])
		if major < 9 {
			return
		}
		found = append(found, tool{Major: major, Path: p, Ver: ver})
	}

	for _, d := range t.dirs {
		exe := "pg_dump"
		if runtime.GOOS == "windows" {
			exe = "pg_dump.exe"
		}
		add(filepath.Join(d, exe))
	}
	if p, err := exec.LookPath("pg_dump"); err == nil {
		add(p)
	}

	sort.Slice(found, func(i, j int) bool { return found[i].Major < found[j].Major })
	// 同一主版本去重，只保留第一个
	dedup := found[:0]
	for i, f := range found {
		if i > 0 && f.Major == found[i-1].Major {
			continue
		}
		dedup = append(dedup, f)
	}

	t.mu.Lock()
	t.tools = dedup
	t.mu.Unlock()
}

func toolVersion(path string) (string, error) {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", err
	}
	s := string(out)
	if m := toolVerRe.FindString(s); m != "" {
		return m, nil
	}
	return strings.TrimSpace(s), nil
}

// Pick 选出 >= serverMajor 的最小可用版本。
func (t *Toolchain) Pick(serverMajor int) (path string, major int, err error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.tools) == 0 {
		return "", 0, fmt.Errorf("未找到 pg_dump：请安装 postgresql-client 或设置 BACKUP_PG_BIN_DIRS")
	}
	for _, tl := range t.tools {
		if tl.Major >= serverMajor {
			return tl.Path, tl.Major, nil
		}
	}
	best := t.tools[len(t.tools)-1]
	return "", 0, fmt.Errorf(
		"本地 pg_dump 最高版本为 %d，低于目标库主版本 %d；旧版客户端无法备份新版服务器，请安装对应版本客户端",
		best.Major, serverMajor)
}

func (t *Toolchain) RestorePath(dumpMajor int) (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, tl := range t.tools {
		if tl.Major == dumpMajor {
			return filepath.Join(filepath.Dir(tl.Path), exeName("pg_restore")), nil
		}
	}
	if len(t.tools) > 0 {
		return filepath.Join(filepath.Dir(t.tools[len(t.tools)-1].Path), exeName("pg_restore")), nil
	}
	return "", fmt.Errorf("未找到 pg_restore")
}

func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// List 返回可读的版本清单，供 /api/system/toolchain 展示。
func (t *Toolchain) List() []map[string]any {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]map[string]any, 0, len(t.tools))
	for _, tl := range t.tools {
		out = append(out, map[string]any{"major": tl.Major, "version": tl.Ver, "path": tl.Path})
	}
	return out
}

// CompressArg 把策略里的压缩配置翻译成 pg_dump 参数。
// PG16+ 客户端支持 zstd，低版本回退 gzip 并告知调用方。
func CompressArg(spec string, clientMajor int) (value string, actual string, err error) {
	spec = strings.TrimSpace(strings.ToLower(spec))
	if spec == "" || spec == "none" {
		return "0", "none", nil
	}
	kind, levelStr, hasLevel := strings.Cut(spec, ":")
	if !hasLevel {
		levelStr = "5"
	}
	level, err := strconv.Atoi(levelStr)
	if err != nil || level < 1 || level > 19 {
		return "", "", fmt.Errorf("压缩级别非法: %s", spec)
	}
	switch kind {
	case "zstd":
		if clientMajor >= 16 {
			return fmt.Sprintf("zstd:%d", level), fmt.Sprintf("zstd:%d", level), nil
		}
		if level > 9 {
			level = 9
		}
		return strconv.Itoa(level), fmt.Sprintf("gzip:%d", level), nil
	case "gzip":
		if level > 9 {
			level = 9
		}
		return strconv.Itoa(level), fmt.Sprintf("gzip:%d", level), nil
	default:
		return "", "", fmt.Errorf("不支持的压缩算法: %s", kind)
	}
}
