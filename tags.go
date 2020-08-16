package main

import (
	"context"
	"os"
	"path"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

type tagsDir struct {
	hasTags
}

func (t tagsDir) Attr(ctx context.Context, attr *fuse.Attr) error {
	attr.Mode = os.ModeDir | 0755
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
	positiveTagNames, negativeTagNames := t.getTagsWithNegative()
	excludeTagNames := append(positiveTagNames, negativeTagNames...)
	items := make(map[string]item)
	if t.tags == "" {
		var baseItems []item
		if err := db.Find(&baseItems, "parent_id = 0 AND type = ?", tag).Error; err != nil {
			return nil, err
		}
		addItems(items, baseItems)
	}
	for _, activeTagName := range positiveTagNames {
		var activeItem item
		if !db.First(&activeItem, "name = ?", activeTagName).RecordNotFound() {
			var childItems, groupTags, childGroupItems []item
			if err := db.Find(&childItems, "parent_id = ? AND name NOT IN (?) AND type = ?", activeItem.ID, excludeTagNames, tag).Error; err != nil {
				return nil, err
			}
			addItems(items, childItems)
			if err := db.Model(&activeItem).
				Where("name NOT IN (?) AND type = ?", excludeTagNames, grouptag).
				Related(&groupTags, "Items").Error; err != nil {
				return nil, err
			}
			childGroupIDs := make([]id, len(groupTags))
			for i := range groupTags {
				childGroupIDs[i] = groupTags[i].ID
			}
			db.Find(&childGroupItems, "parent_id IN (?) AND name NOT IN (?)", childGroupIDs, excludeTagNames)
			addItems(items, childGroupItems)
		}
	}
	for _, v := range items {
		result = append(result, fuse.Dirent{Inode: uint64(v.ID), Name: v.Name, Type: fuse.DT_Dir})
	}
	if path.Base(t.tags) != negativeTag {
		result = append(result,
			fuse.Dirent{Name: contentTag, Type: fuse.DT_Dir},
			fuse.Dirent{Name: renameReceiverTag, Type: fuse.DT_Dir},
			fuse.Dirent{Name: negativeTag, Type: fuse.DT_Dir})
	}
	return result, nil
}

func (t tagsDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	switch name {
	case contentTag:
		return filesDir{hasTags: hasTags{tags: t.tags}}, nil
	case renameReceiverTag:
		return filesDir{hasTags: hasTags{tags: t.tags}, renameReceiver: true}, nil
	case negativeTag:
		return tagsDir{hasTags: hasTags{tags: path.Join(t.tags, negativeTag)}}, nil
	}
	var result item
	if !db.First(&result, "name = ?", name).RecordNotFound() {
		return tagsDir{hasTags: hasTags{tags: path.Join(t.tags, result.Name)}}, nil
	}
	return nil, syscall.ENOENT
}

func (t tagsDir) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	return nil, nil, syscall.EACCES
}

func (t tagsDir) Remove(ctx context.Context, req *fuse.RemoveRequest) error {
	return syscall.EPERM
}

func (t tagsDir) Rename(ctx context.Context, req *fuse.RenameRequest, newDir fs.Node) error {
	return syscall.EPERM
}
