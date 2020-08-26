package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

type (
	filesDir struct {
		hasTags
		dirID   id
		allTags bool
		cache   *fileCache
	}
	filelist       map[string][]fuse.Dirent
	taggedFilelist map[id]*item
)

const (
	negativeTag = "_"
	contentTag  = "@"
	allTagsTag  = "@@"
)

var (
	nameID          = regexp.MustCompile(`^\|(\d+)\|(.*)`)
	nameWithoutTags = regexp.MustCompile(`^\|(\d+)(\||.*)\|([^|]+)$`)
)

func isValidName(s string) bool {
	return !strings.Contains(s, "|")
}

func (f filesDir) getDirectoryItem(dir string) (*item, error) {
	if len(dir) == 0 {
		return nil, nil
	}
	if cached, ok := f.cache.get(dir); ok {
		if cached.missing {
			return nil, syscall.ENOENT
		}
		return cached, nil
	}
	rows, err := f.listFiles(dir)
	if err != nil {
		f.cache.putMissing(dir)
		return nil, err
	}
	var result item
	if !rows.Next() {
		f.cache.putMissing(dir)
		return nil, syscall.ENOENT
	}
	db.ScanRows(rows, &result)
	f.cache.put(dir, &result)
	return &result, nil
}

func (f filesDir) Attr(ctx context.Context, attr *fuse.Attr) error {
	attr.Mode = os.ModeDir | 0755
	attr.Size = 4096
	attr.Uid = uid
	attr.Gid = gid
	return nil
}

func (f filesDir) listFiles(name string) (*sql.Rows, error) {
	return f.listFilesWithTags(name, false)
}

