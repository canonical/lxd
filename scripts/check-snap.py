#!/usr/bin/env python3
"""Query the Snap Store API to check the latest release information for a given snap.

Displays version, revision, architecture, size, and optionally links to git commits
and download URLs. Output format is auto-detected (terminal hyperlinks for TTY, plain text
when piped), but can be explicitly set via --format.
"""
from typing import get_args, Literal, Optional
import argparse
import json
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

Format = Literal["auto", "terminal", "markdown", "plain"]


def _make_link(text: str, url: str, fmt: Format = "terminal") -> str:
    """Format text as a hyperlink.

    Args:
        text: The display text to linkify.
        url: The URL to link to. If empty, returns text unchanged.
        fmt: Output format: "terminal" for terminal hyperlinks, "markdown" for markdown links, "plain" for plain text.

    Returns:
        Formatted link string, or plain text if url is empty.

    Examples:
        >>> _make_link("click here", "")
        'click here'
        >>> _make_link("docs", "https://example.com", "markdown")
        '[docs](https://example.com)'
        >>> _make_link("plain", "https://example.com", "plain")
        'plain'
        >>> link = _make_link("term", "https://example.com", "terminal")
        >>> "term" in link and "https://example.com" in link
        True
    """
    if not url:
        return text
    if fmt == "markdown":
        return f"[{text}]({url})"
    if fmt == "plain":
        return text
    # fmt == "terminal"
    return f"\033]8;;{url}\033\\{text}\033]8;;\033\\"


def _version_display(version: str, github_repo: Optional[str], fmt: Format = "terminal") -> str:
    """Format a version string with optional git commit link.

    Matches both "git-HASH" and "VERSION-HASH" patterns (e.g. 6.7-d814d89, 5.0.6-e49d9f4).

    Args:
        version: The version string, possibly containing a git commit suffix.
        github_repo: GitHub repository in "org/repo" format. If provided and version
                     has a commit suffix, creates a link to the commit. Can be None.
        fmt: Output format: "terminal" for terminal hyperlinks, "markdown" for markdown links, "plain" for plain text.

    Returns:
        Formatted version string, potentially with a hyperlink to the commit.

    Examples:
        >>> _version_display("5.0.6", None)
        '5.0.6'
        >>> _version_display("5.0.6-e49d9f4", None)
        '5.0.6-e49d9f4'
        >>> _version_display("5.0.6-e49d9f4", "canonical/lxd", "markdown")
        '[5.0.6-e49d9f4](https://github.com/canonical/lxd/commit/e49d9f4)'
        >>> _version_display("git-abc1234", "canonical/lxd", "markdown")
        '[git-abc1234](https://github.com/canonical/lxd/commit/abc1234)'
    """
    # Match both "git-HASH" and "VERSION-HASH" (e.g. 6.7-d814d89, 5.0.6-e49d9f4)
    m = re.search(r'-([0-9a-f]{6,})$', version)
    if m and github_repo:
        commit = m.group(1)
        url = f"https://github.com/{github_repo}/commit/{commit}"
        return _make_link(version, url, fmt)
    return version


def check_snap(snap_name: str, track: str, risk: str, github_repo: Optional[str] = None, fmt: Format = "auto") -> None:
    """Query Snap Store API for release information and display formatted output.

    Args:
        snap_name: Name of the snap to query.
        track: The snap channel track (e.g., 'latest').
        risk: The snap channel risk level (e.g., 'stable').
        github_repo: Optional GitHub repository in "org/repo" format for commit linking.
        fmt: Output format: "auto" (default: detect TTY), "terminal", "markdown", or "plain".
    """
    # Resolve "auto" format based on TTY detection
    if fmt == "auto":
        resolved_fmt: str = "terminal" if sys.stdout.isatty() else "plain"
    else:
        resolved_fmt = fmt
    snap_name_encoded = urllib.parse.quote(snap_name, safe='')
    url = f"https://api.snapcraft.io/v2/snaps/info/{snap_name_encoded}"

    headers = {
        # Series 16 is the only valid value; it refers to the snap format generation, not Ubuntu 16.04
        "Snap-Device-Series": "16",
        # An architecture must be supplied or the API returns only the native arch of the requester;
        # amd64 is used as a placeholder since the response includes all architectures regardless
        "Snap-Device-Architecture": "amd64",
    }
    req = urllib.request.Request(url, headers=headers)

    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            data = json.loads(response.read().decode())
    except urllib.error.URLError as e:
        print(f"Failed to reach the Snap Store API: {e}", file=sys.stderr)
        sys.exit(1)
    except json.JSONDecodeError as e:
        print(f"Failed to parse API response: {e}", file=sys.stderr)
        sys.exit(1)

    matches = [
        release for release in data.get('channel-map', [])
        if release.get('channel', {}).get('track') == track
        and release.get('channel', {}).get('risk') == risk
    ]

    if not matches:
        print(f"No releases found in {track}/{risk}.")
        return

    matches.sort(key=lambda r: r.get('channel', {}).get('architecture', ''))

    # Group by version so discrepancies are immediately visible
    groups = {}
    for release in matches:
        version = release.get('version', '?')
        groups.setdefault(version, []).append(release)

    # Sort groups by version string for deterministic output
    for version in sorted(groups.keys()):
        releases = groups[version]
        arch_parts = []
        for release in releases:
            arch = release.get('channel', {}).get('architecture', '?')
            revision = release.get('revision', '?')
            dl = release.get('download', {})
            dl_url = dl.get('url', '')
            size_bytes = dl.get('size')
            size_str = f"{size_bytes / (1024 * 1024):.0f}MiB" if size_bytes is not None else '?'
            arch_link = _make_link(arch, dl_url, resolved_fmt)
            link = f"{arch_link} ({size_str}, rev: {revision})"
            arch_parts.append(link)
        version_str = _version_display(version, github_repo, resolved_fmt)
        print(f"{version_str}: {',  '.join(arch_parts)}")


def main():
    parser = argparse.ArgumentParser(description="Check snap release info from the Snap Store.")
    parser.add_argument("snap", help="Snap name to look up")
    parser.add_argument("--channel", default="latest/edge",
                        help="Channel in track/risk format (default: latest/edge)")
    parser.add_argument("--github", "--gh", metavar="ORG/REPO",
                        help="GitHub repo (e.g. canonical/lxd) to linkify version strings")
    parser.add_argument("--format", "-f", choices=get_args(Format), default="auto",
                        help="Output format: auto (default: terminal if TTY, plain otherwise), terminal, markdown, or plain")

    args = parser.parse_args()

    track, _, risk = args.channel.partition('/')
    if not risk:
        risk = 'stable'

    github_repo = None
    if args.github:
        m = re.match(r'^(?:https?://github\.com/)?([^/]+/[^/?#]+)', args.github)
        if m:
            github_repo = m.group(1).rstrip('/')
        else:
            print(f"Error: Invalid GitHub repository format: {args.github}", file=sys.stderr)
            print("Expected format: 'org/repo' or 'https://github.com/org/repo'", file=sys.stderr)
            sys.exit(1)
    else:
        github_repo = f"canonical/{args.snap}"

    check_snap(args.snap, track, risk, github_repo, fmt=args.format)


if __name__ == "__main__":
    main()
