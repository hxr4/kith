# Windows checks (W1–W10)

Round 5 (2026-10-13, report `WINDOWS_REPORT_5.md`): **W1–W9 passed** on
HARI-PC (RTX 3050) ↔ MacBook Air over a phone hotspot. Only W10 (GitHub/CI)
remains. The fixes for the report's findings are in D-047…D-050; re-check
them with the round-6 items below. These items need the PC on the same LAN
as the Mac. Both machines must be on one network: the Mac
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
| W1 | Windows installer + CLI (`install.ps1`, PATH, PowerShell completion) | new terminal: `kith --version`, `kith status`, `kith <TAB>` | **PASS** (2026-10-13) |
| W2 | Full test suite on the new code | `go test -count=1 ./...` → all `ok` | **PASS** (2026-10-13) |
| W3 | mDNS discovery (new UDP 5353 rule, D-045) | `kith doctor` on the PC shows `firewall mdns OK`; on the Mac `kith pair` **without** `--ip` finds the PC (both running `kith pair`), or `dns-sd -B _kith._tcp` lists `kith-88ee257ad23b` | **PASS** (2026-10-13) |
| W4 | Pairing fail-fast on the PC as responder (D-042) | Mac: `kith pair --ip <pc>` while the PC is *not* pairing → immediate "HARI-PC is not in pairing mode", no code shown | **PASS** (2026-10-13) |
| W5 | Reverse direction Windows → Mac | `WINDOWS_AGENT_PROMPT_4.md` (or `kith run --peer harieees-MacBook-Air-2 -i report.py -i data.bin -o 'out/*' --os macos -- python3 -u report.py` from `examples\reverse`); Mac approves with `kith approve` | **PASS** (2026-10-13) |
| W6 | Real partition | during a GPU run, disable Wi-Fi on the **PC** for ~30 s → training continues, Mac mirror catches up, `kith logs` complete | **PASS** (2026-10-13) |
| W7 | Executor crash under the service | during a GPU run: `taskkill /F /IM kith.exe` (elevated) → SCM restarts it, task `INTERRUPTED` → resumes from checkpoint → `COMPLETED` | **PASS** (2026-10-13) |
| W8 | Cancel kills the GPU process | `kith cancel <id>` from the Mac → `CANCELLED`; the task's python process (pid from `kith task <id>`) is gone: `Get-Process -Id <pid>` fails. `nvidia-smi --query-compute-apps` cannot see session-0 (service) processes on Windows | **PASS** (2026-10-13) |
| W9 | `kith daemon start/stop` with the Windows service (needs elevation) | elevated: `kith daemon stop`, `kith daemon start` | **PASS** (2026-10-13) |
| W10 | Push to GitHub so CI runs on macOS + Windows runners | create a repo, `git remote add origin …`, `git push -u origin main` | pending (needs your GitHub account) |

## Round 6: re-check the fixes from report 5

| # | item | how to verify |
|---|---|---|
| R1 | non-admin `kith status` shows the service state (D-047) | normal PowerShell: `kith status` → `service: installed, running` (not "Access is denied") |
| R2 | executor-side cancel attribution (D-048) | `kith cancel <id>` on the PC → both sides' detail is `cancelled on the executor` |
| R3 | no duplicate timeline rows (D-049) | `kith task <id>` on the Mac for a task that waited for approval: SUBMITTED appears once; FINALIZING appears once |
| R4 | peer named in `kith run` | output says `on HARI-PC` / `on harieees-MacBook-Air-2` |
| R5 | ASCII output (D-050) | capture through Windows PowerShell 5.1: `kith run … *> out.txt; Get-Content out.txt` has no `â€` sequences |
| R6 | install.ps1 elevation check + progress | non-elevated with the service running → immediate clear message; elevated → "Building kith…" then "Built in Ns" |
| R7 | `kith task <id>` shows `pid:` while running | |
