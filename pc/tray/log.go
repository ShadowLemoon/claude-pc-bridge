package main

import (
	"io"
	"os"
	"sync"
)

const maxLogSize = 1 << 20

// rotatingFile 是追加写的日志文件，超过 1MB 时改名为 .old 再重新开始
type rotatingFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func openLog(path string) (*rotatingFile, error) {
	r := &rotatingFile{path: path}
	return r, r.open()
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(p)) > maxLogSize {
		r.f.Close()
		_ = os.Rename(r.path, r.path+".old")
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

var _ io.Writer = (*rotatingFile)(nil)
