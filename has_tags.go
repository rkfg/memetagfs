package main

import (
	"os"
	"strings"
)

type hasTags struct {
	tags string
}

func (h hasTags) getAllTags() []string {
	result := strings.Split(h.tags, string(os.PathSeparator))
	if result[len(result)-1] == contentTag || result[len(result)-1] == renameReceiverTag {
		result = result[:len(result)-1]
	}
	return result
}

func (h hasTags) getTagsWithNegative() (positive []string, negative []string) {
	allTags := h.getAllTags()
	nextIsNegative := false
	for _, tag := range allTags {
		if nextIsNegative {
			if tag != "" {
				negative = append(negative, tag)
			}
			nextIsNegative = false
		} else {
			if tag == negativeTag {
				nextIsNegative = true
			} else {
				if tag != "" {
					positive = append(positive, tag)
				}
			}
		}
	}
	return
}

func (h hasTags) getTags() []string {
	result, _ := h.getTagsWithNegative()
	return result
}
