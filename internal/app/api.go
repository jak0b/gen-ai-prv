package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	return checkFileAt(ctx, path, apiKey, apiEndpoint)
}

func checkFileAt(ctx context.Context, path, apiKey, endpoint string) (checkResponse, error) {
	file, size, contentType, err := openSourceFile(path)
	if err != nil {
		return checkResponse{}, err
	}
	defer file.Close()

	body, multipartContentType := newMultipartFileBody(file, path, size, contentType)
	req, err := newAPIRequest(ctx, endpoint, apiKey, body, multipartContentType)
	if err != nil {
		_ = body.Close()
		return checkResponse{}, fmt.Errorf("create API request: %w", err)
	}

	resp, err := sendAPIRequest(ctx, req)
	if err != nil {
		return checkResponse{}, err
	}
	defer resp.Body.Close()

	return decodeCheckResponse(ctx, req, resp)
}

func newMultipartFileBody(file io.Reader, path string, size int64, contentType string) (*io.PipeReader, string) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	// Stream the multipart body through the pipe so the full file is not buffered in memory.
	go writeMultipartFile(pw, writer, file, path, size, contentType)
	return pr, writer.FormDataContentType()
}

func writeMultipartFile(pipe *io.PipeWriter, writer *multipart.Writer, file io.Reader, path string, size int64, contentType string) {
	err := writeFilePart(writer, file, path, size, contentType)
	_ = pipe.CloseWithError(err)
}

func writeFilePart(writer *multipart.Writer, file io.Reader, path string, size int64, contentType string) error {
	part, err := writer.CreatePart(filePartHeader(path, contentType))
	if err != nil {
		return err
	}

	if _, err := io.CopyN(part, file, size); err != nil {
		return err
	}
	return writer.Close()
}

func filePartHeader(path, contentType string) textproto.MIMEHeader {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name":     "file",
		"filename": uploadFilename(path),
	}))
	header.Set("Content-Type", contentType)
	return header
}

func newAPIRequest(ctx context.Context, endpoint, apiKey string, body io.Reader, contentType string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)
	return req, nil
}

func sendAPIRequest(ctx context.Context, req *http.Request) (*http.Response, error) {
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		logOpenAIAPIRequest(ctx, fmt.Sprintf("%s %s failed", req.Method, apiRequestTarget(req)))
		return nil, fmt.Errorf("send API request: %w", err)
	}
	return resp, nil
}

func decodeCheckResponse(ctx context.Context, req *http.Request, resp *http.Response) (checkResponse, error) {
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return checkResponse{}, apiResponseError(ctx, req, resp)
	}

	var result checkResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logOpenAIAPIRequest(ctx, fmt.Sprintf("%s %s status=%s result=decode_error", req.Method, apiRequestTarget(req), resp.Status))
		return checkResponse{}, fmt.Errorf("decode API response: %w", err)
	}
	logSuccessfulAPIResponse(ctx, req, resp, result)
	return result, nil
}

func apiResponseError(ctx context.Context, req *http.Request, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	logOpenAIAPIRequest(ctx, fmt.Sprintf("%s %s status=%s", req.Method, apiRequestTarget(req), resp.Status))
	var apiErr apiErrorResponse
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
		if apiErr.Error.Code != "" {
			return fmt.Errorf("OpenAI API returned %s (%s): %s", resp.Status, apiErr.Error.Code, apiErr.Error.Message)
		}
		return fmt.Errorf("OpenAI API returned %s: %s", resp.Status, apiErr.Error.Message)
	}
	return fmt.Errorf("OpenAI API returned %s", resp.Status)
}

func logSuccessfulAPIResponse(ctx context.Context, req *http.Request, resp *http.Response, result checkResponse) {
	if resultJSON, err := json.Marshal(result); err == nil {
		logOpenAIAPIRequest(ctx, fmt.Sprintf("%s %s status=%s result=%s", req.Method, apiRequestTarget(req), resp.Status, resultJSON))
	} else {
		logOpenAIAPIRequest(ctx, fmt.Sprintf("%s %s status=%s result=unavailable", req.Method, apiRequestTarget(req), resp.Status))
	}
}

func apiRequestTarget(req *http.Request) string {
	return req.URL.Host + req.URL.EscapedPath()
}

func logOpenAIAPIRequest(ctx context.Context, details string) {
	if !appendServeRequestAPILog(ctx, details) {
		log.Printf("OpenAI API request: %s", details)
	}
}
