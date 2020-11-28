package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
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
	filelist       map[string][]*item
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

func reverseSlice(slice []interface{}) {
	for i, j := 0, len(slice)-1; i < j; i, j = i+1, j-1 {
		slice[i], slice[j] = slice[j], slice[i]
	}
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
	// speed up SQL because latter tags usually have much less files
	if len(negativeTagNames) > 0 { // only reverse SQL filter if there are negative tags, otherwise it's pointless
		sort.Sort(sort.Reverse(sort.StringSlice(tagFilter)))
	}
	reverseSlice(params)
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

func isRegularValid(name string) (string, error) {
	match := nameID.FindStringSubmatch(name)
	if match != nil {
		name = match[2]
	}
	if !isValidName(name) {
		return "", syscall.ENOENT
	}
	return name, nil
}

func (f filesDir) cleanupName(name string, keepID bool) (string, error) {
	if f.allTags {
		return cleanupAllTags(name, keepID)
	}
	cleanName, err := isRegularValid(name)
	if err != nil {
		return "", err
	}
	if keepID {
		return name, nil
	}
	return cleanName, nil
}

func (f filesDir) findFile(name string) (*item, error) {
	if cached, ok := f.cache.get(name); ok {
		if cached.missing {
			return nil, syscall.ENOENT
		}
		return cached, nil
	}
	cleanName, err := f.cleanupName(name, true)
	if err != nil {
		return nil, err
	}
	rows, err := f.listFiles(cleanName)
	if err != nil {
		f.cache.putMissing(name)
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		f.cache.putMissing(name)
		return nil, syscall.ENOENT
	}
	var i item
	db.ScanRows(rows, &i)
	if rows.Next() { // more than one result, that shouldn't happen
		f.cache.putMissing(name)
		return nil, syscall.ENOENT
	}
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

func (f filesDir) dedupFilelist(fl filelist) []fuse.Dirent {
	var result = emptyDirAlloc(len(fl))
	for k := range fl {
		if len(fl[k]) == 1 {
			result = append(result, fl[k][0].toDirent())
			f.cache.put(fl[k][0].Name, fl[k][0])
		} else {
			for i := range fl[k] {
				tmp := *fl[k][i]
				fl[k][i].Name = "|" + strconv.FormatUint(uint64(fl[k][i].ID), 10) + "|" + fl[k][i].Name
				f.cache.put(fl[k][i].Name, &tmp)
				result = append(result, fl[k][i].toDirent())
			}
		}
	}
	return result
}

func (f filesDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	rows, err := f.listFilesWithTags("", f.allTags)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fl := filelist{}
	tfl := taggedFilelist{}
	for rows.Next() {
		var i item
		db.ScanRows(rows, &i)
		if f.allTags {
			if _, ok := tfl[i.ID]; ok {
				tfl[i.ID].tags = append(tfl[i.ID].tags, i.Tag)
			} else {
				i.tags = append(i.tags, i.Tag)
				tfl[i.ID] = &i
			}
		} else {
			name := i.Name
			fl[name] = append(fl[name], &i)
		}
	}
	if f.allTags {
		result := emptyDir()
		for idx := range tfl {
			i := tfl[idx]
			name := fmt.Sprintf("|%d|%s|%s", i.ID, strings.Join(i.tags, "|"), i.Name)
			f.cache.put(name, i)
			result = append(result, fuse.Dirent{Inode: uint64(i.ID), Name: name, Type: i.fuseType()})
		}
		return result, nil
	}
	return f.dedupFilelist(fl), nil
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
	cleanName, err := f.cleanupName(i.Name, false)
	if err != nil {
		return nil, err
	}
	i.Name = cleanName
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
		db.Model(&i).Association("Items").Clear()
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
	if dstItem, err := target.findFile(newName); err == nil && dstItem != nil {
		// when mounted over sshfs inodes are not preserved so the "same file" error isn't reported
		// which can lead to deleting the source file when moved over itself. Only delete the target
		// if it's actually a different file.
		if dstItem.ID != srcItem.ID {
			target.deleteFile(newName)
		}
	}
	rename := srcItem.Name != newName
	srcItem.Name = newName
	srcItem.ParentID = target.dirID
	tx := db.Begin()
	defer tx.RollbackUnlessCommitted()
	tx = tx.Save(&srcItem)
	if !rename {
		tx.Association("Items").Replace(tags)
	} else {
		to, err := filePathWithTx(tx, uint64(srcItem.ID))
		if err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	tx.Commit()
	invalidateCache()
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
