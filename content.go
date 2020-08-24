package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

type content struct {
	id    uint64
	itype itemType
}

type virtualFile struct {
	handle *os.File
}

func nameByID(id uint64) (string, error) {
	var result item
	if db.Model(&item{}).Select("name").First(&result, "id = ?", id).RecordNotFound() {
		return "", syscall.ENOENT
	}
	return result.Name, nil
}

func filePath(id uint64) (string, error) {
	first := fmt.Sprintf("%06d", id/1000)
	second := fmt.Sprintf("%03d", id%1000)
	dir := path.Join(storagePath, first, second)
	os.MkdirAll(dir, 0755)
	name, err := nameByID(id)
	if err != nil {
		return "", err
	}
	return path.Join(dir, fmt.Sprintf("%09d_%s", id, name)), nil
}

func (c content) filePath() (string, error) {
	return filePath(c.id)
}

func (c content) Attr(ctx context.Context, attr *fuse.Attr) error {
	attr.Inode = c.id
	if c.itype == file {
		path, err := c.filePath()
		if err != nil {
			return err
		}
		fi, err := os.Stat(path)
		if err != nil {
			return syscall.ENOENT
		}
		attr.Mode = fi.Mode()
		attr.Size = uint64(fi.Size())
		attr.Atime = time.Now()
		attr.Ctime = fi.ModTime()
		attr.Mtime = fi.ModTime()
	} else {
		attr.Mode = os.ModeDir | 0755
		attr.Size = 4096
	}
	attr.Uid = uid
	attr.Gid = gid
	return nil
}

func (c content) Open(ctx context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	if req.Dir {
		return nil, syscall.EINVAL
	}
	var path string
	path, err := c.filePath()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, int(req.Flags), os.ModePerm)
	if err != nil {
		return nil, err
	}
	return virtualFile{handle: f}, nil
}

func (c content) Setattr(ctx context.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error {
	if req.Valid.Size() {
		path, err := c.filePath()
		if err != nil {
			return err
		}
		os.Truncate(path, int64(req.Size))
	}
	return nil
}

func (c content) Fsync(ctx context.Context, req *fuse.FsyncRequest) error {
	// just for vim and such to work
	return nil
}

func (v virtualFile) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	n, err := v.handle.ReadAt(resp.Data[:req.Size], req.Offset)
	if err != nil && err != io.EOF {
		return err
	}
	resp.Data = resp.Data[:n]
	return nil
}

func (v virtualFile) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	var n int
	var err error
	if int(req.FileFlags)&os.O_APPEND != 0 {
		n, err = v.handle.Write(req.Data)
	} else {
		n, err = v.handle.WriteAt(req.Data, req.Offset)
	}
	if err != nil {
		return err
	}
	resp.Size = n
	return nil
}

func (v virtualFile) Release(ctx context.Context, req *fuse.ReleaseRequest) error {
	return v.handle.Close()
}
