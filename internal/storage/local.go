package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var keyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9/_.-]*$`)

// Local 把对象落到本地目录。key 由服务端生成，仍做严格校验防路径穿越。
type Local struct {
	root string
}

func NewLocal(root string) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &Local{root: abs}, nil
}

func (l *Local) Kind() string { return "local" }

func (l *Local) resolve(key string) (string, error) {
	if !keyRe.MatchString(key) || strings.Contains(key, "..") {
		return "", fmt.Errorf("非法对象 key")
	}
	p := filepath.Join(l.root, filepath.FromSlash(key))
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if abs != l.root && !strings.HasPrefix(abs, l.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("对象路径越界")
	}
	return abs, nil
}

// Put 先写临时文件再 rename：中途失败不会留下半截对象被误认为可用备份。
func (l *Local) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	dst, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (l *Local) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	p, err := l.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return f, err
}

func (l *Local) Delete(ctx context.Context, key string) error {
	p, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l *Local) Stat(ctx context.Context, key string) (int64, error) {
	p, err := l.resolve(key)
	if err != nil {
		return 0, err
	}
	st, err := os.Stat(p)
	if os.IsNotExist(err) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}
