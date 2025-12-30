// Manual test script to verify command wrapping works in practice
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
)

func main() {
	fmt.Println("=== Command Wrapping Verification Test ===")
	fmt.Println()

	// Create executor
	executor := tmux.NewTmuxExecutor()

	// Test session ID
	sessionID := "wrap-test-" + fmt.Sprintf("%d", time.Now().Unix())

	// Clean up on exit
	defer func() {
		fmt.Println("\n=== Cleanup ===")
		executor.KillSession(sessionID)
		fmt.Println("✓ Test session killed")
	}()

	// Create test session
	fmt.Printf("Creating test session: %s\n", sessionID)
	err := executor.CreateSession(sessionID, "/tmp")
	if err != nil {
		log.Fatalf("Failed to create session: %v", err)
	}
	fmt.Println("✓ Session created")

	// Test cases: create windows with commands that would normally exit immediately
	testCases := []struct {
		name    string
		command string
	}{
		{"immediate-quit", "echo 'test' && exit"},
		{"simple-command", "ls"},
		{"already-wrapped", `bash -ic "echo wrapped"`},
	}

	fmt.Println("\n=== Creating Test Windows ===")
	for i, tc := range testCases {
		fmt.Printf("\n%d. Testing: %s\n", i+1, tc.name)
		fmt.Printf("   Original command: %s\n", tc.command)

		// Create window - wrapping happens here
		windowID, err := executor.CreateWindow(sessionID, tc.name, tc.command)
		if err != nil {
			fmt.Printf("   ✗ Failed to create window: %v\n", err)
			continue
		}
		fmt.Printf("   ✓ Window created: %s\n", windowID)

		// Brief pause to let command start
		time.Sleep(100 * time.Millisecond)
	}

	// List all windows to verify they exist
	fmt.Println("\n=== Verifying Windows Exist ===")
	time.Sleep(500 * time.Millisecond) // Give windows time to initialize

	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		log.Fatalf("Failed to list windows: %v", err)
	}

	fmt.Printf("\nFound %d windows:\n", len(windows))
	for _, w := range windows {
		status := "dead"
		if w.Running {
			status = "running"
		}
		fmt.Printf("  - %s (%s): %s\n", w.TmuxWindowID, w.Name, status)
	}

	// Verify wrapping worked: windows should still be alive despite immediate-quit commands
	runningCount := 0
	for _, w := range windows {
		if w.Running {
			runningCount++
		}
	}

	fmt.Println("\n=== Test Results ===")
	if runningCount >= len(testCases) {
		fmt.Printf("✓ SUCCESS: %d/%d windows are running\n", runningCount, len(windows))
		fmt.Println("  Command wrapping is working correctly!")
		os.Exit(0)
	} else {
		fmt.Printf("✗ FAILURE: Only %d/%d windows are running\n", runningCount, len(windows))
		fmt.Println("  Some windows died - wrapping may not be working")
		os.Exit(1)
	}
}
