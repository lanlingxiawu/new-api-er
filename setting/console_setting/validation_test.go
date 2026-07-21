package console_setting

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// parseJSONArray
// ---------------------------------------------------------------------------

func TestParseJSONArray_Valid(t *testing.T) {
	list, err := parseJSONArray(`[{"a":"b"}]`, "X")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "b", list[0]["a"])
}

func TestParseJSONArray_InvalidJSON(t *testing.T) {
	list, err := parseJSONArray(`not-json`, "测试类型")
	require.Error(t, err)
	assert.Nil(t, list)
	assert.Contains(t, err.Error(), "测试类型格式错误")
}

// ---------------------------------------------------------------------------
// validateURL
// ---------------------------------------------------------------------------

func TestValidateURL_Valid(t *testing.T) {
	assert.NoError(t, validateURL("https://example.com/path", 1, "项"))
	assert.NoError(t, validateURL("http://192.168.0.1:8080", 1, "项"))
	assert.NoError(t, validateURL("http://a.b.c.example.co/x?y=z", 1, "项"))
}

func TestValidateURL_RegexReject(t *testing.T) {
	// ftp scheme not allowed by the regex.
	err := validateURL("ftp://example.com", 3, "项")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "第3个项的URL格式不正确")
}

func TestValidateURL_ParseErrorAfterRegex(t *testing.T) {
	// Passes the regex (http:// + /path) but fails url.Parse due to an invalid
	// percent-escape in the path — exercises the second guard.
	err := validateURL("http://example.com/%zz", 2, "项")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "第2个项的URL无法解析")
}

// ---------------------------------------------------------------------------
// checkDangerousContent
// ---------------------------------------------------------------------------

func TestCheckDangerousContent_Clean(t *testing.T) {
	assert.NoError(t, checkDangerousContent("perfectly safe text", 1, "项"))
}

func TestCheckDangerousContent_EachPattern(t *testing.T) {
	patterns := []string{"<script", "<iframe", "javascript:", "onload=", "onerror=", "onclick="}
	for _, p := range patterns {
		// Case-insensitive: uppercase must still trip the check.
		content := "prefix " + strings.ToUpper(p) + " suffix"
		err := checkDangerousContent(content, 7, "项")
		require.Error(t, err, "pattern %q should be rejected", p)
		assert.Contains(t, err.Error(), "第7个项包含不允许的内容")
	}
}

// ---------------------------------------------------------------------------
// getJSONList
// ---------------------------------------------------------------------------

func TestGetJSONList_EmptyString(t *testing.T) {
	list := getJSONList("")
	assert.NotNil(t, list)
	assert.Empty(t, list)
}

func TestGetJSONList_Valid(t *testing.T) {
	list := getJSONList(`[{"k":"v"},{"k":"w"}]`)
	require.Len(t, list, 2)
	assert.Equal(t, "v", list[0]["k"])
}

func TestGetJSONList_InvalidJSONIgnored(t *testing.T) {
	// Parse error is swallowed; result is whatever Unmarshal produced (nil-ish).
	list := getJSONList(`{bad}`)
	assert.Empty(t, list)
}

// ---------------------------------------------------------------------------
// ValidateConsoleSettings — switch dispatch (decision coverage)
// ---------------------------------------------------------------------------

func TestValidateConsoleSettings_EmptyReturnsNil(t *testing.T) {
	assert.NoError(t, ValidateConsoleSettings("", "ApiInfo"))
	assert.NoError(t, ValidateConsoleSettings("", "AnythingUnknown"))
}

func TestValidateConsoleSettings_DispatchesEachType(t *testing.T) {
	// Each known type routes to its validator; use a minimal valid payload.
	assert.NoError(t, ValidateConsoleSettings(`[]`, "ApiInfo"))
	assert.NoError(t, ValidateConsoleSettings(`[]`, "Announcements"))
	assert.NoError(t, ValidateConsoleSettings(`[]`, "FAQ"))
	assert.NoError(t, ValidateConsoleSettings(`[]`, "UptimeKumaGroups"))
}

func TestValidateConsoleSettings_UnknownType(t *testing.T) {
	err := ValidateConsoleSettings(`[]`, "Nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未知的设置类型：Nope")
}

