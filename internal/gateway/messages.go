package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/oaslananka/agentinterposer/internal/compatibility"
)

const supportedAnthropicMessagesVersion = "2023-06-01"

type anthropicMessagesRequest struct {
	Model         string                  `json:"model"`
	MaxTokens     *int                    `json:"max_tokens"`
	Metadata      *anthropicMetadata      `json:"metadata"`
	Messages      []anthropicInputMessage `json:"messages"`
	System        json.RawMessage         `json:"system"`
	Stream        bool                    `json:"stream"`
	Temperature   *float64                `json:"temperature"`
	TopK          *int                    `json:"top_k"`
	TopP          *float64                `json:"top_p"`
	ServiceTier   *string                 `json:"service_tier"`
	StopSequences []string                `json:"stop_sequences"`
	Tools         []anthropicTool         `json:"tools"`
	ToolChoice    *anthropicToolChoice    `json:"tool_choice"`
	Thinking      json.RawMessage         `json:"thinking"`
}

type anthropicInputMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicMetadata struct {
	UserID string `json:"user_id"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl"`
}

type anthropicContentBlock struct {
	Type         string                 `json:"type"`
	CacheControl *anthropicCacheControl `json:"cache_control"`
	Text         string                 `json:"text"`
	Citations    json.RawMessage        `json:"citations"`
	Source       *anthropicImageSource  `json:"source"`
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Input        json.RawMessage        `json:"input"`
	Caller       *anthropicToolCaller   `json:"caller"`
	ToolUseID    string                 `json:"tool_use_id"`
	Content      json.RawMessage        `json:"content"`
	IsError      bool                   `json:"is_error"`
}

type anthropicToolCaller struct {
	Type   string `json:"type"`
	ToolID string `json:"tool_id"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
	FileID    string `json:"file_id"`
}

type anthropicTool struct {
	Type         string                 `json:"type"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  json.RawMessage        `json:"input_schema"`
	Strict       *bool                  `json:"strict"`
	CacheControl *anthropicCacheControl `json:"cache_control"`
}

type anthropicToolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name"`
	DisableParallelToolUse bool   `json:"disable_parallel_tool_use"`
}

type chatCompletionRequest struct {
	Model             string             `json:"model"`
	Messages          []chatMessage      `json:"messages"`
	MaxTokens         int                `json:"max_tokens"`
	Stream            bool               `json:"stream"`
	StreamOptions     *chatStreamOptions `json:"stream_options,omitempty"`
	Temperature       *float64           `json:"temperature,omitempty"`
	TopP              *float64           `json:"top_p,omitempty"`
	Tools             []chatTool         `json:"tools,omitempty"`
	ToolChoice        any                `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool              `json:"parallel_tool_calls,omitempty"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
	Strict      *bool          `json:"strict,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatCompletionResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role      string         `json:"role"`
			Content   *string        `json:"content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func validateAnthropicMessagesVersion(raw string) error {
	version := strings.TrimSpace(raw)
	if version == "" {
		return errors.New("anthropic-version header is required")
	}
	if version != supportedAnthropicMessagesVersion {
		return fmt.Errorf("unsupported anthropic-version %q; expected %s", version, supportedAnthropicMessagesVersion)
	}
	return nil
}

