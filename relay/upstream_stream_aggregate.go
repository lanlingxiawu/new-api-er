package relay

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// chatStreamAggregator re-assembles chat.completion.chunk deltas into the single
// chat.completion body a non-stream client expects.
//
// It is deliberately tolerant: the whole reason this path exists is that the
// request may be cut off mid-stream by the user's deadline, so Snapshot() must
// produce a usable body (and usable usage) from however many chunks arrived.
type chatStreamAggregator struct {
	id                string
	model             string
	created           int64
	systemFingerprint *string
	usage             *dto.Usage
	// usageRaw is upstream's usage object verbatim. A plain non-stream call
	// forwards upstream's bytes, so its usage keeps exactly upstream's fields;
	// re-marshaling dto.Usage would add internal ones (claude_cache_creation_*,
	// input_tokens_details) that client never saw before.
	usageRaw string
	// serviceTier is kept raw for the same reason: the normal path forwards it.
	serviceTier string
	// promptFilterResults is Azure's content-filter verdict on the prompt, raw.
	// The non-stream body carries it at top level; the stream sends it on an
	// early frame with no choices.
	promptFilterResults string

	// Choices are keyed by their stream index. Only index 0 is expected (n>1 is
	// rejected upstream of here) but keying keeps a stray index from corrupting
	// the assembled text.
	choices map[int]*aggregatedChoice
	// sawAny records whether any parsable chunk arrived at all, which
	// distinguishes "upstream produced nothing" from "upstream produced empty
	// content" when deciding whether there is anything to bill.
	sawAny bool

	// A non-stream body has a natural end; a stream does not, so an upstream
	// that never stops would otherwise grow this buffer without bound. Budget
	// is shared across all accumulated text and matches the managed stream path
	// (relaycommon.MaxStreamFrameBytes).
	budget    int
	overBudge bool
}

// aggregatedEntryBytes is what one new choice or tool call entry is charged
// against the budget, besides its text: the map slot, struct and builders.
// Without it an upstream streaming endless distinct entries with empty text
// would grow the aggregator without ever reaching the cut-off.
const aggregatedEntryBytes = 128

type aggregatedChoice struct {
	index   int
	role    string
	content strings.Builder
	// sawStringContent: some delta carried content as a string (even ""), not
	// only null. Upstreams are consistent across modes here, so it decides
	// whether a text-less tool-call message reports content "" or null.
	sawStringContent bool
	reasoning        strings.Builder
	// reasoningField is the name upstream first sent reasoning under
	// ("reasoning_content" or "reasoning"); the message keeps it, as the plain
	// path forwards upstream's message unchanged.
	reasoningField string
	finishReason   string
	// refusal and annotations are not in the stream DTO; they are read from
	// the raw frame (see AddFrameExtras). refusal is the structured-output
	// decline text, annotations the url citations of search models.
	refusal     strings.Builder
	hasRefusal  bool
	annotations []json.RawMessage
	// contentFilterResults is Azure's per-choice content-filter verdict, raw
	// (not in the stream DTO either). The stream repeats it on the choice of
	// many frames, each judging the text so far; the last non-empty one is
	// kept, or "{}" when only empty ones came, so the key is present as in the
	// non-stream body — except that a category once flagged (filtered: true)
	// stays flagged (see keepContentFilterResults).
	contentFilterResults string
	// contentFilterFlagged: the kept verdict has a flagged category, so a new
	// one must be checked for categories to carry over. False on almost every
	// stream, which keeps an unchanged verdict a plain string comparison.
	contentFilterFlagged bool
	// toolCalls is keyed by upstream's index (>= 0) for indexed calls and by
	// -1, -2, ... for calls that arrived without one, so the two can never
	// share a slot; arguments arrive as fragments. toolOrder is arrival order,
	// the order the calls are returned in.
	toolCalls map[int]*aggregatedToolCall
	toolOrder []int
	// indexlessCalls counts the calls placed without an upstream index; the
	// next one takes key -(indexlessCalls+1).
	indexlessCalls int
}

