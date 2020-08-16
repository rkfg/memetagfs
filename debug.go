package main

import (
	"bytes"
	"context"
	"os"
	"runtime/pprof"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

type debugDir struct{}

const (
	heap = "heap.pb.gz"
)

func (d debugDir) Attr(ctx context.Context, attr *fuse.Attr) error {
	attr.Inode = 1
	attr.Mode = os.ModeDir | 0755
	attr.Size = 4096
	return nil
}

func (d debugDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	result := append(emptyDir(),
		fuse.Dirent{Inode: 1, Name: heap, Type: fuse.DT_File},
	)
	return result, nil
}

func (d debugDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	switch name {
	case heap:
		return heapFile{}, nil
	}
	return nil, fuse.ENOENT
}

type heapFile struct{}

func (h heapFile) Attr(ctx context.Context, attr *fuse.Attr) error {
	attr.Inode = 1
	attr.Mode = 0644
	attr.Size = 4096
	return nil
}

func (h heapFile) ReadAll(ctx context.Context) ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	pprof.WriteHeapProfile(buf)
	return buf.Bytes(), nil
}