func (h *handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	if h.upstreamURL == "" {
		writeAnthropicError(w, http.StatusServiceUnavailable, "api_error", "upstream is not configured")
		return
	}
	if err := validateAnthropicMessagesVersion(r.Header.Get("anthropic-version")); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.maxRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeAnthropicError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "unable to read request body")
		return
	}

	var request anthropicMessagesRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON request body: "+err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON request body: trailing JSON value")
		return
	}
	if len(bytes.TrimSpace(request.Thinking)) > 0 && string(bytes.TrimSpace(request.Thinking)) != "null" {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "thinking is not supported by the Messages adapter yet")
		return
	}

	translated, err := translateAnthropicRequest(request)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	translated.Model = h.selectMessagesModel(translated)
	translatedBody, err := json.Marshal(translated)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "unable to encode upstream request")
		return
	}

	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	case <-r.Context().Done():
		writeAnthropicError(w, http.StatusRequestTimeout, "api_error", "request cancelled while waiting for upstream capacity")
		return
	}

	upstreamResponse, err := h.doUpstream(r, translatedBody, "/chat/completions")
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "upstream request failed")
		return
	}
	defer upstreamResponse.Body.Close()
	copyHeader(w.Header(), upstreamResponse.Header, "Retry-After")

	if request.Stream && upstreamResponse.StatusCode >= 200 && upstreamResponse.StatusCode < 300 {
		if !strings.HasPrefix(strings.ToLower(upstreamResponse.Header.Get("Content-Type")), "text/event-stream") {
			writeAnthropicError(w, http.StatusBadGateway, "api_error", "streaming upstream returned a non-SSE response")
			return
		}
		streamAnthropicMessages(w, upstreamResponse.Body, request.Model)
		return
	}

	upstreamBody, err := io.ReadAll(upstreamResponse.Body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "unable to read upstream response")
		return
	}
	if upstreamResponse.StatusCode < 200 || upstreamResponse.StatusCode >= 300 {
		writeAnthropicUpstreamError(w, upstreamResponse.StatusCode, upstreamBody)
		return
	}

	var chatResponse chatCompletionResponse
	if err := json.Unmarshal(upstreamBody, &chatResponse); err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "upstream returned invalid JSON")
		return
	}
	anthropicResponse, err := translateChatResponse(chatResponse, request.Model)
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, anthropicResponse)
}

func (h *handler) selectMessagesModel(request chatCompletionRequest) string {
	if !chatRequestHasVisionInput(request) {
		return request.Model
	}
	return h.selectCertifiedModel(
		request.Model,
		compatibility.CapabilityChatCompletions,
		compatibility.CapabilityVisionInput,
	)
}

func chatRequestHasVisionInput(request chatCompletionRequest) bool {
	for _, message := range request.Messages {
		parts, ok := message.Content.([]chatContentPart)
		if !ok {
			continue
		}
		for _, part := range parts {
			if part.Type == "image_url" && part.ImageURL != nil {
				return true
			}
		}
	}
	return false
}

