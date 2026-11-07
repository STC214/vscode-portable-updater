package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadReviewResponses(t *testing.T) {
	for _, mode := range []string{"etag-changed", "date-changed", "probe-encoding", "probe-truncated", "range-ignored-after-probe", "single-truncated", "single-overlong", "single-unknown-length", "range-unknown-length", "range-overlong"} {
		t.Run(mode, func(t *testing.T) {
			data := bytes.Repeat([]byte("abcdefgh"), 512*1024)
			var singleCalls atomic.Int32
			client := &http.Client{Transport: updateTestTransport(func(req *http.Request) (*http.Response, error) {
				h := make(http.Header)
				h.Set("ETag", `"original"`)
				h.Set("Date", "Fri, 23 Oct 2015 07:28:00 GMT")
				if mode == "date-changed" {
					h.Del("ETag")
					h.Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
				}
				status := http.StatusPartialContent
				var start, end int64
				rangeHeader := req.Header.Get("Range")
				if rangeHeader == "" || strings.HasPrefix(mode, "single-") {
					singleCalls.Add(1)
					status = http.StatusOK
					end = int64(len(data) - 1)
				} else if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end); err != nil {
					return nil, err
				}
				body := data[start : end+1]
				length := int64(len(body))
				if rangeHeader == "bytes=0-0" && !strings.HasPrefix(mode, "single-") {
					if mode == "probe-encoding" {
						h.Set("Content-Encoding", "gzip")
					}
					if mode == "probe-truncated" {
						body = nil
					}
				} else if rangeHeader != "" {
					if mode == "etag-changed" {
						h.Set("ETag", `"different"`)
					}
					if mode == "date-changed" {
						h.Set("Last-Modified", "Thu, 22 Oct 2015 07:28:00 GMT")
					}
					if mode == "range-ignored-after-probe" {
						status = http.StatusOK
						body = data
						length = int64(len(data))
					}
				}
				if mode == "single-truncated" {
					body = body[:len(body)-1]
				}
				if mode == "single-overlong" {
					length--
				}
				if mode == "single-unknown-length" || (mode == "range-unknown-length" && rangeHeader != "bytes=0-0") {
					length = -1
				}
				if mode == "range-overlong" && rangeHeader != "bytes=0-0" {
					body = append(append([]byte(nil), body...), 0)
					length = -1
				}
				h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
				return &http.Response{StatusCode: status, Header: h, ContentLength: length, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})}
			path := filepath.Join(t.TempDir(), "output")
			last := -1
			err := downloadFile(client, "https://fixture.test/archive.zip", path, func(p int) {
				if p < last {
					t.Errorf("progress regressed %d -> %d", last, p)
				}
				last = p
			})
			if mode == "range-ignored-after-probe" || mode == "single-unknown-length" || mode == "range-unknown-length" {
				if err != nil {
					t.Fatalf("server ignored Range after probe: %v", err)
				}
				got, e := os.ReadFile(path)
				wantSingle := int32(1)
				if mode == "range-unknown-length" {
					wantSingle = 0
				}
				if e != nil || !bytes.Equal(got, data) || singleCalls.Load() != wantSingle {
					t.Fatalf("fallback content/calls incorrect: %v calls=%d", e, singleCalls.Load())
				}
			} else if err == nil {
				t.Fatal("invalid response accepted")
			} else if last == 100 {
				t.Fatal("failed download reported 100 percent")
			}
			t.Logf("mode=%s error=%v progress=%d full_streams=%d", mode, err, last, singleCalls.Load())
		})
	}
}

func TestDownloadReviewMixedFallbackProgress(t *testing.T) {
	data := bytes.Repeat([]byte("progress"), 512*1024)
	progressMade := make(chan struct{})
	var once sync.Once
	client := &http.Client{Transport: updateTestTransport(func(req *http.Request) (*http.Response, error) {
		h := make(http.Header)
		h.Set("ETag", `"stable"`)
		r := req.Header.Get("Range")
		var start, end int64
		status := http.StatusPartialContent
		if r == "" {
			status = http.StatusOK
			end = int64(len(data) - 1)
		} else {
			fmt.Sscanf(r, "bytes=%d-%d", &start, &end)
		}
		if r != "" && r != "bytes=0-0" && start == 0 {
			select {
			case <-progressMade:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(3 * time.Second):
				return nil, errors.New("no segment progress")
			}
			status = http.StatusOK
			end = int64(len(data) - 1)
		}
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		return &http.Response{StatusCode: status, Header: h, ContentLength: end - start + 1, Body: io.NopCloser(bytes.NewReader(data[start : end+1]))}, nil
	})}
	last := -1
	err := downloadFile(client, "https://fixture.test/mixed", filepath.Join(t.TempDir(), "out"), func(p int) {
		if p < last {
			t.Errorf("fallback progress regressed: %d -> %d", last, p)
		}
		last = p
		if p >= 90 && p < 100 {
			once.Do(func() { close(progressMade) })
		}
	})
	if err != nil || last != 100 {
		t.Fatalf("err=%v last=%d", err, last)
	}
}

func TestDownloadReviewCanceledParent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: updateTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("request after cancellation")
		return nil, context.Canceled
	})}
	err := downloadRanges(ctx, client, "https://fixture.test/canceled", filepath.Join(t.TempDir(), "out"), 4*1024*1024, `"fixture"`, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
}

