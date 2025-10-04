// init.c - Minimal C init for Volant initramfs
// This init sets up the basic Linux environment and hands off to kestrel or custom entrypoint.
#define _GNU_SOURCE
#include <unistd.h>
#include <errno.h>
#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#include <fcntl.h>
#include <sys/wait.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/reboot.h>
#include <sys/sysmacros.h>

// A proper shutdown function
__attribute__((noreturn)) static void poweroff(void) {
    fflush(stdout);
    fflush(stderr);
    reboot(RB_POWER_OFF);
    // If reboot fails, we're in big trouble.
    // Loop forever to prevent the kernel from panicking on our exit.
    for (;;) {
        sleep(3600);
    }
}

// A proper panic function for our init
static void panic(const char *what) {
    fprintf(stderr, "\n\nINIT PANIC: %s: %s\n\n", what, strerror(errno));
    poweroff();
}

// Ensure the console is available for logging
static void ensure_console(void) {
    // Create /dev/console if it doesn't exist (it should)
    if (mknod("/dev/console", S_IFCHR | 0600, makedev(5, 1)) && errno != EEXIST)
        panic("mknod(/dev/console)");

    int fd = open("/dev/console", O_RDWR);
    if (fd < 0)
        panic("open(/dev/console)");

    // Redirect stdin, stdout, and stderr to the console
    dup2(fd, 0);
    dup2(fd, 1);
    dup2(fd, 2);
    if (fd > 2)
        close(fd);
}

// A more robust filesystem setup
static void mount_filesystems(void) {
    // Mount the essentials for the Go runtime and other tools
    if (mount("proc", "/proc", "proc", 0, NULL))
        panic("mount(/proc)");
    if (mount("sysfs", "/sys", "sysfs", 0, NULL))
        panic("mount(/sys)");
    if (mount("devtmpfs", "/dev", "devtmpfs", 0, NULL))
        panic("mount(/dev)");

    // Create and mount tmpfs for runtime data
    mkdir("/tmp", 0777);
    if (mount("tmpfs", "/tmp", "tmpfs", 0, NULL))
        panic("mount(/tmp)");
    mkdir("/run", 0755);
    if (mount("tmpfs", "/run", "tmpfs", 0, NULL))
        panic("mount(/run)");
}

// Read custom init path from /.volant_init file (if present)
// Returns the path to exec, or NULL for default kestrel
static const char* read_custom_init(void) {
    FILE *f = fopen("/.volant_init", "r");
    if (!f) {
        return NULL; // File doesn't exist, use default kestrel
    }

    static char path_buf[4096];
    if (!fgets(path_buf, sizeof(path_buf), f)) {
        fclose(f);
        return NULL;
    }

    // Strip newline
    size_t len = strlen(path_buf);
    if (len > 0 && path_buf[len-1] == '\n')
        path_buf[len-1] = '\0';
    
    fclose(f);
    
    // Skip if empty
    if (path_buf[0] == '\0')
        return NULL;

    return path_buf;
}

int main(int argc, char *argv[]) {
    // Create a basic directory structure first
    mkdir("/proc", 0755);
    mkdir("/sys", 0755);
    mkdir("/dev", 0755);
    mkdir("/bin", 0755);
    mkdir("/usr", 0755);
    mkdir("/usr/bin", 0755);
    mkdir("/usr/local", 0755);
    mkdir("/usr/local/bin", 0755);

    // Set up the essential filesystems
    mount_filesystems();

    // Now that /dev is mounted, ensure we have a console
    ensure_console();

    printf("C INIT: Basic environment ready (/proc /sys /dev /tmp /run mounted)\n");

    // Check if custom init path is specified
    const char *custom_init = read_custom_init();
    
    if (custom_init) {
        printf("C INIT: Handing off to custom init: %s\n", custom_init);
        char *const custom_argv[] = {(char*)custom_init, NULL};
        execv(custom_init, custom_argv);
        // If we reach here, exec failed
        fprintf(stderr, "C INIT: Failed to exec custom init %s: %s\n", custom_init, strerror(errno));
        panic("execv(custom init)");
    }

    // Default behavior: hand off to kestrel
    printf("C INIT: Handing off to Kestrel agent...\n");
    char *const kestrel_argv[] = {"/bin/kestrel", NULL};
    execv("/bin/kestrel", kestrel_argv);

    // If execv returns, it failed. This is a catastrophe.
    panic("execv(/bin/kestrel)");

    return 1; // Unreachable
}
