package model_setting

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/setting/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// relay 路径上的 Gemini getter 只读已发布快照：草稿在锁内被改写、尚未发布时，getter 必须仍返回已发布的值。
// 读草稿的实现会在这里直接读到未发布的值（这正是与配置写入并发时的数据竞争）。
func TestGeminiRelayGettersReadPublishedSnapshotNotDraft(t *testing.T) {
	original := *GetGeminiSettings()
	t.Cleanup(func() { ReplaceGeminiSettings(original) })
	ReplaceGeminiSettings(GeminiSettings{
		SafetySettings:         map[string]string{"default": "BLOCK_NONE", "published-model": "BLOCK_ONLY_HIGH"},
		VersionSettings:        map[string]string{"default": "v1beta", "published-model": "v1"},
		SupportedImagineModels: []string{"published-image-model"},
	})

	config.WithConfigDraft(func() {
		draft := geminiSettings
		defer func() { geminiSettings = draft }()
		geminiSettings = GeminiSettings{
			SafetySettings:         map[string]string{"default": "BLOCK_LOW_AND_ABOVE", "published-model": "OFF"},
			VersionSettings:        map[string]string{"default": "draft-version", "published-model": "draft-version"},
			SupportedImagineModels: []string{"draft-image-model"},
		}

		assert.Equal(t, "BLOCK_ONLY_HIGH", GetGeminiSafetySetting("published-model"))
		assert.Equal(t, "BLOCK_NONE", GetGeminiSafetySetting("other-model"))
		assert.Equal(t, "v1", GetGeminiVersionSetting("published-model"))
		assert.Equal(t, "v1beta", GetGeminiVersionSetting("other-model"))
		assert.True(t, IsGeminiModelSupportImagine("published-image-model"))
		assert.False(t, IsGeminiModelSupportImagine("draft-image-model"))
	})
}

// ConfigManager 反序列化 slice 时复用草稿的底层数组；已发布快照不得随之被原地改写。
func TestGeminiPublishedSnapshotIsIsolatedFromDraftSliceReuse(t *testing.T) {
	original := *GetGeminiSettings()
	t.Cleanup(func() { ReplaceGeminiSettings(original) })
	ReplaceGeminiSettings(GeminiSettings{SupportedImagineModels: []string{"before-a", "before-b"}})
	published := GetGeminiSettings()

	require.NoError(t, config.GlobalConfig.UpdateFromMap("gemini", map[string]string{
		"supported_imagine_models": `["after-a","after-b"]`,
	}))

	assert.Equal(t, []string{"before-a", "before-b"}, published.SupportedImagineModels, "a published snapshot must never change")
	assert.True(t, IsGeminiModelSupportImagine("after-a"))
	assert.False(t, IsGeminiModelSupportImagine("before-a"))
}

// 与 TestRateLimitDraftAccessIsSerialized 同思路的一致性回归：写者经 ConfigManager 反复切换两份
// 字段自洽的配置，读者从快照读到的每份配置都必须完整属于其中一份，不得混合。
func TestGeminiSnapshotReadsStayConsistentDuringConfigWrites(t *testing.T) {
	original := *GetGeminiSettings()
	t.Cleanup(func() { ReplaceGeminiSettings(original) })
	ReplaceGeminiSettings(GeminiSettings{
		SafetySettings:         map[string]string{"default": "OFF"},
		VersionSettings:        map[string]string{"default": "OFF"},
		SupportedImagineModels: []string{"OFF", "OFF"},
	})

	variants := []map[string]string{
		{"safety_settings": `{"default":"BLOCK_NONE"}`, "version_settings": `{"default":"BLOCK_NONE"}`, "supported_imagine_models": `["BLOCK_NONE","BLOCK_NONE"]`},
		{"safety_settings": `{"default":"OFF"}`, "version_settings": `{"default":"OFF"}`, "supported_imagine_models": `["OFF","OFF"]`},
	}
	const writers, readers, rounds = 2, 4, 300
	var writersWG, readersWG sync.WaitGroup
	stop := make(chan struct{})
	for writer := 0; writer < writers; writer++ {
		writersWG.Add(1)
		go func(seed int) {
			defer writersWG.Done()
			for round := 0; round < rounds; round++ {
				if err := config.GlobalConfig.UpdateFromMap("gemini", variants[(seed+round)%len(variants)]); err != nil {
					t.Errorf("update gemini settings: %v", err)
					return
				}
			}
		}(writer)
	}
	for reader := 0; reader < readers; reader++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snapshot := GetGeminiSettings()
				identity := snapshot.SafetySettings["default"]
				if len(snapshot.SupportedImagineModels) != 2 ||
					snapshot.VersionSettings["default"] != identity ||
					snapshot.SupportedImagineModels[0] != identity ||
					snapshot.SupportedImagineModels[1] != identity {
					t.Errorf("inconsistent gemini snapshot: %+v", *snapshot)
					return
				}
				if safety := GetGeminiSafetySetting("any-model"); safety != "OFF" && safety != "BLOCK_NONE" {
					t.Errorf("unexpected safety setting %q", safety)
					return
				}
			}
		}()
	}
	writersWG.Wait()
	close(stop)
	readersWG.Wait()
}