func translateAnthropicRequest(request anthropicMessagesRequest) (chatCompletionRequest, error) {
	if err := validateAnthropicMetadata(request.Metadata); err != nil {
		return chatCompletionRequest{}, err
	}
	if strings.TrimSpace(request.Model) == "" {
		return chatCompletionRequest{}, errors.New("model is required")
	}
	if request.MaxTokens == nil {
		return chatCompletionRequest{}, errors.New("max_tokens is required")
	}
	if *request.MaxTokens < 0 {
		return chatCompletionRequest{}, errors.New("max_tokens must be non-negative")
	}
	if *request.MaxTokens == 0 {
		return chatCompletionRequest{}, errors.New("max_tokens=0 prompt-cache-only requests are not supported by the Messages adapter")
	}
	if request.TopK != nil {
		return chatCompletionRequest{}, errors.New("top_k is not supported by the Messages adapter")
	}
	if request.ServiceTier != nil {
		return chatCompletionRequest{}, errors.New("service_tier is not supported by the Messages adapter")
	}
	if request.Temperature != nil && (*request.Temperature < 0 || *request.Temperature > 1) {
		return chatCompletionRequest{}, errors.New("temperature must be between 0 and 1")
	}
	if request.TopP != nil && (*request.TopP < 0 || *request.TopP > 1) {
		return chatCompletionRequest{}, errors.New("top_p must be between 0 and 1")
	}
	if len(request.StopSequences) > 0 {
		return chatCompletionRequest{}, errors.New("stop_sequences are not supported by the Messages adapter yet")
	}

	translated := chatCompletionRequest{
		Model:       request.Model,
		MaxTokens:   *request.MaxTokens,
		Stream:      request.Stream,
		Temperature: request.Temperature,
		TopP:        request.TopP,
	}
	if request.Stream {
		translated.StreamOptions = &chatStreamOptions{IncludeUsage: true}
	}

	system, err := decodeTextContent(request.System, "system")
	if err != nil {
		return chatCompletionRequest{}, err
	}
	if system != "" {
		translated.Messages = append(translated.Messages, chatMessage{Role: "system", Content: system})
	}

	toolNames := make(map[string]string)
	var pendingToolUses map[string]string
	for _, message := range request.Messages {
		messages, names, err := translateAnthropicMessage(message)
		if err != nil {
			return chatCompletionRequest{}, err
		}
		if err := validateToolResultSequence(pendingToolUses, message.Role, messages); err != nil {
			return chatCompletionRequest{}, err
		}
		pendingToolUses = names
		for id, name := range names {
			toolNames[id] = name
		}
		for i := range messages {
			if messages[i].Role == "tool" && messages[i].Name == "" {
				messages[i].Name = toolNames[messages[i].ToolCallID]
			}
		}
		translated.Messages = append(translated.Messages, messages...)
	}
	if len(pendingToolUses) > 0 {
		return chatCompletionRequest{}, errors.New("assistant tool_use blocks require an immediately following user tool_result message for every tool_use")
	}

	for _, tool := range request.Tools {
		if err := validateAnthropicCacheControl(tool.CacheControl); err != nil {
			return chatCompletionRequest{}, fmt.Errorf("tool %q cache_control: %w", tool.Name, err)
		}
		if tool.Type != "" {
			return chatCompletionRequest{}, fmt.Errorf("tool type %q is not supported; only custom client tools are supported", tool.Type)
		}
		if strings.TrimSpace(tool.Name) == "" {
			return chatCompletionRequest{}, errors.New("tool name is required")
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil || schema == nil {
			return chatCompletionRequest{}, fmt.Errorf("tool %q has an invalid input_schema", tool.Name)
		}
		translated.Tools = append(translated.Tools, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  schema,
				Strict:      tool.Strict,
			},
		})
	}

	if request.ToolChoice != nil {
		if len(translated.Tools) == 0 && request.ToolChoice.Type != "none" {
			return chatCompletionRequest{}, errors.New("tool_choice requires tools")
		}
		switch request.ToolChoice.Type {
		case "", "auto":
			translated.ToolChoice = "auto"
		case "any":
			translated.ToolChoice = "required"
		case "none":
			translated.ToolChoice = "none"
		case "tool":
			if strings.TrimSpace(request.ToolChoice.Name) == "" {
				return chatCompletionRequest{}, errors.New("tool_choice.name is required for type=tool")
			}
			translated.ToolChoice = map[string]any{
				"type":     "function",
				"function": map[string]string{"name": request.ToolChoice.Name},
			}
		default:
			return chatCompletionRequest{}, fmt.Errorf("unsupported tool_choice type %q", request.ToolChoice.Type)
		}
		if request.ToolChoice.DisableParallelToolUse {
			parallel := false
			translated.ParallelToolCalls = &parallel
		}
	}

	return translated, nil
}

func validateToolResultSequence(pendingToolUses map[string]string, messageRole string, messages []chatMessage) error {
	resultIDs := make([]string, 0)
	for _, message := range messages {
		if message.Role == "tool" {
			resultIDs = append(resultIDs, message.ToolCallID)
		}
	}

	if len(pendingToolUses) == 0 {
		if len(resultIDs) > 0 {
			return errors.New("tool_result must reference a preceding tool_use in the immediately prior assistant message")
		}
		return nil
	}
	if messageRole != "user" || len(resultIDs) != len(pendingToolUses) {
		return errors.New("tool_result message must immediately follow assistant tool_use and include every tool_use id")
	}

	seen := make(map[string]struct{}, len(resultIDs))
	for _, id := range resultIDs {
		if _, duplicate := seen[id]; duplicate {
			return errors.New("tool_result ids must match every tool_use id from the immediately preceding assistant message")
		}
		seen[id] = struct{}{}
		if _, ok := pendingToolUses[id]; !ok {
			return errors.New("tool_result ids must match every tool_use id from the immediately preceding assistant message")
		}
	}
	return nil
}

