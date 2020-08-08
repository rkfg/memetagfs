package main

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/jinzhu/gorm"
)

type controlDir struct {
	ID id
}

var relatedRegexp = regexp.MustCompile(`(^[^\[]*) \[(.*)\]$`)

func parseName(i *item) (related []string, err error) {
	filteredName := i.Name
	if strings.HasPrefix(filteredName, "!") {
		filteredName = filteredName[1:]
		i.Type = grouptag
	} else {
		i.Type = tag
	}
	matches := relatedRegexp.FindStringSubmatch(filteredName)
	if matches != nil {
		filteredName = matches[1]
		related = strings.Split(matches[2], ",")
	} else {
		if strings.ContainsAny(filteredName, "[]") {
			return nil, syscall.EINVAL
		}
	}
	i.Name = filteredName
	return
}

func updateRelated(db *gorm.DB, i *item, related []string) error {
	model := db.Model(i).Association("Items").Clear()
	if len(related) > 0 {
		var otherTags []item
		if err := db.Find(&otherTags, "name IN (?)", related).Error; err != nil {
			return err
		}
		model.Append(otherTags)
	}
	return nil
}

func basetag(s string) string {
	itemName := strings.TrimPrefix(s, "!")
	matches := relatedRegexp.FindStringSubmatch(itemName)
	if matches != nil {
		itemName = matches[1]
	}
	return itemName
}

func (c controlDir) Attr(ctx context.Context, attr *fuse.Attr) error {
	attr.Inode = uint64(c.ID)
	attr.Mode = os.ModeDir | 0o755
	attr.Size = 4096
	attr.Uid = uid
	attr.Gid = gid
	return nil
}

func (c controlDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	result := emptyDir()
	var items []item
	if err := db.Find(&items, "parent_id = ?", c.ID).Error; err != nil {
		return nil, err
	}
	for _, v := range items {
		name := v.Name
		if v.Type == grouptag {
			name = "!" + name
		}
		var related []item
		db.Model(&v).Related(&related, "Items")
		if len(related) > 0 {
			names := make([]string, len(related))
			for i := range related {
				names[i] = related[i].Name
			}
			name += " [" + strings.Join(names, ",") + "]"
		}
		result = append(result, fuse.Dirent{Inode: uint64(v.ID), Name: name, Type: fuse.DT_Dir})
	}
	return result, nil
}

func compareRelated(items []item, related []string) bool {
	if len(items) != len(related) {
		return false
	}
	for i := range items {
		if items[i].Name != related[i] {
			return false
		}
	}
	return true
}

func (c controlDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	var src, result item
	src.Name = name
	related, err := parseName(&src)
	sort.Strings(related)
	if err != nil {
		return nil, err
	}
	if !db.First(&result, "parent_id = ? AND name = ?", c.ID, src.Name).RecordNotFound() {
		if result.Type == src.Type {
			var relatedExisting []item
			db.Model(&result).Order("name ASC").Related(&relatedExisting, "Items")
			if compareRelated(relatedExisting, related) {
				return controlDir{result.ID}, nil
			}
		}
	}
	return nil, syscall.ENOENT
}

func (c controlDir) Mkdir(ctx context.Context, req *fuse.MkdirRequest) (fs.Node, error) {
	newItem := item{Name: req.Name, Type: tag, ParentID: c.ID}
	related, err := parseName(&newItem)
	if err != nil {
		return nil, err
	}
	if !db.First(&item{}, "name = ?", newItem.Name).RecordNotFound() {
		return nil, syscall.EEXIST
	}
	if err := db.Create(&newItem).Error; err != nil {
		return nil, err
	}
	if err := updateRelated(db, &newItem, related); err != nil {
		return nil, err
	}
	return controlDir{ID: newItem.ID}, nil
}

func (c controlDir) Remove(ctx context.Context, req *fuse.RemoveRequest) error {
	var target item
	if db.First(&target, "name = ?", basetag(req.Name), c.ID).RecordNotFound() {
		return syscall.ENOENT
	}
	if !db.First(&item{}, "parent_id = ?", target.ID).RecordNotFound() {
		return syscall.ENOTEMPTY
	}
	db.Delete(&item{}, "id = ?", target.ID)
	return nil
}

func (c controlDir) Rename(ctx context.Context, req *fuse.RenameRequest, newDir fs.Node) error {
	targetDir, ok := newDir.(controlDir)
	if !ok {
		return syscall.EINVAL
	}
	var src item
	if db.First(&src, "name = ? AND parent_id = ?", basetag(req.OldName), c.ID).RecordNotFound() {
		return syscall.ENOENT
	}
	src.ParentID = targetDir.ID
	src.Name = req.NewName
	related, err := parseName(&src)
	if err != nil {
		return err
	}
	tx := db.Begin()
	defer tx.RollbackUnlessCommitted()
	if err := updateRelated(tx, &src, related); err != nil {
		return err
	}
	tx.Save(&src)
	tx.Commit()
	return nil
}

func (c controlDir) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	return nil, nil, syscall.EACCES
}
