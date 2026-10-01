package gemini

import (
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
)

// Cross-format reasoning mapping between OpenAI reasoning_effort and Gemini
// thinkingConfig. The model families, budgets and levels follow upstream
// relaykit/relayconvert/reasoning/gemini.go (RenderGemini, EffortFromBudget).
// Where upstream rejects a combination the model cannot honour, this mapping
// sends the closest setting the model accepts instead, because conversion
// errors are not yet surfaced to clients as 4xx here.

type thinkingControl int

const (
	thinkingUnknown thinkingControl = iota
	thinkingNotConfigurable
	thinkingByBudget
	thinkingByLevel
)

type thinkingCapabilities struct {
	control         thinkingControl
	supportsDisable bool
	minBudget       int
	maxBudget       int
}

func thinkingCapabilitiesFor(model string) thinkingCapabilities {
	model = strings.ToLower(model)
	switch {
	case strings.HasPrefix(model, "gemini-2.5-flash-native-audio"),
		strings.HasPrefix(model, "gemini-live-2.5-flash-preview-native-audio"):
		return thinkingCapabilities{control: thinkingByBudget, supportsDisable: true, maxBudget: 24576}
	case strings.HasPrefix(model, "gemini-2.5-flash-image"),
		strings.Contains(model, "-tts"),
		strings.Contains(model, "-native-audio"),
		strings.Contains(model, "-live"),
		strings.HasPrefix(model, "gemini-3-pro-image"),
		strings.HasPrefix(model, "nano-banana-pro"):
		return thinkingCapabilities{control: thinkingNotConfigurable}
	case model == "gemini-flash-latest", model == "gemini-flash-lite-latest", model == "gemini-pro-latest":
		return thinkingCapabilities{control: thinkingByLevel}
	case strings.HasPrefix(model, "gemini-2.5-pro"):
		return thinkingCapabilities{control: thinkingByBudget, minBudget: 128, maxBudget: 32768}
	case strings.HasPrefix(model, "gemini-2.5-flash-lite"):
		return thinkingCapabilities{control: thinkingByBudget, supportsDisable: true, minBudget: 512, maxBudget: 24576}
	case strings.HasPrefix(model, "gemini-2.5-"):
		return thinkingCapabilities{control: thinkingByBudget, supportsDisable: true, maxBudget: 24576}
	case strings.HasPrefix(model, "gemini-3"):
		return thinkingCapabilities{control: thinkingByLevel}
	default:
		return thinkingCapabilities{}
	}
}

// hasThinkingSuffix reports whether the model name carries one of the
// thinking aliases ApplyThinkingConfig and the adaptor interpret. Such an
// alias already states the reasoning setting, so reasoning_effort is not
// layered on top of it.
func hasThinkingSuffix(model string) bool {
	if strings.Contains(model, "-thinking-") ||
		strings.HasSuffix(model, "-thinking") ||
		strings.HasSuffix(model, "-nothinking") {
		return true
	}
	_, level, ok := reasoning.TrimEffortSuffix(model)
	return ok && level != ""
}

// ApplyReasoningEffort renders an OpenAI reasoning_effort as a Gemini
// thinkingConfig for the target model. It leaves the request unchanged when
// the effort is empty or unknown, the model name carries a thinking alias, or
// the model has no configurable thinking.
//
// "none" disables thinking where the model allows it (budget 0). Models that
// always think (gemini-2.5-pro, the Gemini 3 family) get their lowest
// setting, which is the closest they can come to the request.
func ApplyReasoningEffort(geminiRequest *dto.GeminiChatRequest, info convmeta.Meta, modelName string, effort string) {
	if geminiRequest == nil {
		return
	}
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" || hasThinkingSuffix(modelName) {
		return
	}
	config, applied, ok := thinkingConfigForEffort(modelName, effort)
	if !ok {
		return
	}
	geminiRequest.GenerationConfig.ThinkingConfig = config
	if info != nil {
		info.SetReasoningEffort(applied)
	}
}

// thinkingConfigForEffort returns the config and the effort it amounts to.
func thinkingConfigForEffort(model string, effort string) (*dto.GeminiThinkingConfig, string, bool) {
	caps := thinkingCapabilitiesFor(model)
	switch caps.control {
	case thinkingByBudget:
		budget, ok := budgetForEffort(effort)
		if !ok {
			return nil, "", false
		}
		if budget == 0 && !caps.supportsDisable {
			budget = caps.minBudget
		}
		if budget != 0 {
			budget = min(max(budget, caps.minBudget), caps.maxBudget)
		}
		return &dto.GeminiThinkingConfig{ThinkingBudget: &budget}, effortFromBudget(budget), true
	case thinkingByLevel:
		if effort == "none" {
			effort = "minimal"
		}
		level, ok := levelForEffort(model, effort)
		if !ok {
			return nil, "", false
		}
		return &dto.GeminiThinkingConfig{ThinkingLevel: level}, level, true
	default:
		return nil, "", false
	}
}

func budgetForEffort(effort string) (int, bool) {
	switch effort {
	case "none":
		return 0, true
	case "minimal", "low":
		return 1024, true
	case "medium":
		return 8192, true
	case "high", "xhigh", "max":
		return 24576, true
	default:
		return 0, false
	}
}

