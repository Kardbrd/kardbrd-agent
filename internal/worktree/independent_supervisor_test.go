package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

func TestIndependentSupervisorInterruptedCreationStillPrepares(t *testing.T) {
	for _, phase := range []string{"created", "preparing", "full_materialization_not_recorded"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			remote := filepath.Join(root, "remote.git")
			seed := filepath.Join(root, "seed")
			git(t, "", "init", "--bare", remote)
			git(t, "", "clone", remote, seed)
			configureTestGit(t, seed)
			writeFile(t, filepath.Join(seed, "source.txt"), "main\n")
			git(t, seed, "add", ".")
			git(t, seed, "commit", "-m", "seed")
			git(t, seed, "branch", "-M", "main")
			git(t, seed, "push", "origin", "main")
			base := filepath.Join(root, "base")
			git(t, "", "clone", remote, base)
			stable := filepath.Join(root, "stable")
			if err := os.MkdirAll(stable, 0o700); err != nil {
				t.Fatal(err)
			}
			helper := filepath.Join(stable, "prepare")
			if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf 'ready' > prepared\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			m, err := NewLifecycleManager(base, filepath.Join(root, "worktrees"), "codex", rules.WorktreeConfig{
				Base: rules.WorktreeBase{Remote: "origin", Ref: "refs/heads/main"}, Helpers: rules.WorktreeHelpers{StableRoot: stable},
				Checkout: rules.WorktreeCheckout{Mode: rules.CheckoutFull}, Sharing: rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsDisabled},
				Prepare: &rules.PrepareHook{Hook: rules.Hook{Argv: []string{helper}, TimeoutSeconds: 5}, OnCreate: true, OnReuse: false},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := LifecycleRequest{CardID: "Recover1", BoardID: "board1"}
			rec, path, _, _, err := m.acquireOrCreate(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			rec.Stage = phase
			rec.Materialized = true
			rec.Shared = true
			if phase == "full_materialization_not_recorded" {
				rec.Stage = "created"
				rec.Materialized = false
				rec.Shared = false
				writeFile(t, filepath.Join(path, "source.txt"), "user edit\n")
			}
			if err := m.writeRecord(context.Background(), path, rec); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Prepare(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			prepared, err := os.ReadFile(filepath.Join(path, "prepared"))
			if err != nil || string(prepared) != "ready" {
				t.Fatalf("incomplete creation was marked ready without exactly one required prepare: %q %v", prepared, err)
			}
			if phase == "full_materialization_not_recorded" {
				data, err := os.ReadFile(filepath.Join(path, "source.txt"))
				if err != nil || string(data) != "user edit\n" {
					t.Fatalf("recovery changed existing source: %q %v", data, err)
				}
			}
			recovered, err := m.readRecord(context.Background(), path)
			if err != nil || !recovered.Materialized || recovered.Stage != "ready" {
				t.Fatalf("recovery did not persist a materialized ready record: %#v %v", recovered, err)
			}
		})
	}
}

func TestIndependentSupervisorGitLockHonorsDeadline(t *testing.T) {
	m := &LifecycleManager{BaseRepo: t.TempDir(), WorktreesBase: t.TempDir(), baseCommonGitDir: t.TempDir()}
	m.gitMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.Prepare(ctx, LifecycleRequest{CardID: "Card1", BoardID: "board1"})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("preparation returned %v, want context deadline exceeded", err)
		}
	case <-time.After(150 * time.Millisecond):
		t.Error("preparation ignored deadline while waiting for repository lock")
	}
	m.gitMu.Unlock()
	postTimeoutLock := make(chan error, 1)
	go func() {
		release, err := lockLifecycleMutex(context.Background(), &m.gitMu)
		if err == nil {
			release()
		}
		postTimeoutLock <- err
	}()
	select {
	case err := <-postTimeoutLock:
		if err != nil {
			t.Fatalf("timed-out waiter stranded the in-process lock: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed-out waiter stranded the in-process lock")
	}

	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("platform has no flock lifecycle lock")
	}
	lockPath := filepath.Join(t.TempDir(), "lifecycle.lock")
	readyPath := filepath.Join(t.TempDir(), "helper-ready")
	releasePath := filepath.Join(t.TempDir(), "helper-release")
	helper := exec.Command(os.Args[0], "-test.run=^TestIndependentSupervisorLifecycleLockHelper$", "--", lockPath, readyPath, releasePath)
	helper.Env = append(os.Environ(), "KARDBRD_LIFECYCLE_LOCK_HELPER=1")
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	helperDone := make(chan error, 1)
	go func() { helperDone <- helper.Wait() }()
	helperWaited := false
	t.Cleanup(func() {
		if helperWaited {
			return
		}
		_ = os.WriteFile(releasePath, nil, 0o600)
		select {
		case <-helperDone:
			helperWaited = true
		case <-time.After(time.Second):
			_ = helper.Process.Kill()
			<-helperDone
			helperWaited = true
		}
	})
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		select {
		case err := <-helperDone:
			helperWaited = true
			t.Fatalf("cross-process lock helper exited before ready: %v", err)
		case <-time.After(5 * time.Millisecond):
		}
	}
	crossCtx, crossCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer crossCancel()
	if _, err := lockLifecycleGitAdministration(crossCtx, lockPath); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cross-process lock returned %v, want context deadline exceeded", err)
	}
	if err := os.WriteFile(releasePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-helperDone:
		helperWaited = true
		if err != nil {
			t.Fatalf("cross-process lock helper failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cross-process lock helper did not release")
	}
	releaseAfterCrossProcess, err := lockLifecycleGitAdministration(context.Background(), lockPath)
	if err != nil {
		t.Fatalf("cross-process lock remained stranded: %v", err)
	}
	releaseAfterCrossProcess()
}

