import contextlib
import copy
import importlib.util
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

import yaml

CHECKER_PATH = Path(__file__).with_name("check-ga-assets-changed.py")
SPEC = importlib.util.spec_from_file_location("check_ga_assets_changed", CHECKER_PATH)
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


def json_dumps(doc):
    return json.dumps(doc, indent=2) + "\n"


def make_catalog():
    return {
        "framework": {"maturity": "ga"},
        "assets": [
            {
                "name": "ga-asset",
                "group": "ga",
                "path": "active/ga/ga.yaml.tpl",
                "install": "always",
                "phase": 1,
                "component": "ConfigMap",
                "reconcile_order": 0,
                "conditions": [],
            },
            {
                "name": "dp-asset",
                "group": "dp",
                "path": "active/dp/dp.yaml.tpl",
                "install": "opt-in",
                "phase": 2,
            },
            {
                "name": "excluded-asset",
                "path": "active/framework/hco.yaml.tpl",
                "install": "always",
                "phase": 1,
            },
        ],
        "features": [
            {"name": "GA feature", "maturity": "ga", "groups": ["ga"]},
            {"name": "DP feature", "maturity": "dp", "assets": ["dp-asset"]},
        ],
        "excluded_assets": ["excluded-asset"],
    }


def make_status():
    """Fixture counterpart of the cmd/feature-status-gen JSON for make_catalog."""
    return {
        "framework": {"maturity": "GA", "opt_in": None},
        "features": [
            {
                "name": "GA feature",
                "maturity": "GA",
                "install": "always",
                "opt_in": None,
                "assets": ["ga-asset"],
            },
            {
                "name": "DP feature",
                "maturity": "DP",
                "install": "opt-in",
                "opt_in": None,
                "assets": ["dp-asset"],
            },
        ],
    }


def analyze(catalog_base, catalog_head, files, status_base=None, status_head=None):
    """CHECKER.analyze with the status fixtures wired in by default."""
    return CHECKER.analyze(
        catalog_base,
        catalog_head,
        make_status() if status_base is None else status_base,
        make_status() if status_head is None else status_head,
        files,
    )


def reasons_for_metadata_change(base, head, status_head=None):
    return analyze(base, head, [CHECKER.METADATA], status_head=status_head)


def has_reason(reasons, fragment):
    return any(fragment in reason for reason in reasons)


class StatusReasonsTest(unittest.TestCase):
    """Feature semantics read from the generated feature-status JSONs."""

    def setUp(self):
        self.base = make_status()

    def head(self):
        return copy.deepcopy(self.base)

    def test_framework_maturity_and_opt_in_changes_are_detected(self):
        for field, value in (("maturity", "DP"), ("opt_in", "platform.example/enable")):
            with self.subTest(field=field):
                head = self.head()
                head["framework"][field] = value

                self.assertTrue(CHECKER.status_reasons(self.base, head))

    def test_new_feature_is_detected_at_any_maturity(self):
        for feature in (
            {"name": "New GA", "maturity": "GA", "install": "always",
             "opt_in": None, "assets": ["ga-asset"]},
            {"name": "New DP", "maturity": "DP", "install": "opt-in",
             "opt_in": None, "assets": ["dp-asset"]},
            {"name": "New TP", "maturity": "TP", "install": "opt-in",
             "opt_in": None, "assets": ["dp-asset"]},
        ):
            with self.subTest(feature=feature["name"]):
                head = self.head()
                head["features"].append(feature)

                self.assertTrue(
                    any(
                        f"new feature declared: {feature['name']}" in reason
                        for reason in CHECKER.status_reasons(self.base, head)
                    )
                )

    def test_removing_any_feature_is_detected(self):
        # The generated JSON carries only effective maturity, so explicit and
        # derived previews cannot be told apart; removals always flag.
        for feature_name in ("GA feature", "DP feature"):
            with self.subTest(feature=feature_name):
                head = self.head()
                head["features"] = [
                    feature
                    for feature in head["features"]
                    if feature["name"] != feature_name
                ]

                self.assertTrue(
                    has_reason(
                        CHECKER.status_reasons(self.base, head),
                        f"feature removed: {feature_name}",
                    )
                )

    def test_feature_maturity_change_is_detected(self):
        head = self.head()
        head["features"][1]["maturity"] = "GA"

        self.assertTrue(
            has_reason(
                CHECKER.status_reasons(self.base, head),
                "maturity of feature 'DP feature' changed: DP -> GA",
            )
        )

    def test_ga_feature_asset_set_change_is_detected(self):
        head = self.head()
        head["features"][0]["assets"] = ["ga-asset", "new-ga-asset"]

        self.assertTrue(
            has_reason(
                CHECKER.status_reasons(self.base, head),
                "asset set of GA feature 'GA feature' changed: "
                "+['new-ga-asset'] -[]",
            )
        )

    def test_dp_feature_asset_set_change_does_not_are_detected(self):
        head = self.head()
        head["features"][1]["assets"] = ["dp-asset", "new-dp-asset"]

        self.assertEqual([], CHECKER.status_reasons(self.base, head))

    def test_membership_comparison_is_skipped_without_the_assets_field(self):
        # Guard for revisions generated before FeatureStatus carried assets.
        head = self.head()
        for feature in self.base["features"] + head["features"]:
            feature.pop("assets", None)

        self.assertEqual([], CHECKER.status_reasons(self.base, head))

    def test_install_mode_change_is_caught_through_the_maturity(self):
        # install always -> opt-in flips the derived maturity in the JSON.
        head = self.head()
        head["features"][0]["install"] = "opt-in"
        head["features"][0]["maturity"] = "DP"
        head["features"][0]["opt_in"] = "platform.example/enable-ga"

        self.assertTrue(
            has_reason(
                CHECKER.status_reasons(self.base, head),
                "maturity of feature 'GA feature' changed: GA -> DP",
            )
        )


