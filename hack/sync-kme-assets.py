#!/usr/bin/env python3
"""Sync kubevirt-metrics-exporter assets from kubevirt-metrics-exporter on GitHub.

Usage:
  ./hack/sync-kme-assets.py           # fetch and sync
  ./hack/sync-kme-assets.py --verify  # CI: fail if out of sync

Upstream: https://github.com/openshift-virtualization/kubevirt-metrics-exporter
Optional env: KME_BRANCH=main
"""

from __future__ import annotations

import argparse
import os
import re
import sys
import tempfile
import urllib.error
import urllib.request
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DEST_DIR = REPO_ROOT / "assets/active/kubevirt-metrics-exporter"
KME_REPO = "openshift-virtualization/kubevirt-metrics-exporter"
KME_DEFAULT_BRANCH = "main"
KME_UPSTREAM_FILES = (
    "deploy/base/serviceaccount.yaml",
    "deploy/base/clusterrole.yaml",
    "deploy/base/clusterrolebinding.yaml",
    "deploy/base/daemonset.yaml",
    "deploy/podmonitor/podmonitor.yaml",
    "deploy/prometheus-rules/prometheusrule.yaml",
    "deploy/openshift/scc.yaml",
    "deploy/openshift/datasource.yaml",
    "deploy/openshift/dashboard.yaml",
)
KME_COPY_MAP = {
    "deploy/base/serviceaccount.yaml": "serviceaccount.yaml",
    "deploy/base/clusterrole.yaml": "clusterrole.yaml",
    "deploy/base/clusterrolebinding.yaml": "clusterrolebinding.yaml",
    "deploy/podmonitor/podmonitor.yaml": "podmonitor.yaml",
    "deploy/prometheus-rules/prometheusrule.yaml": "prometheusrule.yaml",
    "deploy/openshift/scc.yaml": "scc.yaml",
    "deploy/openshift/scc-clusterrole.yaml": "scc-clusterrole.yaml",
    "deploy/openshift/scc-clusterrolebinding.yaml": "scc-clusterrolebinding.yaml",
    "deploy/openshift/datasource.yaml": "datasource.yaml",
    "deploy/openshift/dashboard.yaml": "dashboard.yaml",
}

TEMPLATE_HEADER = (
    '{{- $envOverrides := dig "metadata" "annotations" '
    '"platform.kubevirt.io/kubevirt-metrics-exporter-env" "{}" .HCO.Object | fromJson }}'
)
NODE_SELECTOR_BLOCK = (
    "      nodeSelector:\n"
    '        node-role.kubernetes.io/worker: ""\n'
)
NAMESPACE_YAML = """apiVersion: v1
kind: Namespace
metadata:
  name: kubevirt-metrics-exporter
  labels:
    openshift.io/cluster-monitoring: "true"
"""


def upstream_url(repo: str, branch: str, relpath: str) -> str:
    return f"https://raw.githubusercontent.com/{repo}/{branch}/{relpath}"


def fetch_upstream_file(repo: str, branch: str, relpath: str, dest: Path) -> None:
    url = upstream_url(repo, branch, relpath)
    dest.parent.mkdir(parents=True, exist_ok=True)
    try:
        with urllib.request.urlopen(url) as response:
            dest.write_bytes(response.read())
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"failed to fetch {url}: HTTP {exc.code}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"failed to fetch {url}: {exc.reason}") from exc


def try_fetch_upstream_file(repo: str, branch: str, relpath: str, dest: Path) -> bool:
    try:
        fetch_upstream_file(repo, branch, relpath, dest)
    except RuntimeError:
        return False
    return True


def split_scc_role(legacy_path: Path, clusterrole_path: Path, clusterrolebinding_path: Path) -> None:
    docs = [doc.strip() + "\n" for doc in legacy_path.read_text(encoding="utf-8").split("\n---\n") if doc.strip()]
    if len(docs) != 2:
        raise RuntimeError(f"expected two documents in {legacy_path}")
    clusterrole_path.write_text(docs[0], encoding="utf-8")
    clusterrolebinding_path.write_text(docs[1], encoding="utf-8")


def fetch_scc_rbac(repo: str, branch: str, temp_dir: Path) -> None:
    clusterrole = temp_dir / "deploy/openshift/scc-clusterrole.yaml"
    clusterrolebinding = temp_dir / "deploy/openshift/scc-clusterrolebinding.yaml"
    if try_fetch_upstream_file(repo, branch, "deploy/openshift/scc-clusterrole.yaml", clusterrole) and try_fetch_upstream_file(
        repo, branch, "deploy/openshift/scc-clusterrolebinding.yaml", clusterrolebinding
    ):
        return

    legacy = temp_dir / "deploy/openshift/scc-role.yaml"
    fetch_upstream_file(repo, branch, "deploy/openshift/scc-role.yaml", legacy)
    split_scc_role(legacy, clusterrole, clusterrolebinding)


