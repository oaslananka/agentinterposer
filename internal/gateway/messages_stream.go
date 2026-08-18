package gateway

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type chatCompletionStreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content   string                    `json:"content"`
			ToolCalls []chatCompletionToolDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type chatCompletionToolDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type bufferedToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

type messagesStreamState struct {
	started      bool
	messageID    string
	model        string
	textOpen     bool
	textIndex    int
	nextIndex    int
	tools        map[int]*bufferedToolCall
	finishReason string
	inputTokens  int
	outputTokens int
}

func streamAnthropicMessages(w http.ResponseWriter, body io.Reader, requestedModel string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "streaming is not supported by this HTTP server")
		return
	}

	state := messagesStreamState{
		model: requestedModel,
		tools: make(map[int]*bufferedToolCall),
	}
	reader := bufio.NewReader(body)
	for {
		data, err := readSSEData(reader)
		if err != nil && !errors.Is(err, io.EOF) {
			state.fail(w, flusher, "unable to read upstream stream")
			return
		}
		if data != "" {
			if strings.TrimSpace(data) == "[DONE]" {
				if err := state.finish(w, flusher); err != nil {
					state.fail(w, flusher, err.Error())
				}
				return
			}
			var chunk chatCompletionStreamChunk
			if decodeErr := json.Unmarshal([]byte(data), &chunk); decodeErr != nil {
				state.fail(w, flusher, "upstream stream contained invalid JSON")
				return
			}
			if chunk.Error != nil {
				message := chunk.Error.Message
				if message == "" {
					message = "upstream stream returned an error"
				}
				state.failStream(w, flusher, message)
				return
			}
			if err := state.consume(w, flusher, chunk); err != nil {
				state.fail(w, flusher, err.Error())
				return
			}
		}
		if errors.Is(err, io.EOF) {
			if state.finishReason == "" {
				state.fail(w, flusher, "upstream stream ended before a finish reason")
				return
			}
			if finishErr := state.finish(w, flusher); finishErr != nil {
				state.fail(w, flusher, finishErr.Error())
			}
			return
		}
	}
}

func (s *messagesStreamState) consume(w http.ResponseWriter, flusher http.Flusher, chunk chatCompletionStreamChunk) error {
	if chunk.ID != "" {
		if s.messageID != "" && s.messageID != chunk.ID {
			return errors.New("upstream stream changed message id")
		}
		s.messageID = chunk.ID
	}
	if chunk.Model != "" {
		s.model = chunk.Model
	}
	if chunk.Usage != nil {
		s.inputTokens = chunk.Usage.PromptTokens
		s.outputTokens = chunk.Usage.CompletionTokens
	}
	if !s.started {
		if s.messageID == "" {
			return errors.New("upstream stream did not provide a message id")
		}
		if err := s.start(w, flusher); err != nil {
			return err
		}
	}
	if len(chunk.Choices) > 1 {
		return errors.New("upstream stream returned multiple choices")
	}
	if len(chunk.Choices) == 0 {
		return nil
	}
	choice := chunk.Choices[0]
	if choice.Index != 0 {
		return fmt.Errorf("unsupported upstream choice index %d", choice.Index)
	}
	if choice.Delta.Content != "" {
		if !s.textOpen {
			s.textIndex = s.nextIndex
			s.nextIndex++
			if err := writeAnthropicSSE(w, flusher, "content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": s.textIndex,
				"content_block": map[string]any{
					"type": "text",
					"text": "",
				},
			}); err != nil {
				return err
			}
			s.textOpen = true
		}
		if err := writeAnthropicSSE(w, flusher, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": s.textIndex,
			"delta": map[string]any{
				"type": "text_delta",
				"text": choice.Delta.Content,
			},
		}); err != nil {
			return err
		}
	}
	for _, delta := range choice.Delta.ToolCalls {
		if delta.Index < 0 {
			return errors.New("upstream stream returned a negative tool call index")
		}
		if delta.Type != "" && delta.Type != "function" {
			return fmt.Errorf("unsupported upstream tool call type %q", delta.Type)
		}
		tool := s.tools[delta.Index]
		if tool == nil {
			tool = &bufferedToolCall{}
			s.tools[delta.Index] = tool
		}
		if delta.ID != "" {
			if tool.ID != "" && tool.ID != delta.ID {
				return fmt.Errorf("upstream tool call index %d changed id", delta.Index)
			}
			tool.ID = delta.ID
		}
		if delta.Function.Name != "" {
			tool.Name += delta.Function.Name
		}
		if delta.Function.Arguments != "" {
			tool.Arguments.WriteString(delta.Function.Arguments)
		}
	}
	if choice.FinishReason != nil && *choice.FinishReason != "" {
		if s.finishReason != "" && s.finishReason != *choice.FinishReason {
			return errors.New("upstream stream changed finish reason")
		}
		s.finishReason = *choice.FinishReason
	}
	return nil
}

