package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultDownloadWorkers = 16
const downloadAttempts = 3
const downloadIdleTimeout = 2 * time.Minute

var errRangeIgnored = errors.New("server ignored byte ranges")

// Probe with GET rather than HEAD: reuse a 200 response when Range is ignored.
func downloadFile(client *http.Client, url, targetPath string, report func(int)) error {
	if report != nil {
		callback := report
		var progressMu sync.Mutex
		last := -1
		report = func(percent int) {
			progressMu.Lock()
			defer progressMu.Unlock()
			// A complete-stream fallback may restart at byte zero. Keep UI progress monotonic.
			if percent > last {
				last = percent
				callback(percent)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusOK {
		return downloadSingleResponse(resp, targetPath, report)
	}
	if resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return fmt.Errorf("download probe: HTTP %d", resp.StatusCode)
	}
	total, err := parseDownloadRange(resp.Header.Get("Content-Range"), 0, 0)
	if err == nil {
		err = validateDownloadEncoding(resp)
	}
	if err == nil {
		// Bound and verify the one-byte probe rather than trusting headers alone.
		var probe []byte
		probe, err = io.ReadAll(io.LimitReader(resp.Body, 2))
		if err == nil && (len(probe) != 1 || (resp.ContentLength >= 0 && resp.ContentLength != 1)) {
			err = errors.New("invalid download probe body length")
		}
	}
	validator := downloadValidator(resp.Header)
	resp.Body.Close()
	if err != nil {
		return err
	}
	// Tiny files and servers without a stable representation validator use one stream.
	if total < defaultDownloadWorkers*256*1024 || validator == "" {
		return downloadSingleRequest(ctx, client, url, targetPath, report)
	}
	err = downloadRanges(ctx, client, url, targetPath, total, validator, report)
	if errors.Is(err, errRangeIgnored) {
		// All workers have stopped and closed their bodies before replacing the file.
		return downloadSingleRequest(ctx, client, url, targetPath, report)
	}
	return err
}

func downloadValidator(header http.Header) string {
	etag := header.Get("ETag")
	if len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		valid := true
		for _, b := range []byte(etag[1 : len(etag)-1]) {
			if b < 0x21 || b == '"' || b == 0x7f {
				valid = false
				break
			}
		}
		if valid {
			return etag
		}
	}
	// A date is strong enough for If-Range only when the server's Date is at
	// least 60 seconds newer (otherwise same-second changes can be invisible).
	modified, err := http.ParseTime(header.Get("Last-Modified"))
	date, dateErr := http.ParseTime(header.Get("Date"))
	if err == nil && dateErr == nil && date.Sub(modified) >= time.Minute {
		return header.Get("Last-Modified")
	}
	return ""
}

func downloadSingleRequest(ctx context.Context, client *http.Client, url, path string, report func(int)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	return downloadSingleResponse(resp, path, report)
}

func validateDownloadEncoding(resp *http.Response) error {
	if encoding := resp.Header.Get("Content-Encoding"); resp.Uncompressed || (encoding != "" && encoding != "identity") {
		return fmt.Errorf("unexpected download encoding: %q", encoding)
	}
	return nil
}

func parseDownloadRange(value string, start, end int64) (int64, error) {
	prefix := fmt.Sprintf("bytes %d-%d/", start, end)
	if !strings.HasPrefix(value, prefix) {
		return 0, fmt.Errorf("unexpected Content-Range: %q; want %sTOTAL", value, prefix)
	}
	total, err := strconv.ParseInt(strings.TrimPrefix(value, prefix), 10, 64)
	digits := strings.TrimPrefix(value, prefix)
	if err != nil || total <= end || start < 0 || end < start || strings.Trim(digits, "0123456789") != "" {
		return 0, fmt.Errorf("invalid Content-Range: %q", value)
	}
	return total, nil
}

func downloadRanges(parent context.Context, client *http.Client, url, path string, total int64, validator string, report func(int)) (resultErr error) {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
		if resultErr == nil && report != nil {
			report(100)
		}
	}()
	if err := file.Truncate(total); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var completed int64
	if report != nil {
		report(0)
	}
	for i := 0; i < defaultDownloadWorkers; i++ {
		start := downloadBoundary(total, i)
		end := downloadBoundary(total, i+1) - 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			attemptsMade := 0
			for attempt := 0; attempt < downloadAttempts; attempt++ {
				if ctx.Err() != nil {
					return
				}
				if attempt > 0 {
					timer := time.NewTimer(time.Duration(attempt) * 200 * time.Millisecond)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
				attemptsMade++
				err = downloadRange(ctx, client, url, file, start, end, total, validator, downloadIdleTimeout)
				if err == nil || errors.Is(err, errRangeIgnored) {
					break
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("range %d-%d after %d attempts: %w", start, end, attemptsMade, err)
					cancel()
				}
				return
			}
			completed += end - start + 1
			if report != nil && firstErr == nil {
				report(min(99, int(float64(completed)/float64(total)*100)))
			}
		}()
	}
	wg.Wait()
	if firstErr == nil && completed != total {
		return fmt.Errorf("incomplete download: %d/%d: %w", completed, total, ctx.Err())
	}
	return firstErr
}

