package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Every segment pauses after its first chunk: progress must reach the UI
// before even one segment can finish, not just at segment boundaries.
func TestDownloadProgressBeforeSegmentCompletion(t *testing.T) {
	for workers := minDownloadWorkers; workers <= maxDownloadWorkers; workers++ {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			data := bytes.Repeat([]byte("progress"), 1024*1024)
			gate := make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(gate) })
			client := &http.Client{Transport: updateTestTransport(func(req *http.Request) (*http.Response, error) {
				var start, end int64
				if _, err := fmt.Sscanf(req.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
					return nil, err
				}
				h := http.Header{"Etag": {`"stable"`}, "Content-Range": {fmt.Sprintf("bytes %d-%d/%d", start, end, len(data))}}
				var body io.Reader = bytes.NewReader(data[start : end+1])
				if end != 0 {
					body = &progressGatedReader{reader: body, gate: gate}
				}
				return &http.Response{StatusCode: 206, Header: h, ContentLength: end - start + 1, Body: io.NopCloser(body)}, nil
			})}
			a := reviewWorkerApp(t, 1)
			intermediate := make(chan int, 1)
			var once sync.Once
			done := make(chan error, 1)
			path := filepath.Join(t.TempDir(), "archive")
			last := -1 // callback is serialized; read only after done synchronizes.
			go func() {
				done <- downloadFileWithWorkers(client, "https://fixture.test/archive", path, workers, func(p int) {
					if p <= last || p > 100 {
						t.Errorf("invalid progress %d after %d", p, last)
					}
					last = p
					if p > 0 && p < 100 {
						once.Do(func() { intermediate <- p })
					}
				})
			}()
			select {
			case p := <-intermediate:
				a.invokeUI(func() { a.setProgress("下载更新包", p) })
				a.runUITasks()
				if got := int(sendMessage(a.progressBar, WM_USER+8, 0, 0)); got != p {
					t.Errorf("native progress position = %d, want %d", got, p)
				}
				t.Logf("workers=%d progress=%d%% before any segment finishes; native bar verified", workers, p)
			case <-time.After(2 * time.Second):
				t.Error("no download progress before segment completion")
			}
			release.Do(func() { close(gate) })
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if last != 100 {
				t.Errorf("final download progress = %d, want 100", last)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("assembled download mismatch: %v", err)
			}
		})
	}
}

func TestDownloadProgressWriterPartialWrites(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			var reported []int64
			w := &downloadProgressWriter{writer: progressShortWriter{fail: fail}, report: func(n int64) { reported = append(reported, n) }}
			n, err := io.CopyBuffer(w, io.LimitReader(bytes.NewReader(make([]byte, 32)), 32), make([]byte, 32))
			if n != 7 || err == nil || w.written != 7 || len(reported) != 1 || reported[0] != 7 {
				t.Fatalf("partial write overcounted: n=%d err=%v written=%d callbacks=%v", n, err, w.written, reported)
			}
		})
	}
}

type progressShortWriter struct{ fail bool }

func (w progressShortWriter) Write(p []byte) (int, error) {
	if w.fail {
		return min(7, len(p)), errors.New("fixture disk write failure")
	}
	return min(7, len(p)), nil
}

type progressGatedReader struct {
	reader  io.Reader
	gate    <-chan struct{}
	started bool
}

func (r *progressGatedReader) Read(p []byte) (int, error) {
	if r.started {
		<-r.gate
	}
	r.started = true
	return r.reader.Read(p[:min(len(p), 32*1024)])
}

func TestDownloadProgressRetryDoesNotDoubleCount(t *testing.T) {
	const workers = 3
	data := bytes.Repeat([]byte("retry"), 1024*1024)
	gate := make(chan struct{})
	ready := make(chan struct{}, workers)
	var release sync.Once
	defer release.Do(func() { close(gate) })
	var attempts sync.Map
	client := &http.Client{Transport: updateTestTransport(func(req *http.Request) (*http.Response, error) {
		var start, end int64
		key := req.Header.Get("Range")
		if _, err := fmt.Sscanf(key, "bytes=%d-%d", &start, &end); err != nil {
			return nil, err
		}
		var body io.Reader = bytes.NewReader(data[start : end+1])
		if end != 0 {
			_, retry := attempts.LoadOrStore(key, true)
			body = &progressRetryReader{reader: body, remaining: (end - start + 1) / 2, retry: retry, gate: gate, ready: ready}
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Etag": {`"stable"`}, "Content-Range": {fmt.Sprintf("bytes %d-%d/%d", start, end, len(data))}}, ContentLength: end - start + 1, Body: io.NopCloser(body)}, nil
	})}
	var mu sync.Mutex
	last := -1
	done := make(chan error, 1)
	path := filepath.Join(t.TempDir(), "archive")
	go func() {
		done <- downloadFileWithWorkers(client, "https://fixture.test/retry", path, workers, func(p int) {
			mu.Lock()
			defer mu.Unlock()
			if p <= last || p > 100 {
				t.Errorf("invalid progress %d after %d", p, last)
			}
			last = p
		})
	}()
	for i := 0; i < workers; i++ {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Error("retry did not reach halfway gate")
		}
	}
	mu.Lock()
	if last < 49 || last > 50 {
		t.Errorf("retry halfway progress = %d, want 49..50", last)
	}
	t.Logf("all segments retried halfway: progress=%d%% (no duplicate bytes)", last)
	mu.Unlock()
	release.Do(func() { close(gate) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if last != 100 {
		t.Errorf("final progress = %d", last)
	}
	mu.Unlock()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("retry content mismatch: %v", err)
	}
}

type progressRetryReader struct {
	reader    io.Reader
	remaining int64
	retry     bool
	gate      <-chan struct{}
	ready     chan<- struct{}
	resumed   bool
}

func (r *progressRetryReader) Read(p []byte) (int, error) {
	if r.remaining > 0 {
		n, err := r.reader.Read(p[:min(int64(len(p)), r.remaining)])
		r.remaining -= int64(n)
		return n, err
	}
	if !r.retry {
		return 0, errors.New("fixture interrupted segment")
	}
	if !r.resumed {
		r.ready <- struct{}{}
		<-r.gate
		r.resumed = true
	}
	return r.reader.Read(p)
}
