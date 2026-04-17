package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolUseResponse builds a minimal Anthropic tool-use API response for testing.
func toolUseResponse(rowsJSON string) string {
	return `{"content":[{"type":"tool_use","id":"tu_1","name":"extract_bom","input":{"rows":` + rowsJSON + `}}]}`
}

// TestCallAnthropic_UsesToolUseAndHighMaxTokens verifies that every request to
// the Anthropic API uses the extract_bom tool (forced via tool_choice) and sets
// max_tokens high enough to avoid truncating large drawings.
func TestCallAnthropic_UsesToolUseAndHighMaxTokens(t *testing.T) {
	var captured []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(toolUseResponse("[]")))
	}))
	defer ts.Close()

	orig := anthropicAPIURL
	anthropicAPIURL = ts.URL
	defer func() { anthropicAPIURL = orig }()

	_, err := callAnthropic("test drawing text", "test-api-key")
	require.NoError(t, err)

	var body struct {
		MaxTokens  int    `json:"max_tokens"`
		ToolChoice struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tool_choice"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(captured, &body))

	assert.GreaterOrEqual(t, body.MaxTokens, 32000,
		"max_tokens must be ≥32000 to avoid truncating large drawings")
	assert.Equal(t, "tool", body.ToolChoice.Type,
		"tool_choice.type must be 'tool' to force structured output")
	assert.Equal(t, "extract_bom", body.ToolChoice.Name)
	require.Len(t, body.Tools, 1)
	assert.Equal(t, "extract_bom", body.Tools[0].Name)
	// Last message must be the user turn (no assistant prefill).
	require.NotEmpty(t, body.Messages)
	assert.Equal(t, "user", body.Messages[len(body.Messages)-1].Role)
}

// TestCallAnthropic_ReturnsRowsFromToolInput verifies that the rows array from
// the tool input is extracted and returned as a JSON string.
func TestCallAnthropic_ReturnsRowsFromToolInput(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(toolUseResponse(`[{"rawLabel":"1","description":"Wire","rawQuantity":"1","unit":"M","customerPartNumber":"","manufacturerPartNumber":"W-001","supplierReference":"","notes":"","confidence":0.9,"flags":[]}]`)))
	}))
	defer ts.Close()

	orig := anthropicAPIURL
	anthropicAPIURL = ts.URL
	defer func() { anthropicAPIURL = orig }()

	text, err := callAnthropic("drawing", "key")
	require.NoError(t, err)
	assert.True(t, len(text) > 0 && text[0] == '[',
		"returned text must be a JSON array, got: %q", text[:min(30, len(text))])
}

// TestParseBOMRows_ProseResponseWithNoJSON is a regression test for the error
// "no JSON array in response: I need to carefully parse this complex drawing..."
// The prefill fix prevents this from reaching parseBOMRows, but the error path
// should still be explicit.
func TestParseBOMRows_ProseResponseWithNoJSON(t *testing.T) {
	prose := "I need to carefully parse this complex drawing. Let me work through each section systematically. Part Reference Table items..."
	_, _, err := parseBOMRows(prose, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no JSON array")
}