// Avoid total*i overflow even for an untrusted Content-Range near MaxInt64.
func downloadBoundary(total int64, i int) int64 {
	return total/defaultDownloadWorkers*int64(i) + (total%defaultDownloadWorkers)*int64(i)/defaultDownloadWorkers
}

type downloadIdleReader struct {
	reader  io.Reader
	timer   *time.Timer
	timeout time.Duration
}

func (r *downloadIdleReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.timer.Reset(r.timeout)
	}
	return n, err
}

func downloadRange(parent context.Context, client *http.Client, url string, file *os.File, start, end, total int64, validator string, idleTimeout time.Duration) (resultErr error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// A fixed two-minute segment deadline makes slow but healthy links restart
	// forever. Cancel only after inactivity; the parent bounds the entire download.
	idle := time.AfterFunc(idleTimeout, cancel)
	defer func() {
		idle.Stop()
		if ctx.Err() != nil && parent.Err() == nil {
			resultErr = fmt.Errorf("range inactive for %s: %w", idleTimeout, context.DeadlineExceeded)
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.Header.Set("If-Range", validator)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return errRangeIgnored
	}
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("range HTTP %d", resp.StatusCode)
	}
	gotTotal, err := parseDownloadRange(resp.Header.Get("Content-Range"), start, end)
	if err != nil {
		return err
	}
	if gotTotal != total {
		return fmt.Errorf("download size changed: %d != %d", gotTotal, total)
	}
	if err := validateDownloadEncoding(resp); err != nil {
		return err
	}
	header := "ETag"
	if !strings.HasPrefix(validator, `"`) {
		header = "Last-Modified"
	}
	if resp.Header.Get(header) != validator {
		return fmt.Errorf("download representation changed: %s", header)
	}
	length := end - start + 1
	if resp.ContentLength >= 0 && resp.ContentLength != length {
		return fmt.Errorf("range length mismatch: %d != %d", resp.ContentLength, length)
	}
	reader := &downloadIdleReader{reader: resp.Body, timer: idle, timeout: idleTimeout}
	n, err := io.CopyBuffer(io.NewOffsetWriter(file, start), io.LimitReader(reader, length), make([]byte, 256*1024))
	if err != nil {
		return err
	}
	if n != length {
		return io.ErrUnexpectedEOF
	}
	var extra [1]byte
	nExtra, err := io.ReadFull(reader, extra[:])
	if nExtra != 0 || !errors.Is(err, io.EOF) {
		return fmt.Errorf("range body exceeds expected length or failed: %v", err)
	}
	return nil
}