func (f filesDir) listFilesWithTags(name string, tags bool) (*sql.Rows, error) {
	positiveTagNames, negativeTagNames := f.getTagsWithNegative()
	tagFilter := make([]string, 0, len(positiveTagNames)+len(negativeTagNames)+3)
	params := make([]interface{}, 0, len(positiveTagNames)+len(negativeTagNames)+3)
	if f.dirID == 0 {
		for i := range positiveTagNames {
			tagFilter = append(tagFilter, "? IN tags")
			params = append(params, positiveTagNames[i])
		}
		for i := range negativeTagNames {
			tagFilter = append(tagFilter, "? NOT IN tags")
			params = append(params, negativeTagNames[i])
		}
	}
	if name != "" {
		matches := nameID.FindStringSubmatch(name)
		if matches != nil {
			id, err := strconv.ParseUint(matches[1], 10, 64)
			if err == nil {
				name = matches[2]
				tagFilter = append(tagFilter, "i.id = ?")
				params = append(params, id)
			} else {
				log.Printf("Error parsing %s: %v", name, err)
			}
		}
		tagFilter = append(tagFilter, "i.name = ?")
		params = append(params, name)
	}
	tagFilter = append(tagFilter, "i.parent_id = ?", "i.type IN (?)")
	params = append(params, f.dirID, []itemType{file, dir})
	joinTags := " FROM items i"
	if tags {
		joinTags = ", t.name AS tag FROM items i LEFT JOIN item_tags it ON i.id = it.item_id LEFT JOIN items t ON t.id = it.other_id"
	}
	query := "WITH tags AS (SELECT name FROM item_tags LEFT JOIN items ON id = other_id WHERE item_id = i.id) " +
		"SELECT *" + joinTags + " WHERE " + strings.Join(tagFilter, " AND ")
	rows, err := db.Raw(query, params...).Rows()
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func cleanupAllTags(name string, withID bool) (string, error) {
	match := nameWithoutTags.FindStringSubmatch(name)
	if match == nil {
		if !isValidName(name) {
			return "", syscall.ENOENT
		}
		return name, nil
	}
	if !isValidName(match[3]) {
		return "", syscall.ENOENT
	}
	if withID {
		return fmt.Sprintf("|%s|%s", match[1], match[3]), nil
	}
	return match[3], nil
}

func isRegularValid(name string) error {
	match := nameID.FindStringSubmatch(name)
	if match != nil {
		name = match[2]
	}
	if !isValidName(name) {
		return syscall.ENOENT
	}
	return nil
}

func (f filesDir) cleanupName(name string, withID bool) (string, error) {
	if f.allTags {
		return cleanupAllTags(name, withID)
	}
	if err := isRegularValid(name); err != nil {
		return "", err
	}
	return name, nil
}

func (f filesDir) findFile(name string) (*item, error) {
	if cached, ok := f.cache.get(name); ok {
		if cached.missing {
			return nil, nil
		}
		return cached, nil
	}
	name, err := f.cleanupName(name, true)
	if err != nil {
		return nil, err
	}
	rows, err := f.listFiles(name)
	if err != nil {
		f.cache.putMissing(name)
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		f.cache.putMissing(name)
		return nil, nil
	}
	var i item
	db.ScanRows(rows, &i)
	f.cache.put(name, &i)
	return &i, nil
}

func tagsItems(activeTagNames []string) []item {
	var tags = make([]item, len(activeTagNames))
	for i := range activeTagNames {
		db.First(&tags[i], "name = ?", activeTagNames[i])
	}
	return tags
}

func dedupFilelist(fl filelist) []fuse.Dirent {
	var result = emptyDirAlloc(len(fl))
	for k := range fl {
		if len(fl[k]) == 1 {
			result = append(result, fl[k][0])
		} else {
			for i := range fl[k] {
				fl[k][i].Name = "|" + strconv.FormatUint(fl[k][i].Inode, 10) + "|" + fl[k][i].Name
				result = append(result, fl[k][i])
			}
		}
	}
	return result
}

func (f filesDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	tags := false
	if f.allTags {
		tags = true
	}
	rows, err := f.listFilesWithTags("", tags)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fl := filelist{}
	tfl := taggedFilelist{}
	for rows.Next() {
		var i item
		db.ScanRows(rows, &i)
		if tags {
			if _, ok := tfl[i.ID]; ok {
				tfl[i.ID].tags = append(tfl[i.ID].tags, i.Tag)
			} else {
				i.tags = append(i.tags, i.Tag)
				tfl[i.ID] = &i
			}
		} else {
			name := i.Name
			f.cache.put(name, &i)
			fl[name] = append(fl[name], fuse.Dirent{Inode: uint64(i.ID), Name: name, Type: i.fuseType()})
		}
	}
	if tags {
		result := emptyDir()
		for idx := range tfl {
			i := tfl[idx]
			name := fmt.Sprintf("|%d|%s|%s", i.ID, strings.Join(i.tags, "|"), i.Name)
			f.cache.put(name, i)
			result = append(result, fuse.Dirent{Inode: uint64(i.ID), Name: name, Type: i.fuseType()})
		}
		return result, nil
	}
	return dedupFilelist(fl), nil
}

func (f filesDir) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (node fs.Node, handle fs.Handle, err error) {
	name, err := f.cleanupName(req.Name, false)
	if err != nil {
		return nil, nil, err
	}
	activeTagNames := f.getTags()
	var tags []item
	db.Find(&tags, "name IN (?) AND type = ?", activeTagNames, tag)
	var newItem = item{Name: name, Type: file, ParentID: f.dirID}
	tx := db.Begin()
	defer tx.RollbackUnlessCommitted()
	invalidateCache()
	tx.Create(&newItem).Association("Items").Append(tags)
	c := content{itype: file, id: uint64(newItem.ID)}
	path, err := c.filePathWithTx(tx)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	tx.Commit()
	return c, virtualFile{handle: file}, nil
}

func (f filesDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	i, err := f.findFile(name)
	if err != nil {
		return nil, err
	}
	if i == nil {
		return nil, syscall.ENOENT
	}
	if i.Type == dir {
		return filesDir{hasTags: hasTags{tags: f.tags}, dirID: i.ID, cache: newCache()}, nil
	}
	contentCache.putID(i.ID, i)
	return content{id: uint64(i.ID), itype: i.Type}, nil
}

func (f filesDir) deleteFile(name string) error {
	i, err := f.findFile(name)
	if err != nil {
		return err
	}
	if i == nil {
		return syscall.ENOENT
	}
	if i.ID != 0 {
		if i.Type == file {
			path, err := filePath(uint64(i.ID))
			if err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
		} else {
			if !db.Find(&item{}, "parent_id = ?", i.ID).RecordNotFound() {
				return syscall.ENOTEMPTY
			}
		}
		db.Delete(&i)
		invalidateCache()
		return nil
	}
	return syscall.ENOENT
}

func (f filesDir) Remove(ctx context.Context, req *fuse.RemoveRequest) error {
	return f.deleteFile(req.Name)
}

func (f filesDir) Rename(ctx context.Context, req *fuse.RenameRequest, newDir fs.Node) error {
	target, ok := newDir.(filesDir)
	if !ok {
		return syscall.EINVAL
	}
	newName, err := f.cleanupName(req.NewName, false)
	if err != nil {
		return err
	}
	srcItem, err := f.findFile(req.OldName)
	if err != nil {
		return err
	}
	if srcItem == nil {
		return syscall.ENOENT
	}
	tagsNames := target.getTags()
	tags := tagsItems(tagsNames)
	from, err := filePath(uint64(srcItem.ID))
	if err != nil {
		return err
	}
	invalidateCache()
	f.deleteFile(newName)
	srcItem.Name = newName
	srcItem.ParentID = target.dirID
	tx := db.Begin()
	defer tx.RollbackUnlessCommitted()
	tx.Save(&srcItem).Association("Items").Replace(tags)
	to, err := filePathWithTx(tx, uint64(srcItem.ID))
	if err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	tx.Commit()
	return nil
}

func (f filesDir) Mkdir(ctx context.Context, req *fuse.MkdirRequest) (fs.Node, error) {
	name, err := f.cleanupName(req.Name, false)
	if err != nil {
		return nil, syscall.EINVAL
	}
	tagsNames := f.getTags()
	var parentID id = 0
	var parentDir item
	if !db.First(&parentDir, "id = ?", f.dirID).RecordNotFound() {
		parentID = parentDir.ID
	}
	result := filesDir{hasTags{tags: f.tags}, 0, false, newCache()}
	newDir := item{ID: 0, Name: name, Type: dir, ParentID: parentID}
	var tags []item
	if db.Find(&tags, "name IN (?) AND type = ?", tagsNames, tag).RecordNotFound() {
		return nil, syscall.ENOENT
	}
	tx := db.Begin()
	defer tx.RollbackUnlessCommitted()
	if err := tx.Create(&newDir).Association("Items").Replace(&tags).Error; err != nil {
		return nil, syscall.EINVAL
	}
	result.dirID = newDir.ID
	tx.Commit()
	invalidateCache()
	return result, nil
}
