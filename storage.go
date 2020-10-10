package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
)

func makeOldDir(version int) (string, error) {
	olddir := path.Clean(storagePath) + "_v" + strconv.FormatInt(int64(version), 10)
	fi, err := os.Stat(olddir)
	if os.IsExist(err) && !fi.IsDir() {
		return "", fmt.Errorf("%s already exists and is not a directory, can't migrate the storage", olddir)
	}
	os.Rename(storagePath, olddir)
	return olddir, nil
}

func logMigrationStart(version int) {
	log.Printf("Migrating from ver %d to %d", version, version+1)
}

func logMigrationEnd(version int) {
	log.Printf("Successfully migrated from ver %d to %d", version, version+1)
}

func upgradeStorage() error {
	version := 1
	verpath := path.Join(storagePath, "version.txt")
	verstr, err := ioutil.ReadFile(verpath)
	if err == nil {
		verint, err := strconv.ParseInt(string(verstr), 10, 32)
		if err == nil {
			version = int(verint)
		}
	}
	var needUpgrade = true
	for needUpgrade {
		switch version {
		case 1:
			logMigrationStart(version)
			olddir, err := makeOldDir(version)
			if err != nil {
				return err
			}
			filepath.Walk(olddir, func(path string, info os.FileInfo, _ error) error {
				if info == nil || info.IsDir() {
					return nil
				}
				rel, err := filepath.Rel(storagePath, path)
				if err != nil {
					return err
				}
				if rel == "/version.txt" {
					return nil
				}
				match := filenameRegex.FindStringSubmatch(info.Name())
				if match == nil {
					return fmt.Errorf("bad filename %s", info.Name())
				}
				fileid, err := strconv.ParseUint(match[1], 10, 64)
				if err != nil {
					return err
				}
				newpath, err := filePathWithNameTx(fileid, match[2])
				if err != nil {
					return err
				}
				if err = os.Rename(path, newpath); err != nil {
					return err
				}
				return nil
			})
			logMigrationEnd(version)
			version++
		default:
			needUpgrade = false
		}
	}
	ioutil.WriteFile(verpath, []byte(strconv.FormatInt(int64(version), 10)), 0644)
	return nil
}
