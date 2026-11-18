package probe

import (
	"fmt"
	"errors"
	"os"
	"time"
	"context"
	"io"
	"net"
	"net/http"

	"github.com/Queens-Rocket-Engineering-Team/ctl-havoc/internal/timeline"
)

const maxBody = 1 << 20

type HTTPProbe struct {
	name string
	url string
	interval time.Duration
	timeout time.Duration
}

func newHTTP(name, url string, interval, timeout time.Duration) *HTTPProbe {
	return &HTTPProbe {
		name: name,
		url: url,
		interval: interval,
		timeout: timeout,
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
				errMsg 	string
				fields 	map[string]any
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
					body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
					resp.Body.Close()

					switch {
					case err != nil:
						errCode = timeline.ErrBadResponse
						errMsg = err.Error()
					case len(body) > maxBody:
						errCode = timeline.ErrBadResponse
						errMsg = "response body too large"
					default:
						fields = map[string]any{
							"status": resp.StatusCode,
						}
					}
				}
			}

			sample := timeline.Sample{
				Probe:		h.Name(),
				At:			start,
				Latency: 	time.Since(start),
				Err:		errCode,
				ErrMsg:		errMsg,
				Fields: 	fields,
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
	return timeline.ErrUnknown
}