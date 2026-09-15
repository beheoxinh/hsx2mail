# AppImage Archive

This directory contains archived AppImage build scripts and documentation from the AppImage implementation attempt in early 2026.

## Why This Was Archived

AppImage packaging for Aerion proved to be fundamentally incompatible with systems that don't have webkit2gtk-4.1 pre-installed, due to WebKit's hardcoded library paths and bubblewrap namespace limitations. After extensive troubleshooting, we determined that **Flatpak is a better packaging solution** for Wails applications because the GNOME runtime provides webkit2gtk automatically.

## Archived Files

- `build/linux/AppRun` - Custom AppRun launcher script with bwrap sandboxing
- `build/linux/webkit-wrapper.sh` - Wrapper for webkit helper processes
- `build-appimage-docker.sh` - Docker build script for Ubuntu 22.04 environment
- `Dockerfile.ubuntu22.04` - Ubuntu 22.04 Docker container for GLIBC 2.35 compatibility
- `Makefile.appimage.txt` - Extracted Makefile sections for AppImage building

## What Worked

✅ **Systems where AppImage works:**
- **Fedora Silverblue** - Immutable distro, uses tmpfs overlay for /usr/lib
- **Void Linux** - `/usr/lib/x86_64-linux-gnu/` exists but is empty, safe to replace
- **Ubuntu 22.04/24.04 WITH webkit2gtk-4.1 installed** - System webkit directory exists

✅ **Technical solutions that worked:**
- Building on Ubuntu 22.04 for GLIBC 2.35 compatibility
- Using Docker to ensure consistent build environment
- Bundling webkit helper processes from build system
- Using webkit-wrapper.sh to set LD_LIBRARY_PATH for helper processes

## What Failed

❌ **Systems where AppImage DOES NOT work:**
- **Ubuntu/Pop OS/Debian WITHOUT webkit2gtk-4.1 installed**
- **CachyOS/Arch/Manjaro** (and other Arch-based distros)
- **Any system where `/usr/lib/x86_64-linux-gnu/` doesn't exist or is populated**

❌ **Attempted solutions that failed:**
1. Using tmpfs for entire `/usr/lib/${LIB_ARCH}/` - Binary not found in FUSE mount
2. Using tmpfs for just `webkit2gtk-4.1` subdirectory - Permission denied creating mount point
3. Removing `--bind /tmp /tmp` from bwrap - No effect, binary still not found
4. Using `--ro-bind` instead of `--dev-bind` - Same errors
5. Setting LD_LIBRARY_PATH before bwrap execution - Still can't find binary
6. Attempting to create `/usr/lib/x86_64-linux-gnu/` with bwrap `--dir` - Permission denied
7. Using symlinks to redirect webkit path - WebKit ignores them

## The Fundamental Problem

### WebKit's Hardcoded Path

WebKit2GTK has a **compile-time hardcoded path** to its helper processes:
```
/usr/lib/x86_64-linux-gnu/webkit2gtk-4.1/
```