func translateAnthropicMessage(message anthropicInputMessage) ([]chatMessage, map[string]string, error) {
	toolNames := make(map[string]string)
	if message.Role != "user" && message.Role != "assistant" {
		return nil, nil, fmt.Errorf("unsupported message role %q", message.Role)
	}

	if text, ok, err := decodeJSONString(message.Content); err != nil {
		return nil, nil, fmt.Errorf("invalid %s message content: %w", message.Role, err)
	} else if ok {
		return []chatMessage{{Role: message.Role, Content: text}}, toolNames, nil
	}

	blocks, err := decodeAnthropicContentBlocks(message.Content)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid %s message content: %w", message.Role, err)
	}

	if message.Role == "assistant" {
		var text strings.Builder
		var calls []chatToolCall
		for _, block := range blocks {
			if err := validateAnthropicCacheControl(block.CacheControl); err != nil {
				return nil, nil, fmt.Errorf("assistant cache_control: %w", err)
			}
			switch block.Type {
			case "text":
				text.WriteString(block.Text)
			case "tool_use":
				if block.ID == "" || block.Name == "" {
					return nil, nil, errors.New("tool_use requires id and name")
				}
				if block.Caller != nil {
					if block.Caller.Type != "direct" {
						return nil, nil, fmt.Errorf("tool_use %q has unsupported caller %q; server-tool callers are not supported", block.ID, block.Caller.Type)
					}
					if block.Caller.ToolID != "" {
						return nil, nil, fmt.Errorf("tool_use %q direct caller must not include tool_id", block.ID)
					}
				}
				arguments, err := compactJSONObject(block.Input)
				if err != nil {
					return nil, nil, fmt.Errorf("tool_use %q has invalid input: %w", block.ID, err)
				}
				calls = append(calls, chatToolCall{
					ID:   block.ID,
					Type: "function",
					Function: chatToolFunction{
						Name:      block.Name,
						Arguments: arguments,
					},
				})
				toolNames[block.ID] = block.Name
			default:
				return nil, nil, fmt.Errorf("unsupported assistant content block %q", block.Type)
			}
		}
		out := chatMessage{Role: "assistant", ToolCalls: calls}
		if text.Len() > 0 {
			out.Content = text.String()
		}
		return []chatMessage{out}, toolNames, nil
	}

	if containsAnthropicImage(blocks) {
		message, err := translateAnthropicImageMessage(blocks)
		if err != nil {
			return nil, nil, err
		}
		return []chatMessage{message}, toolNames, nil
	}

	var result []chatMessage
	var text strings.Builder
	seenText := false
	for _, block := range blocks {
		if err := validateAnthropicCacheControl(block.CacheControl); err != nil {
			return nil, nil, fmt.Errorf("user cache_control: %w", err)
		}
		switch block.Type {
		case "text":
			seenText = true
			text.WriteString(block.Text)
		case "tool_result":
			if seenText {
				return nil, nil, errors.New("tool_result blocks must appear before text blocks in user content")
			}
			if block.ToolUseID == "" {
				return nil, nil, errors.New("tool_result requires tool_use_id")
			}
			content, err := decodeTextContent(block.Content, "tool_result")
			if err != nil {
				return nil, nil, err
			}
			if block.IsError {
				content, err = encodeToolResultError(content)
				if err != nil {
					return nil, nil, err
				}
			}
			result = append(result, chatMessage{Role: "tool", Content: content, ToolCallID: block.ToolUseID})
		default:
			return nil, nil, fmt.Errorf("unsupported user content block %q", block.Type)
		}
	}
	if text.Len() > 0 {
		result = append(result, chatMessage{Role: "user", Content: text.String()})
	}
	return result, toolNames, nil
}

