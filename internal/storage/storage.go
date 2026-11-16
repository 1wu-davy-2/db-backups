package storage

import (
	"context"
	"errors"
	"io"
)

var ErrNotFound = errors.New("object not found")

// Storage 抽象对象存储。M1 只有本地实现，S3 兼容实现按同一接口接入。
type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Stat(ctx context.Context, key string) (int64, error)
	Kind() string
}
