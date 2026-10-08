#!/usr/bin/env python3
"""
Verify that every GA (phase=1, install=always) asset in assets/active/metadata.yaml
without topology conditions has a matching (Kind, Name) entry in assetsUnderTest
in test/e2e/assets_test.go.

Template names with {{ .Params.KEY }} are resolved using the asset's template_params.
Exits 1 and emits GitHub Actions annotations when coverage is missing.
"""

import re
import sys

import yaml

METADATA = "assets/active/metadata.yaml"
TEST_FILE = "test/e2e/assets_test.go"


def has_topology_condition(conditions):
    """True when any condition restricts deployment to a specific cluster topology."""
    return any(c.get("type") == "topology" for c in (conditions or []))


def resolve_name(raw_name, params):
    """Substitute {{ .Params.KEY }} placeholders using params dict."""
    def replacer(m):
        return str(params.get(m.group(1), m.group(0)))
    return re.sub(r"\{\{\s*\.Params\.(\w+)\s*\}\}", replacer, raw_name)


def extract_k8s_name(template_path, params):
    """
    Read template file, extract metadata.name (2-space indent), resolve params.
    Returns None with a warning if not found.
    """
    full_path = f"assets/{template_path}"
    try:
        with open(full_path) as f:
            content = f.read()
    except FileNotFoundError:
        print(f"::warning::Template not found: {full_path}", file=sys.stderr)
        return None

    m = re.search(r"^  name:\s+(.+)$", content, re.MULTILINE)
    if not m:
        print(f"::warning::No '  name:' in {full_path}", file=sys.stderr)
        return None

    return resolve_name(m.group(1).strip(), params)


def get_ga_assets(metadata_path):
    """
    Return list of (catalog_name, component, k8s_name) for GA always-install assets.
    Skips assets with topology conditions (cluster-topology-specific, not universally testable).
    """
    with open(metadata_path) as f:
        catalog = yaml.safe_load(f)

    result = []
    for asset in catalog["assets"]:
        if asset.get("phase") != 1 or asset.get("install") != "always":
            continue
        if has_topology_condition(asset.get("conditions")):
            continue

        catalog_name = asset["name"]
        component = asset["component"]
        params = asset.get("template_params") or {}
        k8s_name = extract_k8s_name(asset["path"], params)

        if k8s_name is None:
            print(
                f"::warning::Could not resolve k8s name for '{catalog_name}' "
                f"(component={component})",
                file=sys.stderr,
            )
            continue

        result.append((catalog_name, component, k8s_name))

    return result


def get_covered_assets(test_path):
    """Return set of (Kind, Name) pairs from assetsUnderTest."""
    with open(test_path) as f:
        content = f.read()

    m = re.search(
        r"var assetsUnderTest\s*=\s*initAssets\(\[\]testAsset\{(.+?)\}\)",
        content,
        re.DOTALL,
    )
    if not m:
        print(f"::error file={test_path}::Could not locate assetsUnderTest slice")
        sys.exit(1)

    block = m.group(1)
    kinds = re.findall(r'Kind:\s+"([^"]+)"', block)
    names = re.findall(r'\bName:\s+"([^"]+)"', block)

    if len(kinds) != len(names):
        print(
            f"::error file={test_path}::Parsing mismatch in assetsUnderTest: "
            f"{len(kinds)} Kind entries vs {len(names)} Name entries"
        )
        sys.exit(1)

    return set(zip(kinds, names))


def main():
    ga_assets = get_ga_assets(METADATA)
    covered = get_covered_assets(TEST_FILE)

    missing = [
        (catalog_name, component, k8s_name)
        for catalog_name, component, k8s_name in ga_assets
        if (component, k8s_name) not in covered
    ]

    if missing:
        for catalog_name, component, k8s_name in missing:
            print(
                f"::error file={TEST_FILE}::"
                f"GA asset '{catalog_name}' (kind={component}, name={k8s_name}) "
                f"has no entry in assetsUnderTest. "
                f'Add: {{GVK: ...Kind:"{component}", Name:"{k8s_name}", ...}}'
            )
        sys.exit(1)

    print(f"OK: all {len(ga_assets)} GA always-install asset(s) covered in assetsUnderTest")
    for _, component, k8s_name in sorted(ga_assets):
        print(f"    {component}/{k8s_name}")


if __name__ == "__main__":
    main()