func containsAnthropicImage(blocks []anthropicContentBlock) bool {
	for _, block := range blocks {
		if block.Type == "image" {
			return true
		}
	}
	return false
}

func translateAnthropicImageMessage(blocks []anthropicContentBlock) (chatMessage, error) {
	parts := make([]chatContentPart, 0, len(blocks))
	for _, block := range blocks {
		if err := validateAnthropicCacheControl(block.CacheControl); err != nil {
			return chatMessage{}, fmt.Errorf("user cache_control: %w", err)
		}
		switch block.Type {
		case "text":
			parts = append(parts, chatContentPart{Type: "text", Text: block.Text})
		case "image":
			part, err := translateAnthropicImage(block.Source)
			if err != nil {
				return chatMessage{}, err
			}
			parts = append(parts, part)
		case "tool_result":
			return chatMessage{}, errors.New("tool_result cannot be combined with image content in the Messages adapter yet")
		default:
			return chatMessage{}, fmt.Errorf("unsupported user content block %q", block.Type)
		}
	}
	return chatMessage{Role: "user", Content: parts}, nil
}

func translateAnthropicImage(source *anthropicImageSource) (chatContentPart, error) {
	if source == nil {
		return chatContentPart{}, errors.New("image source is required")
	}
	switch source.Type {
	case "base64":
		return translateAnthropicBase64Image(source)
	case "url":
		return translateAnthropicURLImage(source.URL)
	default:
		return chatContentPart{}, fmt.Errorf("unsupported image source type %q", source.Type)
	}
}

func translateAnthropicURLImage(rawURL string) (chatContentPart, error) {
	if strings.TrimSpace(rawURL) == "" {
		return chatContentPart{}, errors.New("image URL is required")
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return chatContentPart{}, errors.New("image URL must be an absolute HTTP(S) URL")
	}
	return chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: rawURL}}, nil
}

func translateAnthropicBase64Image(source *anthropicImageSource) (chatContentPart, error) {
	if strings.TrimSpace(source.MediaType) == "" {
		return chatContentPart{}, errors.New("base64 image media_type is required")
	}
	switch source.MediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return chatContentPart{}, fmt.Errorf("unsupported image media_type %q", source.MediaType)
	}
	if strings.TrimSpace(source.Data) == "" {
		return chatContentPart{}, errors.New("base64 image data is required")
	}
	if _, err := base64.StdEncoding.DecodeString(source.Data); err != nil {
		return chatContentPart{}, errors.New("base64 image data is invalid")
	}
	return chatContentPart{
		Type: "image_url",
		ImageURL: &chatImageURL{
			URL: "data:" + source.MediaType + ";base64," + source.Data,
		},
	}, nil
}

type agentinterposerToolResultErrorEnvelope struct {
	ToolResult agentinterposerToolResultError `json:"agentinterposer_tool_result"`
}

type agentinterposerToolResultError struct {
	Version int    `json:"version"`
	IsError bool   `json:"is_error"`
	Content string `json:"content"`
}

func encodeToolResultError(content string) (string, error) {
	encoded, err := json.Marshal(agentinterposerToolResultErrorEnvelope{
		ToolResult: agentinterposerToolResultError{
			Version: 1,
			IsError: true,
			Content: content,
		},
	})
	if err != nil {
		return "", fmt.Errorf("encode tool_result error: %w", err)
	}
	return string(encoded), nil
}

func decodeTextContent(raw json.RawMessage, field string) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	if text, ok, err := decodeJSONString(raw); err != nil {
		return "", fmt.Errorf("invalid %s content: %w", field, err)
	} else if ok {
		return text, nil
	}
	blocks, err := decodeAnthropicContentBlocks(raw)
	if err != nil {
		return "", fmt.Errorf("invalid %s content: %w", field, err)
	}
	var text strings.Builder
	for _, block := range blocks {
		if err := validateAnthropicCacheControl(block.CacheControl); err != nil {
			return "", fmt.Errorf("%s cache_control: %w", field, err)
		}
		if block.Type != "text" {
			return "", fmt.Errorf("unsupported %s content block %q", field, block.Type)
		}
		text.WriteString(block.Text)
	}
	return text.String(), nil
}

