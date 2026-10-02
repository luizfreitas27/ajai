//go:build browser

package browser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/chrome"
	"github.com/luizfreitas27/aja/internal/procs"
)

// TestCloseQuitsChromeOnAjasOwnProfileAndKeepsTheProfile drives a real headless
// Chrome: with QuitOnClose, Close ends the Chrome process, the profile survives
// (with what the page stored) and is free for the next run at once.
func TestCloseQuitsChromeOnAjasOwnProfileAndKeepsTheProfile(t *testing.T) {
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	profile := filepath.Join(t.TempDir(), "chrome-profile")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	open := func() Browser {
		br, err := Open(ctx, Options{Bin: bin, ProfileDir: profile, HeadlessForTests: true, QuitOnClose: true})
		if err != nil {
			t.Fatal(err)
		}
		return br
	}
	br := open()
	pid := br.(*rodBrowser).launcher.PID()
	if !procs.Alive(pid) {
		t.Fatal("Chrome should be running")
	}
	start := time.Now()
	if err := br.Close(); err != nil {
		t.Fatal(err)
	}
	if procs.Alive(pid) {
		t.Error("Close must end Chrome on the aja profile")
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("Chrome took %s to quit", time.Since(start))
	}
	if _, err := os.Stat(filepath.Join(profile, "Default")); err != nil {
		t.Fatalf("the profile must survive Close: %v", err)
	}
	if profileLocked(profile) {
		t.Error("the profile must be free once Chrome has quit")
	}
	again := open() // the next run
	_ = again.Close()
}

// TestCloseStillQuitsGracefullyAfterCtrlC: Ctrl-C cancels the context Chrome was
// opened with before Close runs; Chrome must still quit at once, not be killed
// after the timeout.
func TestCloseStillQuitsGracefullyAfterCtrlC(t *testing.T) {
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	profile := filepath.Join(t.TempDir(), "chrome-profile")
	ctx, cancel := context.WithCancel(context.Background())
	br, err := Open(ctx, Options{Bin: bin, ProfileDir: profile, HeadlessForTests: true, QuitOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	pid := br.(*rodBrowser).launcher.PID()
	cancel()                // Ctrl-C
	time.Sleep(time.Second) // the run winds down before Chrome is closed
	start := time.Now()
	_ = br.Close()
	if procs.Alive(pid) {
		t.Error("Chrome must have quit")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Close took %s after Ctrl-C: Chrome was killed, not quit", d)
	}
}