type aggregatedToolCall struct {
	id        string
	callType  any
	name      string
	arguments strings.Builder
	// argsShape follows the arguments as they are written, so telling whether
	// they already form one complete JSON value costs nothing per fragment.
	argsShape jsonValueTracker
}

// jsonValueTracker follows the structure of JSON text fed to it in pieces —
// string and escape state and bracket depth — to tell whether the text so far
// is one complete top-level object or array: closed, with nothing but
// whitespace after it. Each byte is scanned once as it is fed; the text is
// validated with gjson.Valid at most once, the first time it is asked after
// the close, since only whitespace can follow without breaking it. A scalar
// at top level or anything after the close breaks it for good.
type jsonValueTracker struct {
	depth    int
	inString bool
	escaped  bool
	opened   bool
	closed   bool
	broken   bool
	checked  bool
	valid    bool
}

func (t *jsonValueTracker) feed(s string) {
	if t.broken {
		return
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if t.inString {
			switch {
			case t.escaped:
				t.escaped = false
			case b == '\\':
				t.escaped = true
			case b == '"':
				t.inString = false
			}
			continue
		}
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		if t.closed || (!t.opened && b != '{' && b != '[') {
			t.broken = true
			return
		}
		switch b {
		case '{', '[':
			t.opened = true
			t.depth++
		case '}', ']':
			t.depth--
			if t.depth == 0 {
				t.closed = true
			}
		case '"':
			t.inString = true
		}
	}
}

// blank reports that nothing but whitespace has been fed.
func (t *jsonValueTracker) blank() bool {
	return !t.opened && !t.broken
}

// complete reports whether text — everything fed so far — is one complete
// JSON object or array.
func (t *jsonValueTracker) complete(text string) bool {
	if !t.closed || t.broken {
		return false
	}
	if !t.checked {
		t.checked = true
		t.valid = gjson.Valid(text)
	}
	return t.valid
}

func newChatStreamAggregator(model string) *chatStreamAggregator {
	return &chatStreamAggregator{
		model:   model,
		choices: map[int]*aggregatedChoice{},
		budget:  relaycommon.MaxStreamFrameBytes,
	}
}

// take reserves n bytes of the shared budget, reporting whether the write may
// proceed. Once exhausted the aggregator stops accumulating text but keeps
// folding in metadata and usage, so the request still settles on real numbers.
func (a *chatStreamAggregator) take(n int) bool {
	if a.overBudge {
		return false
	}
	if n > a.budget {
		a.overBudge = true
		return false
	}
	a.budget -= n
	return true
}

// OverBudget reports that upstream exceeded the accumulation budget, which is
// treated as an abnormal stream rather than a complete response.
func (a *chatStreamAggregator) OverBudget() bool {
	return a != nil && a.overBudge
}

// AddChunk folds one decoded SSE payload into the accumulated response.
func (a *chatStreamAggregator) AddChunk(chunk *dto.ChatCompletionsStreamResponse) {
	if a == nil || chunk == nil {
		return
	}
	a.sawAny = true
	if chunk.Id != "" {
		a.id = chunk.Id
	}
	if chunk.Model != "" {
		a.model = chunk.Model
	}
	if chunk.Created != 0 {
		a.created = chunk.Created
	}
	if chunk.SystemFingerprint != nil {
		a.systemFingerprint = chunk.SystemFingerprint
	}
	// Usage normally arrives only in the final chunk (include_usage). Later
	// chunks win so a mid-stream partial count never overrides the final one.
	if chunk.Usage != nil {
		a.usage = chunk.Usage
	}
	for i := range chunk.Choices {
		a.addChoice(&chunk.Choices[i])
	}
}

// choiceAt returns the choice at index, creating it if the budget allows; nil
// means the budget is spent and the choice is dropped.
func (a *chatStreamAggregator) choiceAt(index int) *aggregatedChoice {
	target, ok := a.choices[index]
	if !ok {
		if !a.take(aggregatedEntryBytes) {
			return nil
		}
		target = &aggregatedChoice{index: index, toolCalls: map[int]*aggregatedToolCall{}}
		a.choices[index] = target
	}
	return target
}

