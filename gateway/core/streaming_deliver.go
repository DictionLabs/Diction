package core

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/coder/websocket"
)

func (s *streamSession) deliverText() {
	s.enhanceEnabled = s.query.Get("enhance") == valueTrue
	if s.g.OnTranscription != nil {
		s.g.OnTranscription(s.ctx, s.backend.Name, s.sttMs, len(s.text), s.audioDurationMs, s.enhanceEnabled, false)
	}

	s.resolveIntent()
	if s.query.Get("split_enhance") == valueTrue && s.inline != nil &&
		s.enhanceEnabled && s.text != "" && !s.isEdit {
		s.g.writeSplitEnhanceFrames(s.ctx, s.conn, splitEnhanceInput{
			raw:         s.text,
			contextJSON: s.enhanceCtxJSON,
			intent:      s.intentParam,
			enhance:     postDeliveryOrInline(s.inline, s.postDelivery),
		})
		return
	}

	mode, mergeHandled, ppCtx, cancelPP := s.mergePhones()
	defer cancelPP()
	if mode, ok := s.postProcess(ppCtx, mode, mergeHandled); ok {
		s.writeResult(mode)
	}
}

func (s *streamSession) resolveIntent() {
	s.intentParam = s.query.Get("intent")
	s.isEdit = s.intentParam == intentEdit || s.intentParam == intentEditSelected
	s.enhanceCtxJSON = WithContextLanguage(s.contextJSON, s.language)
	if s.inline == nil || !s.enhanceEnabled || s.text == "" {
		return
	}
	s.intentParam, s.autoEdit = resolveIntent(s.ctx, s.r, s.text, s.enhanceCtxJSON, s.intentParam)
	if !s.autoEdit {
		return
	}
	s.isEdit = true
	frame, _ := json.Marshal(intentFrame{Type: "intent", Intent: s.intentParam, Source: "auto"})
	if err := s.conn.Write(s.ctx, websocket.MessageText, frame); err != nil {
		log.Printf("ws write intent frame: %v", err)
	}
}

func (s *streamSession) mergePhones() (mode string, handled bool, ppCtx context.Context, cancel context.CancelFunc) {
	ppCtx, cancel = s.ctx, func() {}
	if s.phoneTranscripts == nil || s.isEdit {
		return "", false, ppCtx, cancel
	}
	var cleanupBy time.Time
	s.text, mode, handled, cleanupBy = mergeInline(s.ctx, s.text, s.phoneTranscripts.waitAndReport(s.ctx, s.sttDoneAt), s.enhanceCtxJSON)
	if !cleanupBy.IsZero() {
		ppCtx, cancel = context.WithDeadline(s.ctx, cleanupBy)
	}
	return mode, handled, ppCtx, cancel
}

func (s *streamSession) postProcess(ppCtx context.Context, mode string, mergeHandled bool) (string, bool) {
	if mergeHandled || s.inline == nil || !s.enhanceEnabled || s.text == "" {
		return mode, true
	}
	intent := s.intentParam
	resultText, resultMode, err := s.inline(ppCtx, s.text, s.enhanceCtxJSON, intent)
	if err == nil {
		s.text = resultText
		return resultMode, true
	}
	log.Printf("ws post-process: %v", err)
	if intent == intentEdit || intent == intentEditSelected {
		s.writeEditFailed(intent)
		return mode, false
	}
	ReportEnhanceSkipped(s.ctx, EnhanceSkipReason(s.ctx, err))
	return mode, true
}

func (s *streamSession) writeEditFailed(intent string) {
	editFailed := &ErrorEvent{
		Source:     sourceSTT,
		Kind:       "stt_post_process",
		Endpoint:   routeAudioStream,
		InputChars: len(s.text),
		Hint:       "edit intent post-process failed; returning status=failed",
	}
	result, _ := json.Marshal(streamResult{Text: "", Mode: intent, Status: statusFailed, AutoEdit: s.autoEdit})
	failType := ""
	if err := s.conn.Write(s.ctx, websocket.MessageText, result); err != nil {
		log.Printf("ws write edit-failed result: %v", err)
		failType = errTypeSTTError
	}
	s.out.end(s.ctx, editFailed, failType, websocket.StatusNormalClosure, "")
}

func (s *streamSession) writeResult(mode string) {
	result, _ := json.Marshal(streamResult{Text: s.text, Mode: mode, AutoEdit: s.autoEdit && mode == intentEdit})
	if err := s.conn.Write(s.ctx, websocket.MessageText, result); err != nil {
		log.Printf("ws write result: %v", err)
		s.out.end(s.ctx, &ErrorEvent{
			Source:   sourceStreaming,
			Kind:     "ws_write",
			Endpoint: routeAudioStream,
			Hint:     "client gone before result write",
		}, "", 0, "")
		return
	}
	s.out.end(s.ctx, nil, "", websocket.StatusNormalClosure, "")
}
