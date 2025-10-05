# Fledge Configuration Examples

This directory contains example `fledge.toml` configurations for common use cases.

## Initramfs Examples

### [initramfs-minimal.toml](initramfs-minimal.toml)
The simplest possible initramfs plugin with only busybox and kestrel agent. Perfect for learning or as a starting point.

**Size:** ~2-5 MB  
**Boot time:** < 100ms

### [initramfs-go-webserver.toml](initramfs-go-webserver.toml)
A Go web server packaged as initramfs. Shows how to include static binaries and configuration files.

**Size:** ~10-20 MB (depending on your binary)  
**Best for:** Stateless API servers, microservices

### [initramfs-rust-service.toml](initramfs-rust-service.toml)
A Rust microservice example with static linking. Demonstrates building with musl for minimal dependencies.

**Size:** ~5-15 MB  
**Best for:** High-performance services, systems programming

## OCI Rootfs Examples

### [oci-nginx.toml](oci-nginx.toml)
Nginx web server converted from the official Alpine image. Shows basic OCI to plugin conversion.

**Size:** ~50-60 MB  
**Best for:** Static sites, reverse proxy, load balancing

### [oci-postgres.toml](oci-postgres.toml)
PostgreSQL database plugin. Demonstrates handling larger images with database software.

**Size:** ~200-250 MB  
**Best for:** Relational databases, persistent data services

### [oci-python-flask.toml](oci-python-flask.toml)
Python Flask web application. Shows how to map multiple directories for application code, static files, and templates.

**Size:** ~80-150 MB  
**Best for:** Python web apps, REST APIs, dynamic websites

## Using These Examples

1. **Copy the example** to your project directory:
   ```bash
   cp docs/examples/initramfs-go-webserver.toml my-plugin/fledge.toml
   ```

2. **Modify** the configuration for your needs:
   - Update file mappings
   - Change agent version
   - Adjust filesystem settings

3. **Build** your plugin:
   ```bash
   cd my-plugin
   sudo fledge build
   ```

### Important: Agent requirements

- OCI Rootfs examples require an `[agent]` section. Fledge injects the Kestrel agent into the image.
- Initramfs default mode (no `[init]`): Kestrel is used; you may omit `[agent]` and Fledge will default to `source_strategy = "release"`, `version = "latest"`.
- Initramfs with custom or no init (`[init] path=...` or `[init] none=true`): Do not include `[agent]` (Kestrel is not used).

## Tips for Creating Your Own Configurations

### For Initramfs Plugins

- **Keep it small**: Every megabyte affects boot time
- **Use static binaries**: No dynamic linking in initramfs
  - Go: `CGO_ENABLED=0 go build`
  - Rust: Build with `x86_64-unknown-linux-musl` target
  - C/C++: Link with `-static`
- **Test your binary**: Run it standalone before packaging
- **Verify checksums**: Always validate busybox and kestrel checksums

### For OCI Rootfs Plugins

- **Choose Alpine variants**: Smaller base images = smaller plugins
- **Review the image first**: Use `docker run` to test before converting
- **Map configs carefully**: Understand where the app expects files
- **Add buffer space**: Set `size_buffer_mb` for runtime writes
- **Use ext4**: Best compatibility and supports shrinking

## Common Patterns

### Development vs Production

**Development** (use latest versions):
```toml
[agent]
source_strategy = "release"
version = "latest"
```

**Production** (pin specific versions):
```toml
[agent]
source_strategy = "release"
version = "v0.2.0"  # Specific version
```

### Adding TLS Certificates

```toml
[mappings]
"certs/server.crt" = "/etc/ssl/certs/server.crt"
"certs/server.key" = "/etc/ssl/private/server.key"
"certs/ca-bundle.crt" = "/etc/ssl/certs/ca-bundle.crt"
```

### Multi-Binary Applications

```toml
[mappings]
"payload/main-server" = "/usr/bin/main-server"
"payload/worker" = "/usr/bin/worker"
"payload/cli-tool" = "/usr/bin/cli-tool"
"scripts/startup.sh" = "/usr/local/bin/startup.sh"
```

### Complex Directory Structures

```toml
[mappings]
# Application binaries
"build/bin/" = "/opt/myapp/bin/"

# Libraries
"build/lib/" = "/opt/myapp/lib/"

# Configuration
"configs/" = "/etc/myapp/"

# Static assets
"public/" = "/var/www/public/"
```

## Need Help?

- Check the [main README](../../README.md) for comprehensive tutorials
- Open an issue on [GitHub](https://github.com/volantvm/fledge/issues)
- Join discussions in the [Volant community](https://github.com/volantvm/volant/discussions)
