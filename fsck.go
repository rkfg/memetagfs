package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
)

var (
	filenameRegex = regexp.MustCompile(`(\d+)_(.*)`)
)

func copyFile(srcFilename, dstFilename string) error {
	src, err := os.Open(srcFilename)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(dstFilename)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func fsck(fix bool) error {
	var errors, fixed int
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
	if fix {
		log.Printf("Deleting %d file records from the database...", len(badIDs))
		db.Exec("DELETE FROM item_tags WHERE item_id IN (?)", badIDs)
		db.Exec("DELETE FROM items WHERE id IN (?)", badIDs)
		fixed += len(badIDs)
		log.Println("Done.")
	}
	var badFiles []string
	filepath.Walk(storagePath, func(path string, info os.FileInfo, _ error) error {
		if info.IsDir() {
			return nil
		}
		match := filenameRegex.FindStringSubmatch(info.Name())
		if match == nil {
			log.Printf("Bad filename %s", path)
			badFiles = append(badFiles, path)
			return nil
		}
		rel, err := filepath.Rel(storagePath, path)
		if err != nil {
			return err
		}
		dir := filepath.Dir(rel)
		id6, id3 := filepath.Split(dir)
		id6 = filepath.Base(id6)
		if len(id6) != 6 || len(id3) != 3 || id6+id3 != match[1] {
			badFiles = append(badFiles, path)
			log.Printf("Invalid path %s/%s != %s", id6, id3, match[1])
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
			err := copyFile(src, dst)
			if err != nil {
				log.Printf("Error recovering file %s: %s", src, err)
			} else {
				log.Printf("File %s copied to %s", src, dst)
				if err := os.Remove(src); err != nil {
					log.Printf("Error deleting the lost storage file %s: %s", src, err)
				} else {
					log.Printf("Recovered file %s to lost+found", src)
					fixed++
				}
			}
		}
	}
	if errors > 0 {
		return fmt.Errorf("found %d errors, %d fixed", errors, fixed)
	}
	return nil
}