class MetadataReasonsTest(unittest.TestCase):
    """Catalog fields the status JSON does not carry."""

    def setUp(self):
        self.base = make_catalog()

    def head(self):
        return copy.deepcopy(self.base)

    def test_changes_to_ga_asset_metadata_are_detected(self):
        changes = {
            "path": "active/ga/renamed.yaml.tpl",
            "install": "opt-in",
            "phase": 3,
            "conditions": [{"type": "annotation", "key": "enable-feature"}],
            "group": "new-group",
            "component": "Secret",
            "reconcile_order": 9,
            "gate_crd": "example.test/v1",
            "template_params": {"setting": "changed"},
            "node_disruption_policy": {"asset": "policy"},
        }
        for field, value in changes.items():
            with self.subTest(field=field):
                head = self.head()
                head["assets"][0][field] = value

                self.assertTrue(
                    any(
                        "ga-asset" in reason and field in reason
                        for reason in reasons_for_metadata_change(self.base, head)
                    )
                )

    def test_excluded_asset_metadata_changes_are_detected(self):
        head = self.head()
        head["assets"][2]["phase"] = 2

        self.assertTrue(
            has_reason(
                reasons_for_metadata_change(self.base, head), "excluded-asset"
            )
        )

    def test_excluded_asset_list_changes_are_detected(self):
        for new_excluded in ([], ["excluded-asset", "new-excluded"]):
            with self.subTest(excluded_assets=new_excluded):
                head = self.head()
                head["excluded_assets"] = new_excluded

                self.assertTrue(
                    has_reason(
                        reasons_for_metadata_change(self.base, head),
                        "excluded_assets changed",
                    )
                )

    def test_dp_asset_metadata_change_does_not_are_detected(self):
        head = self.head()
        head["assets"][1]["phase"] = 3

        self.assertEqual([], reasons_for_metadata_change(self.base, head))

    def test_new_group_asset_in_ga_feature_is_detected_via_regenerated_status(self):
        head = self.head()
        head["assets"].append(
            {
                "name": "second-ga-asset",
                "group": "ga",
                "path": "active/ga/second.yaml.tpl",
                "install": "always",
            }
        )
        head_status = make_status()
        head_status["features"][0]["assets"] = ["ga-asset", "second-ga-asset"]

        reasons = reasons_for_metadata_change(self.base, head, status_head=head_status)

        self.assertTrue(
            has_reason(reasons, "asset set of GA feature 'GA feature' changed")
        )
        self.assertTrue(has_reason(reasons, "+['second-ga-asset']"))


