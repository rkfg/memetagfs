package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	filenameRegex = regexp.MustCompile(`(\d+)_(.*)`)
)

func newPassPrinter() func(title string) {
	var num = 1
	return func(title string) {
		log.Printf("Pass %d: %s...", num, title)
		num++
	}
}

func moveFile(srcFilename, dstFilename string) error {
	return exec.Command("mv", srcFilename, dstFilename).Run()
}

func fsck(fix bool) error {
	var errors, fixed int
	pass := newPassPrinter()
	pass("checking database")
	rows, err := db.Model(&item{}).Where("type = ?", file).Select("id, name").Rows()
	if err != nil {
		return err
	}
	var badIDs []id
	for rows.Next() {
		var i item
		db.ScanRows(rows, &i)
		path, err := filePathWithNameTx(uint64(i.ID), i.Name)
		if err != nil {
			return err
		}
		_, err = os.Stat(path)
		if os.IsNotExist(err) {
			log.Printf("File %s doesn't exist but is present in the database", path)
			badIDs = append(badIDs, i.ID)
		}
	}
	errors += len(badIDs)
	if fix && len(badIDs) > 0 {
		pass("removing incorrect database entries")
		log.Printf("Deleting %d file records from the database...", len(badIDs))
		db.Exec("DELETE FROM item_tags WHERE item_id IN (?)", badIDs)
		db.Exec("DELETE FROM items WHERE id IN (?)", badIDs)
		fixed += len(badIDs)
		log.Println("Done.")
	}
	pass("checking dangling tags")
	var dangling int

	db.Raw("WITH allids AS (SELECT id FROM items) SELECT COUNT(*) FROM item_tags WHERE item_id NOT IN allids OR other_id NOT IN allids").Count(&dangling)
	log.Printf("Found %d dangling tag references", dangling)
	errors += dangling
	if dangling > 0 && fix {
		rows := db.Exec("WITH allids AS (SELECT id FROM items) DELETE FROM item_tags WHERE item_id NOT IN allids OR other_id NOT IN allids").RowsAffected
		log.Printf("Deleted %d dangling tag references", rows)
		fixed += int(rows)
	}
	var badFiles []string
	pass("checking storage")
	filepath.Walk(storagePath, func(path string, info os.FileInfo, _ error) error {
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(storagePath, path)
		if err != nil {
			return err
		}
		if rel == "version.txt" {
			return nil
		}
		match := filenameRegex.FindStringSubmatch(info.Name())
		if match == nil {
			log.Printf("Bad filename %s", path)
			badFiles = append(badFiles, path)
			return nil
		}
		dir := filepath.Dir(rel)
		id6, id2 := filepath.Split(dir)
		id6 = filepath.Base(id6)
		if len(id6) != 6 || len(id2) != 2 || !strings.HasPrefix(match[1], id6+id2) {
			badFiles = append(badFiles, path)
			log.Printf("Invalid path %s/%s != %s", id6, id2, match[1])
			return nil
		}
		var i item
		if db.First(&i, "id = ? AND name = ?", match[1], match[2]).RecordNotFound() {
			badFiles = append(badFiles, path)
			log.Printf("File %s is in storage but not in database", path)
		}
		return nil
	})
	errors += len(badFiles)
	if fix && len(badFiles) > 0 {
		pass("recovering lost files")
		lftag := path.Join(mountpoint, "tags", "lost+found")
		_, err := os.Stat(lftag)
		if os.IsNotExist(err) {
			log.Println("lost+found tag doesn't exist, creating...")
			if err := os.Mkdir(lftag, 0755); err != nil {
				log.Println("Error creating lost+found tag:", err)
				return err
			}
		}
		lfbrowse := path.Join(mountpoint, "browse", "lost+found", "@")
		for _, src := range badFiles {
			dst := path.Join(lfbrowse, filepath.Base(src))
			log.Printf("Recovering %s to %s...", src, dst)
			err := moveFile(src, dst)
			if err != nil {
				log.Printf("Error recovering file %s: %s", src, err)
			} else {
				log.Printf("Recovered file %s to lost+found", src)
				fixed++
			}
		}
	}
	if errors > 0 {
		return fmt.Errorf("found %d errors, %d fixed", errors, fixed)
	}
	return nil
}
