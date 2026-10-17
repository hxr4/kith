## Downloads

| You have | Download | Notes |
|---|---|---|
| Mac (Apple Silicon or Intel), macOS 26+ | `Kith_<ver>_macos_universal.dmg` | native menu bar app + `kith` CLI inside; older macOS: use the CLI binary |
| Windows 10/11 (x64) | `Kith_<ver>_windows_amd64_setup.exe` | app + `kith.exe`; starts in the tray at login |
| Linux x86_64 (Ubuntu 24.04+, Debian 12+) | `kith_<ver>_linux_amd64.deb` or `.tar.gz` | needs GTK4 + WebKitGTK 6 |
| Raspberry Pi 4/5, 64-bit Raspberry Pi OS (desktop) | `kith_<ver>_linux_arm64.deb` | lite mode turns on automatically |
| Headless Pi / server | `kith_<ver>_linux_arm64` (CLI only) | `kith daemon install` (starts at boot) |
| CLI only, any platform | `kith_<ver>_<os>_<arch>` | single static binary |

Verify downloads with `SHA256SUMS`.

## First launch (unsigned builds)

These builds are not code-signed yet.

- **macOS:** open the `.dmg` and drag Kith to Applications. On first launch, right-click Kith → **Open** → **Open**. Then allow notifications and "Local Network" access when asked (Local Network is needed to find and reach your other machine; you can change it later in System Settings › Privacy & Security › Local Network). To use `kith` in Terminal, open Settings › Command line › Install kith command.
- **Windows:** if SmartScreen appears, choose **More info → Run anyway**. To run tasks while you're logged out, use the in-app **Settings → Install as a service**; it asks for your Windows password in an Administrator window.
- **Linux / Pi:** install the package with `sudo apt install ./kith_<ver>_linux_arm64.deb`. Open **Kith** from the menu, or run `kith daemon install` for the background service.

## Getting started

1. Open Kith on both machines → **Pair a machine** → compare the 6-character code.
2. Submit work from an AI agent (`kith mcp`), the CLI (`kith run`), or **Tasks → Submit a task file**.
3. Tasks from a peer with the **ask** policy appear as notifications with **Approve / Deny**.

Pairing lets the other machine run commands on this one as your user (approval is required by default). Kith does not sandbox tasks.
