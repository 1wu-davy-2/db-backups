package secrets

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
)

// 文件级分块 AEAD。
// 为什么不是整文件 GCM：GCM 不能流式、大文件直接吃爆内存，且单 key 加密超量会危及 nonce 唯一性。
// 结构：magic(4) | version(1) | salt(16) | chunk*
// chunk：len(4, 大端) | ciphertext。len == 0 表示结束标记，用于检测截断。
const (
	fileMagic     = "DBB1"
	fileVersion   = byte(1)
	fileSaltLen   = 16
	fileChunkSize = 1 << 20 // 1 MiB 明文
	fileInfo      = "dbbackups:file:v1"
	maxChunkLen   = fileChunkSize + 64
)

func (k *Keyring) newFileAEAD(salt []byte) (cipher.AEAD, error) {
	return (&Keyring{master: k.derive(salt, fileInfo)}).aead()
}

func chunkNonce(idx uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[:8], idx)
	return n
}

func chunkAAD(idx uint64) []byte {
	a := make([]byte, 8)
	binary.BigEndian.PutUint64(a, idx)
	return a
}

type encWriter struct {
	w       io.Writer
	gcm     cipher.AEAD
	pending []byte
	idx     uint64
	closed  bool
}

// NewEncWriter 写入加密流。调用方必须在成功路径调用 Close，否则文件缺少结束标记。
func (k *Keyring) NewEncWriter(w io.Writer) (io.WriteCloser, error) {
	salt := make([]byte, fileSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	gcm, err := k.newFileAEAD(salt)
	if err != nil {
		return nil, err
	}
	header := make([]byte, 0, 4+1+fileSaltLen)
	header = append(header, []byte(fileMagic)...)
	header = append(header, fileVersion)
	header = append(header, salt...)
	if _, err := w.Write(header); err != nil {
		return nil, err
	}
	return &encWriter{w: w, gcm: gcm, pending: make([]byte, 0, fileChunkSize)}, nil
}

func (e *encWriter) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errors.New("write to closed enc writer")
	}
	e.pending = append(e.pending, p...)
	for len(e.pending) >= fileChunkSize {
		if err := e.emit(e.pending[:fileChunkSize]); err != nil {
			return 0, err
		}
		rest := copy(e.pending, e.pending[fileChunkSize:])
		e.pending = e.pending[:rest]
	}
	return len(p), nil
}

func (e *encWriter) emit(plain []byte) error {
	ct := e.gcm.Seal(nil, chunkNonce(e.idx), plain, chunkAAD(e.idx))
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(ct)))
	if _, err := e.w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := e.w.Write(ct); err != nil {
		return err
	}
	e.idx++
	return nil
}

func (e *encWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	if len(e.pending) > 0 {
		if err := e.emit(e.pending); err != nil {
			return err
		}
		e.pending = nil
	}
	var hdr [4]byte // len = 0，结束标记
	_, err := e.w.Write(hdr[:])
	return err
}

type decReader struct {
	r    io.Reader
	gcm  cipher.AEAD
	buf  []byte
	idx  uint64
	done bool
}

// NewDecReader 读取加密流。数据被截断时返回 io.ErrUnexpectedEOF 而不是静默 EOF。
func (k *Keyring) NewDecReader(r io.Reader) (io.Reader, error) {
	header := make([]byte, 4+1+fileSaltLen)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, errors.New("备份文件头不完整")
	}
	if string(header[:4]) != fileMagic {
		return nil, errors.New("不是本系统生成的加密备份")
	}
	if header[4] != fileVersion {
		return nil, errors.New("不支持的备份文件版本")
	}
	gcm, err := k.newFileAEAD(header[5:])
	if err != nil {
		return nil, err
	}
	return &decReader{r: r, gcm: gcm}, nil
}

func (d *decReader) Read(p []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.done {
			return 0, io.EOF
		}
		if err := d.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}

func (d *decReader) next() error {
	var hdr [4]byte
	if _, err := io.ReadFull(d.r, hdr[:]); err != nil {
		return io.ErrUnexpectedEOF
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		d.done = true
		return nil
	}
	if n > maxChunkLen {
		return errors.New("备份文件损坏：分块长度异常")
	}
	ct := make([]byte, n)
	if _, err := io.ReadFull(d.r, ct); err != nil {
		return io.ErrUnexpectedEOF
	}
	plain, err := d.gcm.Open(nil, chunkNonce(d.idx), ct, chunkAAD(d.idx))
	if err != nil {
		return errors.New("备份文件解密失败：密钥不匹配或文件被篡改")
	}
	d.idx++
	d.buf = plain
	return nil
}
