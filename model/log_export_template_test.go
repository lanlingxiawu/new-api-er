package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkExportTemplate(t *testing.T, userID int, mut func(tpl *LogExportTemplate)) *LogExportTemplate {
	t.Helper()
	requireDB(t)
	tpl := &LogExportTemplate{
		UserId:     userID,
		Name:       uniq("tpl"),
		ColumnKeys: []string{"created_at", "quota"},
		Format:     LogExportFormatCSVGz,
		Opts:       LogExportOptions{CSVBOM: true, Header: true},
	}
	if mut != nil {
		mut(tpl)
	}
	require.NoError(t, CreateLogExportTemplate(tpl))
	id := tpl.Id
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Delete(&LogExportTemplate{}, id)
		}
	})
	return tpl
}

func TestLogExportTemplate_CreateAndHydrate(t *testing.T) {
	requireDB(t)
	user := nextTestID()
	tpl := mkExportTemplate(t, user, func(tpl *LogExportTemplate) {
		tpl.ColumnKeys = []string{"created_at", "username", "quota"}
		tpl.Opts = LogExportOptions{CSVBOM: false, Header: true, Timezone: "Asia/Shanghai"}
	})

	assert.NotZero(t, tpl.Id)
	assert.Equal(t, "common", tpl.LogCategory)
	assert.Equal(t, []string{"created_at", "username", "quota"}, tpl.ColumnKeys)

	loaded, err := GetLogExportTemplate(tpl.Id)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, []string{"created_at", "username", "quota"}, loaded.ColumnKeys)
	assert.Equal(t, "Asia/Shanghai", loaded.Opts.Timezone)
	assert.False(t, loaded.Opts.CSVBOM)
	assert.True(t, loaded.Opts.Header)
}

func TestLogExportTemplate_RejectsUnknownColumn(t *testing.T) {
	requireDB(t)
	tpl := &LogExportTemplate{
		UserId:     nextTestID(),
		Name:       uniq("bad"),
		ColumnKeys: []string{"created_at", "nope"},
	}
	err := CreateLogExportTemplate(tpl)
	var unknown *UnknownLogExportColumnError
	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, "nope", unknown.Key)
}

func TestLogExportTemplate_NameValidation(t *testing.T) {
	requireDB(t)
	user := nextTestID()

	err := CreateLogExportTemplate(&LogExportTemplate{UserId: user, Name: "   ", ColumnKeys: []string{"quota"}})
	assert.ErrorIs(t, err, ErrLogExportTemplateNameEmpty)

	long := make([]rune, logExportTemplateNameMaxLen+1)
	for i := range long {
		long[i] = 'a'
	}
	err = CreateLogExportTemplate(&LogExportTemplate{UserId: user, Name: string(long), ColumnKeys: []string{"quota"}})
	assert.ErrorIs(t, err, ErrLogExportTemplateNameLong)

	// 边界：恰好等于上限应通过。
	exact := make([]rune, logExportTemplateNameMaxLen)
	for i := range exact {
		exact[i] = 'b'
	}
	tpl := &LogExportTemplate{UserId: user, Name: string(exact), ColumnKeys: []string{"quota"}}
	require.NoError(t, CreateLogExportTemplate(tpl))
	t.Cleanup(func() { DB.Unscoped().Delete(&LogExportTemplate{}, tpl.Id) })
}

func TestLogExportTemplate_DuplicateNameRejected(t *testing.T) {
	requireDB(t)
	user := nextTestID()
	first := mkExportTemplate(t, user, nil)

	err := CreateLogExportTemplate(&LogExportTemplate{
		UserId: user, Name: first.Name, ColumnKeys: []string{"quota"},
	})
	assert.ErrorIs(t, err, ErrLogExportTemplateExists)

	// 不同用户可以重名。
	other := mkExportTemplate(t, nextTestID(), func(tpl *LogExportTemplate) { tpl.Name = first.Name })
	assert.NotZero(t, other.Id)
}

func TestLogExportTemplate_UpdateRenameConflict(t *testing.T) {
	requireDB(t)
	user := nextTestID()
	first := mkExportTemplate(t, user, nil)
	second := mkExportTemplate(t, user, nil)

	second.Name = first.Name
	assert.ErrorIs(t, UpdateLogExportTemplate(second), ErrLogExportTemplateExists)

	// 改回自己的名字（不与他人冲突）应成功。
	second.Name = uniq("renamed")
	second.ColumnKeys = []string{"created_at", "content"}
	require.NoError(t, UpdateLogExportTemplate(second))

	loaded, err := GetLogExportTemplate(second.Id)
	require.NoError(t, err)
	assert.Equal(t, []string{"created_at", "content"}, loaded.ColumnKeys)
}

func TestLogExportTemplate_LimitPerUser(t *testing.T) {
	requireDB(t)
	user := nextTestID()
	s := operation_setting.GetLogExportSetting()
	prev := s.MaxTemplatesPerUser
	s.MaxTemplatesPerUser = 2
	t.Cleanup(func() { s.MaxTemplatesPerUser = prev })

	mkExportTemplate(t, user, nil)
	mkExportTemplate(t, user, nil)

	err := CreateLogExportTemplate(&LogExportTemplate{
		UserId: user, Name: uniq("over"), ColumnKeys: []string{"quota"},
	})
	assert.ErrorIs(t, err, ErrLogExportTemplateLimit)
}

func TestLogExportTemplate_Visibility(t *testing.T) {
	requireDB(t)
	owner := nextTestID()
	stranger := nextTestID()

	own := mkExportTemplate(t, owner, nil)
	shared := mkExportTemplate(t, nextTestID(), func(tpl *LogExportTemplate) { tpl.IsShared = true })
	foreign := mkExportTemplate(t, nextTestID(), nil)

	list, err := ListLogExportTemplates(owner)
	require.NoError(t, err)
	ids := map[int]bool{}
	for _, tpl := range list {
		ids[tpl.Id] = true
	}
	assert.True(t, ids[own.Id], "own template must be visible")
	assert.True(t, ids[shared.Id], "shared template must be visible")
	assert.False(t, ids[foreign.Id], "other users' private templates must stay hidden")

	assert.True(t, CanReadLogExportTemplate(own, owner))
	assert.True(t, CanReadLogExportTemplate(shared, stranger))
	assert.False(t, CanReadLogExportTemplate(foreign, stranger))
}

func TestLogExportTemplate_WritePermission(t *testing.T) {
	owner := 101
	stranger := 202
	tpl := &LogExportTemplate{UserId: owner, IsShared: true}

	assert.True(t, CanWriteLogExportTemplate(tpl, owner, false))
	assert.False(t, CanWriteLogExportTemplate(tpl, stranger, false), "shared templates are read-only for non-owners")
	assert.True(t, CanWriteLogExportTemplate(tpl, stranger, true), "root can manage any template")
}

func TestLogExportTemplate_Delete(t *testing.T) {
	requireDB(t)
	tpl := mkExportTemplate(t, nextTestID(), nil)
	require.NoError(t, DeleteLogExportTemplate(tpl.Id))

	loaded, err := GetLogExportTemplate(tpl.Id)
	require.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestGetLogExportTemplate_MissingReturnsNil(t *testing.T) {
	requireDB(t)
	loaded, err := GetLogExportTemplate(nextTestID())
	require.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestLogExportTemplate_InvalidFormatFallsBackToCSV(t *testing.T) {
	requireDB(t)
	tpl := mkExportTemplate(t, nextTestID(), func(tpl *LogExportTemplate) { tpl.Format = "parquet" })
	assert.Equal(t, LogExportFormatCSVGz, tpl.Format)
}
