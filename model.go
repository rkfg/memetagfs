package main

import "bazil.org/fuse"

type id uint64

type item struct {
	ID       id
	Name     string   `gorm:"index"`
	Type     itemType `gorm:"index"`
	ParentID id       `gorm:"index"`
	Items    []*item  `gorm:"many2many:item_tags;association_jointable_foreignkey:other_id"`
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

type itemType uint

const (
	file itemType = iota
	dir
	tag
	grouptag
)