def fetch_upstream(repo: str, branch: str, temp_dir: Path) -> Path:
    for relpath in KME_UPSTREAM_FILES:
        fetch_upstream_file(repo, branch, relpath, temp_dir / relpath)
    fetch_scc_rbac(repo, branch, temp_dir)
    return temp_dir


def transform_daemonset(daemonset_path: Path) -> str:
    lines = daemonset_path.read_text(encoding="utf-8").splitlines(keepends=True)
    out: list[str] = [TEMPLATE_HEADER + "\n"]
    in_env = False

    index = 0
    while index < len(lines):
        line = lines[index]

        if re.match(r"\s+env:\s*$", line):
            in_env = True
            out.append(line)
            index += 1
            continue

        if in_env and re.match(r"\s+ports:", line):
            in_env = False

        if line.lstrip().startswith("image:"):
            out.append('          image: {{ index .Images "kubevirt-metrics-exporter" }}\n')
            index += 1
            continue

        if "hostPID: true" in line:
            out.append(line)
            index += 1
            next_line = lines[index] if index < len(lines) else ""
            if "nodeSelector:" not in next_line:
                out.append(NODE_SELECTOR_BLOCK)
            continue

        if in_env:
            name_match = re.match(r"(\s+)- name: (\S+)", line)
            if name_match:
                indent, name = name_match.groups()
                out.append(line)
                index += 1

                if name == "NODE_NAME":
                    while index < len(lines) and not re.match(r"\s+- name:", lines[index]):
                        out.append(lines[index])
                        index += 1
                    continue

                value_match = re.match(r'\s+value:\s*"?([^"\n]*)"?', lines[index])
                if not value_match:
                    raise ValueError(f"expected value for env {name} in {daemonset_path}")
                value = value_match.group(1)
                out.append(
                    f'{indent}  value: {{{{ index $envOverrides "{name}" | default "{value}" | quote }}}}\n'
                )
                index += 1
                continue

        out.append(line)
        index += 1

    return "".join(out)


def write_or_compare(path: Path, content: str, verify: bool) -> bool:
    if verify:
        if not path.exists():
            print(f"  ✗ MISSING {path}", file=sys.stderr)
            return False
        current = path.read_text(encoding="utf-8")
        if current != content:
            print(f"  ✗ OUT OF SYNC {path}", file=sys.stderr)
            return False
        print(f"  ✓ {path}")
        return True

    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")
    print(f"  ✓ {path}")
    return True


def copy_file(src: Path, dst: Path, verify: bool) -> bool:
    content = src.read_text(encoding="utf-8")
    return write_or_compare(dst, content, verify)


def sync_assets(
    upstream_root: Path,
    dest: Path,
    *,
    verify: bool,
) -> bool:
    daemonset = upstream_root / "deploy/base/daemonset.yaml"
    ok = True

    for upstream_relpath, dest_name in KME_COPY_MAP.items():
        src = upstream_root / upstream_relpath
        if not src.is_file():
            print(f"  ✗ MISSING upstream file {upstream_relpath}", file=sys.stderr)
            ok = False
            continue
        if not copy_file(src, dest / dest_name, verify):
            ok = False

    if not write_or_compare(dest / "namespace.yaml", NAMESPACE_YAML, verify):
        ok = False

    daemonset_content = transform_daemonset(daemonset)
    if not write_or_compare(dest / "kubevirt-metrics-exporter.yaml.tpl", daemonset_content, verify):
        ok = False

    return ok


def main() -> int:
    os.chdir(REPO_ROOT)

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--branch",
        default=os.environ.get("KME_BRANCH", KME_DEFAULT_BRANCH),
        help=f"KME git branch to fetch (default: {KME_DEFAULT_BRANCH})",
    )
    parser.add_argument("--verify", action="store_true", help="verify files match upstream")
    args = parser.parse_args()

    source = f"https://github.com/{KME_REPO}.git@{args.branch}"
    mode = "Verifying" if args.verify else "Syncing"
    print(f"{mode} kubevirt-metrics-exporter assets from {source}")

    try:
        with tempfile.TemporaryDirectory(prefix="sync-kme-assets-") as temp_dir:
            upstream_root = fetch_upstream(KME_REPO, args.branch, Path(temp_dir))
            ok = sync_assets(
                upstream_root,
                DEST_DIR,
                verify=args.verify,
            )
    except RuntimeError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1

    if args.verify:
        if ok:
            print("✓ All kubevirt-metrics-exporter assets are in sync with KME")
            return 0
        print("✗ kubevirt-metrics-exporter assets are out of sync with KME", file=sys.stderr)
        return 1

    if not ok:
        return 1

    print("✓ Sync complete")
    return 0


if __name__ == "__main__":
    sys.exit(main())
