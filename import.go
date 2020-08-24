package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var (
	insertTag           = regexp.MustCompile(`INSERT INTO PUBLIC.TAG\(.*\) VALUES\((\d+), (.+), (NULL|\d+)\);`)
	insertFileRecord    = regexp.MustCompile(`INSERT INTO PUBLIC.FILERECORD\(.*\) VALUES\((\d+), (.+)\);`)
	insertFileRecordTag = regexp.MustCompile(`INSERT INTO PUBLIC.FILERECORD_TAG\(.*\) VALUES\((\d+), (\d+)\);`)
	strdecode           = regexp.MustCompile(`STRINGDECODE\('(.*)'\)`)
)

type linemode uint

const (
	none linemode = iota
	filevals
	tagfilevals
	tagvals
)

func maybeDecode(s string) string {
	decode := strdecode.FindStringSubmatch(s)
	if decode != nil {
		var decoded string
		err := json.Unmarshal([]byte(`"`+decode[1]+`"`), &decoded)
		if err != nil {
			return s
		}
		return decoded
	}
	if len(s) > 1 && s[0] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

func importTags(tagspath string) error {
	f, err := os.Open(tagspath)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		match := insertTag.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if match[3] == "NULL" {
			match[3] = "0"
		}
		db.Exec("INSERT INTO items(id, name, type, parent_id) VALUES (?, ?, ?, ?)", match[1], maybeDecode(match[2]), tag, match[3])
	}
	return nil
}

func copyStorageFile(storage, srcIDStr, srcFilename string, id uint64) error {
	srcID, err := strconv.ParseInt(srcIDStr, 10, 64)
	if err != nil {
		return err
	}
	src, err := os.Open(path.Join(storage, strconv.FormatInt(srcID%1000, 10), fmt.Sprintf("%d|_|%s", srcID, srcFilename)))
	if err != nil {
		return err
	}
	defer src.Close()
	dstPath, err := filePath(id)
	if err != nil {
		return err
	}
	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func importData(datapath, storage string) error {
	f, err := os.Open(datapath)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	mapping := map[string]int64{}
	for scanner.Scan() {
		line := scanner.Text()
		match := insertFileRecord.FindStringSubmatch(line)
		if match != nil {
			filename := maybeDecode(match[2])
			res, err := db.DB().Exec("INSERT INTO items(name, type, parent_id) VALUES (?, ?, ?)", filename, file, 0)
			if err != nil {
				return err
			}
			id, _ := res.LastInsertId()
			mapping[match[1]] = id
			if err := copyStorageFile(storage, match[1], filename, uint64(id)); err != nil {
				log.Printf("Error migrating file %s [id %s]: %v", filename, match[1], err)
			}
		}
		match = insertFileRecordTag.FindStringSubmatch(line)
		if match != nil {
			if mapped, ok := mapping[match[1]]; ok {
				if err := db.Exec("INSERT INTO item_tags(item_id, other_id) VALUES (?, ?)", mapped, match[2]).Error; err != nil {
					return err
				}
			} else {
				return fmt.Errorf("key %s not found", match[1])
			}
		}
	}
	return nil
}

func importH2(tagspath string, datapath string, storage string) error {
	if err := importTags(tagspath); err != nil {
		return err
	}
	if err := importData(datapath, storage); err != nil {
		return err
	}
	return nil
}
