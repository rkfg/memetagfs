package main

type id uint64

type item struct {
	ID       id
	Name     string
	Type     itemType
	ParentID id
	Items    []*item `gorm:"many2many:item_tags;association_jointable_foreignkey:other_id"`
}

type itemType uint

const (
	file itemType = iota
	dir
	tag
	grouptag
)
