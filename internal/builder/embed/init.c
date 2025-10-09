// init.c - Minimal C init for Volant initramfs
// This init sets up the basic Linux environment and hands off to kestrel or custom entrypoint.
#define _GNU_SOURCE
#include <unistd.h>
#include <ctype.h>
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

static void mount_devtmpfs(void) {
    mkdir("/dev", 0755);
    if (mount("devtmpfs", "/dev", "devtmpfs", 0, NULL) && errno != EBUSY)
        panic("mount(/dev)");
}

static void mount_runtime_filesystems(void) {
    mkdir("/proc", 0755);
    if (mount("proc", "/proc", "proc", 0, NULL) && errno != EBUSY)
        panic("mount(/proc)");
    mkdir("/sys", 0755);
    if (mount("sysfs", "/sys", "sysfs", 0, NULL) && errno != EBUSY)
        panic("mount(/sys)");

    mkdir("/tmp", 0777);
    if (mount("tmpfs", "/tmp", "tmpfs", 0, NULL) && errno != EBUSY)
        panic("mount(/tmp)");
    mkdir("/run", 0755);
    if (mount("tmpfs", "/run", "tmpfs", 0, NULL) && errno != EBUSY)
        panic("mount(/run)");
}

static void trim_trailing_whitespace(char *s) {
    if (!s)
        return;
    size_t len = strlen(s);
    while (len > 0 && isspace((unsigned char)s[len - 1])) {
        s[--len] = '\0';
    }
}


static int wait_for_block_device(const char *path, int max_attempts, useconds_t delay_us) {
    struct stat st;
    for (int attempt = 0; attempt < max_attempts; attempt++) {
        if (stat(path, &st) == 0) {
            if (S_ISBLK(st.st_mode)) {
                return 1;
            }
            fprintf(stderr, "C INIT: %s exists but is not a block device (mode=0%o)\n", path, st.st_mode);
            return 0;
        }

        int err = errno;
        if (err != ENOENT && err != ENODEV && err != ENXIO) {
            fprintf(stderr, "C INIT: unexpected error probing %s: %s\n", path, strerror(err));
        }

        usleep(delay_us);
    }

    fprintf(stderr, "C INIT: root device %s did not appear after %d attempts\n", path, max_attempts);
    return 0;
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
    fclose(f);

    trim_trailing_whitespace(path_buf);

    if (path_buf[0] == '\0')
        return NULL;

    return path_buf;
}

static void read_root_params(char *root_dev, size_t root_dev_len, char *root_fs, size_t root_fs_len) {
    if (root_dev && root_dev_len > 0) {
        strncpy(root_dev, "/dev/vda", root_dev_len - 1);
        root_dev[root_dev_len - 1] = '\0';
    }
    if (root_fs && root_fs_len > 0) {
        strncpy(root_fs, "ext4", root_fs_len - 1);
        root_fs[root_fs_len - 1] = '\0';
    }

    FILE *f = fopen("/proc/cmdline", "r");
    if (!f)
        return;

    char line[4096];
    if (!fgets(line, sizeof(line), f)) {
        fclose(f);
        return;
    }
    fclose(f);

    char *saveptr = NULL;
    for (char *token = strtok_r(line, " \n", &saveptr); token; token = strtok_r(NULL, " \n", &saveptr)) {
        if (strncmp(token, "root=", 5) == 0 && root_dev && root_dev_len > 0) {
            strncpy(root_dev, token + 5, root_dev_len - 1);
            root_dev[root_dev_len - 1] = '\0';
        } else if (strncmp(token, "rootfstype=", 11) == 0 && root_fs && root_fs_len > 0) {
            strncpy(root_fs, token + 11, root_fs_len - 1);
            root_fs[root_fs_len - 1] = '\0';
        }
    }
}

static int try_run_buildkit(void) {
    char root_dev[256];
    char root_fs[64];
    read_root_params(root_dev, sizeof(root_dev), root_fs, sizeof(root_fs));

    printf("C INIT: root device=%s rootfstype=%s\n", root_dev, root_fs);
    if (!wait_for_block_device(root_dev, 50, 100 * 1000)) {
        return 0;
    }

    mkdir("/newroot", 0755);
    if (mount(root_dev, "/newroot", root_fs, 0, NULL)) {
        fprintf(stderr, "C INIT: Failed to mount root device %s (%s): %s\n", root_dev, root_fs, strerror(errno));
        rmdir("/newroot");
        return 0;
    }

    printf("C INIT: mounted root filesystem from %s (%s)\n", root_dev, root_fs);

    char init_path[4096] = {0};
    FILE *f = fopen("/newroot/.volant_init", "r");
    if (f) {
        if (fgets(init_path, sizeof(init_path), f)) {
            trim_trailing_whitespace(init_path);
        }
        fclose(f);
    }

    if (init_path[0] != '\0') {
        printf("C INIT: disk /.volant_init requests %s\n", init_path);
    } else {
        struct stat st;
        if (stat("/newroot/.fledge/init", &st) == 0) {
            if (S_ISREG(st.st_mode) && (st.st_mode & (S_IXUSR | S_IXGRP | S_IXOTH))) {
                strncpy(init_path, "/.fledge/init", sizeof(init_path) - 1);
                init_path[sizeof(init_path) - 1] = '\0';
                printf("C INIT: using /.fledge/init from disk\n");
            } else {
                fprintf(stderr, "C INIT: /.fledge/init exists but is not executable (mode=0%o)\n", st.st_mode);
            }
        } else {
            int err = errno;
            if (err != ENOENT) {
                fprintf(stderr, "C INIT: Failed to stat /.fledge/init: %s\n", strerror(err));
            }
        }
    }

    if (init_path[0] == '\0') {
        printf("C INIT: disk provided no BuildKit init; falling back to kestrel\n");
        if (umount("/newroot")) {
            fprintf(stderr, "C INIT: Failed to unmount /newroot: %s\n", strerror(errno));
        }
        rmdir("/newroot");
        return 0;
    }

    if (chdir("/newroot"))
        panic("chdir(/newroot)");
    if (chroot("."))
        panic("chroot(.)");
    if (chdir("/"))
        panic("chdir(/)");

    mount_devtmpfs();
    mount_runtime_filesystems();

    printf("C INIT: Handing off to custom init: %s\n", init_path);
    char *const custom_argv[] = {init_path, NULL};
    execv(init_path, custom_argv);
    fprintf(stderr, "C INIT: Failed to exec custom init %s: %s\n", init_path, strerror(errno));
    panic("execv(custom init)");
    return 1; // Unreachable
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

    mount_devtmpfs();
    ensure_console();

    if (try_run_buildkit()) {
        return 1; // Unreachable when exec succeeds
    }

    const char *custom_init = read_custom_init();
    if (custom_init) {
        mount_runtime_filesystems();
        printf("C INIT: Handing off to custom init: %s\n", custom_init);
        char *const custom_argv[] = {(char*)custom_init, NULL};
        execv(custom_init, custom_argv);
        fprintf(stderr, "C INIT: Failed to exec custom init %s: %s\n", custom_init, strerror(errno));
        panic("execv(custom init)");
    }

    mount_runtime_filesystems();
    printf("C INIT: Handing off to Kestrel agent...\n");
    char *const kestrel_argv[] = {"/bin/kestrel", NULL};
    execv("/bin/kestrel", kestrel_argv);

    panic("execv(/bin/kestrel)");
    return 1; // Unreachable
}