class FileAssociationTest(unittest.TestCase):
    def setUp(self):
        self.catalog = make_catalog()

    def analyze_files(self, files, base=None, head=None):
        return analyze(base or self.catalog, head or self.catalog, files)

    def test_direct_ga_and_excluded_asset_files_are_detected(self):
        files = [
            "assets/active/ga/ga.yaml.tpl",
            "assets/active/framework/hco.yaml.tpl",
        ]

        reasons = self.analyze_files(files)

        self.assertTrue(any("ga-asset" in reason for reason in reasons))
        self.assertTrue(any("excluded-asset" in reason for reason in reasons))

    def test_direct_dp_asset_file_does_not_are_detected(self):
        reasons = self.analyze_files(["assets/active/dp/dp.yaml.tpl"])

        self.assertEqual([], reasons)

    def test_sibling_supporting_file_is_associated_with_asset(self):
        reasons = self.analyze_files(["assets/active/ga/render-helper.py"])

        self.assertTrue(
            any(
                "render-helper.py" in reason and "ga-asset" in reason
                for reason in reasons
            )
        )

    def test_template_support_directory_suffixes_are_resolved(self):
        for filename, expected_stem in (
            ("01-swap-enable.yaml.tpl", "01-swap-enable"),
            ("02-swap-enable.yaml", "02-swap-enable"),
            ("03-swap-enable.yml", "03-swap-enable"),
        ):
            with self.subTest(filename=filename):
                self.assertEqual(
                    expected_stem,
                    CHECKER.template_stem(f"active/templates/{filename}"),
                )

        template_catalog = {
            "assets": [
                {
                    "name": "template-asset",
                    "path": "active/templates/01-swap-enable.yaml.tpl",
                    "install": "always",
                }
            ],
            "features": [{"name": "Template feature", "assets": ["template-asset"]}],
        }
        template_status = {
            "framework": {"maturity": "GA", "opt_in": None},
            "features": [
                {
                    "name": "Template feature",
                    "maturity": "GA",
                    "install": "always",
                    "opt_in": None,
                    "assets": ["template-asset"],
                }
            ],
        }

        reasons = CHECKER.analyze(
            template_catalog,
            template_catalog,
            template_status,
            template_status,
            ["assets/active/templates/01-swap-enable/settings.yaml"],
        )

        self.assertTrue(any("template-asset" in reason for reason in reasons))

    def test_shared_directory_flags_ga_member_and_ignores_dp_member(self):
        catalog = {
            "assets": [
                {
                    "name": "ga-one",
                    "path": "active/shared/one.yaml",
                    "install": "always",
                },
                {
                    "name": "dp-two",
                    "path": "active/shared/two.yaml",
                    "install": "opt-in",
                },
            ],
            "features": [
                {"name": "GA", "maturity": "ga", "assets": ["ga-one"]},
                {"name": "DP", "maturity": "dp", "assets": ["dp-two"]},
            ],
        }
        status = {
            "framework": {"maturity": "GA", "opt_in": None},
            "features": [
                {
                    "name": "GA",
                    "maturity": "GA",
                    "install": "always",
                    "opt_in": None,
                    "assets": ["ga-one"],
                },
                {
                    "name": "DP",
                    "maturity": "DP",
                    "install": "opt-in",
                    "opt_in": None,
                    "assets": ["dp-two"],
                },
            ],
        }

        reasons = CHECKER.analyze(
            catalog, catalog, status, status, ["assets/active/shared/common.yaml"]
        )

        self.assertEqual(1, len(reasons))
        self.assertIn("ga-one", reasons[0])
        self.assertNotIn("dp-two", reasons[0])

    def test_uncataloged_file_under_active_assets_is_flagged(self):
        reasons = self.analyze_files(["assets/active/new-area/untracked.yaml"])

        self.assertTrue(
            has_reason(reasons, "not covered by the asset catalog")
        )

    def test_owners_and_files_outside_assets_active_are_ignored(self):
        reasons = self.analyze_files(
            [
                "assets/active/ga/OWNERS",
                "assets/active/OWNERS",
                "README.md",
                "assets/tombstones/old.yaml",
            ]
        )

        self.assertEqual([], reasons)

    def test_template_rename_uses_base_and_head_catalogs(self):
        base = {
            "assets": [
                {
                    "name": "renamed-ga",
                    "path": "active/old/template.yaml",
                    "install": "always",
                }
            ],
            "features": [{"name": "GA", "maturity": "ga", "assets": ["renamed-ga"]}],
        }
        status = {
            "framework": {"maturity": "GA", "opt_in": None},
            "features": [
                {
                    "name": "GA",
                    "maturity": "GA",
                    "install": "always",
                    "opt_in": None,
                    "assets": ["renamed-ga"],
                }
            ],
        }
        head = copy.deepcopy(base)
        head["assets"][0]["path"] = "active/new/template.yaml"

        reasons = CHECKER.analyze(
            base,
            head,
            status,
            status,
            ["assets/active/old/template.yaml", "assets/active/new/template.yaml"],
        )

        self.assertTrue(
            has_reason(reasons, "metadata of GA asset 'renamed-ga' changed fields: path")
        )
        self.assertEqual(
            2,
            sum("affects GA assets" in reason for reason in reasons),
        )


