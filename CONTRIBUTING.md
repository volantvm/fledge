# Contributing to Fledge

Thank you for your interest in contributing to Fledge! This document provides guidelines and instructions for contributing.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Making Changes](#making-changes)
- [Testing](#testing)
- [Submitting Changes](#submitting-changes)
- [Code Review Process](#code-review-process)
- [Release Process](#release-process)

## Code of Conduct

By participating in this project, you agree to maintain a respectful and inclusive environment. We expect all contributors to:

- Be respectful and considerate
- Accept constructive criticism gracefully
- Focus on what's best for the community
- Show empathy towards other community members

## Getting Started

### Prerequisites

- Go 1.24 or later
- Git
- Basic understanding of Volant plugins
- For testing:
  - Linux machine (for integration tests)
  - gcc, skopeo, umoci (for building plugins)
  - Root access via sudo

### Fork and Clone

1. Fork the repository on GitHub
2. Clone your fork:
   ```bash
   git clone https://github.com/YOUR_USERNAME/fledge.git
   cd fledge
   ```

3. Add upstream remote:
   ```bash
   git remote add upstream https://github.com/volantvm/fledge.git
   ```

## Development Setup

### Install Dependencies

```bash
# Download Go dependencies
go mod download

# Verify your environment
make test
make build
```

### Project Structure

```
fledge/
├── cmd/fledge/          # CLI entry point
├── internal/
│   ├── builder/         # Core builders (initramfs, OCI)
│   ├── config/          # Configuration parsing
│   ├── logging/         # Logging utilities
│   └── utils/           # Shared utilities
├── docs/examples/       # Example configurations
├── .github/workflows/   # CI/CD pipelines
└── Makefile            # Build automation
```

### Available Make Targets

```bash
make build      # Build the binary
make test       # Run tests
make fmt        # Format code
make vet        # Run static analysis
make ci         # Run all CI checks
make clean      # Clean build artifacts
make install    # Install to /usr/local/bin
```

## Making Changes

### Branch Naming

Use descriptive branch names:
- `feature/add-xfs-support`
- `fix/config-validation-bug`
- `docs/improve-readme`
- `refactor/simplify-mapping-logic`

### Commit Messages

Follow conventional commit format:

```
<type>(<scope>): <subject>

<body>

<footer>
```

**Types:**
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `refactor`: Code refactoring
- `test`: Adding or updating tests
- `ci`: CI/CD changes
- `chore`: Maintenance tasks

**Examples:**

```
feat(builder): add btrfs filesystem support

Implement btrfs filesystem creation for OCI rootfs builds.
Includes mkfs.btrfs invocation and type-specific flags.

Closes #123
```

```
fix(config): validate agent checksum format

Add validation to ensure checksums are in the format "sha256:...".
Previously accepted invalid formats that would fail at runtime.
```

### Code Style

- Run `make fmt` before committing
- Follow standard Go conventions
- Use descriptive variable names
- Add comments for complex logic
- Keep functions focused and small

### Adding Features

1. **Discuss first**: Open an issue to discuss your proposed feature
2. **Write tests**: Add tests for new functionality
3. **Update docs**: Update README and examples if needed
4. **Follow patterns**: Match existing code structure

## Testing

### Running Tests

```bash
# All tests
make test

# Specific package
go test -v ./internal/config

# With coverage
go test -cover ./...

# With race detection
go test -race ./...
```

### Writing Tests

**Unit Tests** (for business logic):

```go
func TestYourFeature(t *testing.T) {
    // Arrange
    input := "test input"
    
    // Act
    result := YourFunction(input)
    
    // Assert
    if result != expected {
        t.Errorf("got %v, want %v", result, expected)
    }
}
```

**Table-Driven Tests** (for multiple cases):

```go
func TestValidation(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        wantErr bool
    }{
        {"valid input", "valid", false},
        {"invalid input", "invalid", true},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := Validate(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("got error %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Test Coverage

- Target: 80%+ for business logic (config, mapping)
- Builders require manual/integration testing
- See [TESTING.md](TESTING.md) for our testing philosophy

## Submitting Changes

### Pull Request Process

1. **Update your branch**:
   ```bash
   git fetch upstream
   git rebase upstream/main
   ```

2. **Run all checks**:
   ```bash
   make ci
   ```

3. **Push to your fork**:
   ```bash
   git push origin feature/your-feature
   ```

4. **Open a Pull Request**:
   - Provide a clear title and description
   - Reference related issues
   - Include testing instructions
   - Add screenshots if applicable

### PR Template

```markdown
## Description
Brief description of the change.

## Motivation
Why is this change needed?

## Changes
- Change 1
- Change 2

## Testing
How was this tested?

## Checklist
- [ ] Tests added/updated
- [ ] Documentation updated
- [ ] Code formatted (make fmt)
- [ ] No vet warnings (make vet)
- [ ] All tests passing
```

## Code Review Process

### What We Look For

- **Correctness**: Does it work as intended?
- **Tests**: Are there adequate tests?
- **Style**: Does it follow Go conventions?
- **Documentation**: Are changes documented?
- **Performance**: Are there any performance concerns?
- **Security**: Are there security implications?

### Review Timeline

- Initial review: Within 3 business days
- Follow-up: Within 2 business days after updates
- Approval requires: 1 maintainer approval

### Addressing Feedback

- Respond to all comments
- Make requested changes in new commits
- Mark resolved conversations
- Request re-review when ready

## Release Process

### Versioning

We follow [Semantic Versioning](https://semver.org/):
- **MAJOR**: Breaking changes
- **MINOR**: New features (backwards compatible)
- **PATCH**: Bug fixes

### Release Steps (Maintainers)

1. Update CHANGELOG.md
2. Update version in documentation
3. Create and push version tag:
   ```bash
   git tag -a v0.2.0 -m "Release v0.2.0"
   git push upstream v0.2.0
   ```
4. GitHub Actions automatically creates release

## Questions?

- **Bug reports**: [Open an issue](https://github.com/volantvm/fledge/issues/new?template=bug_report.md)
- **Feature requests**: [Open an issue](https://github.com/volantvm/fledge/issues/new?template=feature_request.md)
- **Questions**: [Start a discussion](https://github.com/volantvm/fledge/discussions)
- **Security**: Email security@volantvm.io

## Recognition

Contributors are recognized in:
- CHANGELOG.md (for each release)
- GitHub contributors page
- Release notes

Thank you for contributing to Fledge! 🚀