This path cannot be changed via environment variables (WEBKIT_EXEC_PATH doesn't work for spawning helpers). The only solution is to make the bundled webkit helpers available at this exact system path.

### The Catch-22

To make webkit helpers available, we use bubblewrap (bwrap) to create a namespace with bind mounts:
```bash
bwrap --dev-bind / / \
  --bind "${HERE}/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" "/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" \
  "${HERE}/usr/bin/aerion" "$@"
```

**This works on Void because:**
- `/usr/lib/x86_64-linux-gnu/` exists but is empty
- Binding AppImage's directory doesn't hide any system libraries
- System can find both webkit helpers AND system libraries

**This fails on Ubuntu/Debian without webkit because:**
- `/usr/lib/x86_64-linux-gnu/` is FULL of system libraries
- Binding AppImage's directory hides all system libraries
- Either webkit helpers work but system libs are missing, OR
- System libs are available but webkit helpers are missing

**This fails on Arch/CachyOS because:**
- `/usr/lib/x86_64-linux-gnu/` doesn't exist at all
- Arch uses `/usr/lib/` directly without arch-specific subdirectories
- bwrap cannot create this directory even with `--dev-bind / /`
- Permission denied when attempting any tmpfs mount points

### Why Tmpfs Doesn't Work

Attempted solution:
```bash
--tmpfs "/usr/lib/${LIB_ARCH}/webkit2gtk-4.1"
--ro-bind "${HERE}/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" "/usr/lib/${LIB_ARCH}/webkit2gtk-4.1"
```

Result: `bwrap: Can't mkdir /usr/lib/x86_64-linux-gnu/webkit2gtk-4.1: Permission denied`

Even with `--dev-bind / /`, bwrap cannot create mount points in system directories. The tmpfs needs a mount point to exist, but on Ubuntu the directory is missing, and on Arch the entire parent directory is missing.

### Why Binary Not Found

When using tmpfs for `/usr/lib/`:
```bash
--tmpfs /usr/lib
--ro-bind "${HERE}/usr/lib" /usr/lib
```

Result: `bwrap: execvp /tmp/.mount_AerionXXXX/usr/bin/aerion: No such file or directory`

The AppImage FUSE mount at `/tmp/.mount_*` becomes inaccessible from within the bwrap namespace. This is because:
1. AppImage mounts via FUSE at `/tmp/.mount_*`
2. bwrap creates new mount namespace
3. FUSE mount points don't cross namespace boundaries properly
4. Binary path becomes invalid inside namespace

## Test Results

### Ubuntu 22.04 WITHOUT webkit2gtk-4.1
```
$ ./Aerion-0.1.9-x86_64.AppImage
bwrap: execvp /tmp/.mount_AerionXXXX/usr/bin/aerion: No such file or directory
```

### CachyOS (Arch-based)
```
$ ./Aerion-0.1.9-x86_64.AppImage
bwrap: Can't mkdir /usr/lib/x86_64-linux-gnu: Permission denied
```

### Fedora Silverblue (WORKS)
```
$ ./Aerion-0.1.9-x86_64.AppImage
[App launches successfully]
```

### Void Linux (WORKS)
```
$ ./Aerion-0.1.9-x86_64.AppImage
[App launches successfully]
```

## System Detection Logic

The AppRun script attempts to detect the distro type and apply appropriate workarounds:

1. **Ubuntu/Debian WITH webkit** - Only bind webkit directory (works)
2. **Ubuntu/Debian WITHOUT webkit** - Use tmpfs approach (FAILS - binary not found)
3. **Arch-based OR immutable distros** - Use tmpfs for /usr/lib (WORKS on immutable, FAILS on Arch)
4. **Regular distros** - Bind entire arch lib directory (works on Void)

## Lessons Learned

1. **AppImage has fundamental limitations** for apps requiring specific system library paths
2. **Wails + WebKit + AppImage = problematic combination** due to hardcoded paths
3. **GLIBC version matters** - must build on oldest supported Ubuntu version
4. **Flatpak solves these issues** by providing webkit in the runtime
5. **Always test on multiple distros** - what works on one may fail on another

## Why Flatpak Is Better

Flatpak solves all these issues:

- ✅ webkit2gtk provided by GNOME runtime (org.gnome.Platform)
- ✅ No need to bundle webkit or manipulate system paths
- ✅ Works consistently across all distros (Ubuntu, Fedora, Arch, etc.)
- ✅ Sandboxing is built-in and properly designed
- ✅ Distribution through Flathub is well-established
- ✅ Automatic updates via Flatpak system

## Future Possibilities

If AppImage ever improves to handle these issues, this work can be revived:

1. If WebKit adds environment variable support for helper paths
2. If bwrap gains ability to create mount points in system directories
3. If AppImage adds better namespace/FUSE mount handling
4. If webkit2gtk-4.1 becomes universally installed on all distros

Until then, **Flatpak is the recommended packaging format for Aerion**.

## Build Instructions (For Historical Reference)

If you want to build the AppImage:

```bash
# Using Docker (recommended for GLIBC compatibility)
./build-appimage-docker.sh

# Or locally (requires Ubuntu 22.04)
make appimage
```

The AppImage will be created at `build/bin/Aerion-*.AppImage`.

**Note:** This AppImage will only work on:
- Systems with webkit2gtk-4.1 installed, OR
- Void Linux, OR
- Fedora Silverblue/Kinoite and similar immutable distros

It will NOT work on:
- Ubuntu/Debian/Pop OS without webkit2gtk-4.1
- Arch/Manjaro/CachyOS/EndeavourOS
- Most other distros without webkit installed

---

**Archived:** February 2026
**Reason:** Unsolvable webkit bundling issues, migrating to Flatpak
**Status:** Preserved for future reference if AppImage improvements occur
