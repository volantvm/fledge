# Init Modes: Three Tiers of Control

Fledge offers three ways to handle init/PID 1 in your initramfs, each solving different problems with zero friction.

---

## The Ladder of Laziness™

### 🎯 Mode 1: Default (Batteries-Included)
**For the 90% who just want it to work**

**What you get:**
- C init mounts `/proc`, `/sys`, `/dev`, `/tmp`, `/run`
- Hands off to Kestrel agent
- Kestrel manages your workload lifecycle
- Full Volant orchestration (health checks, API proxy, hot reload)

**Configuration:**
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
"./myapp" = "/usr/bin/myapp"
```

**No `[init]` section = default mode** ✨

**Boot flow:**
```
Kernel → C init → Kestrel → Your app
```

---

### 🛠️ Mode 2: Custom Init
**For the lazy genius who needs ONE custom thing**

**Use cases:**
- Need to create a special socket before app starts
- Custom environment setup
- Simple pre-flight checks
- Your own supervisor logic

**What you get:**
- C init still handles filesystem mounting
- Your custom init script/binary runs as PID 1
- YOU manage the workload
- **No Kestrel** (you handle lifecycle yourself)

**Configuration:**
```toml
version = "1"
strategy = "initramfs"

[init]
path = "/usr/local/bin/my-init.sh"

[source]
busybox_url = "https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox"
busybox_sha256 = "6e123e7f3202a8c1e9b1f94d8941580a25135382b99e8d3e34fb858bba311348"

[mappings]
"./my-init.sh" = "/usr/local/bin/my-init.sh"
"./myapp" = "/usr/bin/myapp"
```

**Example `my-init.sh`:**
```bash
#!/bin/sh
# /proc /sys /dev /tmp /run already mounted by C init!

# Do your one custom thing
echo "Setting up custom socket..."
mkdir -p /var/run/myapp
touch /var/run/myapp/control.sock

# Start your app
exec /usr/bin/myapp
```

**Boot flow:**
```
Kernel → C init → Your custom init → Your app
```

---

### ⚡ Mode 3: No Init Wrapper
**For the ultimate lazy genius with battle-tested supervisor**

**Use cases:**
- App already has supervisor built-in (Rust actix, systemd-style apps)
- Performance-critical (eliminate C init overhead)
- You've already solved PID 1 reaping/signals
- Existing init you trust completely

**What you get:**
- **NO C init wrapper**
- Your binary becomes PID 1 directly
- **YOU must mount filesystems** (`/proc`, `/sys`, `/dev`, etc.)
- Maximum performance, maximum control

**Configuration:**
```toml
version = "1"
strategy = "initramfs"

[init]
none = true

[source]
busybox_url = "https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox"
busybox_sha256 = "6e123e7f3202a8c1e9b1f94d8941580a25135382b99e8d3e34fb858bba311348"

[mappings]
"./my-supervisor" = "/init"  # MUST map to /init for PID 1
```

**Your binary must:**
```rust
// Example Rust init (you provide this)
use std::fs;

fn main() {
    // Mount essential filesystems
    mount("proc", "/proc", "proc", 0, None).unwrap();
    mount("sysfs", "/sys", "sysfs", 0, None).unwrap();
    mount("devtmpfs", "/dev", "devtmpfs", 0, None).unwrap();
    
    // Your app logic here
    run_my_server();
    
    // Handle PID 1 reaping (zombie processes)
    loop {
        let _ = wait();
    }
}
```

**Boot flow:**
```
Kernel → Your binary (as PID 1)
```

---

## Comparison Table

| Feature | Default | Custom Init | No Init |
|---------|---------|-------------|---------|
| **Filesystem mounting** | ✅ Automatic | ✅ Automatic | ❌ You handle |
| **Kestrel agent** | ✅ Yes | ❌ No | ❌ No |
| **Volant orchestration** | ✅ Full | ❌ Manual | ❌ Manual |
| **Custom pre-flight** | ❌ No | ✅ Yes | ✅ Yes |
| **Boot overhead** | ~50ms | ~20ms | ~5ms |
| **PID 1 responsibilities** | Kestrel | Your init | Your binary |
| **Best for** | 90% of users | Simple customization | Battle-tested supervisors |

---

## Decision Tree

```
Do you need Volant health checks / API proxy?
├─ YES → Mode 1 (Default)
└─ NO
   ├─ Need filesystems mounted for you?
   │  ├─ YES → Mode 2 (Custom Init)
   │  └─ NO → Mode 3 (No Init)
   └─ Have your own PID 1 supervisor?
      └─ YES → Mode 3 (No Init)
```

---

## Examples

### Mode 1: Web Server (Default)
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
"./caddy" = "/usr/local/bin/caddy"
```

### Mode 2: Custom Setup Script
```toml
version = "1"
strategy = "initramfs"

[init]
path = "/sbin/my-init"

[source]
busybox_url = "https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox"
busybox_sha256 = "6e123e7f3202a8c1e9b1f94d8941580a25135382b99e8d3e34fb858bba311348"

[mappings]
"./init-wrapper.sh" = "/sbin/my-init"
"./redis-server" = "/usr/bin/redis-server"
```

### Mode 3: Rust Supervisor
```toml
version = "1"
strategy = "initramfs"

[init]
none = true

[source]
busybox_url = "https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox"
busybox_sha256 = "6e123e7f3202a8c1e9b1f94d8941580a25135382b99e8d3e34fb858bba311348"

[mappings]
"./target/x86_64-unknown-linux-musl/release/my-supervisor" = "/init"
```

---

## Migration Paths

### From Default → Custom Init
1. Create your init script
2. Add `[init] path = "/your/init"`
3. Remove `[agent]` section
4. Your script must exec the workload

### From Custom Init → No Init
1. Move all logic into your binary
2. Change to `[init] none = true`
3. Map your binary to `/init`
4. Handle PID 1 duties (reaping, signals)

---

## Troubleshooting

### "panic: mount(/proc)"
**Problem:** Using `none = true` but your binary doesn't mount filesystems

**Solution:** Mount `/proc /sys /dev` in your binary, or use `path = "..."` mode instead

### "exec: /bin/kestrel: no such file"
**Problem:** Using default mode but kestrel not installed

**Solution:** Add `[agent]` section with `source_strategy = "release"`

### "exec: custom init: permission denied"
**Problem:** Custom init not executable

**Solution:** Ensure file mapped to `/init` or custom path has execute permission (755)

---

## Best Practices

1. **Start with Default** - Most users never need anything else
2. **Custom Init for Scripts** - Shell scripts are perfect for simple pre-flight
3. **No Init for Performance** - Only if you've benchmarked and it matters
4. **Document Your Choice** - Future you will thank present you

---

**Remember: Batteries-included first, modular second. The system serves beginners and experts equally.**
