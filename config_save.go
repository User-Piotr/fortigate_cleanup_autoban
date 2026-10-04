package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var errSaveOutcomeUnknown = errors.New("save request returned no response")

type configGlobal struct {
	CfgSave string `json:"cfg-save"`
}

func (c *apiClient) cfgSaveMode() (string, error) {
	status, body, err := c.do(http.MethodGet, "system/global", nil)
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("read cfg-save mode: %s", summarizeError(status, body, err))
	}

	var envelope struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("parse system/global response: %w", err)
	}

	// FortiOS may return results as one object or a one-element array.
	var rawMode string
	var one configGlobal
	if err := json.Unmarshal(envelope.Results, &one); err == nil && one.CfgSave != "" {
		rawMode = one.CfgSave
	}

	if rawMode == "" {
		var many []configGlobal
		if err := json.Unmarshal(envelope.Results, &many); err == nil && len(many) == 1 {
			rawMode = many[0].CfgSave
		}
	}

	if rawMode == "" {
		return "", fmt.Errorf("cfg-save is missing in system/global response")
	}

	mode := strings.ToLower(rawMode)
	switch mode {
	case "automatic", "manual", "revert":
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported cfg-save mode %q", mode)
	}
}

func (c *apiClient) saveConfigIfNeeded() (string, error) {
	currentCfgSaveMode, err := c.cfgSaveMode()
	if err != nil {
		return "", err
	}

	switch currentCfgSaveMode {
	case "automatic":
		return "", nil
	case "manual", "revert":
		// These modes keep changes in memory until they are explicitly saved.
	default:
		return "", fmt.Errorf("unsupported cfg-save mode %q", currentCfgSaveMode)
	}

	status, body, err := c.doMonitor(
		http.MethodPost,
		"system/config/save?vdom=root",
		[]byte(`{}`),
	)
	if err != nil {
		return "", fmt.Errorf("%w: %s", errSaveOutcomeUnknown, summarizeError(status, body, err))
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", fmt.Errorf("save configuration: %s", summarizeError(status, body, err))
	}

	return currentCfgSaveMode, nil
}