// Anthropic metadata and cache-control values are validated for protocol fidelity,
// but they are intentionally not forwarded to a different upstream provider.
func validateAnthropicMetadata(metadata *anthropicMetadata) error {
	if metadata == nil {
		return nil
	}
	if len([]rune(metadata.UserID)) > 512 {
		return errors.New("metadata.user_id must be at most 512 characters")
	}
	return nil
}

func validateAnthropicCacheControl(cacheControl *anthropicCacheControl) error {
	if cacheControl == nil {
		return nil
	}
	if cacheControl.Type != "ephemeral" {
		return fmt.Errorf("type %q is not supported; expected ephemeral", cacheControl.Type)
	}
	switch cacheControl.TTL {
	case "", "5m", "1h":
		return nil
	default:
		return fmt.Errorf("ttl %q is not supported; expected 5m or 1h", cacheControl.TTL)
	}
}

func decodeAnthropicContentBlocks(raw json.RawMessage) ([]anthropicContentBlock, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var blocks []anthropicContentBlock
	if err := decoder.Decode(&blocks); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing JSON value")
	}
	return blocks, nil
}

func decodeJSONString(raw json.RawMessage) (string, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false, nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err != nil {
		return "", false, err
	}
	return text, true, nil
}

func compactJSONObject(raw json.RawMessage) (string, error) {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return "", errors.New("expected a JSON object")
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func translateChatResponse(response chatCompletionResponse, requestedModel string) (map[string]any, error) {
	if len(response.Choices) == 0 {
		return nil, errors.New("upstream response contained no choices")
	}
	choice := response.Choices[0]
	content := make([]any, 0, 1+len(choice.Message.ToolCalls))
	if choice.Message.Content != nil && *choice.Message.Content != "" {
		content = append(content, map[string]any{"type": "text", "text": *choice.Message.Content})
	}
	for _, call := range choice.Message.ToolCalls {
		var input map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil || input == nil {
			return nil, fmt.Errorf("upstream tool call %q contained invalid arguments", call.ID)
		}
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    call.ID,
			"name":  call.Function.Name,
			"input": input,
		})
	}

	stopReason, err := anthropicStopReason(choice.FinishReason)
	if err != nil {
		return nil, err
	}
	model := response.Model
	if model == "" {
		model = requestedModel
	}
	return map[string]any{
		"id":            response.ID,
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]int{
			"input_tokens":  response.Usage.PromptTokens,
			"output_tokens": response.Usage.CompletionTokens,
		},
	}, nil
}

func anthropicStopReason(finishReason string) (string, error) {
	switch finishReason {
	case "stop":
		return "end_turn", nil
	case "length":
		return "max_tokens", nil
	case "tool_calls":
		return "tool_use", nil
	case "content_filter":
		return "refusal", nil
	default:
		return "", fmt.Errorf("unsupported upstream finish_reason %q", finishReason)
	}
}

func writeAnthropicUpstreamError(w http.ResponseWriter, status int, body []byte) {
	message := "upstream request failed"
	var decoded struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &decoded) == nil {
		switch value := decoded.Error.(type) {
		case string:
			if value != "" {
				message = value
			}
		case map[string]any:
			if text, ok := value["message"].(string); ok && text != "" {
				message = text
			}
		}
	}
	writeAnthropicError(w, status, anthropicErrorType(status), message)
}

func anthropicErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "api_error"
	}
}

func writeAnthropicError(w http.ResponseWriter, status int, errorType, message string) {
	writeJSON(w, status, map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    errorType,
			"message": message,
		},
	})
}
