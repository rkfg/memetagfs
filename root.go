package main

import (
	"context"
	"os"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

const (
	control = "tags"
	browse  = "browse"
	debug   = "debug"
)

type filesystem struct{}

func emptyDir() []fuse.Dirent {
	return []fuse.Dirent{
		{Inode: 0, Name: ".", Type: fuse.DT_Dir},
		{Inode: 0, Name: "..", Type: fuse.DT_Dir},
	}
}

func (filesystem) Root() (fs.Node, error) {
	return rootDir{}, nil
}

type rootDir struct {
}

func (d rootDir) Attr(ctx context.Context, attr *fuse.Attr) error {
	attr.Inode = 1
	attr.Mode = os.ModeDir | 0755
	attr.Size = 4096
	return nil
}

func (d rootDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	var result = emptyDir()
	result = append(result,
		fuse.Dirent{Inode: 1, Name: control, Type: fuse.DT_Dir},
		fuse.Dirent{Inode: 2, Name: browse, Type: fuse.DT_Dir},
	)
	return result, nil
}

func (d rootDir) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	return nil, nil, syscall.EACCES
}

func (d rootDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	switch name {
	case control:
		return controlDir{}, nil
	case browse:
		return tagsDir{}, nil
	}
	return nil, syscall.ENOENT
}
