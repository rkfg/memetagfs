package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"strconv"

	_ "net/http/pprof"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/docopt/docopt-go"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
)

var (
	db  *gorm.DB
	uid uint32
	gid uint32
)

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

const usage = `Usage:
	memetagfs [-v] [-s storage] [-d database.db] [-p] <mountpoint>
	memetagfs -h

Options:
	-s --storage dir        Storage directory [default: storage]
	-d --database database  Path to the database [default: fs.db]
	-p --prof               Run a webserver to profile the binary
	-v --verbose            Verbose logging
	-h --help               Show this help.
`

var storagePath string

func main() {
	opts, err := docopt.ParseDoc(usage)
	if err != nil {
		log.Fatalln("Error parsing options:", err)
	}
	mountpoint, _ := opts.String("<mountpoint>")
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
	dbPath, _ := opts.String("--database")
	db, err = gorm.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if v, _ := opts.Bool("--verbose"); v {
		db.LogMode(true)
	}
	storagePath, _ = opts.String("--storage")
	fuse.Debug = func(msg interface{}) {
		// if !strings.Contains(msg.(fmt.Stringer).String(), ".git") {
		// log.Println(msg)
		// }
	}
	db.AutoMigrate(item{})
	if p, _ := opts.Bool("--prof"); p {
		go func() {
			log.Println(http.ListenAndServe("localhost:6060", nil))
		}()
	}
	if err = fs.Serve(c, filesystem{}); err != nil {
		log.Fatal(err)
	}
}
