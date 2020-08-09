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
	renameReceiver bool
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
	activeTagNames := f.getTags()
	query := make([]string, len(activeTagNames))
	params := make([]interface{}, len(activeTagNames)+1)
	params[0] = []itemType{file, dir}
	for i := range activeTagNames {
		query[i] = "? IN tags"
		params[i+1] = activeTagNames[i]
	}
	if name != "" {
		params = append(params, name)
		query = append(query, "i.name = ?")
	}
	rows, err := db.Raw("WITH tags AS (SELECT name FROM item_tags LEFT JOIN items ON id = other_id WHERE item_id = i.id) "+
		"SELECT * FROM items i WHERE i.type IN (?) AND "+
		strings.Join(query, " AND "), params...).Rows()
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

func (f filesDir) getTags() []string {
	result := strings.Split(f.tags, string(os.PathSeparator))
	if result[len(result)-1] == "@" || result[len(result)-1] == "@@" {
		result = result[:len(result)-1]
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
	var tags = make([]item, len(activeTagNames))
	for i := range activeTagNames {
		db.First(&tags[i], "name = ?", activeTagNames[i])
	}
	var newItem = item{Name: req.Name, Type: file}
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
		path, err := filePath(uint64(i.ID))
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
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
	tags := tagsItems(target.getTags())
	from, err := filePath(uint64(srcItem.ID))
	if err != nil {
		return err
	}
	srcItem.Name = req.NewName
	db.Save(&srcItem).Association("Items").Clear().Append(tags)
	to, err := filePath(uint64(srcItem.ID))
	if err != nil {
		return err
	}
	os.Rename(from, to)
	return nil
}