func (a *chatStreamAggregator) addChoice(choice *dto.ChatCompletionsStreamResponseChoice) {
	target := a.choiceAt(choice.Index)
	if target == nil {
		return
	}
	if choice.Delta.Role != "" {
		target.role = choice.Delta.Role
	}
	if choice.Delta.Content != nil {
		target.sawStringContent = true
		if a.take(len(*choice.Delta.Content)) {
			target.content.WriteString(*choice.Delta.Content)
		}
	}
	// Providers disagree on which of the two reasoning fields they emit, and
	// some send the same text under both. They are aliases (as in
	// Message.GetReasoningContent): per delta reasoning_content wins unless it
	// is empty, else reasoning.
	reasoning, field := choice.Delta.ReasoningContent, "reasoning_content"
	if reasoning == nil || (*reasoning == "" && choice.Delta.Reasoning != nil) {
		reasoning, field = choice.Delta.Reasoning, "reasoning"
	}
	if reasoning != nil {
		if target.reasoningField == "" && *reasoning != "" {
			target.reasoningField = field
		}
		if a.take(len(*reasoning)) {
			target.reasoning.WriteString(*reasoning)
		}
	}
	if choice.FinishReason != nil && *choice.FinishReason != "" {
		target.finishReason = *choice.FinishReason
	}
	for i := range choice.Delta.ToolCalls {
		target.addToolCall(&choice.Delta.ToolCalls[i], a)
	}
}

func (c *aggregatedChoice) addToolCall(call *dto.ToolCallResponse, budget *chatStreamAggregator) {
	var index int
	if call.Index != nil {
		// Negative keys belong to index-less calls; upstream's are clamped.
		index = max(*call.Index, 0)
	} else {
		index = c.indexlessToolCallIndex(call)
	}
	target, ok := c.toolCalls[index]
	if !ok {
		if !budget.take(aggregatedEntryBytes) {
			return
		}
		target = &aggregatedToolCall{}
		c.toolCalls[index] = target
		c.toolOrder = append(c.toolOrder, index)
		if index < 0 {
			c.indexlessCalls++
		}
	}
	// id and name are normally sent once; charged only when they change.
	if call.ID != "" && call.ID != target.id && budget.take(len(call.ID)) {
		target.id = call.ID
	}
	if call.Type != nil {
		target.callType = call.Type
	}
	if call.Function.Name != "" && call.Function.Name != target.name && budget.take(len(call.Function.Name)) {
		target.name = call.Function.Name
	}
	if call.Function.Arguments != "" && budget.take(len(call.Function.Arguments)) {
		target.arguments.WriteString(call.Function.Arguments)
		target.argsShape.feed(call.Function.Arguments)
	}
}

