package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
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

func fixFilename(itemID uint64) error {
	var i item
	tx := db.Begin()
	defer tx.RollbackUnlessCommitted()
	tx.First(&i, "id = ?", itemID)
	i.Name = strings.ReplaceAll(i.Name, "|", "\u00a6")
	path, err := filePathWithTx(tx, itemID)
	if err != nil {
		return fmt.Errorf("error getting path of file with id = %d: %s", itemID, err)
	}
	fn := strings.ReplaceAll(filepath.Base(path), "|", "\u00a6")
	newPath := filepath.Join(filepath.Dir(path), fn)
	if err := os.Rename(path, newPath); err != nil {
		return fmt.Errorf("error renaming %s => %s: %s", path, newPath, err)
	}
	log.Printf("Renamed %s => %s", path, newPath)
	tx.Save(&i)
	tx.Commit()
	return nil
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
	var lostFiles []string
	var badFiles []uint64
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
			lostFiles = append(lostFiles, path)
			return nil
		}
		dir := filepath.Dir(rel)
		id6, id2 := filepath.Split(dir)
		id6 = filepath.Base(id6)
		if len(id6) != 6 || len(id2) != 2 || !strings.HasPrefix(match[1], id6+id2) {
			lostFiles = append(lostFiles, path)
			log.Printf("Invalid path %s/%s != %s", id6, id2, match[1])
			return nil
		}
		var i item
		if db.First(&i, "id = ? AND name = ?", match[1], match[2]).RecordNotFound() {
			lostFiles = append(lostFiles, path)
			log.Printf("File %s is in storage but not in database", path)
		}
		if strings.Contains(info.Name(), "|") {
			fileID, _ := strconv.ParseUint(match[1], 10, 64)
			badFiles = append(badFiles, fileID)
			log.Printf("File %s name contains invalid characters", path)
		}
		return nil
	})
	errors += len(lostFiles)
	if fix && len(lostFiles) > 0 {
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
		for _, src := range lostFiles {
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
	if fix && len(badFiles) > 0 {
		pass("fixing invalid filenames")
		for _, itemID := range badFiles {
			if err := fixFilename(itemID); err != nil {
				log.Println(err)
			}
		}
	}
	if errors > 0 {
		return fmt.Errorf("found %d errors, %d fixed", errors, fixed)
	}
	return nil
}
