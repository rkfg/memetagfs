package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

type filesDir struct {
	tags           string
	id             uint64
	dirID          id
	renameReceiver bool
}

const (
	negativeTag       = "_"
	contentTag        = "@"
	renameReceiverTag = "@@"
)

func (f filesDir) getDirectoryItem(dir string) (*item, error) {
	if len(dir) == 0 {
		return nil, nil
	}
	rows, err := f.listFiles(dir)
	if err != nil {
		return nil, err
	}
	var result item
	if !rows.Next() {
		return nil, syscall.ENOENT
	}
	db.ScanRows(rows, &result)
	return &result, nil
}

func (f filesDir) Attr(ctx context.Context, attr *fuse.Attr) error {
	attr.Inode = f.id
	attr.Mode = os.ModeDir | 0755
	attr.Size = 4096
	attr.Uid = uid
	attr.Gid = gid
	return nil
}

func (f filesDir) listFiles(name string) (*sql.Rows, error) {
	activeTagNames := f.getAllTags()
	tagFilter := make([]string, 0, len(activeTagNames))
	params := make([]interface{}, 0, len(activeTagNames))
	negative := false
	for i := range activeTagNames {
		if negative {
			negative = false
			tagFilter = append(tagFilter, "? NOT IN tags")
			params = append(params, activeTagNames[i])
		} else {
			if activeTagNames[i] == negativeTag {
				negative = true
			} else {
				tagFilter = append(tagFilter, "? IN tags")
				params = append(params, activeTagNames[i])
			}
		}
	}
	if name != "" {
		params = append(params, name)
		tagFilter = append(tagFilter, "i.name = ?")
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
	rows, err := f.listFiles(name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	var i item
	db.ScanRows(rows, &i)
	return &i, nil
}

func (f filesDir) getAllTags() []string {
	result := strings.Split(f.tags, string(os.PathSeparator))
	if result[len(result)-1] == contentTag || result[len(result)-1] == renameReceiverTag {
		result = result[:len(result)-1]
	}
	return result
}

func (f filesDir) getTags() []string {
	allTags := f.getAllTags()
	var result []string
	skipTag := false
	for _, tag := range allTags {
		if skipTag {
			skipTag = false
		} else {
			if tag == negativeTag {
				skipTag = true
			} else {
				result = append(result, tag)
			}
		}
	}
	return result
}

func tagsItems(activeTagNames []string) []item {
	var tags = make([]item, len(activeTagNames))
	for i := range activeTagNames {
		db.First(&tags[i], "name = ?", activeTagNames[i])
	}
	return tags
}

func (f filesDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	var result = emptyDir()
	if f.renameReceiver {
		return result, nil
	}
	rows, err := f.listFiles("")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var i item
		db.ScanRows(rows, &i)
		result = append(result, fuse.Dirent{Inode: uint64(i.ID), Name: i.Name, Type: i.fuseType()})
	}
	return result, nil
}

func (f filesDir) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (node fs.Node, handle fs.Handle, err error) {
	activeTagNames := f.getTags()
	var tags []item
	db.Find(&tags, "name IN (?)", activeTagNames)
	var newItem = item{Name: req.Name, Type: file, ParentID: f.dirID}
	db.Create(&newItem).Association("Items").Append(tags)
	c := content{itype: file, id: uint64(newItem.ID)}
	path, err := c.filePath()
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
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
		return filesDir{tags: f.tags, dirID: i.ID, id: f.id + 1}, nil
	}
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
		return nil
	}
	return syscall.ENOENT
}

func (f filesDir) Rename(ctx context.Context, req *fuse.RenameRequest, newDir fs.Node) error {
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
	srcItem.Name = req.NewName
	db.Save(&srcItem).Association("Items").Replace(tags)
	to, err := filePath(uint64(srcItem.ID))
	if err != nil {
		return err
	}
	os.Rename(from, to)
	return nil
}

func (f filesDir) Mkdir(ctx context.Context, req *fuse.MkdirRequest) (fs.Node, error) {
	tagsNames := f.getTags()
	var parentID id = 0
	var parentDir item
	if !db.First(&parentDir, "id = ?", f.dirID).RecordNotFound() {
		parentID = parentDir.ID
	}
	result := filesDir{f.tags, f.id + 1, 0, false}
	newDir := item{0, req.Name, dir, parentID, nil}
	var tags []item
	if db.Find(&tags, "name in (?)", tagsNames).RecordNotFound() {
		return nil, syscall.ENOENT
	}
	if err := db.Create(&newDir).Association("Items").Replace(&tags).Error; err != nil {
		return nil, syscall.EINVAL
	}
	result.dirID = newDir.ID
	return result, nil
}
