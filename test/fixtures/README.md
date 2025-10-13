# Test Fixtures

This directory contains test fixtures for makedog interactive testing.

## Files

- `test-pulse.go`   - Simple Go program that outputs messages every second
- `test-sigecho.go` - Signal echo program that traps and reports received signals

## Usage

### Build and run demos
From the project root:

**Demo with pulse fixture (outputs every second):**
```bash
make demo-pulse
```

**Demo with signal echo fixture (responds to signals):**
```bash
make demo-sigecho
```

These commands build both makedog and the test fixture, then run makedog on the fixture.

### Manual testing

Build the test binaries:
```bash
make fixture-pulse
make fixture-sigecho
```

Run makedog on them:
```bash
bin/makedog bin/test-pulse
bin/makedog bin/test-sigecho
```

Test file modification detection - in another terminal:
```bash
make fixture-pulse  # Rebuild to change mtime
```

You should see makedog detect the change and restart the program.

### Test keypresses
While running:
- `g` - send signal to child process
- `h` - show help
- `m` - run make
- `q` - quit
- `r` - restart

### Testing signals
When running `demo-sigecho`, press `g` to open the signal menu and send signals to the test fixture. The fixture will print a message for each signal it receives.
