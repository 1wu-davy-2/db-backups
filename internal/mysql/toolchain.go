package mysql

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Tool struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Major   int    `json:"major"`
	Minor   int    `json:"minor"`
	Path    string `json:"path"`
}

type Toolchain struct {
	tools    []Tool
	searched []string
}

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)
var mariaPattern = regexp.MustCompile(`(\d+\.\d+\.\d+)-MariaDB`)

func ParseVersion(text string) (Tool, error) {
	engine := "mysql"
	if strings.Contains(strings.ToLower(text), "mariadb") {
		engine = "mariadb"
		if m := mariaPattern.FindStringSubmatch(text); m != nil {
			text = m[1]
		}
	}
	m := versionPattern.FindStringSubmatch(text)
	if m == nil {
		return Tool{}, fmt.Errorf("无法识别数据库版本: %s", text)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return Tool{Engine: engine, Version: m[0], Major: major, Minor: minor}, nil
}

func NewToolchain(dirs []string) *Toolchain {
	tc := &Toolchain{tools: []Tool{}}
	seen := map[string]bool{}
	add := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		tc.searched = append(tc.searched, path)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, path, "--version").Output()
		if err != nil {
			return
		}
		tool, err := ParseVersion(string(out))
		if err != nil {
			return
		}
		tool.Path = path
		tc.tools = append(tc.tools, tool)
	}
	for _, dir := range dirs {
		for _, name := range []string{"mysqldump", "mariadb-dump"} {
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			add(filepath.Join(dir, name))
		}
	}
	for _, name := range []string{"mysqldump", "mariadb-dump"} {
		if path, err := exec.LookPath(name); err == nil {
			add(path)
		}
	}
	return tc
}

func (tc *Toolchain) List() []Tool       { return tc.tools }
func (tc *Toolchain) Searched() []string { return tc.searched }
func (tc *Toolchain) Pick(engine, version string) (Tool, error) {
	server, err := ParseVersion(version)
	if err != nil {
		return Tool{}, err
	}
	if server.Engine != engine {
		return Tool{}, fmt.Errorf("所选类型 %s 与服务端 %s 不符", engine, server.Engine)
	}
	for _, tool := range tc.tools {
		if tool.Engine == engine && tool.Major == server.Major && tool.Minor == server.Minor {
			return tool, nil
		}
	}
	return Tool{}, fmt.Errorf("未找到 %s %d.%d 系列的备份客户端；请安装同厂商、同版本系列的 mysqldump/mariadb-dump，设置 BACKUP_MYSQL_BIN_DIRS 后重启。已探测：\n%s", engine, server.Major, server.Minor, strings.Join(tc.searched, "\n"))
}
