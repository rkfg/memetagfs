package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

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

func parseUserAndSet(uidgid []string) {
	uidParsed, err := strconv.ParseUint(uidgid[0], 10, 32)
	if err != nil {
		log.Fatal("Error parsing uid:", err)
	}
	gidParsed, err := strconv.ParseUint(uidgid[1], 10, 32)
	if err != nil {
		log.Fatal("Error parsing gid:", err)
	}
	uid = uint32(uidParsed)
	gid = uint32(gidParsed)
}

func setUIDGID(uidgid string) {
	if uidgid == "" {
		uid = uint32(syscall.Getuid())
		gid = uint32(syscall.Getgid())
	} else {
		split := strings.Split(uidgid, ":")
		if len(split) != 2 {
			log.Fatal("Invalid uid:gid parameter")
		}
		parseUserAndSet(split)
	}
}

const usage = `Usage:
	memetagfs [-v] [-s storage] [-d database.db] [-u uid:gid] [-p] <mountpoint>
	memetagfs [-d database.db] [-s storage] -i -t tags.sql -c data.sql -r storage
	memetagfs -h

Options:
	-s --storage dir        Storage directory [default: storage]
	-d --database database  Path to the database [default: fs.db]
	-p --prof               Run a webserver to profile the binary
	-u uid:gid              Use this uid and gid for files instead of current user
	-i                      Import H2 database from jtagsfs
	-t tags.sql             tags.sql file from jtagsfs
	-c data.sql             data.sql file from jtagsfs
	-r storage              storage from jtagsfs
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
	if uidgid, err := opts.String("-u"); err == nil {
		setUIDGID(uidgid)
	} else {
		setUIDGID("")
	}
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
	if i, _ := opts.Bool("-i"); i {
		if err := importH2(opts["-t"].(string), opts["-c"].(string), opts["-r"].(string)); err != nil {
			log.Fatal(err)
		}
		return
	}
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
	if err = fs.Serve(c, filesystem{}); err != nil {
		log.Fatal(err)
	}
}
