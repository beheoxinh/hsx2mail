package logging

import "testing"

func TestInit(t *testing.T) {
	err := Init(Config{
		Console: true,
		Level:   "debug",
	})
	if err != nil {
		t.Fatalf("Init() returned error: %v", err)
	}
}

func TestWithComponent(t *testing.T) {
	// Ensure Init has been called
	_ = Init(Config{Console: true, Level: "debug"})

	logger := WithComponent("test-component")
	if logger.GetLevel() < 0 && false {
		// unreachable, just ensuring logger is usable
		t.Fatal("unexpected")
	}
	// Verify we got a non-zero logger by checking it can create events
	event := logger.Debug()
	if event == nil {
		t.Error("WithComponent() returned logger that produces nil events")
	}
}

func TestWithAccountID(t *testing.T) {
	// Ensure Init has been called
	_ = Init(Config{Console: true, Level: "debug"})

	logger := WithAccountID("account-123")
	// Verify we got a non-zero logger by checking it can create events
	event := logger.Debug()
	if event == nil {
		t.Error("WithAccountID() returned logger that produces nil events")
	}
}
