#!/usr/bin/env bash
# Apply performance-oriented git/git-lfs config to the *current* repo.
# Run from inside the repo (after `git init` or `git clone`, before fetch/checkout).
#
# Uses `git config --local set` (modern subcommand form, git >= 2.46).
# For older git, replace `--local set` with `--local`.
#
# These settings assume an EPHEMERAL CI worker:
#   - the repo is thrown away after the job
#   - crash durability is not required
#   - the remote is trusted
# Do NOT use blindly on a developer machine.

set -euo pipefail

git_set() { git config --local set "$1" "$2"; }

# ---------------------------------------------------------------------------
# Core: I/O durability and indexing
# ---------------------------------------------------------------------------
git_set core.fsync                            none
git_set core.fsyncMethod                      writeout-only
git_set core.preloadIndex                     true
git_set core.untrackedCache                   true
git_set feature.manyFiles                     true
git_set index.threads                         true
git_set index.version                         4
git_set index.skipHash                        true

# ---------------------------------------------------------------------------
# Disable background bookkeeping (CI throws the repo away)
# ---------------------------------------------------------------------------
git_set gc.auto                               0
git_set gc.writeCommitGraph                   false
git_set maintenance.auto                      false
git_set fetch.writeCommitGraph                false
git_set commitGraph.generationVersion         2     # only matters if a graph exists
git_set core.commitGraph                      true  # read it if present, just don't write

# ---------------------------------------------------------------------------
# Protocol / transfer
# ---------------------------------------------------------------------------
git_set protocol.version                      2
git_set http.version                          HTTP/2
git_set pack.threads                          0     # 0 = all cores
git_set fetch.negotiationAlgorithm            skipping

# Skip object integrity checks on fetch (only if you trust the remote)
git_set transfer.fsckObjects                  false
git_set fetch.fsckObjects                     false
git_set receive.fsckObjects                   false

# ---------------------------------------------------------------------------
# Checkout parallelism
# ---------------------------------------------------------------------------
git_set checkout.workers                      0     # 0 = one per logical CPU
git_set checkout.thresholdForParallelism      100   # default; lower if your repo is small

# ---------------------------------------------------------------------------
# Submodules (only relevant if you use them)
# ---------------------------------------------------------------------------
git_set submodule.fetchJobs                   0     # 0 = #cpus
git_set submodule.recurse                     true
# git_set protocol.file.allow                 always   # only if you trust local-path submodules

# ---------------------------------------------------------------------------
# git-lfs: concurrency and batching
# ---------------------------------------------------------------------------
git_set lfs.concurrenttransfers               128   # tune; 512 may trigger backend rate-limits
git_set lfs.concurrentbatchrequests           16
git_set lfs.transfer.batchSize                500
git_set lfs.tustransfers                      true
git_set lfs.cachecredentials                  true

# ---------------------------------------------------------------------------
# git-lfs: timeouts (bump on flaky networks, lower for fail-fast)
# ---------------------------------------------------------------------------
git_set lfs.dialtimeout                       30
git_set lfs.tlstimeout                        30
git_set lfs.activitytimeout                   60
git_set lfs.keepalive                         30
git_set lfs.transfer.maxretries               5
git_set lfs.transfer.maxretrydelay            10

# ---------------------------------------------------------------------------
# git-lfs: optional, situational
# ---------------------------------------------------------------------------
# Compress LFS payloads on the wire (server must support):
# git_set lfs.transfer.httpdownloadencoding   zstd

# Co-locate LFS storage with the working tree on a reflink-capable FS.
# On a reflink-capable FS (XFS, btrfs, ext4 -O reflink, APFS), this lets
# `git lfs checkout` reflink instead of copying.
# git_set lfs.storage                         /workspace/.git/lfs

# Restrict which LFS files get materialized (huge win if your job only needs a subset):
# git_set lfs.fetchinclude                    "src/**,assets/needed/**"
# git_set lfs.fetchexclude                    "assets/huge-unused/**"

# ---------------------------------------------------------------------------
# Windows-only (no-ops elsewhere; safe to set unconditionally)
# ---------------------------------------------------------------------------
git_set core.fscache                          true
git_set core.longpaths                        true
# git_set core.symlinks                       false   # only if you don't need symlinks

# ---------------------------------------------------------------------------
# Misc
# ---------------------------------------------------------------------------
git_set advice.detachedHead                   false   # quieter CI logs
git_set core.autocrlf                         false   # avoid per-file conversion in smudge
git_set core.eol                              lf

echo "CI git config applied to $(git rev-parse --show-toplevel)"
