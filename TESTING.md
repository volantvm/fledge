# Testing Strategy

This document describes Fledge's testing approach and coverage.

## Test Coverage

Current test coverage: **21.3% overall**

### Coverage by Package

| Package | Coverage | Notes |
|---------|----------|-------|
| `internal/config` | 81.6% | ✅ Comprehensive unit tests |
| `internal/builder` | 20.0% | ⚠️ Mapping/agent only (see below) |
| `internal/utils` | 0.0% | Utility functions (download, checksum) |
| `internal/logging` | 0.0% | Simple logger wrapper |
| `cmd/fledge` | 0.0% | CLI entry point |

## Why 21.3% Instead of 80%?

The low overall coverage is **intentional and appropriate** for this type of tool:

### The Core Builders Require Root and System Dependencies

The two main builders (`initramfs.go` and `oci_rootfs.go`) cannot be easily unit tested because they:

1. **Require root privileges**
   - Mount/unmount filesystems
   - Create loop devices
   - Write to `/dev/loop*`

2. **Require system binaries**
   - `gcc` (initramfs: compiling init.c)
   - `skopeo` (OCI: downloading images)
   - `umoci` (OCI: unpacking layers)
   - `losetup`, `mount`, `umount` (OCI: filesystem operations)
   - `mkfs.ext4`, `mkfs.xfs`, `mkfs.btrfs` (OCI: filesystem creation)
   - `e2fsck`, `resize2fs`, `dumpe2fs` (OCI: ext4 shrinking)

3. **Perform complex system operations**
   - Download multi-gigabyte Docker images
   - Unpack and manipulate filesystem layers
   - Create, mount, and unmount disk images
   - Compile C code

### What We DO Test

✅ **Configuration parsing and validation** (81.6% coverage)
- All TOML parsing logic
- Strategy-specific validation
- Default value application
- Error handling

✅ **File mapping logic** (100% of mapping.go)
- FHS path detection
- Permission determination
- File/directory copying
- Mapping preparation and application

✅ **Agent sourcing** (100% of testable paths)
- Local file strategy
- Error handling
- Cleanup logic

## Testing Approach

### Unit Tests (Automated in CI)

Located in `*_test.go` files alongside source code.

```bash
# Run all unit tests
go test ./...

# With coverage
go test -cover ./...

# With race detection
go test -race ./...
```

### Integration Tests (Manual)

The builders are tested through **real-world usage**:

1. **Build the tool**: `make build`
2. **Test initramfs strategy**:
   ```bash
   cd test/fixtures/initramfs-minimal
   sudo ../../../fledge build
   file plugin.cpio.gz
   ```

3. **Test OCI rootfs strategy**:
   ```bash
   cd test/fixtures/oci-nginx
   sudo ../../../fledge build
   file nginx.img
   ```

4. **Test with Volant**:
   ```bash
   volant run --plugin plugin.cpio.gz
   ```

### CI/CD Testing

Our GitHub Actions workflow tests:

- ✅ Unit tests on every push/PR
- ✅ Code formatting (`gofmt`)
- ✅ Static analysis (`go vet`)
- ✅ Race condition detection (`-race`)
- ✅ Security scanning (gosec, CodeQL)
- ✅ Dependency vulnerabilities (govulncheck)
- ✅ Multi-platform builds (Linux, macOS × AMD64, ARM64)

## Quality Assurance

Even with 21.3% test coverage, quality is assured through:

### 1. High Coverage Where It Matters

The **critical logic** is thoroughly tested:
- Configuration parsing: **81.6%** - validates user input
- File mapping: **100%** - ensures correct permissions
- Agent sourcing: **100%** - verifies binary acquisition

### 2. Static Analysis

- `go vet` catches common mistakes
- `gosec` finds security issues
- CodeQL performs deep code analysis
- `govulncheck` detects vulnerable dependencies

### 3. Real-World Testing

The builders are tested by:
- Manual testing during development
- Integration testing before releases
- Community usage and bug reports
- Example configurations in `docs/examples/`

### 4. Code Review

- All changes require review
- Each commit is logically isolated
- Comprehensive commit messages

## Adding Tests

### For New Configuration Options

Add tests to `internal/config/config_test.go`:

```go
func TestYourNewFeature(t *testing.T) {
    // Test valid configuration
    // Test invalid configuration
    // Test default values
}
```

### For New Mapping Logic

Add tests to `internal/builder/mapping_test.go`:

```go
func TestYourMappingFeature(t *testing.T) {
    // Test file operations
    // Test permission handling
    // Test error cases
}
```

### For Builder Changes

**Do NOT add unit tests for builders**. Instead:

1. Update integration test fixtures in `test/fixtures/`
2. Document the manual testing steps
3. Test with actual Volant deployment

## Running Tests Locally

```bash
# Quick test
make test

# With coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Race detection (important for concurrent code)
go test -race ./...

# Specific package
go test -v ./internal/config

# Verbose output
go test -v ./...
```

## Continuous Integration

Tests run automatically on:
- Every push to feature branches
- Every pull request
- Weekly security scans (Mondays)

See `.github/workflows/ci.yml` for complete CI configuration.

## Future Testing Improvements

Potential enhancements (not required for v1.0):

1. **Mock filesystem operations** for builder testing
2. **Docker-in-Docker** for OCI testing in CI
3. **Benchmark tests** for performance regression detection
4. **Fuzzing** for input validation robustness

## Conclusion

Fledge's 21.3% test coverage reflects **appropriate testing for a system integration tool**:

- ✅ High coverage (81.6%) for business logic (config)
- ✅ Tested critical paths (mapping, agent sourcing)
- ✅ Comprehensive CI/CD pipeline
- ✅ Security scanning and static analysis
- ✅ Manual integration testing

The untested code (builders) requires root and system dependencies, making unit testing impractical. These components are validated through integration testing and real-world usage.

---

**Questions?** Open an issue or discussion on GitHub.