// levelForEffort maps an effort to a thinkingLevel the model accepts.
func levelForEffort(model string, effort string) (string, bool) {
	model = strings.ToLower(model)
	switch {
	case strings.HasPrefix(model, "gemini-3.1-flash-image"),
		strings.HasPrefix(model, "gemini-3.1-flash-lite-image"):
		if effort == "minimal" || effort == "low" {
			return "minimal", true
		}
		if isKnownEffort(effort) {
			return "high", true
		}
		return "", false
	case strings.HasPrefix(model, "gemini-3-pro"):
		if effort == "minimal" || effort == "low" {
			return "low", true
		}
		if isKnownEffort(effort) {
			return "high", true
		}
		return "", false
	case strings.HasPrefix(model, "gemini-3.1-pro"), model == "gemini-pro-latest":
		if effort == "minimal" {
			return "low", true
		}
	}
	switch effort {
	case "minimal", "low", "medium", "high":
		return effort, true
	case "xhigh", "max":
		return "high", true
	default:
		return "", false
	}
}

func isKnownEffort(effort string) bool {
	switch effort {
	case "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

// effortFromBudget converts a non-negative Gemini thinking budget to the
// nearest OpenAI reasoning effort (upstream reasoning.EffortFromBudget).
func effortFromBudget(budget int) string {
	switch {
	case budget == 0:
		return "none"
	case budget <= 1024:
		return "low"
	case budget <= 8192:
		return "medium"
	default:
		return "high"
	}
}

// OpenAIReasoningEffortFor renders a Gemini thinkingConfig as the
// reasoning_effort to send with targetModel on the OpenAI pivot, or "" when
// nothing should be sent. The pivot is shared by OpenAI chat, Responses and
// Claude targets, and reasoning_effort is rejected by models without it
// (gpt-4o, Claude, most OpenAI-compatible providers) and by reasoning models
// for values they lack ("none" before GPT-5.1, "minimal" on o-series), so it is
// only emitted for model families known to accept it, clamped to a value that
// family accepts:
//   - GPT-5.1 / 5.2 / 5.4 (and dated snapshots): none, low, medium, high.
//   - gpt-5, gpt-5-mini, gpt-5-nano (and dated snapshots): minimal and up;
//     none becomes minimal.
//   - Other GPT-5 variants except -pro (high only): low and up.
//   - o1 / o3 / o4 except o1-mini and o1-preview: low and up.
//   - Gemini 2.5 / 3 models (Gemini's OpenAI-compatible endpoint): the same
//     closest-supported rule as ApplyReasoningEffort.
func OpenAIReasoningEffortFor(targetModel string, config *dto.GeminiThinkingConfig) string {
	effort := reasoningEffortFromThinkingConfig(config)
	if effort == "" {
		return ""
	}
	model := strings.ToLower(strings.TrimSpace(targetModel))
	switch {
	case isOpenAIModelOrSnapshot(model, "gpt-5.1", "gpt-5.2", "gpt-5.4"):
		if effort == "minimal" {
			return "low"
		}
		return effort
	case isOpenAIModelOrSnapshot(model, "gpt-5", "gpt-5-mini", "gpt-5-nano"):
		if effort == "none" {
			return "minimal"
		}
		return effort
	case dto.IsOpenAIGPT5Model(model):
		if strings.Contains(model, "-pro") {
			return ""
		}
		return atLeastLow(effort)
	case dto.IsOpenAIReasoningOModel(model):
		if strings.HasPrefix(model, "o1-mini") || strings.HasPrefix(model, "o1-preview") {
			return ""
		}
		return atLeastLow(effort)
	}

	caps := thinkingCapabilitiesFor(model)
	switch caps.control {
	case thinkingByBudget:
		if effort == "minimal" || (effort == "none" && !caps.supportsDisable) {
			return "low"
		}
		return effort
	case thinkingByLevel:
		if effort == "none" {
			effort = "minimal"
		}
		level, _ := levelForEffort(model, effort)
		return level
	default:
		return ""
	}
}

func atLeastLow(effort string) string {
	if effort == "none" || effort == "minimal" {
		return "low"
	}
	return effort
}

// isOpenAIModelOrSnapshot reports whether model is one of bases or a dated
// snapshot of one (base-YYYY-MM-DD).
func isOpenAIModelOrSnapshot(model string, bases ...string) bool {
	for _, base := range bases {
		if model == base {
			return true
		}
		if snapshot, ok := strings.CutPrefix(model, base+"-"); ok {
			if _, err := time.Parse(time.DateOnly, snapshot); err == nil {
				return true
			}
		}
	}
	return false
}

// reasoningEffortFromThinkingConfig reads the effort a Gemini thinkingConfig
// asks for. thinkingLevel wins over thinkingBudget. A dynamic budget (-1) or
// any other negative budget means "model default" and maps to no effort, so
// the target keeps its own default instead of being escalated.
func reasoningEffortFromThinkingConfig(config *dto.GeminiThinkingConfig) string {
	if config == nil {
		return ""
	}
	switch level := strings.ToLower(strings.TrimSpace(config.ThinkingLevel)); level {
	case "minimal", "low", "medium", "high":
		return level
	}
	if config.ThinkingBudget == nil || *config.ThinkingBudget < 0 {
		return ""
	}
	return effortFromBudget(*config.ThinkingBudget)
}