class CatalogParsingTest(unittest.TestCase):
    def test_empty_yaml_is_an_empty_catalog(self):
        for text in ("", "  \n", "null\n"):
            with self.subTest(text=text):
                self.assertEqual(({}, None), CHECKER.parse_catalog(text, "fixture"))

    def test_valid_yaml_is_parsed(self):
        self.assertEqual(
            ({"assets": [{"name": "asset"}]}, None),
            CHECKER.parse_catalog("assets:\n  - name: asset\n", "fixture"),
        )

    def test_invalid_yaml_returns_parse_error(self):
        catalog, error = CHECKER.parse_catalog("assets: [unterminated", "fixture")

        self.assertIsNone(catalog)
        self.assertIn("fixture could not be parsed", error)

    def test_valid_json_status_is_parsed(self):
        self.assertEqual(
            ({"framework": {"maturity": "GA"}}, None),
            CHECKER.parse_status('{"framework": {"maturity": "GA"}}', "fixture"),
        )

    def test_invalid_json_status_returns_parse_error(self):
        status, error = CHECKER.parse_status("{unterminated", "fixture")

        self.assertIsNone(status)
        self.assertIn("fixture could not be parsed", error)

    def test_missing_status_returns_error(self):
        status, error = CHECKER.parse_status(None, "fixture")

        self.assertIsNone(status)
        self.assertEqual("fixture is missing", error)


class GitInputHelperTest(unittest.TestCase):
    def test_changed_files_uses_three_dot_diff_and_drops_empty_lines(self):
        completed = SimpleNamespace(stdout="a.yaml\n\nb.yaml\n", returncode=0)
        with patch.object(CHECKER.subprocess, "run", return_value=completed) as run:
            self.assertEqual(
                ["a.yaml", "b.yaml"],
                CHECKER.git_changed_files("origin/main"),
            )

        run.assert_called_once_with(
            ["git", "diff", "--name-only", "--no-renames", "origin/main...HEAD"],
            capture_output=True,
            text=True,
            check=True,
        )

    def test_git_show_returns_text_when_it_succeeds(self):
        completed = SimpleNamespace(stdout="assets: []\n", returncode=0)
        with patch.object(CHECKER.subprocess, "run", return_value=completed) as run:
            result = CHECKER.git_show("origin/main", CHECKER.METADATA)

        self.assertEqual("assets: []\n", result)
        run.assert_called_once_with(
            ["git", "show", f"origin/main:{CHECKER.METADATA}"],
            capture_output=True,
            text=True,
        )

    def test_git_show_returns_none_when_file_is_missing_on_base(self):
        completed = SimpleNamespace(stdout="", returncode=128)
        with patch.object(CHECKER.subprocess, "run", return_value=completed):
            self.assertIsNone(CHECKER.git_show("origin/main", CHECKER.METADATA))


