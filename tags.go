package main

import (
	"context"
	"os"
	"path"
	"strings"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

type tagsDir struct {
	ID   id
	Tags string
}

func (t tagsDir) Attr(ctx context.Context, attr *fuse.Attr) error {
	attr.Inode = uint64(t.ID)
	attr.Mode = os.ModeDir | 0o755
	attr.Size = 4096
	attr.Uid = uid
	attr.Gid = gid
	return nil
}

func addItems(items map[string]item, newItems []item) {
	for i := range newItems {
		items[newItems[i].Name] = newItems[i]
	}
}

func (t tagsDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	var result = emptyDir()
	activeTagNames := strings.Split(t.Tags, string(os.PathSeparator))
	items := make(map[string]item)
	if t.Tags == "" {
		var baseItems []item
		if err := db.Find(&baseItems, "parent_id = 0 AND type = ?", tag).Error; err != nil {
			return nil, err
		}
		addItems(items, baseItems)
	}
	for _, activeTagName := range activeTagNames {
		var activeItem item
		if !db.First(&activeItem, "name = ?", activeTagName).RecordNotFound() {
			var childItems, groupTags, childGroupItems []item
			if err := db.Find(&childItems, "parent_id = ? AND name NOT IN (?) AND type = ?", activeItem.ID, activeTagNames, tag).Error; err != nil {
				return nil, err
			}
			addItems(items, childItems)
			if err := db.Model(&activeItem).
				Where("name NOT IN (?) AND type = ?", activeTagNames, grouptag).
				Related(&groupTags, "Items").Error; err != nil {
				return nil, err
			}
			childGroupIDs := make([]id, len(groupTags))
			for i := range groupTags {
				childGroupIDs[i] = groupTags[i].ID
			}
			db.Find(&childGroupItems, "parent_id IN (?) AND name NOT IN (?)", childGroupIDs, activeTagNames)
			addItems(items, childGroupItems)
		}
	}
	for _, v := range items {
		result = append(result, fuse.Dirent{Inode: uint64(v.ID), Name: v.Name, Type: fuse.DT_Dir})
	}
	return result, nil
}

func (t tagsDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	var result item
	if !db.First(&result, "name = ?", name).RecordNotFound() {
		return tagsDir{ID: result.ID, Tags: path.Join(t.Tags, result.Name)}, nil
	}
	return nil, syscall.ENOENT
}

func (t tagsDir) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	return nil, nil, syscall.EACCES
}