func TestValidateConsoleSettings_RoutedValidatorErrorPropagates(t *testing.T) {
	err := ValidateConsoleSettings(`bad-json`, "ApiInfo")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// validateApiInfo — every branch
// ---------------------------------------------------------------------------

func validApiInfoItem() map[string]interface{} {
	return map[string]interface{}{
		"url":         "https://example.com",
		"route":       "line-a",
		"description": "desc",
		"color":       "blue",
	}
}

func TestValidateApiInfo_Valid(t *testing.T) {
	assert.NoError(t, validateApiInfo(`[{"url":"https://example.com","route":"r","description":"d","color":"green"}]`))
}

func TestValidateApiInfo_ParseError(t *testing.T) {
	require.Error(t, validateApiInfo(`nope`))
}

func TestValidateApiInfo_TooMany(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < 51; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"url":"https://e.com","route":"r","description":"d","color":"blue"}`)
	}
	sb.WriteString("]")
	err := validateApiInfo(sb.String())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不能超过50个")
}

func TestValidateApiInfo_MissingFields(t *testing.T) {
	cases := []struct {
		drop string
		want string
	}{
		{"url", "缺少URL字段"},
		{"route", "缺少线路描述字段"},
		{"description", "缺少说明字段"},
		{"color", "缺少颜色字段"},
	}
	for _, c := range cases {
		item := validApiInfoItem()
		delete(item, c.drop)
		err := validateApiInfoOne(t, item)
		require.Error(t, err, "dropping %s", c.drop)
		assert.Contains(t, err.Error(), c.want)
	}
}

func TestValidateApiInfo_EmptyStringFieldTreatedAsMissing(t *testing.T) {
	item := validApiInfoItem()
	item["url"] = ""
	err := validateApiInfoOne(t, item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少URL字段")
}

func TestValidateApiInfo_WrongTypeFieldTreatedAsMissing(t *testing.T) {
	item := validApiInfoItem()
	item["route"] = 123 // not a string
	err := validateApiInfoOne(t, item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少线路描述字段")
}

func TestValidateApiInfo_InvalidURL(t *testing.T) {
	item := validApiInfoItem()
	item["url"] = "ftp://bad"
	err := validateApiInfoOne(t, item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "URL格式不正确")
}

func TestValidateApiInfo_URLTooLong(t *testing.T) {
	item := validApiInfoItem()
	item["url"] = "https://example.com/" + strings.Repeat("a", 500)
	err := validateApiInfoOne(t, item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "URL长度不能超过500字符")
}

func TestValidateApiInfo_RouteTooLong(t *testing.T) {
	item := validApiInfoItem()
	item["route"] = strings.Repeat("r", 101)
	err := validateApiInfoOne(t, item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "线路描述长度不能超过100字符")
}

func TestValidateApiInfo_DescriptionTooLong(t *testing.T) {
	item := validApiInfoItem()
	item["description"] = strings.Repeat("d", 201)
	err := validateApiInfoOne(t, item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "说明长度不能超过200字符")
}

func TestValidateApiInfo_InvalidColor(t *testing.T) {
	item := validApiInfoItem()
	item["color"] = "chartreuse"
	err := validateApiInfoOne(t, item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "颜色值不合法")
}

func TestValidateApiInfo_AllValidColorsAccepted(t *testing.T) {
	for color := range validColors {
		item := validApiInfoItem()
		item["color"] = color
		assert.NoError(t, validateApiInfoOne(t, item), "color %s", color)
	}
}

func TestValidateApiInfo_DangerousDescription(t *testing.T) {
	item := validApiInfoItem()
	item["description"] = "<script>alert(1)</script>"
	err := validateApiInfoOne(t, item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "包含不允许的内容")
}

func TestValidateApiInfo_DangerousRoute(t *testing.T) {
	item := validApiInfoItem()
	item["route"] = "javascript:evil"
	err := validateApiInfoOne(t, item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "包含不允许的内容")
}

func TestValidateApiInfo_BoundaryLengthsAccepted(t *testing.T) {
	item := validApiInfoItem()
	item["url"] = "https://e.com/" + strings.Repeat("a", 500-len("https://e.com/"))
	item["route"] = strings.Repeat("r", 100)
	item["description"] = strings.Repeat("d", 200)
	require.Len(t, item["url"], 500)
	assert.NoError(t, validateApiInfoOne(t, item))
}

// validateApiInfoOne marshals a single item into a one-element array and runs
// validateApiInfo, returning the error for the first (and only) element.
func validateApiInfoOne(t *testing.T, item map[string]interface{}) error {
	t.Helper()
	return validateApiInfo(marshalArray(t, item))
}

// ---------------------------------------------------------------------------
// GetApiInfo
// ---------------------------------------------------------------------------

func TestGetApiInfo(t *testing.T) {
	saveConsoleSetting(t)
	consoleSetting.ApiInfo = `[{"url":"https://e.com"}]`
	list := GetApiInfo()
	require.Len(t, list, 1)
	assert.Equal(t, "https://e.com", list[0]["url"])
}

func TestGetApiInfo_Empty(t *testing.T) {
	saveConsoleSetting(t)
	consoleSetting.ApiInfo = ""
	assert.Empty(t, GetApiInfo())
}

// ---------------------------------------------------------------------------
// validateAnnouncements — every branch
// ---------------------------------------------------------------------------

func validAnnouncement() map[string]interface{} {
	return map[string]interface{}{
		"content":     "hello",
		"publishDate": "2026-07-20T00:00:00Z",
	}
}

func TestValidateAnnouncements_Valid(t *testing.T) {
	item := validAnnouncement()
	item["type"] = "success"
	item["extra"] = "note"
	assert.NoError(t, validateAnnouncements(marshalArray(t, item)))
}

func TestValidateAnnouncements_ParseError(t *testing.T) {
	require.Error(t, validateAnnouncements(`nope`))
}

func TestValidateAnnouncements_TooMany(t *testing.T) {
	items := make([]map[string]interface{}, 101)
	for i := range items {
		items[i] = validAnnouncement()
	}
	err := validateAnnouncements(marshalArray(t, items))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不能超过100个")
}

func TestValidateAnnouncements_MissingContent(t *testing.T) {
	item := validAnnouncement()
	delete(item, "content")
	err := validateAnnouncements(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少内容字段")
}

func TestValidateAnnouncements_MissingPublishDate(t *testing.T) {
	item := validAnnouncement()
	delete(item, "publishDate")
	err := validateAnnouncements(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少发布日期字段")
}

func TestValidateAnnouncements_PublishDateNotStringOrEmpty(t *testing.T) {
	// present but not a string
	item := validAnnouncement()
	item["publishDate"] = 123
	err := validateAnnouncements(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "发布日期不能为空")

	// present, string, but empty
	item2 := validAnnouncement()
	item2["publishDate"] = ""
	err2 := validateAnnouncements(marshalArray(t, item2))
	require.Error(t, err2)
	assert.Contains(t, err2.Error(), "发布日期不能为空")
}

func TestValidateAnnouncements_PublishDateBadFormat(t *testing.T) {
	item := validAnnouncement()
	item["publishDate"] = "2026/07/20"
	err := validateAnnouncements(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "发布日期格式错误")
}

func TestValidateAnnouncements_InvalidType(t *testing.T) {
	item := validAnnouncement()
	item["type"] = "bogus"
	err := validateAnnouncements(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "类型值不合法")
}

func TestValidateAnnouncements_NonStringTypeIgnored(t *testing.T) {
	// type present but not a string => inner `if ok` is false, no validation.
	item := validAnnouncement()
	item["type"] = 5
	assert.NoError(t, validateAnnouncements(marshalArray(t, item)))
}

func TestValidateAnnouncements_ContentTooLong(t *testing.T) {
	item := validAnnouncement()
	item["content"] = strings.Repeat("c", 501)
	err := validateAnnouncements(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "内容长度不能超过500字符")
}

func TestValidateAnnouncements_ExtraTooLong(t *testing.T) {
	item := validAnnouncement()
	item["extra"] = strings.Repeat("e", 201)
	err := validateAnnouncements(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "说明长度不能超过200字符")
}

func TestValidateAnnouncements_ExtraNonStringIgnored(t *testing.T) {
	item := validAnnouncement()
	item["extra"] = 999
	assert.NoError(t, validateAnnouncements(marshalArray(t, item)))
}

func TestValidateAnnouncements_AllValidTypes(t *testing.T) {
	for _, ty := range []string{"default", "ongoing", "success", "warning", "error"} {
		item := validAnnouncement()
		item["type"] = ty
		assert.NoError(t, validateAnnouncements(marshalArray(t, item)), "type %s", ty)
	}
}

// ---------------------------------------------------------------------------
// validateFAQ
// ---------------------------------------------------------------------------

func validFAQ() map[string]interface{} {
	return map[string]interface{}{"question": "q?", "answer": "a."}
}

func TestValidateFAQ_Valid(t *testing.T) {
	assert.NoError(t, validateFAQ(marshalArray(t, validFAQ())))
}

func TestValidateFAQ_ParseError(t *testing.T) {
	require.Error(t, validateFAQ(`nope`))
}

func TestValidateFAQ_TooMany(t *testing.T) {
	items := make([]map[string]interface{}, 101)
	for i := range items {
		items[i] = validFAQ()
	}
	err := validateFAQ(marshalArray(t, items))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAQ数量不能超过100个")
}

func TestValidateFAQ_MissingQuestion(t *testing.T) {
	item := validFAQ()
	delete(item, "question")
	err := validateFAQ(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少问题字段")
}

func TestValidateFAQ_MissingAnswer(t *testing.T) {
	item := validFAQ()
	delete(item, "answer")
	err := validateFAQ(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少答案字段")
}

func TestValidateFAQ_QuestionTooLong(t *testing.T) {
	item := validFAQ()
	item["question"] = strings.Repeat("q", 201)
	err := validateFAQ(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "问题长度不能超过200字符")
}

func TestValidateFAQ_AnswerTooLong(t *testing.T) {
	item := validFAQ()
	item["answer"] = strings.Repeat("a", 1001)
	err := validateFAQ(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "答案长度不能超过1000字符")
}

// ---------------------------------------------------------------------------
// getPublishTime
// ---------------------------------------------------------------------------

func TestGetPublishTime_Valid(t *testing.T) {
	tm := getPublishTime(map[string]interface{}{"publishDate": "2026-07-20T10:00:00Z"})
	assert.False(t, tm.IsZero())
	assert.Equal(t, 2026, tm.Year())
}

func TestGetPublishTime_Missing(t *testing.T) {
	assert.True(t, getPublishTime(map[string]interface{}{}).IsZero())
}

func TestGetPublishTime_WrongType(t *testing.T) {
	assert.True(t, getPublishTime(map[string]interface{}{"publishDate": 5}).IsZero())
}

func TestGetPublishTime_BadFormat(t *testing.T) {
	assert.True(t, getPublishTime(map[string]interface{}{"publishDate": "yesterday"}).IsZero())
}

// ---------------------------------------------------------------------------
// GetAnnouncements — sorted descending by publishDate
// ---------------------------------------------------------------------------

func TestGetAnnouncements_SortedNewestFirst(t *testing.T) {
	saveConsoleSetting(t)
	consoleSetting.Announcements = `[
		{"content":"old","publishDate":"2020-01-01T00:00:00Z"},
		{"content":"new","publishDate":"2026-01-01T00:00:00Z"},
		{"content":"mid","publishDate":"2023-01-01T00:00:00Z"}
	]`
	list := GetAnnouncements()
	require.Len(t, list, 3)
	assert.Equal(t, "new", list[0]["content"])
	assert.Equal(t, "mid", list[1]["content"])
	assert.Equal(t, "old", list[2]["content"])
}

func TestGetAnnouncements_Empty(t *testing.T) {
	saveConsoleSetting(t)
	consoleSetting.Announcements = ""
	assert.Empty(t, GetAnnouncements())
}

// ---------------------------------------------------------------------------
// GetFAQ / GetUptimeKumaGroups
// ---------------------------------------------------------------------------

func TestGetFAQ(t *testing.T) {
	saveConsoleSetting(t)
	consoleSetting.FAQ = `[{"question":"q","answer":"a"}]`
	list := GetFAQ()
	require.Len(t, list, 1)
	assert.Equal(t, "q", list[0]["question"])
}

func TestGetUptimeKumaGroups(t *testing.T) {
	saveConsoleSetting(t)
	consoleSetting.UptimeKumaGroups = `[{"categoryName":"c","url":"https://e.com","slug":"s"}]`
	list := GetUptimeKumaGroups()
	require.Len(t, list, 1)
	assert.Equal(t, "c", list[0]["categoryName"])
}

// ---------------------------------------------------------------------------
// validateUptimeKumaGroups — every branch
// ---------------------------------------------------------------------------

func validGroup(slug string) map[string]interface{} {
	return map[string]interface{}{
		"categoryName": "cat-" + slug,
		"url":          "https://example.com",
		"slug":         slug,
		"description":  "desc",
	}
}

func TestValidateUptimeKumaGroups_Valid(t *testing.T) {
	assert.NoError(t, validateUptimeKumaGroups(marshalArray(t, validGroup("s1"))))
}

func TestValidateUptimeKumaGroups_ValidNoDescription(t *testing.T) {
	// description absent => defaults to "" and passes (covers the else branch).
	item := validGroup("s1")
	delete(item, "description")
	assert.NoError(t, validateUptimeKumaGroups(marshalArray(t, item)))
}

func TestValidateUptimeKumaGroups_ParseError(t *testing.T) {
	require.Error(t, validateUptimeKumaGroups(`nope`))
}

func TestValidateUptimeKumaGroups_TooMany(t *testing.T) {
	items := make([]map[string]interface{}, 21)
	for i := range items {
		items[i] = validGroup(string(rune('a'+i)))
	}
	err := validateUptimeKumaGroups(marshalArray(t, items))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不能超过20个")
}

func TestValidateUptimeKumaGroups_MissingCategoryName(t *testing.T) {
	item := validGroup("s1")
	delete(item, "categoryName")
	err := validateUptimeKumaGroups(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少分类名称字段")
}

func TestValidateUptimeKumaGroups_DuplicateName(t *testing.T) {
	g1 := validGroup("s1")
	g2 := validGroup("s2")
	g2["categoryName"] = g1["categoryName"] // force duplicate
	err := validateUptimeKumaGroups(marshalArray(t, []map[string]interface{}{g1, g2}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "分类名称与其他分组重复")
}

func TestValidateUptimeKumaGroups_MissingURL(t *testing.T) {
	item := validGroup("s1")
	delete(item, "url")
	err := validateUptimeKumaGroups(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少URL字段")
}

func TestValidateUptimeKumaGroups_MissingSlug(t *testing.T) {
	item := validGroup("s1")
	delete(item, "slug")
	err := validateUptimeKumaGroups(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少Slug字段")
}

func TestValidateUptimeKumaGroups_InvalidURL(t *testing.T) {
	item := validGroup("s1")
	item["url"] = "ftp://bad"
	err := validateUptimeKumaGroups(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "URL格式不正确")
}

func TestValidateUptimeKumaGroups_CategoryNameTooLong(t *testing.T) {
	item := validGroup("s1")
	item["categoryName"] = strings.Repeat("c", 51)
	err := validateUptimeKumaGroups(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "分类名称长度不能超过50字符")
}

func TestValidateUptimeKumaGroups_URLTooLong(t *testing.T) {
	item := validGroup("s1")
	item["url"] = "https://example.com/" + strings.Repeat("a", 500)
	err := validateUptimeKumaGroups(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "URL长度不能超过500字符")
}

func TestValidateUptimeKumaGroups_SlugTooLong(t *testing.T) {
	item := validGroup("s1")
	item["categoryName"] = "short-cat"
	item["slug"] = strings.Repeat("s", 101)
	err := validateUptimeKumaGroups(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Slug长度不能超过100字符")
}

func TestValidateUptimeKumaGroups_DescriptionTooLong(t *testing.T) {
	item := validGroup("s1")
	item["description"] = strings.Repeat("d", 201)
	err := validateUptimeKumaGroups(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "描述长度不能超过200字符")
}

func TestValidateUptimeKumaGroups_InvalidSlug(t *testing.T) {
	item := validGroup("bad slug!")
	err := validateUptimeKumaGroups(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Slug只能包含字母、数字、下划线和连字符")
}

func TestValidateUptimeKumaGroups_DangerousDescription(t *testing.T) {
	item := validGroup("s1")
	item["description"] = "<iframe src=x>"
	err := validateUptimeKumaGroups(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "包含不允许的内容")
}

func TestValidateUptimeKumaGroups_DangerousCategoryName(t *testing.T) {
	item := validGroup("s1")
	item["categoryName"] = "onerror=alert(1)"
	err := validateUptimeKumaGroups(marshalArray(t, item))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "包含不允许的内容")
}

func TestValidateUptimeKumaGroups_SlugBoundaryAccepted(t *testing.T) {
	item := validGroup("s1")
	item["categoryName"] = "short-cat"
	item["slug"] = strings.Repeat("s", 100)
	assert.NoError(t, validateUptimeKumaGroups(marshalArray(t, item)))
}
