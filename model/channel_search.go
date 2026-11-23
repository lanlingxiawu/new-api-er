package model

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type ChannelSearchOptions struct {
	Keyword, Group, Model, LimitFilter string
	Status, Type, Page, PageSize       int
	TagMode                            bool
	StatDate                           int64
	Sort                               ChannelSortOptions
}

// SearchChannelPage keeps tag expansion and pre-type-filter counts, while only
// loading full channel records for the requested page. Contains searches and
// exact counts still inspect matching candidates within the query budget.
func SearchChannelPage(ctx context.Context, options ChannelSearchOptions) ([]*Channel, int64, map[int64]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db := DB.WithContext(ctx)
	channels := make([]*Channel, 0)
	typeCounts := make(map[int64]int64)
	var tags []*string
	query := channelSearchQuery(db, options.Keyword, options.Group, options.Model)
	if options.TagMode {
		var err error
		tags, err = searchTags(db, options.Keyword, options.Group, options.Model, options.Sort.IDSort)
		if err != nil || len(tags) == 0 {
			return channels, 0, typeCounts, err
		}
		// A matching tag expands to all channels in that tag, retaining the
		// group/limit/status restrictions, as in the original search API.
		query = ApplyChannelGroupFilter(db.Model(&Channel{}), options.Group).Where("tag IN ?", tags)
	}
	query = ApplyChannelLimitFilter(query, options.LimitFilter, options.StatDate)
	switch options.Status {
	case common.ChannelStatusEnabled:
		query = query.Where("status = ?", common.ChannelStatusEnabled)
	case 0:
		query = query.Where("status <> ? OR status IS NULL", common.ChannelStatusEnabled)
	}
	var counts []struct {
		Tag   string
		Type  int64
		Count int64
	}
	countQuery := query.Session(&gorm.Session{}).Select("type, count(*) AS count").Group("type")
	if options.TagMode {
		countQuery = countQuery.Select("tag, type, count(*) AS count").Group("tag")
	}
	if err := countQuery.Scan(&counts).Error; err != nil {
		return nil, 0, nil, err
	}
	var total int64
	tagCounts := make(map[string]int64)
	for _, count := range counts {
		typeCounts[count.Type] += count.Count
		if options.Type < 0 || count.Type == int64(options.Type) {
			total += count.Count
			tagCounts[count.Tag] += count.Count
		}
	}
	pageSize := options.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	pageSize = min(pageSize, 100)
	page := max(options.Page, 1)
	// Check before multiplying so arbitrarily large page numbers cannot wrap.
	if int64(page-1) > total/int64(pageSize) {
		return channels, total, typeCounts, nil
	}
	offset := (page - 1) * pageSize
	if options.Type == 0 {
		query = query.Where("type = 0 OR type IS NULL")
	} else if options.Type > 0 {
		query = query.Where("type = ?", options.Type)
	}
	query = query.Omit("key")
	if !options.TagMode {
		err := options.Sort.Apply(query).Offset(offset).Limit(pageSize).Find(&channels).Error
		return channels, total, typeCounts, err
	}
	for _, tag := range tags {
		if tag == nil || *tag == "" {
			continue
		}
		count := tagCounts[*tag]
		if int64(offset) >= count {
			offset -= int(count)
			continue
		}
		var part []*Channel
		pageQuery := query.Session(&gorm.Session{}).Where("tag = ?", *tag)
		if err := options.Sort.Apply(pageQuery).Offset(offset).Limit(pageSize - len(channels)).Find(&part).Error; err != nil {
			return nil, 0, nil, err
		}
		channels = append(channels, part...)
		offset = 0
		if len(channels) == pageSize {
			break
		}
	}
	return channels, total, typeCounts, nil
}
