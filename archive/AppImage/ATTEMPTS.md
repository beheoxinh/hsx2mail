# Chronological List of AppImage Fix Attempts

This document records every approach we tried to fix the AppImage webkit bundling issues.

## Initial Issues Identified

1. **GLIBC version mismatch** - Ubuntu 24.04 build incompatible with Ubuntu 22.04
   - ✅ **FIXED:** Build using Docker with Ubuntu 22.04 (Dockerfile.ubuntu22.04)

2. **WebKit helper processes not found** - Path hardcoded to `/usr/lib/x86_64-linux-gnu/webkit2gtk-4.1/`
   - ❌ **UNFIXABLE:** Cannot override via environment variables

## Attempted Solutions

### Attempt 1: Basic bwrap bind mount
**Approach:** Bind AppImage's webkit directory to system path
```bash
bwrap --dev-bind / / \
  --bind "${HERE}/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" "/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" \
  "${HERE}/usr/bin/aerion" "$@"
```
**Result:** ❌ FAILED
- Works on Void Linux (empty /usr/lib/x86_64-linux-gnu/)
- Fails on Ubuntu (hides system libraries)
- Fails on Arch (directory doesn't exist)

### Attempt 2: Bind entire arch lib directory
**Approach:** Replace entire `/usr/lib/x86_64-linux-gnu/` with AppImage version
```bash
bwrap --dev-bind / / \
  --bind "${HERE}/usr/lib/${LIB_ARCH}" "/usr/lib/${LIB_ARCH}" \
  "${HERE}/usr/bin/aerion" "$@"
```
**Result:** ❌ FAILED
- Hides ALL system libraries on Ubuntu/Debian
- Missing libc, libm, etc. causes binary to fail

### Attempt 3: Set LD_LIBRARY_PATH before bwrap
**Approach:** Use LD_LIBRARY_PATH to prefer AppImage libs
```bash
export LD_LIBRARY_PATH="${HERE}/usr/lib:${LD_LIBRARY_PATH}"
exec bwrap --dev-bind / / ...
```
**Result:** ❌ FAILED
- LD_LIBRARY_PATH doesn't help webkit find helpers at system path
- Binary still fails with same errors

### Attempt 4: Tmpfs for entire /usr/lib with ro-bind
**Approach:** Create tmpfs overlay for all of /usr/lib
```bash
bwrap --ro-bind / / \
  --tmpfs /usr/lib \
  --ro-bind "${HERE}/usr/lib" /usr/lib \
  "${HERE}/usr/bin/aerion" "$@"
```
**Result:** ❌ FAILED on Ubuntu/Debian
- Error: `bwrap: execvp /tmp/.mount_AerionXXXX/usr/bin/aerion: No such file or directory`
- AppImage FUSE mount not accessible in namespace
- ✅ WORKS on Fedora Silverblue (immutable distros handle this differently)

### Attempt 5: Remove --bind /tmp /tmp
**Approach:** Maybe /tmp binding interferes with FUSE mount
```bash
bwrap --dev-bind / / \
  --tmpfs /usr/lib \
  --ro-bind "${HERE}/usr/lib" /usr/lib \
  "${HERE}/usr/bin/aerion" "$@"
# (no --bind /tmp /tmp)
```
**Result:** ❌ NO CHANGE
- Same "No such file or directory" error
- Removing /tmp bind makes no difference

### Attempt 6: Tmpfs for webkit subdirectory only
**Approach:** Create tmpfs just for webkit dir to avoid hiding system libs
```bash
bwrap --dev-bind / / \
  --bind /tmp /tmp \
  --tmpfs "/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" \
  --ro-bind "${HERE}/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" "/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" \
  "${HERE}/usr/bin/aerion" "$@"
```
**Result:** ❌ FAILED
- Error: `bwrap: Can't mkdir /usr/lib/x86_64-linux-gnu/webkit2gtk-4.1: Permission denied`
- Even with `--dev-bind / /`, cannot create mount point in system directory
- Tried on both Ubuntu and Arch - same permission error

### Attempt 7: Use bwrap --dir to create directory
**Approach:** Use bwrap's --dir flag to create missing directory
```bash
bwrap --dev-bind / / \
  --dir "/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" \
  --bind "${HERE}/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" "/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" \
  "${HERE}/usr/bin/aerion" "$@"
```
**Result:** ❌ FAILED
- Error: `bwrap: Can't mkdir /usr/lib/x86_64-linux-gnu: Permission denied`
- On Arch, needs to create parent directory first
- Cannot create directories in /usr/lib even with --dev-bind

### Attempt 8: Detect distro type and use appropriate approach
**Approach:** Different bwrap configs for different distros
- Ubuntu/Debian WITH webkit: Bind webkit dir only ✅
- Ubuntu/Debian WITHOUT webkit: Use tmpfs approach ❌
- Arch/immutable distros: Use tmpfs for /usr/lib ⚠️
- Regular distros (Void): Bind arch lib directory ✅

**Result:** PARTIALLY WORKING
- Silverblue: Works ✅
- Void: Works ✅
- Ubuntu with webkit: Works ✅
- Ubuntu without webkit: Fails ❌
- Arch/CachyOS: Fails ❌

### Attempt 9: Set LD_LIBRARY_PATH + tmpfs for webkit subdir
**Approach:** Combine LD_LIBRARY_PATH with targeted tmpfs
```bash
export LD_LIBRARY_PATH="${HERE}/usr/lib:${LD_LIBRARY_PATH}"
bwrap --dev-bind / / \
  --bind /tmp /tmp \
  --tmpfs "/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" \
  --ro-bind "${HERE}/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" "/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" \
  "${HERE}/usr/bin/aerion" "$@"
```
**Result:** ❌ FAILED
- Same permission denied error
- Confirmed this was attempted previously (user pointed out circular reasoning)

### Attempt 10: Use symlinks to redirect webkit path
**Approach:** Create symlink from system path to AppImage path
```bash
ln -s "${HERE}/usr/lib/${LIB_ARCH}/webkit2gtk-4.1" "/usr/lib/${LIB_ARCH}/webkit2gtk-4.1"
```
**Result:** ❌ NOT ATTEMPTED
- Requires root permissions to create symlink in /usr/lib
- Would conflict with system webkit if installed
- Not suitable for portable AppImage

## Root Causes Identified

### 1. WebKit Hardcoded Path
- Path `/usr/lib/x86_64-linux-gnu/webkit2gtk-4.1/` is compiled into webkit2gtk
- Cannot be changed via environment variables
- WEBKIT_EXEC_PATH env var is ignored for helper process spawning
- Only solution: Make helpers available at exact system path

### 2. Bubblewrap Namespace Limitations
- Cannot create directories in /usr/lib even with `--dev-bind / /`
- Tmpfs requires mount point to exist
- `--dir` flag also fails with permission denied
- System directories are protected even in user namespaces

### 3. Distro Library Structure Differences
- **Debian/Ubuntu:** Uses `/usr/lib/x86_64-linux-gnu/` for arch-specific libs
  - This directory is FULL of critical system libraries
  - Replacing it hides libc, libm, etc.
- **Arch/Manjaro/CachyOS:** Uses `/usr/lib/` directly
  - No `/usr/lib/x86_64-linux-gnu/` directory at all
  - Cannot create it even with bwrap
- **Void Linux:** Has `/usr/lib/x86_64-linux-gnu/` but it's EMPTY
  - Safe to replace without hiding system libraries
  - This is why our approach works on Void
- **Silverblue/immutable:** Special handling allows tmpfs overlays
  - Exact reason unclear, but works consistently

### 4. FUSE Mount Namespace Issues
- AppImage mounts via FUSE at `/tmp/.mount_*`
- When using tmpfs for /usr/lib, FUSE mount becomes inaccessible
- Binary path `/tmp/.mount_*/usr/bin/aerion` not found
- This only affects certain bwrap namespace configurations

## Why Nothing Worked

The fundamental issue is a **three-way incompatibility:**

1. ✅ WebKit needs helpers at `/usr/lib/x86_64-linux-gnu/webkit2gtk-4.1/`
2. ✅ bwrap can make files available at that path
3. ❌ BUT: Either hides system libraries (Ubuntu) or can't create directory (Arch)

There is NO solution that satisfies all three requirements:
- Can't bundle webkit without bwrap → webkit can't find helpers
- Can't use bwrap bind mount → hides system libraries OR can't create directory
- Can't use bwrap tmpfs → can't create mount point OR binary not found

## What Actually Works

### Distros Where AppImage Works
1. **Fedora Silverblue/Kinoite** - Immutable distros with special tmpfs handling
2. **Void Linux** - Empty `/usr/lib/x86_64-linux-gnu/` can be safely replaced
3. **Any distro WITH webkit2gtk-4.1 installed** - No bundling needed

### Alternative Packaging Solutions
1. **Flatpak** ✅ - webkit provided by GNOME runtime, no bundling needed
2. **Distribution packages** ✅ - webkit2gtk-4.1 as runtime dependency
3. **Snap** ✅ - Content snaps can provide webkit
4. **Native binary** ⚠️ - Requires webkit2gtk-4.1 system installation

## Conclusion

After 10+ different approaches and extensive testing on multiple distros, we determined that **AppImage is not a viable packaging format** for Wails applications that use WebKit, unless targeting only:
- Systems with webkit already installed
- Void Linux
- Immutable distros (Silverblue/Kinoite)

**Flatpak is the recommended solution** for cross-distro webkit application distribution.

## Timeline

- **Day 1:** Identified GLIBC issue, fixed with Docker build
- **Day 1:** Discovered webkit hardcoded path issue
- **Day 1-2:** Attempted 10+ different bwrap configurations
- **Day 2:** Tested on CachyOS, Void, Silverblue, Ubuntu, Pop OS
- **Day 2:** Confirmed fundamental incompatibility
- **Day 2:** User decided to pivot to Flatpak
- **Day 2:** Archived all AppImage work for future reference

---

**Status:** Archived, unsolvable with current AppImage/bwrap/webkit stack
**Next Steps:** Implement Flatpak packaging
**Archive Date:** February 3, 2026