// indexlessToolCallIndex places a tool-call delta that carries no index. It
// continues the most recent call — how providers that stream one call in
// fragments behave — unless the delta starts another call:
//   - it and the most recent call both have an id, and the ids differ;
//   - otherwise (either has no id) it names a function, and the most recent
//     call has a different name;
//   - or the same name — a second call to the same function, sent without
//     index or id — and either the most recent call's arguments already form
//     one complete JSON value and the delta brings non-blank arguments, a call
//     header (a type) or an id that call does not have; or the most recent
//     call has no arguments yet (only whitespace) and the delta is a header
//     with blank arguments too: two calls with empty arguments.
//
// A delta naming no function, with no id to compare, continues the call; so
// does a bare repeat of the name (no type, no new id, blank arguments), which
// some providers put on later fragments, even after the arguments closed; and
// so does anything while the arguments are open (or invalid).
//
// Limits: two same-name calls with blank arguments and no type, id or index
// cannot be told from a repeated name and fold into one; nor can a call with
// blank arguments followed by a same-name header bringing arguments, which
// continues it (the first call is lost); a header re-sent with blank arguments
// after complete or blank arguments reads as a second call. Completeness only
// recognizes an object or array — function arguments are a JSON object per
// the API — so after bare scalar arguments ("1") a same-name call is never
// split off.
//
// Providers that send each call whole and index-less (several per response, in
// one frame or across frames) mark every call that way; folding them into one
// would return a single call with concatenated, invalid-JSON arguments and
// bill per-call tool pricing once. A new call takes the next negative key, a
// space upstream indexes never use, so no indexed call — seen before or after
// — can land on it. Every check is O(1) per delta — argument completeness is
// tracked as the arguments are written (jsonValueTracker) — so an upstream
// streaming endless index-less calls or fragments never makes one delta cost
// a scan of what came before.
func (c *aggregatedChoice) indexlessToolCallIndex(call *dto.ToolCallResponse) int {
	next := -(c.indexlessCalls + 1)
	if len(c.toolOrder) == 0 {
		return next
	}
	last := c.toolOrder[len(c.toolOrder)-1]
	current := c.toolCalls[last]
	if call.ID != "" && current.id != "" {
		if call.ID != current.id {
			return next
		}
		return last
	}
	name := call.Function.Name
	if name == "" || current.name == "" {
		return last
	}
	if name != current.name {
		return next
	}
	header := call.Type != nil
	blankArgs := strings.TrimSpace(call.Function.Arguments) == ""
	if current.argsShape.blank() {
		// Two headers with blank arguments are two calls. A header that brings
		// arguments starts the arguments of the call: providers that re-send
		// the header on every fragment may open a call with empty ones.
		if header && blankArgs {
			return next
		}
		return last
	}
	startsCall := header || call.ID != "" || !blankArgs
	if startsCall && current.argsShape.complete(current.arguments.String()) {
		return next
	}
	return last
}

// AddFrameExtras folds in the fields of one raw frame that the stream DTO does
// not model but a plain non-stream response carries. It runs on every frame, so
// each lookup is gated by a substring test and costs nothing on the frames that
// carry none of them. The decoder is left alone on purpose: it is held to
// Unmarshal into the DTO, and these fields are not in the DTO.
func (a *chatStreamAggregator) AddFrameExtras(data string) {
	if a == nil {
		return
	}
	if strings.Contains(data, `"service_tier"`) {
		if v := gjson.Get(data, "service_tier"); v.Exists() && v.Type != gjson.Null {
			a.serviceTier = v.Raw
		}
	}
	// Later frames win, matching how AddChunk keeps the decoded usage.
	if strings.Contains(data, `"usage"`) {
		if v := gjson.Get(data, "usage"); v.IsObject() {
			a.usageRaw = v.Raw
		}
	}
	// Azure sends it once; charged to the budget since it is upstream-sized.
	if strings.Contains(data, `"prompt_filter_results"`) {
		if v := gjson.Get(data, "prompt_filter_results"); v.IsArray() && a.take(len(v.Raw)) {
			a.promptFilterResults = v.Raw
		}
	}
	// Azure's per-choice verdict sits on the choice itself, next to delta.
	hasFilterKey := strings.Contains(data, `"content_filter_results"`)
	if !hasFilterKey && !strings.Contains(data, `"refusal"`) && !strings.Contains(data, `"annotations"`) {
		return
	}
	gjson.Get(data, "choices").ForEach(func(_, choice gjson.Result) bool {
		delta := choice.Get("delta")
		refusal := delta.Get("refusal")
		annotations := delta.Get("annotations")
		var filter gjson.Result
		if hasFilterKey {
			filter = choice.Get("content_filter_results")
		}
		// OpenAI sends "refusal": null on ordinary frames; only text counts.
		hasRefusal := refusal.Type == gjson.String && refusal.Str != ""
		if !hasRefusal && !annotations.IsArray() && !filter.IsObject() {
			return true
		}
		target := a.choiceAt(int(choice.Get("index").Int()))
		if target == nil {
			return true
		}
		if filter.IsObject() {
			a.keepContentFilterResults(target, filter.Raw)
		}
		if hasRefusal && a.take(len(refusal.Str)) {
			target.refusal.WriteString(refusal.Str)
			target.hasRefusal = true
		}
		if annotations.IsArray() {
			annotations.ForEach(func(_, item gjson.Result) bool {
				if a.take(len(item.Raw)) {
					target.annotations = append(target.annotations, json.RawMessage(item.Raw))
				}
				return true
			})
		}
		return true
	})
}

