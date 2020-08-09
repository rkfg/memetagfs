package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
)

const (
	control = "control"
	browse  = "browse"
)

type filesystem struct{}

type itemFS struct {
	name     string
	itemType fuse.DirentType
	size     uint64
	file     io.ReadCloser
}

var (
	db  *gorm.DB
	uid uint32
	gid uint32
)

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
		fuse.Dirent{Inode: 2, Name: browse, Type: fuse.DT_Dir})
	return result, nil
}

func (d rootDir) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	return nil, nil, syscall.EACCES
}

func (d rootDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	if name == control {
		return controlDir{0}, nil
	}
	if name == browse {
		return tagsDir{0, ""}, nil
	}
	return nil, syscall.ENOENT
}

func getUIDGID() {
	u, err := user.Current()
	if err != nil {
		log.Fatal("Error getting current user:", err)
	}
	uidParsed, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		log.Fatal("Error parsing current user uid:", err)
	}
	gidParsed, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		log.Fatal("Error parsing current user gid:", err)
	}
	uid = uint32(uidParsed)
	gid = uint32(gidParsed)
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Enter mountpoint as the only argument")
	}
	mountpoint := os.Args[1]
	getUIDGID()
	c, err := fuse.Mount(mountpoint)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	cc := make(chan os.Signal)
	signal.Notify(cc, os.Interrupt)
	go func() {
		for range cc {
			fuse.Unmount(mountpoint)
		}
	}()
	db, err = gorm.Open("sqlite3", "fs.db")
	// db.LogMode(true)
	fuse.Debug = func(msg interface{}) {
		if !strings.Contains(msg.(fmt.Stringer).String(), ".git") {
			// log.Println(msg)
		}
	}
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.AutoMigrate(item{})
	if err = fs.Serve(c, filesystem{}); err != nil {
		log.Fatal(err)
	}
}
