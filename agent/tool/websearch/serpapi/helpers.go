package serpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// doGet executes an HTTP GET request with context.
func doGet(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	return resp, nil
}

// apiError reads the response body and returns a formatted error.
func apiError(resp *http.Response, engine string) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("SerpAPI %s returned %d: %s", engine, resp.StatusCode, string(body))
}
