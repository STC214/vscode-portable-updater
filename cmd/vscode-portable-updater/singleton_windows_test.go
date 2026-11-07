package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func instanceTestName() string {
	return fmt.Sprintf(`Global\AutoUpdataVSCode.Test.%d.%d`, os.Getpid(), time.Now().UnixNano())
}

func TestSingleInstanceLifecycle(t *testing.T) {
	name := instanceTestName()
	lock, err := acquireSingleInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	if duplicate, err := acquireSingleInstance(name); !errors.Is(err, errInstanceRunning) {
		if duplicate != nil {
			duplicate.Close()
		}
		t.Fatalf("duplicate error=%v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := acquireSingleInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	t.Log("first launch accepted; duplicate rejected; close/restart accepted")
}

func TestSingleInstanceInvalidName(t *testing.T) {
	for _, name := range []string{"", "invalid\x00name", `Global\invalid\nested`} {
		lock, err := acquireSingleInstance(name)
		if err == nil {
			lock.Close()
			t.Errorf("invalid name accepted: %q", name)
		}
	}
}

// Child helpers never start the GUI or read/write the application's config.
func TestSingleInstanceChild(t *testing.T) {
	name := os.Getenv("UPDATER_INSTANCE_TEST_NAME")
	if name == "" {
		return
	}
	lock, err := acquireSingleInstance(name)
	if errors.Is(err, errInstanceRunning) {
		fmt.Println("DUPLICATE")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	fmt.Println("LOCKED")
	io.Copy(io.Discard, os.Stdin)
}

type instanceChild struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
}

func startInstanceChild(t *testing.T, name string) instanceChild {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestSingleInstanceChild$", "-test.count=1", "-test.timeout=20s")
	cmd.Env = append(os.Environ(), "UPDATER_INSTANCE_TEST_NAME="+name)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); cmd.Process.Kill() })
	return instanceChild{cmd: cmd, input: input, output: bufio.NewReader(output)}
}

func childStatus(t *testing.T, child instanceChild) string {
	t.Helper()
	line, err := child.output.ReadString('\n')
	if err != nil {
		t.Fatalf("child status=%q error=%v", line, err)
	}
	return strings.TrimSpace(line)
}

func TestSingleInstanceConcurrentProcesses(t *testing.T) {
	name := instanceTestName()
	var children []instanceChild
	for i := 0; i < 8; i++ {
		children = append(children, startInstanceChild(t, name))
	}
	accepted, rejected := 0, 0
	for _, child := range children {
		switch status := childStatus(t, child); status {
		case "LOCKED":
			accepted++
		case "DUPLICATE":
			rejected++
		default:
			t.Errorf("unexpected status=%q", status)
		}
	}
	for _, child := range children {
		child.input.Close()
		if err := child.cmd.Wait(); err != nil {
			t.Error(err)
		}
	}
	if accepted != 1 || rejected != 7 {
		t.Fatalf("accepted=%d rejected=%d", accepted, rejected)
	}
	lock, err := acquireSingleInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	t.Logf("8 concurrent processes: accepted=%d rejected=%d; restart accepted", accepted, rejected)
}

func TestSingleInstanceAbnormalExit(t *testing.T) {
	name := instanceTestName()
	child := startInstanceChild(t, name)
	if status := childStatus(t, child); status != "LOCKED" {
		t.Fatalf("status=%s", status)
	}
	if err := child.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	child.cmd.Wait()
	lock, err := acquireSingleInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	t.Log("terminated holder process; restart accepted without stale lock file")
}
