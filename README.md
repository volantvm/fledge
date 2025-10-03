# Fledge - Volant Plugin Builder

**Fledge** is a production-grade command-line tool for building Volant plugin artifacts. It provides a simple, declarative interface for creating bootable filesystem images and initramfs archives for the [Volant](https://github.com/volantvm/volant) microVM orchestration engine.

## Features

- **Declarative Configuration**: Define your plugin with a simple `fledge.toml` file
- **Two Build Strategies**:
  - **OCI Rootfs**: Convert OCI/Docker images to bootable ext4/xfs/btrfs filesystem images
  - **Initramfs**: Build minimal initramfs archives with busybox, kestrel agent, and your application
- **FHS Compliance**: Follows the Filesystem Hierarchy Standard for predictable and compatible artifacts
- **Reproducible Builds**: Deterministic output with normalized file timestamps
- **Smart Permissions**: Automatically sets execute permissions for binaries in standard paths
- **Flexible Agent Sourcing**: Multiple strategies for obtaining the Volant kestrel agent

## Prerequisites

### For All Builds
- Go 1.24 or later
- Root privileges (required for filesystem operations and Volant ecosystem)

### For OCI Rootfs Strategy
- `skopeo` - OCI image operations
- `umoci` - OCI image unpacking
- `mkfs.<type>` - Filesystem utilities (e.g., `e2fsprogs` for ext4, `xfsprogs` for xfs)
- `losetup`, `mount` - Kernel utilities

### For Initramfs Strategy
- `gcc` - C compiler (for compiling embedded init.c)
- `curl` - Download utilities
- `find`, `cpio`, `gzip` - Archive creation tools

## Installation

### From Source

```bash
git clone https://github.com/volantvm/fledge.git
cd fledge
make build
sudo make install
```

### From Release

Download the latest release from [GitHub Releases](https://github.com/volantvm/fledge/releases) and place the binary in your PATH.

## Quick Start

### 1. Create a Plugin Repository

Create a directory for your plugin with the following structure:

```
my-plugin/
├── fledge.toml              # Build configuration
├── plugin-manifest.json     # Volant plugin manifest
├── payload/                 # Your application files
│   ├── my-app              # Application binary
│   └── config.yml          # Configuration files
└── README.md
```

### 2. Write fledge.toml

#### Example: Initramfs Plugin

```toml
version = "1"
strategy = "initramfs"

[agent]
source_strategy = "release"
version = "latest"

[source]
busybox_url = "https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox"
busybox_sha256 = "6e123e7f3202a8c1e9b1f94d8941580a25135382b99e8d3e34fb858bba311348"

[mappings]
"payload/my-app" = "/usr/bin/my-app"
"payload/config.yml" = "/etc/my-app/config.yml"
```

#### Example: OCI Rootfs Plugin

```toml
version = "1"
strategy = "oci_rootfs"

[source]
image = "docker.io/library/nginx:alpine"

[filesystem]
type = "ext4"
size_buffer_mb = 100

[mappings]
"payload/nginx.conf" = "/etc/nginx/nginx.conf"
```

### 3. Build the Artifact

```bash
# Build with default fledge.toml
sudo fledge build

# Build with custom config and output
sudo fledge build -c path/to/fledge.toml -o my-plugin.img

# Verbose build
sudo fledge build -v

# Quiet build (errors only)
sudo fledge build -q
```

## Documentation

- [Complete fledge.toml Specification](docs/fledge-toml-spec.md)
- [Plugin Authoring Guide](docs/plugin-authoring.md)
- [Examples](docs/examples/)

## Development

### Running Tests

```bash
make test
```

### Building from Source

```bash
make build
```

### Running CI Checks

```bash
make ci
```

## License

Fledge is licensed under the [Business Source License 1.1](LICENSE).

- **Non-production use**: Free for development, testing, and evaluation
- **Production use**: Licensed for Volant plugin development and distribution
- **Change date**: 2029-10-04
- **Change license**: Apache License 2.0

## Contributing

Contributions are welcome! Please ensure:
- All tests pass (`make test`)
- Code is formatted (`make fmt`)
- No vet warnings (`make vet`)
- Documentation is updated

## Support

- GitHub Issues: [volantvm/fledge/issues](https://github.com/volantvm/fledge/issues)
- Volant Documentation: [github.com/volantvm/volant](https://github.com/volantvm/volant)

---

**Copyright © 2025 HYPR. PTE. LTD.**
