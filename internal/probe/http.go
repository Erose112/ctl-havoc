package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Queens-Rocket-Engineering-Team/ctl-havoc/internal/timeline"
)

const maxBody = 1 << 20 // 1 MiB

type HTTPProbe struct {
	name     string
	url      string
	interval time.Duration
	timeout  time.Duration
}

func newHTTP(name, url string, interval, timeout time.Duration) *HTTPProbe {
	return &HTTPProbe{
		name:     name,
		url:      url,
		interval: interval,
		timeout:  timeout,
	}
}

func (h *HTTPProbe) Name() string {
	return h.name
}

func (h *HTTPProbe) Start(ctx context.Context, t timeline.Timeline) error {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	client := &http.Client{
		Timeout: h.timeout,
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Loop stopped")
			return ctx.Err()
		case <-ticker.C:
			var (
				errCode timeline.ErrCode = timeline.ErrNone
				errMsg  string
				fields  map[string]any
			)

			start := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url, nil)
			if err != nil {
				errCode = classify(err)
				errMsg = err.Error()
			} else {
				resp, err := client.Do(req)
				if err != nil {
					errCode = classify(err)
					errMsg = err.Error()
				} else {
					// Read up to maxBody + 1 to detect if the body exceeds byte limit
					body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
					mediatype, _, mtErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))

					resp.Body.Close()

					switch {
					case err != nil:
						errCode = timeline.ErrBadResponse
						errMsg = err.Error()
					case len(body) > maxBody:
						errCode = timeline.ErrBadResponse
						errMsg = "response body too large"
					default:
						// Field map for where: status and body keys live in Fields
						// err and latency stay on Sample
						fields = map[string]any{
							"status": resp.StatusCode,
						}

						trimmed := bytes.TrimSpace(body)
						looksJSON := (mtErr == nil && (mediatype == "application/json" || strings.HasSuffix(mediatype, "+json"))) ||
							(len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '['))
						if looksJSON && len(trimmed) > 0 && trimmed[0] == '{' {

							var payload map[string]any
							if json.Unmarshal(trimmed, &payload) == nil {
								for k, v := range payload {
									fields[k] = v
								}
								fields["status"] = resp.StatusCode
							}
						}
					}
				}
			}

			sample := timeline.Sample{
				Probe:   h.Name(),
				At:      start,
				Latency: time.Since(start),
				Err:     errCode,
				ErrMsg:  errMsg,
				Fields:  fields,
			}

			t.Append(sample)
		}
	}
}

func classify(err error) timeline.ErrCode {
	switch {
	case err == nil:
		return timeline.ErrNone
	case errors.Is(err, context.Canceled):
		return timeline.ErrCanceled
	case errors.Is(err, context.DeadlineExceeded), os.IsTimeout(err):
		return timeline.ErrTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return timeline.ErrTimeout
	}

	// connections faults differ when on Windows vs Unix systems
	if isConnRefused(err) {
		return timeline.ErrConnRefused
	}
	if isConnReset(err) {
		return timeline.ErrConnReset
	}

	// TODO: potentially implement DNS/TLS error handling?
	return timeline.ErrUnknown
}