// keepContentFilterResults stores a choice's content_filter_results object.
// Azure repeats it on most frames, so the stored copy is replaced, never
// appended to: the budget is charged only for growth, keeping the charge equal
// to what is held, and raw is copied only when it changed, so the rest of the
// frame is not kept alive by the substring. An empty object never replaces a
// verdict. A category the kept verdict flagged (filtered: true) is carried
// into a new one that reports it unflagged or omits it: a frame judging later
// text must not clear the verdict that stopped the answer. Until some verdict
// flags a category that check is skipped, so the usual Azure frame (an
// unchanged, unflagged verdict) stays one string comparison.
func (a *chatStreamAggregator) keepContentFilterResults(target *aggregatedChoice, raw string) {
	if strings.TrimSpace(raw[1:len(raw)-1]) == "" {
		if target.contentFilterResults == "" {
			target.contentFilterResults = "{}"
		}
		return
	}
	if raw == target.contentFilterResults {
		return
	}
	merged := raw
	if target.contentFilterFlagged {
		merged = carryFlaggedCategories(target.contentFilterResults, raw)
		if merged == target.contentFilterResults {
			return
		}
	}
	if grow := len(merged) - len(target.contentFilterResults); grow > 0 && !a.take(grow) {
		return
	}
	if merged == raw {
		// Still a substring of the frame.
		merged = strings.Clone(raw)
	}
	target.contentFilterResults = merged
	if !target.contentFilterFlagged {
		target.contentFilterFlagged = hasFlaggedCategory(merged)
	}
}

// hasFlaggedCategory reports whether a content_filter_results object has a
// category whose "filtered" is true.
func hasFlaggedCategory(verdict string) bool {
	flagged := false
	gjson.Parse(verdict).ForEach(func(_, category gjson.Result) bool {
		flagged = category.Get("filtered").Type == gjson.True
		return !flagged
	})
	return flagged
}

// carryFlaggedCategories returns next with each category flagged in kept but
// not in next copied from kept. With nothing to carry it returns next itself.
func carryFlaggedCategories(kept, next string) string {
	merged := next
	gjson.Parse(kept).ForEach(func(key, category gjson.Result) bool {
		if category.Get("filtered").Type != gjson.True {
			return true
		}
		path := gjson.Escape(key.Str)
		if gjson.Get(merged, path+".filtered").Type == gjson.True {
			return true
		}
		if updated, err := sjson.SetRaw(merged, path, category.Raw); err == nil {
			merged = updated
		}
		return true
	})
	return merged
}

// CountBillableToolCalls records each assembled function call for per-call tool
// pricing, once per call — what the ordinary stream and non-stream handlers do.
// This path bypasses both, so without it priced tools settle for nothing.
func (a *chatStreamAggregator) CountBillableToolCalls(info *relaycommon.RelayInfo) {
	if a == nil || info == nil {
		return
	}
	for _, choice := range a.sortedChoices() {
		for _, index := range choice.toolOrder {
			if name := choice.toolCalls[index].name; name != "" {
				info.CountBillableToolCall(dto.BuildInCallFunctionCall, name)
			}
		}
	}
}

// AssembledText returns everything the model produced as text, for local token
// estimation when upstream never reported usage (the timeout case).
func (a *chatStreamAggregator) AssembledText() string {
	if a == nil {
		return ""
	}
	var b strings.Builder
	for _, choice := range a.sortedChoices() {
		b.WriteString(choice.reasoning.String())
		b.WriteString(choice.content.String())
		b.WriteString(choice.refusal.String())
		for _, index := range choice.toolOrder {
			call := choice.toolCalls[index]
			b.WriteString(call.name)
			b.WriteString(call.arguments.String())
		}
	}
	return b.String()
}

// Usage returns the usage upstream reported, or nil when it reported none.
func (a *chatStreamAggregator) Usage() *dto.Usage {
	if a == nil {
		return nil
	}
	return a.usage
}

