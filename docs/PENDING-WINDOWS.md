# Pending: needs the Windows PC

Everything on the Mac side of v1 is done and tested. These items need the
PC on the same LAN as the Mac. Both machines must be on one network: the Mac
last saw the PC at `10.157.240.135` (Wi-Fi `10.157.240.0/24`).

Bring the PC up to date first (from an elevated PowerShell, in a fresh copy
of the `kith` folder):

```powershell
powershell -ExecutionPolicy Bypass -File scripts\install.ps1
kith daemon install      # re-run: adds the new mDNS firewall rule (asks for your password)
kith status
```

| # | item | how to verify | status |
|---|---|---|---|
| W1 | Windows installer + CLI (`install.ps1`, PATH, PowerShell completion) | new terminal: `kith --version`, `kith status`, `kith <TAB>` | pending |
| W2 | Full test suite on the new code | `go test -count=1 ./...` → all `ok` | pending |
| W3 | mDNS discovery (new UDP 5353 rule, D-045) | `kith doctor` on the PC shows `firewall mdns OK`; on the Mac `kith pair` **without** `--ip` finds the PC (both running `kith pair`), or `dns-sd -B _kith._tcp` lists `kith-88ee257ad23b` | pending |
| W4 | Pairing fail-fast on the PC as responder (D-042) | Mac: `kith pair --ip <pc>` while the PC is *not* pairing → immediate "HARI-PC is not in pairing mode", no code shown | pending |
| W5 | Reverse direction Windows → Mac | `WINDOWS_AGENT_PROMPT_4.md` (or `kith run --peer harieees-MacBook-Air-2 -i report.py -i data.bin -o 'out/*' --os macos -- python3 -u report.py` from `examples\reverse`); Mac approves with `kith approve` | pending |
| W6 | Real partition | during a GPU run, disable Wi-Fi on the **PC** for ~30 s → training continues, Mac mirror catches up, `kith logs` complete | pending |
| W7 | Executor crash under the service | during a GPU run: `taskkill /F /IM kith.exe` (elevated) → SCM restarts it, task `INTERRUPTED` → resumes from checkpoint → `COMPLETED` | pending |
| W8 | Cancel kills the GPU process | `kith cancel <id>` from the Mac → `CANCELLED`; `nvidia-smi` shows no leftover python | pending |
| W9 | `kith daemon start/stop` with the Windows service (needs elevation) | elevated: `kith daemon stop`, `kith daemon start` | pending |
| W10 | Push to GitHub so CI runs on macOS + Windows runners | create a repo, `git remote add origin …`, `git push -u origin main` | pending (needs your GitHub account) |