class CommandLineTest(unittest.TestCase):
    """End-to-end runs in throwaway git repositories, like the workflow does."""

    def setUp(self):
        self.cwd = os.getcwd()
        self.addCleanup(os.chdir, self.cwd)

    def git(self, repo, *args):
        return subprocess.run(
            ["git", "-C", repo, *args],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()

    def commit(self, repo, message):
        subprocess.run(["git", "-C", repo, "add", "-A"], check=True, capture_output=True)
        subprocess.run(
            [
                "git", "-C", repo,
                "-c", "user.name=test",
                "-c", "user.email=test@example.com",
                "commit", "-m", message,
            ],
            check=True,
            capture_output=True,
        )

    def make_repo(self):
        repo = tempfile.mkdtemp(prefix="qe-checker-")
        self.addCleanup(shutil.rmtree, repo, ignore_errors=True)
        subprocess.run(["git", "-C", repo, "init"], check=True, capture_output=True)
        return repo

    def write(self, repo, rel_path, text):
        path = Path(repo) / rel_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def commit_base(self, repo):
        self.write(repo, CHECKER.METADATA, yaml.safe_dump(make_catalog()))
        self.write(repo, CHECKER.STATUS_JSON, json_dumps(make_status()))
        self.commit(repo, "base")
        return self.git(repo, "rev-parse", "HEAD")

    def run_checker(self, base_rev, extra_patches=()):
        output_path = Path(tempfile.mkdtemp(prefix="qe-output-")) / "github-output.txt"
        argv = [str(CHECKER_PATH), "--base-rev", base_rev]
        stdout = io.StringIO()
        with contextlib.ExitStack() as stack:
            stack.enter_context(patch.object(sys, "argv", argv))
            stack.enter_context(
                patch.dict(os.environ, {"GITHUB_OUTPUT": str(output_path)})
            )
            stack.enter_context(contextlib.redirect_stdout(stdout))
            for extra in extra_patches:
                stack.enter_context(extra)
            exit_code = CHECKER.main()
        return exit_code, stdout.getvalue(), output_path.read_text()

    def test_cli_detects_ga_asset_metadata_change(self):
        repo = self.make_repo()
        base_rev = self.commit_base(repo)
        head = make_catalog()
        head["assets"][0]["phase"] = 2
        self.write(repo, CHECKER.METADATA, yaml.safe_dump(head))
        # The workflow regenerates the artifact; a phase change leaves it as-is.
        self.write(repo, CHECKER.STATUS_JSON, json_dumps(make_status()))
        self.commit(repo, "head")
        os.chdir(repo)

        exit_code, stdout, github_output = self.run_checker(base_rev)

        self.assertEqual(0, exit_code)
        self.assertIn("ga-assets-changed: true", stdout)
        self.assertIn("ga-asset", stdout)
        self.assertEqual("ga_assets_changed=true\n", github_output)

    def test_cli_false_for_a_change_outside_the_catalog(self):
        repo = self.make_repo()
        base_rev = self.commit_base(repo)
        self.write(repo, "README.md", "docs only\n")
        self.commit(repo, "head")
        os.chdir(repo)

        exit_code, stdout, github_output = self.run_checker(base_rev)

        self.assertEqual(0, exit_code)
        self.assertIn("ga-assets-changed: false", stdout)
        self.assertEqual("ga_assets_changed=false\n", github_output)

    def test_cli_missing_head_status_json_fails_safe(self):
        repo = self.make_repo()
        base_rev = self.commit_base(repo)
        (Path(repo) / CHECKER.STATUS_JSON).unlink()
        self.commit(repo, "head")
        os.chdir(repo)

        exit_code, stdout, github_output = self.run_checker(base_rev)

        self.assertEqual(0, exit_code)
        self.assertIn("ga-assets-changed: true", stdout)
        self.assertIn("is missing", stdout)
        self.assertEqual("ga_assets_changed=true\n", github_output)

    def test_cli_invalid_head_metadata_fails_safe(self):
        repo = self.make_repo()
        base_rev = self.commit_base(repo)
        self.write(repo, CHECKER.METADATA, "assets: [unterminated")
        self.commit(repo, "head")
        os.chdir(repo)

        exit_code, stdout, github_output = self.run_checker(base_rev)

        self.assertEqual(0, exit_code)
        self.assertIn("ga-assets-changed: true", stdout)
        self.assertIn("could not be parsed", stdout)
        self.assertEqual("ga_assets_changed=true\n", github_output)

    def test_cli_unexpected_analysis_error_fails_safe(self):
        repo = self.make_repo()
        base_rev = self.commit_base(repo)
        self.write(repo, "README.md", "docs only\n")
        self.commit(repo, "head")
        os.chdir(repo)

        exit_code, stdout, github_output = self.run_checker(
            base_rev,
            extra_patches=(
                patch.object(CHECKER, "analyze", side_effect=RuntimeError("boom")),
            ),
        )

        self.assertEqual(0, exit_code)
        self.assertIn("ga-assets-changed: true", stdout)
        self.assertIn("internal error while computing the verdict", stdout)
        self.assertEqual("ga_assets_changed=true\n", github_output)


if __name__ == "__main__":
    unittest.main()
