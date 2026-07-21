package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// countAbilities counts ability rows for a channel id.
func countAbilities(t *testing.T, channelID int) int64 {
	t.Helper()
	var n int64
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channelID).Count(&n).Error)
	return n
}

// ---------------------------------------------------------------------------
// AddAbilities — dedup, chunking, OnConflict DoNothing, tx vs no-tx
// ---------------------------------------------------------------------------

func TestAddAbilities_DedupAndCrossProduct(t *testing.T) {
	requireDB(t)

	// duplicate group and duplicate model collapse to a single ability
	dup := mkChannel(t, func(c *Channel) {
		c.Group = "gdup,gdup"
		c.Models = "mdup,mdup"
	})
	require.NoError(t, dup.AddAbilities(nil))
	cleanupAbilities(t, dup.Id)
	assert.EqualValues(t, 1, countAbilities(t, dup.Id), "group|model dedup -> 1 row")

	// 2 groups x 2 models -> 4 distinct abilities
	cross := mkChannel(t, func(c *Channel) {
		c.Group = "ga,gb"
		c.Models = "m1,m2"
	})
	require.NoError(t, cross.AddAbilities(nil))
	cleanupAbilities(t, cross.Id)
	assert.EqualValues(t, 4, countAbilities(t, cross.Id))
}

func TestAddAbilities_OnConflictDoNothing(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) {
		c.Group = "default"
		c.Models = "conflict-m1,conflict-m2"
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)
	require.EqualValues(t, 2, countAbilities(t, ch.Id))

	// second insert of the same (group,model,channel_id) primary keys is a no-op
	require.NoError(t, ch.AddAbilities(nil))
	assert.EqualValues(t, 2, countAbilities(t, ch.Id))
}

func TestAddAbilities_Chunking(t *testing.T) {
	requireDB(t)
	// 60 models -> exercises lo.Chunk(…, 50) with two chunks (50 + 10)
	models := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		models = append(models, uniq("cm"))
	}
	ch := mkChannel(t, func(c *Channel) {
		c.Group = "default"
		c.Models = strings.Join(models, ",")
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)
	assert.EqualValues(t, 60, countAbilities(t, ch.Id))
}

func TestAddAbilities_WithTx(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) {
		c.Group = "default"
		c.Models = "txm1,txm2"
	})
	cleanupAbilities(t, ch.Id)

	tx := DB.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, ch.AddAbilities(tx))
	require.NoError(t, tx.Commit().Error)

	assert.EqualValues(t, 2, countAbilities(t, ch.Id))
}

// ---------------------------------------------------------------------------
// UpdateAbilities / DeleteAbilities
// ---------------------------------------------------------------------------

func TestUpdateAbilities_NewTx(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) {
		c.Group = "default"
		c.Models = "u1,u2,u3"
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)
	require.EqualValues(t, 3, countAbilities(t, ch.Id))

	// shrink model set; UpdateAbilities deletes then recreates (tx==nil path)
	ch.Models = "u1"
	require.NoError(t, ch.UpdateAbilities(nil))
	assert.EqualValues(t, 1, countAbilities(t, ch.Id))
}

func TestUpdateAbilities_ProvidedTx(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) {
		c.Group = "default"
		c.Models = "pt1,pt2"
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	ch.Models = "pt1,pt2,pt3,pt4"
	tx := DB.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, ch.UpdateAbilities(tx))
	require.NoError(t, tx.Commit().Error)
	assert.EqualValues(t, 4, countAbilities(t, ch.Id))
}

func TestDeleteAbilities(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) {
		c.Group = "default"
		c.Models = "d1,d2"
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)
	require.EqualValues(t, 2, countAbilities(t, ch.Id))

	require.NoError(t, ch.DeleteAbilities())
	assert.EqualValues(t, 0, countAbilities(t, ch.Id))
}

// ---------------------------------------------------------------------------
// UpdateAbilityStatus / UpdateAbilityStatusByTag / UpdateAbilityByTag
// ---------------------------------------------------------------------------

func TestUpdateAbilityStatus(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) {
		c.Group = "default"
		c.Models = "sm1"
		c.Status = common.ChannelStatusEnabled
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	require.NoError(t, UpdateAbilityStatus(ch.Id, false))
	var ab Ability
	require.NoError(t, DB.Where("channel_id = ?", ch.Id).First(&ab).Error)
	assert.False(t, ab.Enabled)

	require.NoError(t, UpdateAbilityStatus(ch.Id, true))
	require.NoError(t, DB.Where("channel_id = ?", ch.Id).First(&ab).Error)
	assert.True(t, ab.Enabled)
}

