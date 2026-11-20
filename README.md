# Antigravity Manager (AGM)

A single-binary CLI for managing Google accounts, live model quotas, and session switching across **Antigravity CLI (`agy`)**, **Antigravity IDE**, and **Antigravity Desktop**.

## Usage

### Install

```bash
go install github.com/dev-ansung/agm@latest
```

Or build from source:

```bash
go build -o agm .
cp agm ~/.local/bin/agm
```

### Quick Start

```bash
# Add accounts via browser OAuth (or import an active session)
agm login
agm import-ide

# Inspect live quotas and reset countdowns
agm list
agm info user@example.com

# Switch only the agy CLI (leaving IDE untouched), or auto-select by quota
agm switch user@example.com --target agy
agm auto-switch --refresh --target agy --min 60 --yes --json
```

### Command Reference

| Command | Flags / Args | Description |
|---|---|---|
| `init` | — | Initialize `~/.antigravity-agent/`, master key (`.mk`), and SQLite store |
| `login` (`add`) | — | Authenticate a Google account via localhost OAuth (`8888`–`8892`) |
| `import-cli` (`import-agy`) | — | Import the active `agy` account from the OS credential store |
| `import-ide` | — | Import the active Antigravity IDE account from `state.vscdb` |
| `list` (`ls`) | `--json` | List accounts, active targets (`cli`, `ide`), group quotas, and next reset |
| `info` | `<email\|alias>`, `--json` | Show sorted per-model quota percentages and local reset times |
| `status` | `--json` | Show active account summary and next reset countdown |
| `switch` | `<email\|alias>`, `-t agy\|ide\|all` | Apply account credentials to the selected target(s) |
| `auto-switch` | `--min`, `--model`, `-t`, `--refresh`, `-y`, `--json` | Score accounts by `min(quota)` across matching models and switch `--target` to the highest `>= --min` |
| `refresh` / `refresh-all` | `<email\|alias>` | Refresh OAuth access tokens and live per-model quota snapshots |
| `validate` | — | Refresh expired access tokens across stored accounts |
| `sync` | — | Compare local DB against IDE `state.vscdb` and `agy` credential store |
| `doctor` | — | Verify data paths, executables, credential stores, and `QUOTA_URL` |
| `alias` / `unalias` | `[name] [email]` | Manage short aliases for account emails |
| `export` / `import-backup` | `[file]` | Export or restore encrypted account backups |
| `remove` (`rm`) | `<email\|alias>` | Delete an account from the local store |
| `watch` | `-n <seconds>` | Poll and render `agm list` on an interval |

#### Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `AGM_DATA_DIR` | `~/.antigravity-agent` | Root directory for `.mk` and `cloud_accounts.db` |
| `AGM_DB_PATH` | `$AGM_DATA_DIR/cloud_accounts.db` | Override SQLite database path |
| `AGM_QUOTA_URL` | `https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:fetchAvailableModels` | Override quota endpoint |
| `ANTIGRAVITY_AGENT_DIR` | — | Alias for `AGM_DATA_DIR` |

## How it works?

1. **Authentication & Storage**: `agm login` runs a local loopback OAuth2 flow (`http://localhost:8888`–`8892`) using the public Antigravity OAuth client, encrypts the resulting tokens with AES-256-GCM, and persists them in `~/.antigravity-agent/cloud_accounts.db`.
2. **Quota Polling**: `FetchLiveQuota` resolves `cloudaicompanionProject` via `v1internal:loadCodeAssist` and queries `v1internal:fetchAvailableModels` on `daily-cloudcode-pa.sandbox.googleapis.com` to retrieve per-model `remainingFraction` and `resetTime` values.
3. **Target Switching**:
   - `--target agy` updates the OS credential store (`service=gemini`, `username=antigravity`) directly with no GUI restart.
   - `--target ide` injects the protobuf-encoded OAuth payload into Antigravity IDE's `state.vscdb` and restarts the IDE process if running.
   - `--target all` applies both.

## Compatibility

| Platform | CLI Credential Store (`agy`) | IDE Storage (`state.vscdb`) |
|---|---|---|
| macOS | Keychain (`security`) | `~/Library/Application Support/Antigravity IDE/User/globalStorage/state.vscdb` |
| Linux | Secret Service (`secret-tool`) | `~/.config/Antigravity IDE/User/globalStorage/state.vscdb` |
| Windows | Credential Manager (`gemini:antigravity`) | `%APPDATA%\Antigravity IDE\User\globalStorage\state.vscdb` |

## Security

- OAuth access and refresh tokens are encrypted at rest with **AES-256-GCM** (`agm_enc_v1:...`) using a locally generated master key (`.mk`).
- Store directory and key files are created with restrictive permissions (`0700` / `0600`).
- `agm export` preserves encrypted token fields; restoring on another host requires the matching `.mk` file.
- Never commit `.mk`, `cloud_accounts.db`, or exported backup JSON files.

## Troubleshooting

| Symptom | Resolution |
|---|---|
| Missing DB or master key | Run `agm init` or `agm login` |
| OAuth callback hangs | Ensure ports `8888`–`8892` are free on `127.0.0.1` |
| `import-cli` finds no credentials | Sign into `agy` once or use `agm login` |
| `ide_db: not found` | Launch Antigravity IDE once to initialize `state.vscdb` |
| Stale or empty quotas | Run `agm refresh <email>` or `agm refresh-all` |
| Expired access token | Run `agm validate` |
| Quotas stuck at `100%` (`now + 5h`) | Verify `QUOTA_URL` in `agm doctor` uses `daily-cloudcode-pa.sandbox.googleapis.com` (or override via `AGM_QUOTA_URL`) |

### Understanding Quotas

- **5-hour vs. weekly limits**: `fetchAvailableModels` returns a single `quotaInfo` (`remainingFraction` and `resetTime`), which reflects the active 5-hour window. To inspect weekly limits as well, run `agy -p "/usage"`.
- **Shared model pools**: Models within the same pool (for example, Gemini models vs. Claude / `gpt-oss-120b-medium`) share the same remaining percentage and reset timestamp.
- **Minor percentage differences**: `agm` rounds `remainingFraction * 100` to the nearest integer, so values may differ by ~1% from `agy -p "/usage"`.

Run `agm doctor` for full environment diagnostics.

## Development

```bash
go test ./...
go vet ./...
go build -o agm .
```

```plaintext
.
├── main.go
├── go.mod
├── cmd/               # Cobra CLI commands and JSON/table formatters
└── internal/
    ├── aliases/       # Account alias persistence
    ├── api/           # OAuth2, token refresh, loadCodeAssist, quota API
    ├── credstore/     # OS credential store adapters
    ├── crypto/        # Master key and AES-256-GCM encryption
    ├── db/            # SQLite store and IDE state.vscdb injection
    ├── paths/         # Platform-specific product and data paths
    ├── process/       # IDE/CLI process discovery and restart
    ├── proto/         # Protobuf wire helpers for state.vscdb
    └── target/        # Target parsing (agy | ide | all)
```

## License

No `LICENSE` file is currently included in this repository.