func (s *messagesStreamState) start(w http.ResponseWriter, flusher http.Flusher) error {
	s.started = true
	return writeAnthropicSSE(w, flusher, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            s.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         s.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]int{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	})
}

func (s *messagesStreamState) finish(w http.ResponseWriter, flusher http.Flusher) error {
	if !s.started {
		return errors.New("upstream stream ended before message_start")
	}
	if s.finishReason == "" {
		return errors.New("upstream stream ended before a finish reason")
	}
	if len(s.tools) > 0 && s.finishReason != "tool_calls" {
		return errors.New("upstream stream returned tool calls without finish_reason=tool_calls")
	}
	if len(s.tools) == 0 && s.finishReason == "tool_calls" {
		return errors.New("upstream stream ended with finish_reason=tool_calls but no tool calls")
	}
	if s.textOpen {
		if err := writeAnthropicSSE(w, flusher, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": s.textIndex,
		}); err != nil {
			return err
		}
		s.textOpen = false
	}

	indices := make([]int, 0, len(s.tools))
	for index := range s.tools {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, toolIndex := range indices {
		tool := s.tools[toolIndex]
		if tool.ID == "" || tool.Name == "" {
			return fmt.Errorf("upstream tool call index %d was incomplete", toolIndex)
		}
		arguments, err := compactJSONObject(json.RawMessage(tool.Arguments.String()))
		if err != nil {
			return fmt.Errorf("upstream tool call %q contained invalid arguments", tool.ID)
		}
		blockIndex := s.nextIndex
		s.nextIndex++
		if err := writeAnthropicSSE(w, flusher, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": blockIndex,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    tool.ID,
				"name":  tool.Name,
				"input": map[string]any{},
			},
		}); err != nil {
			return err
		}
		if err := writeAnthropicSSE(w, flusher, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": blockIndex,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": arguments,
			},
		}); err != nil {
			return err
		}
		if err := writeAnthropicSSE(w, flusher, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": blockIndex,
		}); err != nil {
			return err
		}
	}

	stopReason, err := anthropicStopReason(s.finishReason)
	if err != nil {
		return err
	}
	if err := writeAnthropicSSE(w, flusher, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]int{
			"input_tokens":  s.inputTokens,
			"output_tokens": s.outputTokens,
		},
	}); err != nil {
		return err
	}
	return writeAnthropicSSE(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
}

func (s *messagesStreamState) fail(w http.ResponseWriter, flusher http.Flusher, message string) {
	if !s.started {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", message)
		return
	}
	s.failStream(w, flusher, message)
}

func (s *messagesStreamState) failStream(w http.ResponseWriter, flusher http.Flusher, message string) {
	_ = writeAnthropicSSE(w, flusher, "error", map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    "api_error",
			"message": message,
		},
	})
}

func readSSEData(reader *bufio.Reader) (string, error) {
	var data []string
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				if len(data) > 0 {
					return strings.Join(data, "\n"), err
				}
			} else if strings.HasPrefix(line, "data:") {
				value := strings.TrimPrefix(line, "data:")
				value = strings.TrimPrefix(value, " ")
				data = append(data, value)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(data) > 0 {
				return strings.Join(data, "\n"), io.EOF
			}
			return "", err
		}
	}
}

func writeAnthropicSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
