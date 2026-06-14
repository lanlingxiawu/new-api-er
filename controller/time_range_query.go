package controller

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type unixTimeRangeQuery struct {
	StartTime int64
	EndTime   int64
}

func parseUnixTimeRangeQuery(c *gin.Context) (unixTimeRangeQuery, error) {
	parse := func(key string) (int64, error) {
		raw := strings.TrimSpace(c.Query(key))
		if raw == "" {
			return 0, nil
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			return 0, fmt.Errorf("invalid %s", key)
		}
		return value, nil
	}

	startTime, err := parse("start_time")
	if err != nil {
		return unixTimeRangeQuery{}, err
	}
	endTime, err := parse("end_time")
	if err != nil {
		return unixTimeRangeQuery{}, err
	}
	if startTime != 0 && endTime != 0 && startTime > endTime {
		return unixTimeRangeQuery{}, errors.New("start_time cannot be greater than end_time")
	}
	return unixTimeRangeQuery{StartTime: startTime, EndTime: endTime}, nil
}

func parseOptionalInt64Query(c *gin.Context, key string) (int64, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return value, nil
}