func TestDownloadReviewBoundary(t *testing.T) {
	for _, total := range []int64{4 * 1024 * 1024, 4194307, math.MaxInt64 - 7, math.MaxInt64} {
		prev := int64(0)
		for i := 0; i <= 16; i++ {
			boundary := downloadBoundary(total, i)
			if boundary < prev || boundary < 0 || boundary > total {
				t.Fatalf("total=%d index=%d boundary=%d previous=%d", total, i, boundary, prev)
			}
			prev = boundary
		}
		if prev != total {
			t.Fatalf("total=%d final=%d", total, prev)
		}
	}
}

func TestDownloadReviewValidators(t *testing.T) {
	for _, tc := range []struct{ etag, modified, date, want string }{
		{`"strong"`, "", "", `"strong"`},
		{`""`, "", "", `""`},
		{`W/"weak"`, "", "", ""},
		{"unquoted", "", "", ""},
		{"\"has space\"", "", "", ""},
		{"", "Wed, 21 Oct 2015 07:28:00 GMT", "Wed, 21 Oct 2015 07:29:00 GMT", "Wed, 21 Oct 2015 07:28:00 GMT"},
		{"", "Wed, 21 Oct 2015 07:28:00 GMT", "Wed, 21 Oct 2015 07:28:59 GMT", ""},
		{"", "Wed, 21 Oct 2015 07:28:00 GMT", "", ""},
		{"", "invalid", "Wed, 21 Oct 2015 07:29:00 GMT", ""},
	} {
		h := make(http.Header)
		h.Set("ETag", tc.etag)
		h.Set("Last-Modified", tc.modified)
		h.Set("Date", tc.date)
		if got := downloadValidator(h); got != tc.want {
			t.Errorf("etag=%q modified=%q date=%q got=%q want=%q", tc.etag, tc.modified, tc.date, got, tc.want)
		}
	}
}

func TestDownloadReviewFailedPackageKeepsCache(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(cache, "latest-x64.zip")
	old := testUpdateZIP(t, "1.99.0")
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	u := NewUpdater(cache, nil, nil)
	u.client.Transport = updateTestTransport(func(req *http.Request) (*http.Response, error) {
		h := make(http.Header)
		h.Set("ETag", `"stable"`)
		if req.Header.Get("Range") == "bytes=0-0" {
			h.Set("Content-Range", "bytes 0-0/8388608")
			return &http.Response{StatusCode: 206, Header: h, ContentLength: 1, Body: io.NopCloser(strings.NewReader("x"))}, nil
		}
		return &http.Response{StatusCode: 503, Header: h, Body: io.NopCloser(strings.NewReader("retry exhausted"))}, nil
	})
	_, err := u.getLatestPackage(LatestRelease{Arch: "x64", Version: "1.100.0", URL: "https://fixture.test/package"})
	if err == nil {
		t.Fatal("expected failure")
	}
	got, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(got, old) {
		t.Fatalf("old cache changed: %v", e)
	}
	if _, e := os.Stat(path + ".download"); !os.IsNotExist(e) {
		t.Fatalf("partial file retained: %v", e)
	}
}

func FuzzDownloadContentRange(f *testing.F) {
	for _, seed := range []string{"bytes 0-0/1", "bytes 0-0/9223372036854775807", "bytes 0-0/+1", "bytes 0-0/*", "bytes 0-0/0", "bytes 1-2/3", "invalid"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		total, err := parseDownloadRange(value, 0, 0)
		if err == nil {
			if total < 1 || !strings.HasPrefix(value, "bytes 0-0/") || strings.Trim(strings.TrimPrefix(value, "bytes 0-0/"), "0123456789") != "" {
				t.Fatalf("invalid range accepted: %q", value)
			}
			prev := int64(0)
			for i := 0; i <= 16; i++ {
				n := downloadBoundary(total, i)
				if n < prev || n > total {
					t.Fatalf("boundary overflow total=%d i=%d n=%d", total, i, n)
				}
				prev = n
			}
		}
	})
}

type pacedDownloadBody struct {
	ctx       context.Context
	remaining int
	pause     time.Duration
	closed    *atomic.Bool
}

func (b *pacedDownloadBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	timer := time.NewTimer(b.pause)
	defer timer.Stop()
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-timer.C:
	}
	p[0] = 'x'
	b.remaining--
	return 1, nil
}
func (b *pacedDownloadBody) Close() error { b.closed.Store(true); return nil }

func TestDownloadReviewIdleTimeout(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(fmt.Sprint(stalled), func(t *testing.T) {
			var closed atomic.Bool
			client := &http.Client{Transport: updateTestTransport(func(r *http.Request) (*http.Response, error) {
				h := make(http.Header)
				h.Set("ETag", `"stable"`)
				h.Set("Content-Range", "bytes 0-7/8")
				pause := 50 * time.Millisecond
				if stalled {
					pause = 5 * time.Second
				}
				return &http.Response{StatusCode: 206, ContentLength: 8, Header: h, Body: &pacedDownloadBody{ctx: r.Context(), remaining: 8, pause: pause, closed: &closed}}, nil
			})}
			file, err := os.Create(filepath.Join(t.TempDir(), "out"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			parent, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			start := time.Now()
			err = downloadRange(parent, client, "https://fixture.test/paced", file, 0, 7, 8, `"stable"`, 200*time.Millisecond)
			if stalled {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("want idle deadline: %v", err)
				}
			} else if err != nil {
				t.Fatalf("healthy slow response timed out: %v", err)
			}
			if !closed.Load() {
				t.Fatal("response body leaked")
			}
			t.Logf("stalled=%v elapsed=%s err=%v", stalled, time.Since(start), err)
		})
	}
}
