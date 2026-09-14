package common

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/tidwall/gjson"
)

// SetReceivedEstimator installs a request-local factory; SDK retries reset its state.
func (s *StreamSession) SetReceivedEstimator(factory func() func(string) int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receivedFactory = factory
}

// RecordReceivedMedia marks received media independently from successful delivery.
// audioTokens must come from the existing codec estimator, not raw byte length.
func (s *StreamSession) RecordReceivedMedia(audioTokens int) {
	if !s.Active() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.ReceivedResponse = true
	s.state.ReceivedAudioOutput += max(0, audioTokens)
}

// observeReceivedLocked counts only parsed business responses before downstream I/O.
// The separate bounded content tracker never marks the real stream as delivered.
func (s *StreamSession) observeReceivedLocked(event string, data []byte, v gjson.Result) {
	kind := v.Get("type").String()
	if kind == "" {
		kind = event
	}
	if kind == "ping" || kind == "heartbeat" {
		return
	}
	if s.format == types.RelayFormatOpenAIRealtime && !strings.HasPrefix(kind, "response.") {
		return // Session and input acknowledgements are not an upstream answer.
	}
	s.state.ReceivedResponse = true
	if s.receivedFactory == nil {
		return
	}
	if s.received == nil {
		s.received = NewStreamSession(s.format)
		s.received.receivedOnly = true
		s.receivedEstimate = s.receivedFactory()
	}
	s.received.CommitDelivery(data)
	text := s.received.TakeDeliveredText()
	// Native adapters convert these text fields later; collect them before that write.
	if text == "" {
		switch s.ChannelType {
		case constant.ChannelTypeCohere:
			if !v.Get("is_finished").Bool() {
				text = v.Get("text").String()
			}
		case constant.ChannelTypeBaidu:
			text = v.Get("result").String()
		case constant.ChannelTypeDify:
			if v.Get("event").String() == "message" || v.Get("event").String() == "agent_message" {
				text = v.Get("answer").String()
			}
		case constant.ChannelTypeXunfei:
			var native strings.Builder
			for _, part := range v.Get("payload.choices.text").Array() {
				native.WriteString(part.Get("content").String())
			}
			text = native.String()
		case constant.ChannelTypeTencent:
			var native strings.Builder
			for _, choice := range v.Get("Choices").Array() {
				native.WriteString(choice.Get("Delta.Content").String())
			}
			text = native.String()
		case constant.ChannelTypeCoze:
			if event == "conversation.message.delta" {
				text = v.Get("content").String()
			}
		case constant.ChannelTypePaLM:
			if candidates := v.Get("candidates").Array(); len(candidates) > 0 {
				text = candidates[0].Get("content").String()
			}
		case constant.ChannelTypeOllama:
			text = v.Get("message.content").String() + v.Get("response").String()
		}
	}
	if text != "" {
		s.state.ReceivedOutput += s.receivedEstimate(text)
	}
}