func TestIndependentSupervisorLifecycleLockHelper(t *testing.T) {
	if os.Getenv("KARDBRD_LIFECYCLE_LOCK_HELPER") != "1" {
		return
	}
	marker := -1
	for index, arg := range os.Args {
		if arg == "--" {
			marker = index
			break
		}
	}
	if marker < 0 || len(os.Args) != marker+4 {
		t.Fatal("missing lifecycle lock helper arguments")
	}
	lockPath, readyPath, releasePath := os.Args[marker+1], os.Args[marker+2], os.Args[marker+3]
	release, err := lockLifecycleGitAdministration(context.Background(), lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.WriteFile(readyPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(releasePath); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestIndependentSupervisorNormalHelperExitReapsInheritedPipes(t *testing.T) {
	m := &LifecycleManager{}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "child.pid")
	t.Cleanup(func() {
		data, err := os.ReadFile(pidPath)
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			_ = exec.Command("kill", "-KILL", strconv.Itoa(pid)).Run()
		}
	})
	result, err := m.run(ctx, dir, []string{"/bin/sh", "-c", "printf direct-output; sleep 30 & echo $! > child.pid; exit 0"})
	if err != nil {
		t.Fatalf("successful direct helper exit became a timeout while descendant held output pipes: %v", err)
	}
	if result.Stdout != "direct-output" {
		t.Fatalf("successful helper output was not preserved: %q", result.Stdout)
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("helper did not record its inherited-pipe descendant: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("invalid inherited-pipe descendant PID %q: %v", data, err)
	}
	deadline := time.After(time.Second)
	for {
		if err := exec.Command("kill", "-0", strconv.Itoa(pid)).Run(); err != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("direct helper descendant %d survived normal-exit cleanup", pid)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestExternalSupervisorSuccessfulHooksCloseOutputDescriptors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses Linux process descriptor inventory")
	}
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	manager := &LifecycleManager{}
	for range 10 {
		if _, err := manager.run(context.Background(), t.TempDir(), []string{"/bin/true"}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) > len(before)+1 {
		t.Fatalf("successful helpers leaked output descriptors: before=%d after=%d", len(before), len(after))
	}
}
