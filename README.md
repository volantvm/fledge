<div align="center">
  <img src="banner.png" alt="Fledge" width="600"/>
  <h1>Fledge: The Volant Plugin Toolkit</h1>
  <p><strong>Your complete guide to building production-ready Volant plugins</strong></p>
</div>

---

## Welcome to Fledge

Fledge is your toolkit for creating plugins that run on [Volant](https://github.com/volantvm/volant), the next-generation microVM orchestration engine. Whether you're packaging a web server, a database, or a custom application, Fledge transforms your software into lightweight, bootable artifacts that Volant can deploy in milliseconds.

This guide will walk you through everything you need to know to build your first plugin and become proficient in Volant plugin development.

## Table of Contents

- [What You'll Build](#what-youll-build)
- [Before You Begin](#before-you-begin)
- [Your First Plugin in 5 Minutes](#your-first-plugin-in-5-minutes)
- [Understanding Build Strategies](#understanding-build-strategies)
- [The Configuration File: fledge.toml](#the-configuration-file-fledgetoml)
- [Complete Tutorials](#complete-tutorials)
  - [Tutorial 1: Building an Initramfs Plugin](#tutorial-1-building-an-initramfs-plugin)
  - [Tutorial 2: Converting a Docker Image to a Plugin](#tutorial-2-converting-a-docker-image-to-a-plugin)
  - [Tutorial 3: Adding Custom Files and Configurations](#tutorial-3-adding-custom-files-and-configurations)
- [Configuration Reference](#configuration-reference)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)
- [Advanced Topics](#advanced-topics)

---

## What You'll Build

With Fledge, you create **plugin artifacts** that Volant uses to launch ultra-lightweight virtual machines. These artifacts come in two flavors:

1. **Initramfs Plugins** (`.cpio.gz`) - Minimal, fast-booting archives perfect for stateless workloads
2. **OCI Rootfs Plugins** (`.img`) - Full filesystem images created from Docker/OCI images

Both types include the Volant kestrel agent, which enables Volant to manage your application's lifecycle, networking, and communication with the host.

---

## Before You Begin

### What You Need

**Essential for all plugins:**
- Linux machine (required for building)
- Root access (via `sudo`)
- Basic command-line familiarity

**For initramfs plugins:**
- `gcc` (C compiler)
- `curl`
- `cpio` and `gzip`

**For OCI rootfs plugins:**
- `skopeo` (install: `sudo apt install skopeo` or `brew install skopeo`)
- `umoci` (install from [GitHub releases](https://github.com/opencontainers/umoci/releases))
- Filesystem tools: `e2fsprogs` (ext4), `xfsprogs` (xfs), or `btrfs-progs` (btrfs)

### Installing Fledge

**From pre-built binary:**
```bash
# Download the latest release
curl -LO https://github.com/volantvm/fledge/releases/latest/download/fledge-linux-amd64
chmod +x fledge-linux-amd64
sudo mv fledge-linux-amd64 /usr/local/bin/fledge

# Verify installation
fledge version
```

**From source:**
```bash
git clone https://github.com/volantvm/fledge.git
cd fledge
make build
sudo make install
```

---

## Your First Plugin in 5 Minutes

Let's build a simple "Hello Volant" plugin to get familiar with the workflow.

### Step 1: Create Your Plugin Directory

```bash
mkdir hello-volant
cd hello-volant
```

### Step 2: Create fledge.toml

Create a file named `fledge.toml` with this content:

```toml
version = "1"
strategy = "initramfs"

[agent]
source_strategy = "release"
version = "latest"

[source]
busybox_url = "https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox"
busybox_sha256 = "6e123e7f3202a8c1e9b1f94d8941580a25135382b99e8d3e34fb858bba311348"
```

### Step 3: Build It

```bash
sudo fledge build
```

That's it! You've just created `plugin.cpio.gz` - a bootable initramfs that Volant can run. This minimal plugin includes:
- Busybox (providing essential Unix utilities)
- The kestrel agent (for Volant orchestration)
- A Volant-compatible init system

**What just happened?**
- Fledge downloaded busybox and verified its checksum
- It fetched the latest kestrel agent from GitHub
- It compiled an init program and assembled everything into a bootable archive
- File timestamps were normalized for reproducible builds

---

## Understanding Build Strategies

Fledge offers two build strategies, each suited for different use cases. Understanding when to use each will help you make the right choice for your plugin.

### Initramfs Strategy: Lightweight and Fast

**Best for:**
- Stateless applications
- Quick boot times (< 100ms)
- Minimal resource footprint
- Custom applications you compile yourself

**How it works:**
Fledge creates a CPIO archive containing busybox (for Unix utilities), the kestrel agent, and your application files. The entire filesystem lives in RAM, making it extremely fast but non-persistent.

**Example use cases:**
- API servers
- Data processors
- Queue workers
- Edge compute functions

**Typical sizes:** 5-50 MB

### OCI Rootfs Strategy: Full Compatibility

**Best for:**
- Existing Docker images
- Applications with many dependencies
- Need for persistent filesystem features
- Applications that expect a standard Linux environment

**How it works:**
Fledge takes an OCI (Docker) image, unpacks all its layers, adds the kestrel agent, and converts it to a bootable ext4/xfs/btrfs filesystem image. The result is a full Linux filesystem with all your application's dependencies intact.

**Example use cases:**
- Database servers (PostgreSQL, MySQL)
- Web applications (Node.js, Python apps)
- Pre-built software (Nginx, Redis)
- Complex application stacks

**Typical sizes:** 50 MB - 2 GB

### Choosing Your Strategy

Ask yourself:

1. **Do you already have a Docker image?** → Use **OCI Rootfs**
2. **Do you need the absolute fastest boot time?** → Use **Initramfs**
3. **Is your application stateless?** → Use **Initramfs**
4. **Do you have complex system dependencies?** → Use **OCI Rootfs**
5. **Do you want to minimize resource usage?** → Use **Initramfs**

---

## The Configuration File: fledge.toml

Every plugin is defined by a `fledge.toml` file. This declarative configuration tells Fledge exactly how to build your artifact. Let's break down each section.

### Basic Structure

```toml
version = "1"           # Configuration schema version (always "1" for now)
strategy = "initramfs"  # Build strategy: "initramfs" or "oci_rootfs"

[agent]
# How to obtain the kestrel agent

[source]
# Source materials for your build

[filesystem]
# Filesystem options (OCI rootfs only)

[mappings]
# Custom files to include in your plugin
```

### The [agent] Section

The kestrel agent is required for all Volant plugins. It's the bridge between your application and the Volant orchestrator. Fledge offers three ways to obtain it:

#### Strategy 1: GitHub Release (Recommended)

```toml
[agent]
source_strategy = "release"
version = "latest"  # or specific version like "v0.2.0"
```

Fledge downloads the agent from `github.com/volantvm/volant/releases`. Using `"latest"` ensures you always get the newest features and fixes.

#### Strategy 2: Local File

```toml
[agent]
source_strategy = "local"
path = "./kestrel"  # or "/absolute/path/to/kestrel"
```

Useful when you're developing the agent itself or working offline. The path can be relative to your fledge.toml or absolute.

#### Strategy 3: HTTP URL

```toml
[agent]
source_strategy = "http"
url = "https://your-server.com/kestrel"
checksum = "sha256:abc123..."  # SHA256 checksum (optional but recommended)
```

For custom distribution or CI/CD pipelines. Always include a checksum for security.

### The [source] Section

**For initramfs strategy:**

```toml
[source]
busybox_url = "https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox"
busybox_sha256 = "6e123e7f3202a8c1e9b1f94d8941580a25135382b99e8d3e34fb858bba311348"
```

Busybox provides essential Unix utilities (ls, cat, sh, etc.). The checksum ensures integrity.

**For oci_rootfs strategy:**

```toml
[source]
image = "nginx:alpine"  # Can be any OCI image
```

You can use:
- Docker Hub images: `nginx:alpine`, `postgres:15`
- Full references: `docker.io/library/nginx:alpine`
- Private registries: `registry.example.com/myapp:v1.0`
- Local images: `my-local-image:latest` (if present in Docker daemon)

### The [filesystem] Section (OCI Rootfs Only)

```toml
[filesystem]
type = "ext4"           # Options: "ext4", "xfs", "btrfs"
size_buffer_mb = 50     # Extra space to add (prevents "disk full" errors)
preallocate = false     # true = fallocate (faster), false = sparse (smaller)
```

**Choosing a filesystem type:**
- **ext4**: Universal compatibility, fast, supports shrinking (recommended)
- **xfs**: Better for large files and high throughput
- **btrfs**: Advanced features like snapshots (if you need them)

**Size buffer:** Fledge calculates the exact space needed and adds this buffer. 50-100 MB is typically sufficient.

**Preallocate:** 
- `false` (default): Creates sparse files, saves disk space during build
- `true`: Preallocates space, slightly faster builds but uses more disk

### The [mappings] Section

This is where you add your custom files to the plugin:

```toml
[mappings]
"./payload/myapp" = "/usr/bin/myapp"
"./configs/app.conf" = "/etc/myapp/app.conf"
"./scripts/startup.sh" = "/usr/local/bin/startup.sh"
"./data" = "/var/lib/myapp"  # Entire directory
```

**Key points:**
- Source paths are relative to your fledge.toml location
- Destination paths must be absolute (start with `/`)
- Fledge automatically sets permissions based on standard paths:
  - `/usr/bin/*`, `/bin/*`, `/usr/local/bin/*` → executable (755)
  - `/lib/*`, `/usr/lib/*` → libraries (755)
  - Everything else → readable (644 for files, 755 for directories)
- You can map individual files or entire directories

---

## Complete Tutorials

### Tutorial 1: Building an Initramfs Plugin

Let's build a real plugin for a Go application that serves HTTP traffic.

**Scenario:** You have a Go web server called `webserver` that you want to run on Volant.

#### Step 1: Prepare Your Application

```bash
mkdir webserver-plugin
cd webserver-plugin

# Create a payload directory
mkdir payload

# Build your Go app (static binary required)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o payload/webserver ./cmd/webserver

# Add any config files
cat > payload/webserver.yaml << EOF
port: 8080
timeout: 30s
EOF
```

#### Step 2: Create fledge.toml

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
"payload/webserver" = "/usr/bin/webserver"
"payload/webserver.yaml" = "/etc/webserver/config.yaml"
```

#### Step 3: Build and Verify

```bash
# Build with verbose output to see what's happening
sudo fledge build -v

# Check the output
ls -lh plugin.cpio.gz
```

#### Step 4: Test With Volant

```bash
# Run your plugin (requires Volant installation)
volant run --plugin plugin.cpio.gz --memory 128M
```

**What we learned:**
- Static compilation is crucial (`CGO_ENABLED=0`)
- The kestrel agent handles startup and process management
- Configuration files can be mapped to standard locations

---

### Tutorial 2: Converting a Docker Image to a Plugin

Let's convert an Nginx Docker image into a Volant plugin.

**Scenario:** You want to run Nginx on Volant, possibly with custom configuration.

#### Step 1: Create Plugin Directory

```bash
mkdir nginx-plugin
cd nginx-plugin
```

#### Step 2: Create fledge.toml

```toml
version = "1"
strategy = "oci_rootfs"

[source]
image = "nginx:alpine"

[filesystem]
type = "ext4"
size_buffer_mb = 100
preallocate = false
```

#### Step 3: Build It

```bash
sudo fledge build -v
```

**Watch the magic happen:**
1. Skopeo downloads the image layers
2. Umoci unpacks them into a rootfs
3. Fledge installs the kestrel agent
4. A bootable ext4 image is created
5. The filesystem is optimized and shrunk

The output file will be named `nginx.img` (automatically derived from the image name).

#### Step 4: Adding Custom Configuration

Let's customize the Nginx configuration:

```bash
# Create a configs directory
mkdir configs

# Add custom nginx.conf
cat > configs/nginx.conf << 'EOF'
events {
    worker_connections 1024;
}

http {
    server {
        listen 80;
        location / {
            return 200 "Hello from Volant!\n";
        }
    }
}
EOF

# Update fledge.toml to include it
```

```toml
version = "1"
strategy = "oci_rootfs"

[source]
image = "nginx:alpine"

[filesystem]
type = "ext4"
size_buffer_mb = 100
preallocate = false

[mappings]
"configs/nginx.conf" = "/etc/nginx/nginx.conf"
```

Rebuild, and your custom configuration is now baked into the image!

---

### Tutorial 3: Adding Custom Files and Configurations

Let's create a plugin that includes multiple custom files and directories.

**Scenario:** Python Flask application with configs, static files, and dependencies.

#### Step 1: Project Structure

```bash
mkdir flask-plugin
cd flask-plugin

# Create directory structure
mkdir -p payload/app
mkdir -p payload/static
mkdir -p payload/templates
mkdir -p configs
```

#### Step 2: Application Files

```python
# payload/app/server.py
from flask import Flask, render_template

app = Flask(__name__, 
            template_folder='/var/www/templates',
            static_folder='/var/www/static')

@app.route('/')
def index():
    return render_template('index.html')

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=5000)
```

```html
<!-- payload/templates/index.html -->
<!DOCTYPE html>
<html>
<head><title>Volant Flask App</title></head>
<body>
    <h1>Running on Volant!</h1>
</body>
</html>
```

```yaml
# configs/app.yaml
debug: false
log_level: info
```

#### Step 3: fledge.toml Configuration

```toml
version = "1"
strategy = "oci_rootfs"

[source]
image = "python:3.11-alpine"

[filesystem]
type = "ext4"
size_buffer_mb = 150
preallocate = false

[mappings]
# Application code
"payload/app" = "/opt/app"

# Web content
"payload/static" = "/var/www/static"
"payload/templates" = "/var/www/templates"

# Configuration
"configs/app.yaml" = "/etc/flask-app/config.yaml"
```

#### Step 4: Build and Deploy

```bash
sudo fledge build -o flask-app.img
```

**What we learned:**
- Directory mappings preserve structure
- You can map multiple directories
- OCI images come with pre-installed dependencies (Python, in this case)

---

## Configuration Reference

### Complete fledge.toml Schema

```toml
version = "1"  # REQUIRED: Config schema version

strategy = "initramfs"  # REQUIRED: "initramfs" or "oci_rootfs"

# REQUIRED for initramfs
[agent]
source_strategy = "release"  # "release", "local", or "http"
version = "latest"           # For "release" strategy
path = "./kestrel"           # For "local" strategy
url = "https://..."          # For "http" strategy
checksum = "sha256:..."      # For "http" strategy (optional)

# REQUIRED: Source configuration
[source]
# For initramfs:
busybox_url = "https://..."
busybox_sha256 = "abc123..."

# For oci_rootfs:
image = "nginx:alpine"

# REQUIRED for oci_rootfs
[filesystem]
type = "ext4"              # "ext4", "xfs", or "btrfs"
size_buffer_mb = 50        # Integer, extra space in MB
preallocate = false        # Boolean

# OPTIONAL: File mappings
[mappings]
"source/path" = "/destination/path"
```

### Field Descriptions

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `version` | string | Yes | Always `"1"` |
| `strategy` | string | Yes | Build strategy |
| `agent.source_strategy` | string | Yes (initramfs) | How to get kestrel |
| `agent.version` | string | Conditional | For "release" strategy |
| `agent.path` | string | Conditional | For "local" strategy |
| `agent.url` | string | Conditional | For "http" strategy |
| `agent.checksum` | string | Optional | SHA256 for "http" |
| `source.busybox_url` | string | Yes (initramfs) | Busybox binary URL |
| `source.busybox_sha256` | string | Yes (initramfs) | Busybox checksum |
| `source.image` | string | Yes (oci_rootfs) | OCI image reference |
| `filesystem.type` | string | Yes (oci_rootfs) | Filesystem type |
| `filesystem.size_buffer_mb` | integer | Yes (oci_rootfs) | Extra space |
| `filesystem.preallocate` | boolean | Yes (oci_rootfs) | Allocation mode |
| `mappings` | table | Optional | Custom file mappings |

---

## Best Practices

### Security

1. **Always verify checksums** for downloaded binaries
2. **Use specific versions** instead of `latest` in production
3. **Review OCI images** before converting (scan with tools like Trivy)
4. **Minimize attack surface** by removing unnecessary files

### Performance

1. **Keep initramfs small** - every megabyte affects boot time
2. **Use static binaries** when possible (no runtime dependencies)
3. **Choose ext4 for OCI** if you don't need advanced filesystem features
4. **Enable preallocate** for faster builds on fast storage

### Reliability

1. **Test plugins** in a staging environment before production
2. **Version your plugins** alongside your application versions
3. **Store fledge.toml in version control** with your application
4. **Document custom mappings** for your team

### Organization

**Recommended project structure:**
```
my-plugin/
├── fledge.toml           # Build configuration
├── plugin-manifest.json  # Volant metadata
├── payload/              # Application binaries
├── configs/              # Configuration files
├── scripts/              # Helper scripts
├── tests/                # Integration tests
└── README.md             # Documentation
```

---

## Troubleshooting

### Common Issues

#### "must run as root"

**Problem:** Fledge requires root for filesystem operations.

**Solution:**
```bash
sudo fledge build
```

#### "skopeo: command not found"

**Problem:** OCI tools not installed.

**Solution:**
```bash
# Debian/Ubuntu
sudo apt install skopeo

# macOS
brew install skopeo

# For umoci, download from GitHub releases
```

#### "failed to create loop device"

**Problem:** No available loop devices or permission issues.

**Solution:**
```bash
# Check available loop devices
sudo losetup -f

# Load loop module if needed
sudo modprobe loop

# Increase max loop devices (if needed)
echo "options loop max_loop=64" | sudo tee /etc/modprobe.d/loop.conf
```

#### "image not found in Docker daemon"

**Problem:** Fledge can't find the OCI image locally.

**Solution:** It will automatically try the remote registry, or pull it first:
```bash
docker pull nginx:alpine
sudo fledge build
```

#### Build is slow

**Potential causes and solutions:**
1. **Large OCI image** → Use smaller base images (alpine variants)
2. **Network speed** → Pre-download images with `docker pull`
3. **Disk I/O** → Enable `preallocate = true` for faster writes

### Getting Help

If you're stuck:

1. **Run with verbose logging:** `sudo fledge build -v`
2. **Check system dependencies:** Ensure all required tools are installed
3. **Validate your config:** Fledge provides detailed validation errors
4. **Consult examples:** See `docs/examples/` for working configurations
5. **Ask the community:** [GitHub Discussions](https://github.com/volantvm/fledge/discussions)

---

## Advanced Topics

### Reproducible Builds

Fledge normalizes file timestamps and uses deterministic compression to ensure builds are reproducible. Building the same configuration twice produces identical artifacts (byte-for-byte).

**Why this matters:**
- Verify builds haven't been tampered with
- Efficient caching in CI/CD
- Compliance and audit requirements

### Multi-Stage Builds

For complex applications, you might build in stages:

```bash
# Stage 1: Build application
docker build -t myapp:latest .

# Stage 2: Convert to plugin
cd plugin/
sudo fledge build  # References myapp:latest in fledge.toml
```

### Custom Init Systems

The initramfs builder includes a minimal init program. For advanced use cases, you can provide your own init by mapping it to `/init`:

```toml
[mappings]
"custom-init" = "/init"  # Must be executable
```

### Filesystem Optimization

For OCI rootfs plugins, Fledge automatically:
- Removes unused space (ext4 only)
- Aligns blocks for optimal performance
- Preserves symlinks and special files

### Integration with CI/CD

Example GitHub Actions workflow:

```yaml
name: Build Plugin

on: [push]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Install dependencies
        run: |
          sudo apt-get update
          sudo apt-get install -y skopeo
          curl -LO https://github.com/opencontainers/umoci/releases/download/v0.4.7/umoci.amd64
          sudo mv umoci.amd64 /usr/local/bin/umoci
          sudo chmod +x /usr/local/bin/umoci
      
      - name: Install Fledge
        run: |
          curl -LO https://github.com/volantvm/fledge/releases/latest/download/fledge-linux-amd64
          sudo mv fledge-linux-amd64 /usr/local/bin/fledge
          sudo chmod +x /usr/local/bin/fledge
      
      - name: Build Plugin
        run: sudo fledge build -v
      
      - name: Upload Artifact
        uses: actions/upload-artifact@v3
        with:
          name: plugin
          path: '*.img'
```

---

## Contributing to Fledge

We welcome contributions! Here's how you can help:

1. **Report bugs** - Open an issue with reproduction steps
2. **Suggest features** - Describe your use case in discussions
3. **Submit PRs** - Fix bugs or add features (see CONTRIBUTING.md)
4. **Improve docs** - Help make this guide even better
5. **Share examples** - Submit example configurations

### Development Setup

```bash
git clone https://github.com/volantvm/fledge.git
cd fledge
make build    # Build the binary
make test     # Run tests
make fmt      # Format code
make vet      # Run static analysis
```

---

## License

Fledge is licensed under the **Business Source License 1.1**.

**What this means:**
- ✅ **Free for Volant plugin development** - Create and distribute plugins freely
- ✅ **Free for evaluation** - Test and develop without restrictions
- ✅ **Open source** - View and modify the source code
- ⏰ **Becomes Apache 2.0** - On October 4, 2029, automatically converts to Apache License 2.0

See [LICENSE](LICENSE) for complete details.

---

## What's Next?

Now that you understand how to build plugins with Fledge:

1. **Build your first production plugin** - Start with something simple
2. **Explore the examples** - See real-world configurations in `docs/examples/`
3. **Join the community** - Connect with other Volant developers
4. **Read the Volant documentation** - Understand how plugins are deployed and managed

Happy building! 🚀

---

**Copyright © 2025 HYPR. PTE. LTD.**
