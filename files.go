package main

import (
	"context"
	"database/sql"
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
		dirID          id
		renameReceiver bool
		cache          *fileCache
	}
	filelist map[string][]fuse.Dirent
)

const (
	negativeTag       = "_"
	contentTag        = "@"
	renameReceiverTag = "@@"
)

var (
	nameID = regexp.MustCompile(`^\|(\d+)\|(.*)`)
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
	query := "WITH tags AS (SELECT name FROM item_tags LEFT JOIN items ON id = other_id WHERE item_id = i.id) " +
		"SELECT * FROM items i WHERE " + strings.Join(tagFilter, " AND ")
	rows, err := db.Raw(query, params...).Rows()
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (f filesDir) findFile(name string) (*item, error) {
	if cached, ok := f.cache.get(name); ok {
		if cached.missing {
			return nil, nil
		}
		return cached, nil
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
	if f.renameReceiver {
		return emptyDir(), nil
	}
	rows, err := f.listFiles("")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fl := filelist{}
	for rows.Next() {
		var i item
		db.ScanRows(rows, &i)
		f.cache.put(i.Name, &i)
		fl[i.Name] = append(fl[i.Name], fuse.Dirent{Inode: uint64(i.ID), Name: i.Name, Type: i.fuseType()})
	}
	return dedupFilelist(fl), nil
}

func (f filesDir) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (node fs.Node, handle fs.Handle, err error) {
	if !isValidName(req.Name) {
		return nil, nil, syscall.EINVAL
	}
	activeTagNames := f.getTags()
	var tags []item
	db.Find(&tags, "name IN (?) AND type = ?", activeTagNames, tag)
	var newItem = item{Name: req.Name, Type: file, ParentID: f.dirID}
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
	if f.renameReceiver {
		return nil, syscall.ENOENT
	}
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

func (f filesDir) Remove(ctx context.Context, req *fuse.RemoveRequest) error {
	i, err := f.findFile(req.Name)
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

func (f filesDir) Rename(ctx context.Context, req *fuse.RenameRequest, newDir fs.Node) error {
	if !isValidName(req.NewName) {
		return syscall.EINVAL
	}
	target, ok := newDir.(filesDir)
	if !ok {
		return syscall.EINVAL
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
	srcItem.Name = req.NewName
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
	if !isValidName(req.Name) {
		return nil, syscall.EINVAL
	}
	tagsNames := f.getTags()
	var parentID id = 0
	var parentDir item
	if !db.First(&parentDir, "id = ?", f.dirID).RecordNotFound() {
		parentID = parentDir.ID
	}
	result := filesDir{hasTags{tags: f.tags}, 0, false, newCache()}
	newDir := item{0, req.Name, dir, parentID, nil, false}
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