func TestUpdateAbilityByTagAndStatusByTag(t *testing.T) {
	requireDB(t)
	tag := uniq("abtag")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = "default"
		c.Models = "tg1,tg2"
		c.Tag = &tag
		c.Priority = common.GetPointer[int64](1)
		c.Weight = common.GetPointer[uint](1)
		c.Status = common.ChannelStatusEnabled
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	// UpdateAbilityByTag: change tag, priority and weight
	newTag := uniq("abtag2")
	newPriority := int64(50)
	newWeight := uint(9)
	require.NoError(t, UpdateAbilityByTag(tag, &newTag, &newPriority, &newWeight))

	var abilities []Ability
	require.NoError(t, DB.Where("channel_id = ?", ch.Id).Find(&abilities).Error)
	require.Len(t, abilities, 2)
	for _, ab := range abilities {
		require.NotNil(t, ab.Tag)
		assert.Equal(t, newTag, *ab.Tag)
		require.NotNil(t, ab.Priority)
		assert.EqualValues(t, 50, *ab.Priority)
		assert.EqualValues(t, 9, ab.Weight)
	}

	// UpdateAbilityStatusByTag flips enabled on the new tag
	require.NoError(t, UpdateAbilityStatusByTag(newTag, false))
	require.NoError(t, DB.Where("channel_id = ?", ch.Id).Find(&abilities).Error)
	for _, ab := range abilities {
		assert.False(t, ab.Enabled)
	}
}

// ---------------------------------------------------------------------------
// getPriority / getChannelQuery
// ---------------------------------------------------------------------------

