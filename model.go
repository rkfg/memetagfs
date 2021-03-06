package main

import "bazil.org/fuse"

type id uint64

type item struct {
	ID       id
	Name     string   `gorm:"index"`
	Type     itemType `gorm:"index"`
	ParentID id       `gorm:"index"`
	Items    []*item  `gorm:"many2many:item_tags;association_jointable_foreignkey:other_id"`
	Tag      string   `gorm:"-"`
	missing  bool
	tags     []string
}

func (i *item) fuseType() fuse.DirentType {
	switch i.Type {
	case dir, tag, grouptag:
		return fuse.DT_Dir
	case file:
		return fuse.DT_File
	}
	return fuse.DT_Unknown
}

func (i *item) toDirent() fuse.Dirent {
	return fuse.Dirent{Inode: uint64(i.ID), Name: i.Name, Type: i.fuseType()}
}

type itemType uint

const (
	file itemType = iota
	dir
	tag
	grouptag
)
