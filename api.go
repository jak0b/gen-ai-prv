package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

const apiEndpoint = "https://api.openai.com/v1/content_provenance_checks"

type checkResponse struct {
	Object    string        `json:"object,omitempty"`
	CreatedAt int64         `json:"created_at,omitempty"`
	Results   []checkResult `json:"results"`
}

type checkResult struct {
	Type            string  `json:"type"`
	Outcome         string  `json:"outcome"`
	ValidationState *string `json:"validation_state,omitempty"`
	Issuer          *string `json:"issuer,omitempty"`
	Model           *string `json:"model,omitempty"`
	GeneratedAt     *string `json:"generated_at,omitempty"`
}

type apiErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

func checkFile(ctx context.Context, path, apiKey string) (checkResponse, error) {
	file, size, contentType, err := openSourceFile(path)
	if err != nil {
		return checkResponse{}, err
	}
	defer file.Close()

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	go func() {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
			"name":     "file",
			"filename": uploadFilename(path),
		}))
		header.Set("Content-Type", contentType)
		part, createErr := writer.CreatePart(header)
		if createErr == nil {
			_, createErr = io.CopyN(part, file, size)
		}
		if createErr == nil {
			createErr = writer.Close()
		}
		_ = pw.CloseWithError(createErr)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiEndpoint, pr)
	if err != nil {
		_ = pr.Close()
		return checkResponse{}, fmt.Errorf("create API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return checkResponse{}, fmt.Errorf("send API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var apiErr apiErrorResponse
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
			if apiErr.Error.Code != "" {
				return checkResponse{}, fmt.Errorf("OpenAI API returned %s (%s): %s", resp.Status, apiErr.Error.Code, apiErr.Error.Message)
			}
			return checkResponse{}, fmt.Errorf("OpenAI API returned %s: %s", resp.Status, apiErr.Error.Message)
		}
		return checkResponse{}, fmt.Errorf("OpenAI API returned %s", resp.Status)
	}

	var result checkResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return checkResponse{}, fmt.Errorf("decode API response: %w", err)
	}
	return result, nil
}
