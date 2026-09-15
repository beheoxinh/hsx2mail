# AppImage Archive Index

Complete documentation of the AppImage implementation attempt for Aerion email client.

## Quick Start

- 📖 **Start here:** [README.md](README.md) - Overview, why it failed, and what works
- 📋 **Detailed attempts:** [ATTEMPTS.md](ATTEMPTS.md) - Chronological list of every solution tried
- 🛠️ **Implementation files:** See below

## Documentation Files

| File | Description |
|------|-------------|
| [README.md](README.md) | Main documentation - what worked, what failed, why Flatpak is better |
| [ATTEMPTS.md](ATTEMPTS.md) | Detailed chronology of all 10+ attempted solutions |
| [Makefile.appimage.txt](Makefile.appimage.txt) | Extracted Makefile sections for AppImage build process |
| INDEX.md | This file - quick reference index |

## Implementation Files

### Build Scripts
- `build-appimage-docker.sh` - Docker build wrapper for Ubuntu 22.04 environment
- `Dockerfile.ubuntu22.04` - Ubuntu 22.04 container for GLIBC 2.35 compatibility

### Runtime Scripts
- `build/linux/AppRun` - Custom AppRun launcher with bwrap sandboxing logic
- `build/linux/webkit-wrapper.sh` - Wrapper for webkit helper processes

## Key Findings

### ✅ What Works
- Fedora Silverblue/Kinoite
- Void Linux
- Any distro with webkit2gtk-4.1 pre-installed

### ❌ What Doesn't Work
- Ubuntu/Debian WITHOUT webkit2gtk-4.1
- Arch/Manjaro/CachyOS
- Most other distros without webkit installed

### 🔍 Root Cause
WebKit's hardcoded path `/usr/lib/x86_64-linux-gnu/webkit2gtk-4.1/` + bubblewrap namespace limitations = unsolvable on most distros

### ✨ Solution
Use Flatpak packaging with GNOME runtime (provides webkit automatically)

## Archive Status

- **Date:** February 3, 2026
- **Reason:** Fundamental incompatibility with AppImage packaging
- **Next Steps:** Flatpak implementation
- **Status:** Complete, preserved for future reference

## File Structure

```
archive/AppImage/
├── INDEX.md (this file)
├── README.md (overview and technical details)
├── ATTEMPTS.md (chronological attempt log)
├── Makefile.appimage.txt (build system configuration)
├── build-appimage-docker.sh (Docker build script)
├── Dockerfile.ubuntu22.04 (Ubuntu 22.04 build environment)
└── build/
    └── linux/
        ├── AppRun (custom launcher with bwrap)
        └── webkit-wrapper.sh (webkit helper wrapper)
```

## Quick Reference

**To understand what happened:**
1. Read README.md sections: "What Failed" and "The Fundamental Problem"

**To see what was tried:**
1. Read ATTEMPTS.md for chronological list

**To revive AppImage in future:**
1. Check if webkit2gtk added env var support for helper paths
2. Check if bwrap gained ability to create system directories
3. Test on target distros before proceeding

**To implement alternative packaging:**
1. Use Flatpak with org.gnome.Platform runtime (recommended)
2. Or native package with webkit2gtk-4.1 dependency

---

*"We tried everything. AppImage isn't the right tool for webkit-based apps on modern Linux distros."*