// ToolCallCount mirrors the per-tool-call surcharge the stream path applies.
func (a *chatStreamAggregator) ToolCallCount() int {
	if a == nil {
		return 0
	}
	count := 0
	for _, choice := range a.choices {
		count += len(choice.toolOrder)
	}
	return count
}

// ReceivedAnything reports whether upstream sent a single parsable chunk.
func (a *chatStreamAggregator) ReceivedAnything() bool {
	return a != nil && a.sawAny
}

// HasOutput reports whether upstream produced anything a caller can use: text,
// reasoning, a refusal or a tool call with a name or arguments. A role-only
// delta, a usage frame or a tool call that got no further than its id is a
// parsable chunk but no output.
func (a *chatStreamAggregator) HasOutput() bool {
	if a == nil {
		return false
	}
	for _, choice := range a.choices {
		if choice.content.Len() > 0 || choice.reasoning.Len() > 0 || choice.refusal.Len() > 0 {
			return true
		}
		for _, call := range choice.toolCalls {
			if call.name != "" || call.arguments.Len() > 0 {
				return true
			}
		}
	}
	return false
}

func (a *chatStreamAggregator) sortedChoices() []*aggregatedChoice {
	indexes := make([]int, 0, len(a.choices))
	for index := range a.choices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	ordered := make([]*aggregatedChoice, 0, len(indexes))
	for _, index := range indexes {
		ordered = append(ordered, a.choices[index])
	}
	return ordered
}

// UpstreamFinishedWith reports whether upstream ended some choice with reason.
// It reads upstream's own reasons, which Snapshot may replace with "length".
func (a *chatStreamAggregator) UpstreamFinishedWith(reason string) bool {
	if a == nil {
		return false
	}
	for _, choice := range a.choices {
		if choice.finishReason == reason {
			return true
		}
	}
	return false
}

// UpstreamFinished reports whether upstream ended some choice with any reason.
func (a *chatStreamAggregator) UpstreamFinished() bool {
	if a == nil {
		return false
	}
	for _, choice := range a.choices {
		if choice.finishReason != "" {
			return true
		}
	}
	return false
}

// Snapshot builds the chat.completion body. An incomplete answer is reported as
// finish_reason "length", the standard signal for an incomplete completion:
// always once the budget dropped output (whatever reason upstream sent with
// it), and otherwise when truncated and upstream never sent a reason.
func (a *chatStreamAggregator) Snapshot(fallbackId string, truncated bool) *dto.OpenAITextResponse {
	if a == nil {
		return nil
	}
	id := a.id
	if id == "" {
		id = fallbackId
	}
	created := a.created
	if created == 0 {
		// Upstream chunks may omit created; 0 is not a valid unix timestamp for
		// a chat.completion and some clients reject it.
		created = time.Now().Unix()
	}
	response := &dto.OpenAITextResponse{
		Id:     id,
		Model:  a.model,
		Object: "chat.completion",
		// choices must marshal as [] rather than null: the OpenAI schema types it
		// as an array and SDKs iterate it directly. A usage-only stream (no content
		// frame at all) is exactly the case that would otherwise emit null.
		Choices: []dto.OpenAITextResponseChoice{},
		Created: created,
	}
	for _, choice := range a.sortedChoices() {
		message := dto.Message{Role: choice.role}
		if message.Role == "" {
			message.Role = "assistant"
		}
		message.SetStringContent(choice.content.String())
		if reasoning := choice.reasoning.String(); reasoning != "" {
			if choice.reasoningField == "reasoning" {
				message.Reasoning = &reasoning
			} else {
				message.ReasoningContent = &reasoning
			}
		}
		if len(choice.toolOrder) > 0 {
			calls := make([]dto.ToolCallResponse, 0, len(choice.toolOrder))
			for _, index := range choice.toolOrder {
				call := choice.toolCalls[index]
				calls = append(calls, dto.ToolCallResponse{
					ID:       call.id,
					Type:     call.callType,
					Function: dto.FunctionResponse{Name: call.name, Arguments: call.arguments.String()},
				})
			}
			// Message carries tool_calls as raw JSON; a marshal failure here must
			// not lose the assembled text, so the calls are simply omitted.
			if encoded, err := common.Marshal(calls); err == nil {
				message.ToolCalls = encoded
			}
		}
		// annotations is a field dto.Message models, so it survives ForceFormat
		// as on the plain path.
		if len(choice.annotations) > 0 {
			if encoded, err := common.Marshal(choice.annotations); err == nil {
				message.Annotations = encoded
			}
		}
		finishReason := choice.finishReason
		if a.overBudge || (finishReason == "" && truncated) {
			finishReason = "length"
		}
		response.Choices = append(response.Choices, dto.OpenAITextResponseChoice{
			Index:        choice.index,
			Message:      message,
			FinishReason: finishReason,
		})
	}
	if usage := a.Usage(); usage != nil {
		response.Usage = *usage
	}
	return response
}

