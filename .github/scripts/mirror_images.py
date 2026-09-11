#!/usr/bin/env python3
"""Reconcile the Diction Labs image mirrors against .github/mirror-images.json.

The manifest is the source of truth. `upstream_digest` is the digest we have deliberately
promoted, and what our tag must serve. This script never follows upstream on its own: when
upstream moves past the pinned digest it reports drift and leaves the mirror alone, so a
promotion stays a reviewed decision.

Ported from ~/scripts/diction-image-mirror.sh on Din (2026-09-11), which had authenticated with a
classic PAT that expired and stopped publishing for two weeks. In Actions the job's own
GITHUB_TOKEN can push to ghcr.io/dictionlabs, so no long-lived credential is needed for GHCR.

Exit codes: 0 = in sync (drift is reported, not fatal), 1 = a copy failed.
"""
import json, os, pathlib, subprocess, sys

REGCTL = os.environ.get("REGCTL", "regctl")
MANIFEST = pathlib.Path(__file__).resolve().parent.parent / "mirror-images.json"
TARGETS = ["ghcr.io/dictionlabs", "docker.io/dictionlabs"]


def run(*args):
    return subprocess.run([REGCTL, *args], capture_output=True, text=True)


def digest(ref):
    """Digest of a ref, or None when it cannot be resolved (missing/unreachable)."""
    r = run("image", "digest", ref)
    return r.stdout.strip() if r.returncode == 0 and r.stdout.strip() else None


def summary(line):
    print(line, flush=True)
    path = os.environ.get("GITHUB_STEP_SUMMARY")
    if path:
        with open(path, "a") as fh:
            fh.write(line + "\n")


def main():
    manifest = json.loads(MANIFEST.read_text())
    failures, drift = [], []

    summary("| image | upstream vs pinned | mirror action |")
    summary("|---|---|---|")

    for img in manifest["images"]:
        key = f"{img['name']}:{img['tag']}"
        pinned = img["upstream_digest"]

        # The manifest's `upstream` already carries a tag, so re-applying one blindly produced
        # `…:latest-cpu:latest-cpu`, which regctl rejects. That silently made this arm of the
        # comparison report "unreachable" on every run for months, meaning real upstream drift
        # was never once detected. Strip any existing tag/digest, then apply the manifest tag.
        upstream_ref = img["upstream"].split("@")[0].rsplit(":", 1)[0] + ":" + img["tag"]
        pinned_ref = f"{img['upstream'].split('@')[0]}@{pinned}"

        live = digest(upstream_ref)
        if live is None:
            state = "unreachable"
            drift.append(f"{key}: upstream unreachable ({upstream_ref})")
        elif live != pinned:
            state = f"MOVED (live {live[:19]}…)"
            drift.append(
                f"{key}: upstream moved past the pinned digest.\n"
                f"  pinned: {pinned}\n  live:   {live}\n"
                f"  upstream: {upstream_ref}"
            )
        else:
            state = "matches pinned"

        actions = []
        for target in TARGETS:
            dest = f"{target}/{img['name']}:{img['tag']}"
            current = digest(dest)
            if current == pinned:
                actions.append(f"{target.split('/')[0]}: in sync")
                continue
            if current is not None:
                drift.append(
                    f"{key}: {dest} had diverged from the pinned digest and was re-copied.\n"
                    f"  was:    {current}\n  pinned: {pinned}"
                )
            r = run("image", "copy", pinned_ref, dest)
            if r.returncode == 0:
                actions.append(f"{target.split('/')[0]}: copied")
            else:
                actions.append(f"{target.split('/')[0]}: **FAILED**")
                failures.append(f"{dest}: {(r.stderr or r.stdout).strip()[:300]}")

        summary(f"| `{key}` | {state} | {', '.join(actions)} |")

    if drift:
        summary("\n### Drift\n")
        for d in drift:
            summary("```\n" + d + "\n```")
        pathlib.Path("drift.txt").write_text("\n\n".join(drift))

    if failures:
        summary("\n### Copy failures\n")
        for f in failures:
            summary(f"- `{f}`")
        print("::error::mirror copy failed", file=sys.stderr)
        return 1

    summary("\nAll mirrors serve their pinned digests.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