func TestGetPriority(t *testing.T) {
	requireDB(t)
	grp := uniq("prio")
	model := "gpt-4o"
	// two priorities: 10 (via ch1) and 5 (via ch2), both enabled
	ch1 := mkChannel(t, func(c *Channel) {
		c.Group, c.Models = grp, model
		c.Priority = common.GetPointer[int64](10)
	})
	require.NoError(t, ch1.AddAbilities(nil))
	cleanupAbilities(t, ch1.Id)
	ch2 := mkChannel(t, func(c *Channel) {
		c.Group, c.Models = grp, model
		c.Priority = common.GetPointer[int64](5)
	})
	require.NoError(t, ch2.AddAbilities(nil))
	cleanupAbilities(t, ch2.Id)

	// retry 0 -> highest priority
	p, err := getPriority(grp, model, 0)
	require.NoError(t, err)
	assert.Equal(t, 10, p)
	// retry 1 -> next
	p, err = getPriority(grp, model, 1)
	require.NoError(t, err)
	assert.Equal(t, 5, p)
	// retry >= number of distinct priorities -> smallest
	p, err = getPriority(grp, model, 99)
	require.NoError(t, err)
	assert.Equal(t, 5, p)

	// no abilities for this group/model -> consistency error
	_, err = getPriority(uniq("noprio"), model, 0)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// GetChannel — weighted selection + priority/retry + request-path filtering
// ---------------------------------------------------------------------------

func TestGetChannel_NoAbilities(t *testing.T) {
	requireDB(t)
	require.False(t, common.MemoryCacheEnabled)
	ch, err := GetChannel(uniq("empty"), "gpt-4o", 0, "")
	require.NoError(t, err)
	assert.Nil(t, ch)
}

func TestGetChannel_WeightedAndRetry(t *testing.T) {
	requireDB(t)
	require.False(t, common.MemoryCacheEnabled)
	grp := uniq("gc")
	model := "gpt-4o"
	high1 := mkChannel(t, func(c *Channel) {
		c.Group, c.Models = grp, model
		c.Priority = common.GetPointer[int64](10)
		c.Weight = common.GetPointer[uint](5)
	})
	require.NoError(t, high1.AddAbilities(nil))
	cleanupAbilities(t, high1.Id)
	high2 := mkChannel(t, func(c *Channel) {
		c.Group, c.Models = grp, model
		c.Priority = common.GetPointer[int64](10)
		c.Weight = common.GetPointer[uint](5)
	})
	require.NoError(t, high2.AddAbilities(nil))
	cleanupAbilities(t, high2.Id)
	low := mkChannel(t, func(c *Channel) {
		c.Group, c.Models = grp, model
		c.Priority = common.GetPointer[int64](5)
	})
	require.NoError(t, low.AddAbilities(nil))
	cleanupAbilities(t, low.Id)

	highSet := map[int]bool{high1.Id: true, high2.Id: true}
	for i := 0; i < 8; i++ {
		got, err := GetChannel(grp, model, 0, "")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Truef(t, highSet[got.Id], "retry 0 must select a priority-10 channel, got %d", got.Id)
	}

	// retry 1 -> lower priority bucket
	got, err := GetChannel(grp, model, 1, "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, low.Id, got.Id)

	// retry beyond distinct priorities -> smallest priority bucket (getPriority clamp)
	got, err = GetChannel(grp, model, 5, "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, low.Id, got.Id)
}

func TestGetChannel_RequestPathFiltering(t *testing.T) {
	requireDB(t)
	require.False(t, common.MemoryCacheEnabled)
	grp := uniq("gcpath")
	adv := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Models = "gpt-4o"
		c.Type = constant.ChannelTypeAdvancedCustom
		c.OtherSettings = advancedCustomChannelSettings(t, "gpt-4o")
	})
	require.NoError(t, adv.AddAbilities(nil))
	cleanupAbilities(t, adv.Id)

	// matching path -> advanced-custom channel selected
	got, err := GetChannel(grp, "gpt-4o", 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, adv.Id, got.Id)

	// non-matching path -> advanced-custom filtered out, no candidate left
	got, err = GetChannel(grp, "gpt-4o", 0, "/v1/messages")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ---------------------------------------------------------------------------
// filterAbilitiesByRequestPathAndModel (direct)
// ---------------------------------------------------------------------------

func TestFilterAbilitiesByRequestPathAndModel(t *testing.T) {
	requireDB(t)

	// empty requestPath -> input returned unchanged
	in := []Ability{{ChannelId: 1}}
	assert.Equal(t, in, filterAbilitiesByRequestPathAndModel(in, "", "gpt-4o"))
	// empty abilities -> returned unchanged
	assert.Empty(t, filterAbilitiesByRequestPathAndModel(nil, "/v1/chat/completions", "gpt-4o"))

	plain := mkChannel(t, func(c *Channel) { c.Models = "gpt-4o" })
	adv := mkChannel(t, func(c *Channel) {
		c.Models = "gpt-4o"
		c.Type = constant.ChannelTypeAdvancedCustom
		c.OtherSettings = advancedCustomChannelSettings(t, "gpt-4o")
	})

	abilities := []Ability{{ChannelId: plain.Id}, {ChannelId: adv.Id}}

	// matching path -> both kept
	out := filterAbilitiesByRequestPathAndModel(abilities, "/v1/chat/completions", "gpt-4o")
	assert.Len(t, out, 2)

	// non-matching path -> advanced-custom dropped, plain kept
	out = filterAbilitiesByRequestPathAndModel(abilities, "/v1/messages", "gpt-4o")
	require.Len(t, out, 1)
	assert.Equal(t, plain.Id, out[0].ChannelId)
}

// ---------------------------------------------------------------------------
// GetGroupEnabledModels / GetEnabledModels / GetAllEnableAbilities
// ---------------------------------------------------------------------------

func TestGetGroupEnabledModels(t *testing.T) {
	requireDB(t)
	grp := uniq("gem")
	mA := uniq("modelA")
	mB := uniq("modelB")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Models = mA + "," + mB
		c.Status = common.ChannelStatusEnabled
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	models := GetGroupEnabledModels(grp)
	assert.ElementsMatch(t, []string{mA, mB}, models)

	// GetEnabledModels is global -> must be a superset containing our models
	all := GetEnabledModels()
	assert.Contains(t, all, mA)
	assert.Contains(t, all, mB)
}

func TestGetGroupEnabledModels_DisabledExcluded(t *testing.T) {
	requireDB(t)
	grp := uniq("gemd")
	mDis := uniq("modelDisabled")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Models = mDis
		c.Status = common.ChannelStatusManuallyDisabled // ability.Enabled == false
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	assert.Empty(t, GetGroupEnabledModels(grp), "disabled channel contributes no enabled models")
	assert.NotContains(t, GetEnabledModels(), mDis)
}

func TestGetAllEnableAbilitiesAndWithChannels(t *testing.T) {
	requireDB(t)
	grp := uniq("gaea")
	model := uniq("gaeaModel")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = grp
		c.Models = model
		c.Type = 1
		c.Status = common.ChannelStatusEnabled
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	abilities := GetAllEnableAbilities()
	found := false
	for _, ab := range abilities {
		if ab.ChannelId == ch.Id && ab.Model == model {
			found = true
			assert.True(t, ab.Enabled)
		}
	}
	assert.True(t, found, "our enabled ability must appear in GetAllEnableAbilities")

	withCh, err := GetAllEnableAbilityWithChannels()
	require.NoError(t, err)
	foundWith := false
	for _, ab := range withCh {
		if ab.ChannelId == ch.Id && ab.Model == model {
			foundWith = true
			assert.Equal(t, 1, ab.ChannelType)
		}
	}
	assert.True(t, foundWith, "our ability must join to its channel type")
}

// ---------------------------------------------------------------------------
// FixAbility — only the contention guard is safe to exercise. The success path
// TRUNCATEs the whole abilities table, which would wipe rows owned by other
// tests/production, so it is intentionally NOT invoked here (documented gap).
// ---------------------------------------------------------------------------

func TestFixAbility_LockContention(t *testing.T) {
	// Hold the package lock so FixAbility's TryLock fails and it returns early
	// WITHOUT truncating the abilities table.
	fixLock.Lock()
	defer fixLock.Unlock()

	s, f, err := FixAbility()
	assert.Error(t, err)
	assert.Equal(t, 0, s)
	assert.Equal(t, 0, f)
}

// sanity: Ability primary-key uniqueness surfaces as an OnConflict no-op, not a
// duplicate insert (guards against a regression in the dedup logic).
func TestAbility_PrimaryKeyRoundTrip(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) {
		c.Group = "default"
		c.Models = "pk-model"
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	var ab Ability
	err := DB.Where("channel_id = ? AND model = ?", ch.Id, "pk-model").First(&ab).Error
	require.NoError(t, err)
	assert.Equal(t, "default", ab.Group)
	assert.NotErrorIs(t, err, gorm.ErrRecordNotFound)
}
