# Test Fixtures

This directory contains test fixtures for makedog interactive testing.

## Files

- `test-program.go`            - Simple Go program that outputs messages every second
- `PROJ_ROOT/bin/test-program` - Compiled binary (created by `make fixture`)

## Usage

### Build and run demo
From the project root:
```bash
make demo
```

This builds both makedog and the test fixture, then runs makedog on the fixture.

### Manual testing

Build the test binary:
```bash
make fixture
```

Run makedog on it:
```bash
bin/makedog test/fixtures/test-program
```

Test file modification detection - in another terminal:
```bash
make fixture  # Rebuild to change mtime
```

You should see makedog detect the change and restart the program.

### Test keypresses
While running:
- `h` - show help
- `m` - run make
- `q` - quit