// DecorateBody writes into a marshaled Snapshot the fields dto.OpenAITextResponse
// and dto.Message cannot hold, so an adapted response carries what a plain
// non-stream response would have: system_fingerprint, service_tier, Azure's
// prompt_filter_results and per-choice content_filter_results, per-choice
// refusal, and — when
// upstream reported it — upstream's own usage object rather than a re-marshaled
// dto.Usage.
//
// forceFormat is the channel's ForceFormat setting. With it the plain path
// sends upstream's body re-marshaled through dto.OpenAITextResponse, so only
// what that DTO models survives; here that leaves the null content of a
// text-less turn (dto.Message.Content keeps upstream's null) and drops every
// other decoration, upstream's usage object included. annotations is modelled
// by dto.Message, so Snapshot sets it and it survives either way.
//
// choices in body are in sortedChoices order (Snapshot builds them that way),
// so array position i is the i-th sorted choice.
func (a *chatStreamAggregator) DecorateBody(body []byte, upstreamUsage, forceFormat bool) ([]byte, error) {
	if a == nil {
		return body, nil
	}
	var err error
	if forceFormat {
		for i, choice := range a.sortedChoices() {
			if choice.reportsNullContent() {
				if body, err = sjson.SetRawBytes(body, "choices."+strconv.Itoa(i)+".message.content", []byte("null")); err != nil {
					return nil, err
				}
			}
		}
		return body, nil
	}
	if a.promptFilterResults != "" {
		if body, err = sjson.SetRawBytes(body, "prompt_filter_results", []byte(a.promptFilterResults)); err != nil {
			return nil, err
		}
	}
	if a.systemFingerprint != nil {
		if body, err = sjson.SetBytes(body, "system_fingerprint", *a.systemFingerprint); err != nil {
			return nil, err
		}
	}
	if a.serviceTier != "" {
		if body, err = sjson.SetRawBytes(body, "service_tier", []byte(a.serviceTier)); err != nil {
			return nil, err
		}
	}
	if upstreamUsage && a.usageRaw != "" {
		if body, err = sjson.SetRawBytes(body, "usage", []byte(a.usageRaw)); err != nil {
			return nil, err
		}
	}
	for i, choice := range a.sortedChoices() {
		if choice.contentFilterResults != "" {
			if body, err = sjson.SetRawBytes(body, "choices."+strconv.Itoa(i)+".content_filter_results", []byte(choice.contentFilterResults)); err != nil {
				return nil, err
			}
		}
		path := "choices." + strconv.Itoa(i) + ".message."
		if choice.hasRefusal {
			if body, err = sjson.SetBytes(body, path+"refusal", choice.refusal.String()); err != nil {
				return nil, err
			}
		}
		if choice.reportsNullContent() {
			if body, err = sjson.SetRawBytes(body, path+"content", []byte("null")); err != nil {
				return nil, err
			}
		}
	}
	return body, nil
}

// reportsNullContent: a message that is only a refusal or only tool calls has
// no content. OpenAI's non-stream reports content null there, not "", and
// agent frameworks branch on that null to recognize a tool-call turn. Relays
// that answer such turns with content "" stream it as "" too, so their "" is
// kept rather than replaced by null.
func (c *aggregatedChoice) reportsNullContent() bool {
	return c.content.Len() == 0 && !c.sawStringContent && (c.hasRefusal || len(c.toolOrder) > 0)
}
