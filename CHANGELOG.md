# Changelog

All notable changes to Fledge will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2025-10-04

### Initial Release

First public release of Fledge, the Volant Plugin Builder toolkit.

### Added

#### Core Features
- **Two Build Strategies**
  - Initramfs: Lightweight CPIO archives for fast-booting stateless workloads
  - OCI Rootfs: Convert Docker/OCI images to bootable filesystem images (ext4/xfs/btrfs)

#### Configuration System
- Declarative TOML-based configuration (`fledge.toml`)
- Comprehensive validation with helpful error messages
- Strategy-specific configuration options
- Default value application for optional fields
- 81.6% test coverage

#### Agent Sourcing
- Three strategies for obtaining the Volant kestrel agent:
  - `release`: Download from GitHub releases
  - `local`: Use local filesystem binary
  - `http`: Download from custom URL with checksum verification
- SHA256 checksum validation
- Automatic cleanup of temporary files

#### File Mapping System
- Custom file and directory mappings
- FHS (Filesystem Hierarchy Standard) aware permissions
- Automatic executable detection for standard paths:
  - `/bin/*`, `/sbin/*`, `/usr/bin/*`, `/usr/sbin/*`, `/usr/local/bin/*`, `/opt/bin/*`
  - `/lib/*`, `/usr/lib/*`, `/lib64/*` (for shared libraries)
- Recursive directory copying with per-file permissions
- Symlink preservation

#### Initramfs Builder
- Embedded init.c with go:embed
- Deterministic gcc compilation
- Busybox integration with automatic symlink generation
- CPIO archive creation with gzip compression
- Mtime normalization for reproducible builds
- 8-step build pipeline with progress logging

#### OCI Rootfs Builder  
- Skopeo integration for image download (Docker daemon + remote registry fallback)
- Umoci integration for layer unpacking
- OCI config extraction to `/etc/fsify-entrypoint`
- Multi-filesystem support (ext4/xfs/btrfs)
- Automatic filesystem sizing with configurable buffer
- Loop device management
- ext4 filesystem shrinking to optimal size
- 12-step build pipeline with progress bars

#### CLI
- `fledge build` command with comprehensive flag support
- `-c, --config`: Custom config file path
- `-o, --output`: Custom output file path
- `-v, --verbose`: Debug-level logging
- `-q, --quiet`: Error-only logging
- `fledge version`: Version information
- Signal handling for graceful cleanup (SIGINT/SIGTERM)
- Root privilege verification
- Smart output path generation from image names

#### Documentation
- Comprehensive README as plugin authoring guide
- 6 example configurations for common use cases:
  - Minimal initramfs
  - Go web server (initramfs)
  - Rust microservice (initramfs)
  - Nginx (OCI)
  - PostgreSQL (OCI)
  - Python Flask (OCI)
- TESTING.md explaining testing strategy
- Complete tutorials and best practices
- Troubleshooting guide
- CI/CD integration examples

#### CI/CD
- GitHub Actions workflows:
  - Main CI pipeline (test, lint, build, security scan)
  - Automated releases on version tags
  - CodeQL security analysis
- Multi-platform builds (Linux/macOS × AMD64/ARM64)
- Security scanning (gosec, CodeQL, govulncheck)
- Code coverage tracking (Codecov)
- Automated changelog generation

### Technical Details

- **Language**: Go 1.24
- **License**: Business Source License 1.1 (converts to Apache 2.0 on 2029-10-04)
- **Dependencies**:
  - github.com/BurntSushi/toml v1.4.0
  - github.com/schollz/progressbar/v3 v3.17.1
  - github.com/spf13/cobra v1.8.1
- **System Requirements**:
  - Linux (required for building)
  - Root access (for filesystem operations)
  - For initramfs: gcc, curl, cpio, gzip
  - For OCI rootfs: skopeo, umoci, mkfs.*, losetup, mount

### Development Stats

- 9 commits in feature branch
- 2,500+ lines of Go code
- 37 test cases
- 21.3% overall test coverage (81.6% for config, 20% for builder)
- 8 example configurations
- 3 GitHub Actions workflows

[Unreleased]: https://github.com/volantvm/fledge/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/volantvm/fledge/releases/tag/v0.1.0
